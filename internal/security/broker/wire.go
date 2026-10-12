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

	"github.com/vmihailenco/msgpack/v5"

	"github.com/Kzeezee/travis/internal/core"
)

// ProtocolVersion is carried on every request; a broker refuses any version it does not speak.
const ProtocolVersion uint16 = 1

const (
	// MaxFrameBytes bounds what either side allocates for one message.
	MaxFrameBytes = 1 << 20
	// MaxReadChunkBytes bounds the data one read_file response carries, well inside a frame.
	MaxReadChunkBytes = 256 << 10
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
)

type Request struct {
	Version uint16 `msgpack:"v"`
	// ID is chosen by the client and echoed in the response.
	ID       uint64 `msgpack:"id"`
	Op       Op     `msgpack:"op"`
	Path     string `msgpack:"path,omitempty"`
	Handle   uint64 `msgpack:"handle,omitempty"`
	MaxBytes uint32 `msgpack:"max_bytes,omitempty"`
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
	// ErrorFailed is any failure without a more specific code; Message carries the detail.
	ErrorFailed ErrorCode = "failed"
)

type ErrorBody struct {
	Code    ErrorCode `msgpack:"code"`
	Message string    `msgpack:"message"`
}

var errMalformedFrame = fmt.Errorf("malformed broker frame: %w", core.ErrInvalid)

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
	case OpReadFile:
		if req.Handle == 0 || req.MaxBytes == 0 || req.MaxBytes > MaxReadChunkBytes {
			return fmt.Errorf("%s needs a handle and 1..%d max_bytes: %w", req.Op, MaxReadChunkBytes, core.ErrInvalid)
		}
	case OpCloseFile:
		if req.Handle == 0 {
			return fmt.Errorf("%s needs a handle: %w", req.Op, core.ErrInvalid)
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

// ConvertErrorBody turns a wire error back into an error carrying the matching core kind.
func ConvertErrorBody(body ErrorBody) error {
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
		return fmt.Errorf("broker frame of %d bytes exceeds %d: %w", len(payload), MaxFrameBytes, core.ErrInvalid)
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
