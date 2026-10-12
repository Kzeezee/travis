// Wire protocol between travis and travis-broker (D-19): length-prefixed msgpack frames,
// one Response per Request. Frozen: existing ops, fields and codes never change meaning;
// growth is new ops or fields under a new ProtocolVersion. wire_test.go pins the bytes.

package broker

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/vmihailenco/msgpack/v5"

	"github.com/Kzeezee/travis/internal/core"
)

// ProtocolVersion is carried on every request; a broker refuses any version it does not speak.
// Version 2 added write_file, approve_once, add_decision_rule, the needs_approval error and
// list_directory paging.
const ProtocolVersion uint16 = 2

const (
	// MaxFrameBytes bounds what either side allocates for one message.
	MaxFrameBytes = 1 << 20
	// MaxReadChunkBytes bounds the data one read_file response carries, well inside a frame.
	MaxReadChunkBytes = 256 << 10
	// MaxWriteFileBytes bounds one write_file's content, leaving the rest of a frame for the envelope.
	MaxWriteFileBytes = 512 << 10
	frameHeaderBytes  = 4
)

type Op string

const (
	OpDescribePath  Op = "describe_path"
	OpListDirectory Op = "list_directory"
	// OpOpenFile returns a handle; the client reads it with OpReadFile and releases it with OpCloseFile.
	OpOpenFile  Op = "open_file"
	OpReadFile  Op = "read_file"
	OpCloseFile Op = "close_file"
	// OpWriteFile replaces a file with Data; the bytes must fit in one frame.
	OpWriteFile Op = "write_file"
	// OpApproveOnce and OpAddDecisionRule report the human's answer to a needs_approval error.
	OpApproveOnce     Op = "approve_once"
	OpAddDecisionRule Op = "add_decision_rule"
)

type Request struct {
	Version uint16 `msgpack:"v"`
	// ID is chosen by the client and echoed in the response.
	ID       uint64 `msgpack:"id"`
	Op       Op     `msgpack:"op"`
	Path     string `msgpack:"path,omitempty"`
	Handle   uint64 `msgpack:"handle,omitempty"`
	MaxBytes uint32 `msgpack:"max_bytes,omitempty"`
	Data     []byte `msgpack:"data,omitempty"`
	// Offset asks list_directory for the page starting at a Response.NextOffset.
	Offset uint32 `msgpack:"offset,omitempty"`
	// Capability is what approve_once lets through; Rule is what add_decision_rule persists.
	Capability *CapabilityMessage   `msgpack:"capability,omitempty"`
	Rule       *DecisionRuleMessage `msgpack:"rule,omitempty"`
}

type CapabilityMessage struct {
	Kind  string `msgpack:"kind"`
	Scope string `msgpack:"scope"`
}

type DecisionRuleMessage struct {
	Kind     string    `msgpack:"kind"`
	Scope    string    `msgpack:"scope"`
	Decision string    `msgpack:"decision"`
	Source   string    `msgpack:"source"`
	AddedAt  time.Time `msgpack:"added_at,omitempty"`
}

type Response struct {
	ID    uint64     `msgpack:"id"`
	Error *ErrorBody `msgpack:"error,omitempty"`
	// Info answers describe_path and open_file.
	Info    *PathInfo  `msgpack:"info,omitempty"`
	Entries []PathInfo `msgpack:"entries,omitempty"`
	Handle  uint64     `msgpack:"handle,omitempty"`
	Data    []byte     `msgpack:"data,omitempty"`
	IsEOF   bool       `msgpack:"eof,omitempty"`
	// NextOffset is where list_directory's next page starts; zero means the listing is complete.
	NextOffset uint32 `msgpack:"next_offset,omitempty"`
}

type PathInfo struct {
	Path          string `msgpack:"path"`
	WorkspacePath string `msgpack:"workspace_path"`
	Kind          string `msgpack:"kind"`
}

type ErrorCode string

const (
	ErrorDenied             ErrorCode = "denied"
	ErrorNotFound           ErrorCode = "not_found"
	ErrorIsDirectory        ErrorCode = "is_directory"
	ErrorInvalid            ErrorCode = "invalid"
	ErrorUnsupportedVersion ErrorCode = "unsupported_version"
	// ErrorNeedsApproval comes with Approval: the operation to put to the human.
	ErrorNeedsApproval ErrorCode = "needs_approval"
	// ErrorFailed is any failure without a more specific code; Message carries the detail.
	ErrorFailed ErrorCode = "failed"
)

