package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

// RemoteBroker forwards every effect to a broker process over one connection (D-18). It fails
// closed: once the connection breaks, every call errors, and nothing falls back to local IO.
// When the broker answers needs_approval, it puts the question to the human and retries.
type RemoteBroker struct {
	mu     sync.Mutex
	conn   net.Conn
	nextID uint64
	// brokenErr is set by the first transport failure; after it the stream cannot be trusted.
	brokenErr error
	// isClosing is set by Close before it closes conn, so failures Close itself causes are not
	// reported as the broker becoming unavailable.
	isClosing atomic.Bool
	options   RemoteOptions
}

// RemoteOptions are what a RemoteBroker reports to and asks. Nil Events drops the facts; a nil
// Approver refuses every Ask, which is the fail-closed answer.
type RemoteOptions struct {
	Events   core.EventSink
	Approver core.Approver
	Clock    core.Clock
}

// brokerExchangeTimeout is how long a broker may take to answer one request before the
// connection is declared broken.
const brokerExchangeTimeout = 30 * time.Second

// commandExchangeTimeout is brokerExchangeTimeout for run_command, whose answer waits on a
// child process the broker caps at commandTimeout rather than on bounded local IO. It leaves
// the broker room to kill a timed-out command and still report it, instead of the connection
// breaking and the command's fate being unknown.
const commandExchangeTimeout = commandTimeout + commandWaitDelay + 10*time.Second

// maxApprovalsPerEffect covers a write whose as-written and resolved paths each Ask and which
// then turns out too large to back up.
const maxApprovalsPerEffect = 3

// decisionRuleSource is the audit text on rules the approval modal creates.
const decisionRuleSource = "approval modal"

var errBrokerUnavailable = errors.New("broker unavailable")
var errBrokerKnownDown = errors.New("broker was already unavailable before the request")

var (
	_ core.Broker        = (*RemoteBroker)(nil)
	_ core.SessionLog    = (*RemoteBroker)(nil)
	_ core.MemoryStore   = (*RemoteBroker)(nil)
	_ core.CommandRunner = (*RemoteBroker)(nil)
)

func BuildRemoteBroker(conn net.Conn, options RemoteOptions) *RemoteBroker {
	if options.Clock == nil {
		options.Clock = core.SystemClock{}
	}
	return &RemoteBroker{conn: conn, options: options}
}

// Close ends the connection. conn is closed before taking the lock, so an exchange stuck on a
// hung broker returns instead of holding Close up.
func (r *RemoteBroker) Close() error {
	r.isClosing.Store(true)
	closeErr := r.conn.Close()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.brokenErr == nil {
		r.brokenErr = fmt.Errorf("broker connection closed: %w", net.ErrClosed)
	}
	return closeErr
}

// reportBrokerExit marks the connection broken as soon as the broker process exits, so the red
// bar does not wait for the next effect to find out.
func (r *RemoteBroker) reportBrokerExit(exitErr error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.brokenErr == nil {
		r.markBrokenLocked(context.Background(), errors.Join(errors.New("broker process exited"), exitErr))
	}
}

// markBrokenLocked records that the stream is unusable and reports it; r.mu must be held.
func (r *RemoteBroker) markBrokenLocked(ctx context.Context, cause error) {
	r.brokenErr = fmt.Errorf("broker unreachable: %w", errors.Join(errBrokerUnavailable, cause))
	r.publishUnavailableLocked(ctx)
}

// publishUnavailableLocked reports the broken connection, unless Close broke it on purpose.
func (r *RemoteBroker) publishUnavailableLocked(ctx context.Context) {
	if !r.isClosing.Load() {
		r.publish(ctx, core.BrokerUnavailable{EventHeader: r.buildEventHeader(), Reason: r.brokenErr.Error()})
	}
}

func (r *RemoteBroker) DescribePath(ctx context.Context, path string) (core.PathInfo, error) {
	resp, err := r.exchangeEffect(ctx, core.CapabilityFSRead, path, Request{Op: OpDescribePath, Path: path})
	if err != nil {
		return core.PathInfo{}, err
	}
	return convertRequiredPathInfo(resp)
}

