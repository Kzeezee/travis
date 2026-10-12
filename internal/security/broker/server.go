package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/Kzeezee/travis/internal/core"
)

// ServeConnection answers one client's requests on conn, in order, until the client hangs up
// or ctx ends. effects performs each request and is where policy is enforced. A frame that
// cannot be decoded ends the connection, since framing can no longer be trusted after it.
func ServeConnection(ctx context.Context, conn net.Conn, effects core.Broker) error {
	stopClosingOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClosingOnCancel()

	session := &connectionSession{effects: effects, openFiles: map[uint64]io.ReadCloser{}}
	defer session.closeOpenFiles()

	for {
		req, err := ReadRequest(conn)
		switch {
		case err == nil:
			err := WriteResponse(conn, session.answer(ctx, req))
			if errors.Is(err, errFrameTooLarge) {
				// Nothing was written, so refuse just this request and keep serving.
				err = WriteResponse(conn, Response{ID: req.ID, Error: BuildErrorBody(err)})
			}
			if err != nil {
				return reportServeEnd(ctx, err)
			}
		case isFramingError(err):
			return reportServeEnd(ctx, err)
		default:
			// The frame decoded but asked for something unsupported; say so and keep serving.
			if err := WriteResponse(conn, Response{ID: req.ID, Error: BuildErrorBody(err)}); err != nil {
				return reportServeEnd(ctx, err)
			}
		}
	}
}

// isFramingError reports a failure to read a whole frame, as opposed to a frame whose request
// was refused by ReadRequest's version and op checks.
func isFramingError(err error) bool {
	isRefusedRequest := errors.Is(err, core.ErrUnsupported) || errors.Is(err, core.ErrInvalid)
	return errors.Is(err, errMalformedFrame) || !isRefusedRequest
}

// reportServeEnd treats the client hanging up or the session ending as a normal finish.
func reportServeEnd(ctx context.Context, err error) error {
	if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// onceApprover is a broker that can ask, and so can be told the human approved once.
type onceApprover interface {
	ApproveOnce(ctx context.Context, approved core.Capability) error
}

// connectionSession holds the files one client has open, and the listing it is paging through;
// both are only valid on their connection.
type connectionSession struct {
	effects    core.Broker
	openFiles  map[uint64]io.ReadCloser
	nextHandle uint64
	listing    pagedListing
}

// pagedListing is a directory listing too large for one frame, kept so later pages come from the
// same authorized snapshot.
type pagedListing struct {
	path    string
	entries []core.PathInfo
}

// listPageBudgetBytes keeps one list_directory page well inside a frame, whatever the path lengths.
const listPageBudgetBytes = MaxFrameBytes / 2

func (s *connectionSession) answer(ctx context.Context, req Request) Response {
	if req.IsFromUntrustedStep {
		ctx = core.MarkUntrustedStep(ctx)
	}
	resp, err := s.perform(ctx, req)
	if err != nil {
		return Response{ID: req.ID, Error: BuildErrorBody(err)}
	}
	resp.ID = req.ID
	return resp
}

func (s *connectionSession) perform(ctx context.Context, req Request) (Response, error) {
	switch req.Op {
	case OpDescribePath:
		info, err := s.effects.DescribePath(ctx, req.Path)
		if err != nil {
			return Response{}, err
		}
		msg := BuildPathInfoMessage(info)
		return Response{Info: &msg}, nil
	case OpListDirectory:
		return s.listDirectoryPage(ctx, req.Path, req.Offset)
	case OpOpenFile:
		opened, err := s.effects.OpenFile(ctx, req.Path)
		if err != nil {
			return Response{}, err
		}
		s.nextHandle++
		s.openFiles[s.nextHandle] = opened.Content
		msg := BuildPathInfoMessage(opened.Info)
		return Response{Info: &msg, Handle: s.nextHandle}, nil
	case OpReadFile:
		return s.readOpenFile(req.Handle, req.MaxBytes)
	case OpCloseFile:
		content, err := s.selectOpenFile(req.Handle)
		if err != nil {
			return Response{}, err
		}
		delete(s.openFiles, req.Handle)
		return Response{}, content.Close()
	case OpWriteFile:
		info, err := s.effects.WriteFile(ctx, req.Path, req.Data)
		if err != nil {
			return Response{}, err
		}
		msg := BuildPathInfoMessage(info)
		return Response{Info: &msg}, nil
	case OpApproveOnce:
		approver, canApprove := s.effects.(onceApprover)
		if !canApprove {
			return Response{}, fmt.Errorf("this broker never asks, so it has nothing to approve: %w", core.ErrDenied)
		}
		return Response{}, approver.ApproveOnce(ctx, core.Capability{Kind: core.CapabilityKind(req.Capability.Kind), Scope: req.Capability.Scope})
	case OpAddDecisionRule:
		return Response{}, s.effects.AddDecisionRule(ctx, ConvertDecisionRuleMessage(*req.Rule))
	}
	return Response{}, fmt.Errorf("unknown op %q: %w", req.Op, core.ErrInvalid)
}

// listDirectoryPage lists path at offset zero and answers later offsets from that listing, so a
// directory is authorized once however many pages it takes.
func (s *connectionSession) listDirectoryPage(ctx context.Context, path string, offset uint32) (Response, error) {
	if offset == 0 {
		entries, err := s.effects.ListDirectory(ctx, path)
		if err != nil {
			return Response{}, err
		}
		s.listing = pagedListing{path: path, entries: entries}
	} else if s.listing.path != path || int(offset) >= len(s.listing.entries) {
		return Response{}, fmt.Errorf("no listing of %q to continue at %d: %w", path, offset, core.ErrInvalid)
	}

	entries, next := s.listing.entries, int(offset)
	page := []PathInfo{}
	for budget := listPageBudgetBytes; next < len(entries); next++ {
		msg := BuildPathInfoMessage(entries[next])
		size := len(msg.Path) + len(msg.WorkspacePath) + len(msg.Kind) + 64 // field names and headers
		if len(page) > 0 && size > budget {
			break
		}
		budget -= size
		page = append(page, msg)
	}
	if next == len(entries) {
		s.listing = pagedListing{}
		return Response{Entries: page}, nil
	}
	return Response{Entries: page, NextOffset: uint32(next)}, nil //nolint:gosec // next < len(entries), a directory's entry count
}

func (s *connectionSession) readOpenFile(handle uint64, maxBytes uint32) (Response, error) {
	content, err := s.selectOpenFile(handle)
	if err != nil {
		return Response{}, err
	}
	chunk := make([]byte, maxBytes)
	n, err := io.ReadFull(content, chunk)
	switch {
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF):
		return Response{Data: chunk[:n], IsEOF: true}, nil
	case err != nil:
		return Response{}, err
	}
	return Response{Data: chunk[:n]}, nil
}

func (s *connectionSession) selectOpenFile(handle uint64) (io.ReadCloser, error) {
	content, isOpen := s.openFiles[handle]
	if !isOpen {
		return nil, fmt.Errorf("handle %d is not open: %w", handle, core.ErrInvalid)
	}
	return content, nil
}

func (s *connectionSession) closeOpenFiles() {
	for handle, content := range s.openFiles {
		_ = content.Close()
		delete(s.openFiles, handle)
	}
}