type ErrorBody struct {
	Code     ErrorCode         `msgpack:"code"`
	Message  string            `msgpack:"message"`
	Approval *OperationMessage `msgpack:"approval,omitempty"`
}

type OperationMessage struct {
	Kind        string `msgpack:"kind"`
	Target      string `msgpack:"target"`
	Explanation string `msgpack:"explanation"`
}

var errMalformedFrame = fmt.Errorf("malformed broker frame: %w", core.ErrInvalid)

// errFrameTooLarge is refused before any byte is written, so the stream stays usable.
var errFrameTooLarge = fmt.Errorf("broker frame too large: %w", core.ErrInvalid)

func WriteRequest(w io.Writer, req Request) error {
	return writeFrame(w, req)
}

// ReadRequest is the broker's trust boundary: it rejects frames that are oversize, carry
// unknown fields, speak another version, or name an unknown op before anything acts on them.
func ReadRequest(r io.Reader) (Request, error) {
	var req Request
	if err := readFrame(r, &req); err != nil {
		return Request{}, err
	}
	if req.Version != ProtocolVersion {
		return req, fmt.Errorf("protocol version %d, want %d: %w", req.Version, ProtocolVersion, core.ErrUnsupported)
	}
	if err := validateRequest(req); err != nil {
		return req, err
	}
	return req, nil
}

func WriteResponse(w io.Writer, resp Response) error {
	return writeFrame(w, resp)
}