// ListDirectory collects every page of the listing; a large directory spans several frames.
func (r *RemoteBroker) ListDirectory(ctx context.Context, path string) ([]core.PathInfo, error) {
	entries := []core.PathInfo{}
	for offset := uint32(0); ; {
		resp, err := r.exchangeEffect(ctx, core.CapabilityFSRead, path, Request{Op: OpListDirectory, Path: path, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, msg := range resp.Entries {
			entry, err := ConvertPathInfoMessage(msg)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry)
		}
		if resp.NextOffset == 0 {
			return entries, nil
		}
		if resp.NextOffset <= offset {
			return nil, fmt.Errorf("list_directory page at %d points back to %d: %w", offset, resp.NextOffset, errMalformedFrame)
		}
		offset = resp.NextOffset
	}
}

func (r *RemoteBroker) OpenFile(ctx context.Context, path string) (core.OpenedFile, error) {
	resp, err := r.exchangeEffect(ctx, core.CapabilityFSRead, path, Request{Op: OpOpenFile, Path: path})
	if err != nil {
		return core.OpenedFile{}, err
	}
	info, err := convertRequiredPathInfo(resp)
	if err != nil {
		return core.OpenedFile{}, err
	}
	if resp.Handle == 0 {
		return core.OpenedFile{}, fmt.Errorf("broker opened %q without a handle: %w", path, core.ErrInvalid)
	}
	return core.OpenedFile{Info: info, Content: &remoteFile{broker: r, ctx: ctx, handle: resp.Handle}}, nil
}

func (r *RemoteBroker) WriteFile(ctx context.Context, path string, content []byte) (core.PathInfo, error) {
	if len(content) > MaxWriteFileBytes {
		return core.PathInfo{}, fmt.Errorf("%q: %d bytes exceeds the broker's %d-byte write limit: %w", path, len(content), MaxWriteFileBytes, core.ErrInvalid)
	}
	resp, err := r.exchangeEffect(ctx, core.CapabilityFSWrite, path, Request{Op: OpWriteFile, Path: path, Data: content})
	if err != nil {
		return core.PathInfo{}, err
	}
	info, err := convertRequiredPathInfo(resp)
	if err != nil {
		return core.PathInfo{}, err
	}
	header := r.buildEventHeader()
	header.TurnID = core.ReportTurnID(ctx)
	r.publish(ctx, core.FileChanged{EventHeader: header, CallID: core.ReportToolCallID(ctx), Path: info.WorkspacePath})
	return info, nil
}

func (r *RemoteBroker) AddDecisionRule(ctx context.Context, rule core.DecisionRule) error {
	_, err := r.exchange(ctx, Request{Op: OpAddDecisionRule, Rule: BuildDecisionRuleMessage(rule)})
	return err
}

func (r *RemoteBroker) ReadMemory(ctx context.Context) (string, error) {
	resp, err := r.exchangeEffect(ctx, core.CapabilityFSRead, "memory", Request{Op: OpReadMemory, Path: "memory"})
	if err != nil {
		return "", err
	}
	return string(resp.Data), nil
}

func (r *RemoteBroker) ReplaceMemory(ctx context.Context, content string) error {
	if len(content) > 8<<10 {
		return fmt.Errorf("memory exceeds 8 KB: %w", core.ErrInvalid)
	}
	_, err := r.exchangeEffect(ctx, core.CapabilityFSWrite, "memory", Request{Op: OpReplaceMemory, Path: "memory", Data: []byte(content)})
	return err
}

// RunCommand asks the broker to run argv; the broker is the only side that decides whether it
// may, where it runs and what it may see.
func (r *RemoteBroker) RunCommand(ctx context.Context, argv []string) (core.CommandResult, error) {
	if err := validateCommandArgv(argv); err != nil {
		return core.CommandResult{}, err
	}
	resp, err := r.exchangeEffect(ctx, core.CapabilityExec, argv[0], Request{Op: OpRunCommand, Argv: argv})
	if err != nil {
		return core.CommandResult{}, err
	}
	if resp.Command == nil {
		return core.CommandResult{}, fmt.Errorf("broker answered run_command without a result: %w", core.ErrInvalid)
	}
	return ConvertCommandResultMessage(*resp.Command)
}

// AppendEntries logs completed messages in the broker's session log and returns once they are durable.
func (r *RemoteBroker) AppendEntries(ctx context.Context, entries []core.SessionEntry) error {
	messages, err := BuildEntryMessages(entries)
	if err != nil {
		return err
	}
	_, err = r.exchange(ctx, Request{Op: OpAppendEntries, Entries: messages})
	return err
}

func (r *RemoteBroker) ListTurns(ctx context.Context) ([]core.TurnSummary, error) {
	resp, err := r.exchange(ctx, Request{Op: OpListTurns})
	if err != nil {
		return nil, err
	}
	return ConvertTurnSummaryMessages(resp.Turns), nil
}

func (r *RemoteBroker) Rewind(ctx context.Context, fromTurn core.ID, mode core.RewindMode) (core.RewindReport, error) {
	resp, err := r.exchange(ctx, Request{Op: OpRewind, FromTurn: string(fromTurn), Mode: string(mode)})
	if err != nil {
		return core.RewindReport{}, err
	}
	if resp.Rewind == nil {
		return core.RewindReport{}, fmt.Errorf("broker answered rewind without a report: %w", core.ErrInvalid)
	}
	return ConvertRewindReportMessage(*resp.Rewind)
}

// exchangeEffect is exchange for a request policy decides: an Ask goes to the human and the
// request is retried on approval, and a refusal is published as PolicyDenied (D-29). scope is
// what the capability names — a path, or the program an exec request runs.
func (r *RemoteBroker) exchangeEffect(ctx context.Context, kind core.CapabilityKind, scope string, req Request) (Response, error) {
	resp, err := r.exchange(ctx, req)
	for approvals := 0; ; approvals++ {
		var required *core.ApprovalRequiredError
		if !errors.As(err, &required) {
			break
		}
		if approvals == maxApprovalsPerEffect {
			err = fmt.Errorf("%s still needs approval after %d answers: %w", scope, approvals, core.ErrDenied)
			break
		}
		if err = r.putToHuman(ctx, required.Operation); err != nil {
			break
		}
		resp, err = r.exchange(ctx, req)
	}
	if errors.Is(err, core.ErrDenied) || (errors.Is(err, errBrokerKnownDown) && !r.isClosing.Load()) {
		r.publish(ctx, core.PolicyDenied{EventHeader: r.buildEventHeader(), CallID: core.ReportToolCallID(ctx), Capability: core.Capability{Kind: kind, Scope: scope}, Rule: err.Error()})
	}
	return resp, err
}

// putToHuman asks the approver and reports its answer to the broker; only a real answer can
// let the effect through (D-10).
func (r *RemoteBroker) putToHuman(ctx context.Context, op core.Operation) error {
	if r.options.Approver == nil {
		return fmt.Errorf("%s %q needs approval and nobody can answer: %w", op.Kind, op.Target, core.ErrDenied)
	}
	decision, err := r.options.Approver.RequestApproval(ctx, op)
	if err != nil {
		return err
	}
	switch decision {
	case core.ApprovalOnce:
		_, err := r.exchange(ctx, Request{Op: OpApproveOnce, Capability: &CapabilityMessage{Kind: string(op.Kind), Scope: op.Target}})
		return err
	case core.ApprovalAlways:
		return r.AddDecisionRule(ctx, core.DecisionRule{Kind: op.Kind, Scope: op.Target, Decision: core.DecisionAllow, Source: decisionRuleSource})
	case core.ApprovalDenied:
	}
	return fmt.Errorf("%s %q was denied by the user: %w", op.Kind, op.Target, core.ErrDenied)
}

func (r *RemoteBroker) publish(ctx context.Context, event core.Event) {
	if r.options.Events == nil {
		return
	}
	r.options.Events.Publish(ctx, event)
}

func (r *RemoteBroker) buildEventHeader() core.EventHeader {
	return core.EventHeader{At: r.options.Clock.ReportCurrentTime()}
}

// exchange sends one request and waits for its response. A ctx that is already done stops it
// before sending; once sent, the exchange runs to completion even if ctx ends, because abandoning
// it mid-frame would leave the stream unusable for every later call. Broker effects are bounded
// local IO, so only a broker that stops answering for brokerExchangeTimeout breaks the connection.
func (r *RemoteBroker) exchange(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.brokenErr != nil {
		// Every refusal repeats the fact: the bus drops events for a full subscriber, and the red
		// bar must not hang on the first report getting through.
		r.publishUnavailableLocked(ctx)
		return Response{}, errors.Join(r.brokenErr, errBrokerKnownDown)
	}

	_ = r.conn.SetDeadline(r.options.Clock.ReportCurrentTime().Add(selectExchangeTimeout(req.Op)))
	r.nextID++
	req.Version, req.ID = ProtocolVersion, r.nextID
	req.IsFromUntrustedStep = core.ReportIsFromUntrustedStep(ctx)
	req.Turn, req.Call = string(core.ReportTurnID(ctx)), string(core.ReportToolCallID(ctx))
	resp, err := r.sendAndReceive(req)
	if errors.Is(err, errFrameTooLarge) {
		return Response{}, err
	}
	if err != nil {
		// A half-sent request or half-read response leaves the stream unusable for everyone.
		r.markBrokenLocked(ctx, err)
		return Response{}, r.brokenErr
	}
	if resp.Error != nil {
		return Response{}, ConvertErrorBody(*resp.Error)
	}
	return resp, nil
}

// selectExchangeTimeout is how long this op may take to answer: bounded local IO for every op
// but run_command, which waits on a child process.
func selectExchangeTimeout(op Op) time.Duration {
	if op == OpRunCommand {
		return commandExchangeTimeout
	}
	return brokerExchangeTimeout
}

func (r *RemoteBroker) sendAndReceive(req Request) (Response, error) {
	if err := WriteRequest(r.conn, req); err != nil {
		return Response{}, err
	}
	resp, err := ReadResponse(r.conn)
	if err != nil {
		return Response{}, err
	}
	if resp.ID != req.ID {
		return Response{}, fmt.Errorf("response id %d for request %d: %w", resp.ID, req.ID, errMalformedFrame)
	}
	return resp, nil
}

func convertRequiredPathInfo(resp Response) (core.PathInfo, error) {
	if resp.Info == nil {
		return core.PathInfo{}, fmt.Errorf("broker response has no path info: %w", core.ErrInvalid)
	}
	return ConvertPathInfoMessage(*resp.Info)
}

// remoteFile streams a file the broker holds open, one read_file chunk at a time.
type remoteFile struct {
	broker  *RemoteBroker
	ctx     context.Context
	handle  uint64
	pending []byte
	isEOF   bool
}

func (f *remoteFile) Read(p []byte) (int, error) {
	for len(f.pending) == 0 {
		if f.isEOF {
			return 0, io.EOF
		}
		resp, err := f.broker.exchange(f.ctx, Request{Op: OpReadFile, Handle: f.handle, MaxBytes: MaxReadChunkBytes})
		if err != nil {
			return 0, err
		}
		f.pending, f.isEOF = resp.Data, resp.IsEOF
	}
	n := copy(p, f.pending)
	f.pending = f.pending[n:]
	return n, nil
}

// Close releases the broker's handle even if the opening context has since ended.
func (f *remoteFile) Close() error {
	_, err := f.broker.exchange(context.WithoutCancel(f.ctx), Request{Op: OpCloseFile, Handle: f.handle})
	return err
}
