package core

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// Broker performs file effects on a tool's behalf (D-18). Tools never touch the filesystem
// themselves: a broker resolves the requested path, decides whether the effect may happen,
// and only then performs it. Paths may be relative to the workspace or absolute.
//
// Refusals and failures are errors wrapping ErrDenied, ErrNotFound, ErrIsDirectory or
// ErrInvalid, so a tool can tell the model why without knowing which broker it talks to.
type Broker interface {
	// DescribePath follows symlinks and reports what the path resolves to.
	DescribePath(ctx context.Context, path string) (PathInfo, error)
	// ListDirectory reports a directory's entries sorted by name, without following symlinks.
	ListDirectory(ctx context.Context, path string) ([]PathInfo, error)
	// OpenFile opens a regular file for reading; the caller closes Content.
	OpenFile(ctx context.Context, path string) (OpenedFile, error)
	// AddDecisionRule durably narrows one future Ask verdict to Allow (D-30). Callers must
	// reach this only through a real keypress in the approval modal — see
	// PolicyEnforcedBroker for why a rule can only narrow an Ask.
	AddDecisionRule(ctx context.Context, rule DecisionRule) error
	// WriteFile creates or atomically replaces a regular file inside the workspace, creating parent dirs; never follows a final symlink.
	WriteFile(ctx context.Context, path string, content []byte) (PathInfo, error)
}

type PathKind string

const (
	PathKindFile      PathKind = "file"
	PathKindDirectory PathKind = "directory"
	PathKindSymlink   PathKind = "symlink"
	PathKindOther     PathKind = "other"
)

type PathInfo struct {
	// Path is absolute; it is symlink-resolved except for symlink entries from ListDirectory.
	Path string
	// WorkspacePath is Path relative to the workspace root with forward slashes, so it reads the
	// same on every OS and can be passed straight back as a path argument.
	WorkspacePath string
	Kind          PathKind
}

type OpenedFile struct {
	Info    PathInfo
	Content io.ReadCloser
}

var (
	// ErrDenied means the broker refused the effect.
	ErrDenied = errors.New("denied")
	// ErrNotFound means the path does not exist.
	ErrNotFound = errors.New("not_found")
	// ErrIsDirectory means a file operation was asked of a directory.
	ErrIsDirectory = errors.New("is_directory")
)

// ApprovalRequiredError means policy evaluated the effect to Ask: it is neither allowed nor
// refused until a human answers, so it is not an ErrDenied. It carries the Operation a client
// must put to the human before retrying (§6.4).
type ApprovalRequiredError struct {
	Operation Operation
}

func (e *ApprovalRequiredError) Error() string {
	return fmt.Sprintf("%s %q needs approval", e.Operation.Kind, e.Operation.Target)
}