func ReadResponse(r io.Reader) (Response, error) {
	var resp Response
	if err := readFrame(r, &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func validateRequest(req Request) error {
	switch req.Op {
	case OpDescribePath, OpListDirectory, OpOpenFile:
		if req.Path == "" {
			return fmt.Errorf("%s needs a path: %w", req.Op, core.ErrInvalid)
		}
	case OpWriteFile:
		if req.Path == "" || len(req.Data) > MaxWriteFileBytes {
			return fmt.Errorf("%s needs a path and at most %d bytes: %w", req.Op, MaxWriteFileBytes, core.ErrInvalid)
		}
	case OpReadFile:
		if req.Handle == 0 || req.MaxBytes == 0 || req.MaxBytes > MaxReadChunkBytes {
			return fmt.Errorf("%s needs a handle and 1..%d max_bytes: %w", req.Op, MaxReadChunkBytes, core.ErrInvalid)
		}
	case OpCloseFile:
		if req.Handle == 0 {
			return fmt.Errorf("%s needs a handle: %w", req.Op, core.ErrInvalid)
		}
	case OpApproveOnce:
		if req.Capability == nil || req.Capability.Kind == "" || req.Capability.Scope == "" {
			return fmt.Errorf("%s needs a capability kind and scope: %w", req.Op, core.ErrInvalid)
		}
	case OpAddDecisionRule:
		if req.Rule == nil {
			return fmt.Errorf("%s needs a rule: %w", req.Op, core.ErrInvalid)
		}
	default:
		return fmt.Errorf("unknown op %q: %w", req.Op, core.ErrInvalid)
	}
	return nil
}

// BuildPathInfoMessage converts a core path description for the wire.
func BuildPathInfoMessage(info core.PathInfo) PathInfo {
	return PathInfo{Path: info.Path, WorkspacePath: info.WorkspacePath, Kind: string(info.Kind)}
}

// ConvertPathInfoMessage validates a wire path description into its core form.
func ConvertPathInfoMessage(msg PathInfo) (core.PathInfo, error) {
	kind := core.PathKind(msg.Kind)
	switch kind {
	case core.PathKindFile, core.PathKindDirectory, core.PathKindSymlink, core.PathKindOther:
	default:
		return core.PathInfo{}, fmt.Errorf("unknown path kind %q: %w", msg.Kind, core.ErrInvalid)
	}
	return core.PathInfo{Path: msg.Path, WorkspacePath: msg.WorkspacePath, Kind: kind}, nil
}

// BuildErrorBody classifies a broker error for the wire; the cause's text travels as Message.
func BuildErrorBody(err error) *ErrorBody {
	var approvalRequired *core.ApprovalRequiredError
	if errors.As(err, &approvalRequired) {
		op := approvalRequired.Operation
		return &ErrorBody{Code: ErrorNeedsApproval, Message: err.Error(), Approval: &OperationMessage{
			Kind: string(op.Kind), Target: op.Target, Explanation: op.Explanation,
		}}
	}
	code := ErrorFailed
	switch {
	case errors.Is(err, core.ErrDenied):
		code = ErrorDenied
	case errors.Is(err, core.ErrNotFound):
		code = ErrorNotFound
	case errors.Is(err, core.ErrIsDirectory):
		code = ErrorIsDirectory
	case errors.Is(err, core.ErrUnsupported):
		code = ErrorUnsupportedVersion
	case errors.Is(err, core.ErrInvalid):
		code = ErrorInvalid
	}
	return &ErrorBody{Code: code, Message: err.Error()}
}

// ConvertErrorBody turns a wire error back into an error carrying the matching core kind. A
// needs_approval without its operation is refused, since there is nothing to ask.
func ConvertErrorBody(body ErrorBody) error {
	if body.Code == ErrorNeedsApproval {
		if body.Approval == nil {
			return fmt.Errorf("broker: %s without an operation: %w", body.Message, core.ErrDenied)
		}
		return &core.ApprovalRequiredError{Operation: core.Operation{
			Kind: core.CapabilityKind(body.Approval.Kind), Target: body.Approval.Target, Explanation: body.Approval.Explanation,
		}}
	}
	kinds := map[ErrorCode]error{
		ErrorDenied:             core.ErrDenied,
		ErrorNotFound:           core.ErrNotFound,
		ErrorIsDirectory:        core.ErrIsDirectory,
		ErrorInvalid:            core.ErrInvalid,
		ErrorUnsupportedVersion: core.ErrUnsupported,
	}
	if kind, isKnown := kinds[body.Code]; isKnown {
		return fmt.Errorf("broker: %s: %w", body.Message, kind)
	}
	return fmt.Errorf("broker: %s", body.Message)
}

func writeFrame(w io.Writer, msg any) error {
	payload, err := msgpack.Marshal(msg)
	if err != nil {
		return err
	}
	if len(payload) > MaxFrameBytes {
		return fmt.Errorf("%d bytes exceeds %d: %w", len(payload), MaxFrameBytes, errFrameTooLarge)
	}
	frame := make([]byte, frameHeaderBytes, frameHeaderBytes+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(len(payload))) //nolint:gosec // bounded by MaxFrameBytes above
	_, err = w.Write(append(frame, payload...))
	return err
}

func readFrame(r io.Reader, msg any) error {
	var header [frameHeaderBytes]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > MaxFrameBytes {
		return fmt.Errorf("frame of %d bytes: %w", size, errMalformedFrame)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return fmt.Errorf("truncated frame: %w", errors.Join(errMalformedFrame, err))
	}

	// bytes.Reader is an io.ByteScanner, so the decoder reads it directly and Len shows what is left.
	payloadReader := bytes.NewReader(payload)
	decoder := msgpack.NewDecoder(payloadReader)
	decoder.DisallowUnknownFields(true)
	if err := decoder.Decode(msg); err != nil {
		return fmt.Errorf("%w: %w", errMalformedFrame, err)
	}
	if payloadReader.Len() > 0 {
		return fmt.Errorf("trailing data in frame: %w", errMalformedFrame)
	}
	return nil
}

// BuildDecisionRuleMessage converts a durable rule for the wire.
func BuildDecisionRuleMessage(rule core.DecisionRule) *DecisionRuleMessage {
	return &DecisionRuleMessage{Kind: string(rule.Kind), Scope: rule.Scope, Decision: string(rule.Decision), Source: rule.Source, AddedAt: rule.AddedAt}
}

// ConvertDecisionRuleMessage is the inverse; the receiving broker still validates the rule.
func ConvertDecisionRuleMessage(msg DecisionRuleMessage) core.DecisionRule {
	return core.DecisionRule{
		Kind: core.CapabilityKind(msg.Kind), Scope: msg.Scope, Decision: core.DecisionOutcome(msg.Decision),
		Source: msg.Source, AddedAt: msg.AddedAt,
	}
}
