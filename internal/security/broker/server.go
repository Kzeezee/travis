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
			if err := WriteResponse(conn, session.answer(ctx, req)); err != nil {
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

// connectionSession holds the files one client has open; handles are only valid on their connection.
type connectionSession struct {
	effects    core.Broker
	openFiles  map[uint64]io.ReadCloser
	nextHandle uint64
}

func (s *connectionSession) answer(ctx context.Context, req Request) Response {
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
		entries, err := s.effects.ListDirectory(ctx, req.Path)
		if err != nil {
			return Response{}, err
		}
		msgs := make([]PathInfo, 0, len(entries))
		for _, entry := range entries {
			msgs = append(msgs, BuildPathInfoMessage(entry))
		}
		return Response{Entries: msgs}, nil
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
