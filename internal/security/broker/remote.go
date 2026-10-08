package broker

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Kzeezee/travis/internal/core"
)

// RemoteBroker forwards every effect to a broker process over one connection (D-18). It fails
// closed: once the connection breaks, every call errors, and nothing falls back to local IO.
type RemoteBroker struct {
	mu     sync.Mutex
	conn   net.Conn
	nextID uint64
	// brokenErr is set by the first transport failure; after it the stream cannot be trusted.
	brokenErr error
}

var _ core.Broker = (*RemoteBroker)(nil)

func BuildRemoteBroker(conn net.Conn) *RemoteBroker {
	return &RemoteBroker{conn: conn}
}

func (r *RemoteBroker) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.brokenErr == nil {
		r.brokenErr = fmt.Errorf("broker connection closed: %w", net.ErrClosed)
	}
	return r.conn.Close()
}

func (r *RemoteBroker) DescribePath(ctx context.Context, path string) (core.PathInfo, error) {
	resp, err := r.exchange(ctx, Request{Op: OpDescribePath, Path: path})
	if err != nil {
		return core.PathInfo{}, err
	}
	return convertRequiredPathInfo(resp)
}

func (r *RemoteBroker) ListDirectory(ctx context.Context, path string) ([]core.PathInfo, error) {
	resp, err := r.exchange(ctx, Request{Op: OpListDirectory, Path: path})
	if err != nil {
		return nil, err
	}
	entries := make([]core.PathInfo, 0, len(resp.Entries))
	for _, msg := range resp.Entries {
		entry, err := ConvertPathInfoMessage(msg)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (r *RemoteBroker) OpenFile(ctx context.Context, path string) (core.OpenedFile, error) {
	resp, err := r.exchange(ctx, Request{Op: OpOpenFile, Path: path})
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

// exchange sends one request and waits for its response, honouring ctx's deadline and cancellation.
func (r *RemoteBroker) exchange(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.brokenErr != nil {
		return Response{}, r.brokenErr
	}

	deadline, _ := ctx.Deadline()
	_ = r.conn.SetDeadline(deadline)
	stopInterruptingOnCancel := context.AfterFunc(ctx, func() { _ = r.conn.SetDeadline(time.Unix(1, 0)) })
	defer stopInterruptingOnCancel()

	r.nextID++
	req.Version, req.ID = ProtocolVersion, r.nextID
	resp, err := r.sendAndReceive(req)
	if err != nil {
		// A half-sent request or half-read response leaves the stream unusable for everyone.
		r.brokenErr = fmt.Errorf("broker unreachable: %w", err)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Response{}, ctxErr
		}
		return Response{}, r.brokenErr
	}
	if resp.Error != nil {
		return Response{}, ConvertErrorBody(*resp.Error)
	}
	return resp, nil
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
