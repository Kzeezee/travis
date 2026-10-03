// Search flow: Invoke → decodeGrepArgs (compiles the pattern, validates the glob) → workspace.resolvePath →
// searchTree walks files in lexical order, skipping hidden entries, non-regular files and escaping symlinks →
// each file is filtered by include, then scanned line by line (content) or until its first match (files) →
// results stop after maxGrepResults → formatGrepResults for the model, describeSearch for the person.

package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	grepToolName    = "grep"
	grepDescription = "Search the workspace; this is also how to find files. " +
		"mode=content (default) returns matching lines as path:line:text. " +
		"mode=files returns the paths of files containing the pattern, or of every file when pattern is empty. " +
		"pattern is an RE2 regular expression; start it with (?i) to ignore case. " +
		"path narrows the search to a folder or file relative to the workspace root. " +
		"include is a glob relative to path, such as **/*.md; a glob without a slash matches file names at any depth. " +
		"Hidden files and folders are skipped. Returned paths are relative to the workspace root."
	grepSchema = `{"type":"object","properties":{` +
		`"pattern":{"type":"string","description":"RE2 regular expression; required in content mode, empty lists files in files mode"},` +
		`"path":{"type":"string","description":"Folder or file to search, relative to the workspace root; defaults to the root"},` +
		`"include":{"type":"string","description":"Glob that file paths must match, relative to path, e.g. **/*.md"},` +
		`"mode":{"type":"string","enum":["content","files"],"description":"content returns matching lines; files returns file paths"}},` +
		`"additionalProperties":false}`

	grepModeContent = "content"
	grepModeFiles   = "files"

	// maxGrepResults counts lines in content mode and files in files mode.
	maxGrepResults   = 200
	maxGrepLineBytes = 300
)

var expectedGrepArgs = map[string]string{
	"pattern": "string (RE2; required in content mode)",
	"path":    `string (folder or file, default ".")`,
	"include": "string (glob, e.g. **/*.md)",
	"mode":    `"content" (default) or "files"`,
}

type GrepTool struct {
	workspace workspaceRoot
}

type grepArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Include string `json:"include,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

// grepQuery is grepArgs validated into the form the search runs on.
type grepQuery struct {
	// matcher is nil only in files mode with an empty pattern, which lists every file.
	matcher *regexp.Regexp
	// include is "" to match every file; a slash-free glob has already been widened to any depth.
	include string
	mode    string
}

type grepMatch struct {
	path string
	// line is 0 in files mode, where only the path is reported.
	line int
	text string
}

type grepResults struct {
	matches     []grepMatch
	fileCount   int
	isTruncated bool
}

func BuildGrepTool(workspaceRoot string) *GrepTool {
	return &GrepTool{workspace: buildWorkspaceRoot(workspaceRoot)}
}

func (t *GrepTool) Name() string {
	return grepToolName
}

func (t *GrepTool) Describe() core.ToolSpec {
	return core.ToolSpec{
		Name:         grepToolName,
		Description:  grepDescription,
		InputSchema:  json.RawMessage(grepSchema),
		Capabilities: []core.Capability{{Kind: core.CapabilityFSRead, Scope: t.workspace.root}},
	}
}

func (t *GrepTool) Invoke(ctx context.Context, arguments json.RawMessage) (core.ToolOutput, error) {
	if err := ctx.Err(); err != nil {
		return core.ToolOutput{}, err
	}

	args, query, err := decodeGrepArgs(arguments)
	if err != nil {
		return buildErrorOutput(errorInvalidArguments, map[string]any{
			"detail":   err.Error(),
			"expected": expectedGrepArgs,
		}), nil
	}

	start, rejection, isAllowed := t.workspace.resolvePath(args.Path, expectedGrepArgs)
	if !isAllowed {
		return rejection, nil
	}

	results, err := t.searchTree(ctx, start, query)
	if err != nil {
		return core.ToolOutput{}, err
	}
	return core.ToolOutput{Content: formatGrepResults(results, query.mode), Display: describeSearch(args, results)}, nil
}

// decodeGrepArgs fills defaults into the returned args, so callers can describe the search as it ran.
func decodeGrepArgs(arguments json.RawMessage) (grepArgs, grepQuery, error) {
	var args grepArgs
	decoder := json.NewDecoder(bytes.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&args); err != nil {
		return grepArgs{}, grepQuery{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return grepArgs{}, grepQuery{}, errors.New("unexpected data after the arguments object")
	}

	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}
	if args.Mode == "" {
		args.Mode = grepModeContent
	}
	if args.Mode != grepModeContent && args.Mode != grepModeFiles {
		return grepArgs{}, grepQuery{}, fmt.Errorf("mode is %q; it must be %q or %q", args.Mode, grepModeContent, grepModeFiles)
	}
	query := grepQuery{mode: args.Mode}

	switch {
	case args.Pattern != "":
		matcher, err := regexp.Compile(args.Pattern)
		if err != nil {
			return grepArgs{}, grepQuery{}, fmt.Errorf("pattern is not a valid RE2 regular expression: %w", err)
		}
		query.matcher = matcher
	case args.Mode == grepModeContent:
		return grepArgs{}, grepQuery{}, errors.New("pattern is required in content mode; use mode=files to list files")
	}

	if include := strings.TrimPrefix(strings.TrimSpace(args.Include), "./"); include != "" {
		// Slash-free globs match file names at any depth, as in ripgrep and .gitignore.
		if !strings.Contains(include, "/") {
			include = "**/" + include
		}
		if !doublestar.ValidatePattern(include) {
			return grepArgs{}, grepQuery{}, fmt.Errorf("include %q is not a valid glob", args.Include)
		}
		query.include = include
	}
	return args, query, nil
}

// searchTree returns a Go error only when the context ends; a file it cannot read is skipped.
func (t *GrepTool) searchTree(ctx context.Context, start string, query grepQuery) (grepResults, error) {
	var results grepResults
	err := filepath.WalkDir(start, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return nil
		}
		if path != start && strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !t.isSearchableFile(path, entry) || !matchesInclude(query.include, start, path) {
			return nil
		}

		shown := t.workspace.reportDisplayPath(path, path)
		if err := searchFile(ctx, path, shown, query, &results); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if results.isTruncated {
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return grepResults{}, err
	}
	return results, nil
}

// isSearchableFile admits regular files, and symlinks to regular files inside the workspace;
// symlinked folders are never entered, so a link cycle cannot trap the walk.
func (t *GrepTool) isSearchableFile(path string, entry fs.DirEntry) bool {
	if entry.Type()&fs.ModeSymlink == 0 {
		return entry.Type().IsRegular()
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil || !t.workspace.containsResolvedPath(target) {
		return false
	}
	info, err := os.Stat(target)
	return err == nil && info.Mode().IsRegular()
}

func matchesInclude(include, start, path string) bool {
	if include == "" {
		return true
	}
	rel, err := filepath.Rel(start, path)
	if err != nil {
		return false
	}
	// When the search path is itself a file, its name is all there is to match.
	if rel == "." {
		rel = filepath.Base(path)
	}
	isMatch, err := doublestar.Match(include, filepath.ToSlash(rel))
	return err == nil && isMatch
}

// searchFile adds this file's matches to results; binary files are skipped like unreadable ones.
func searchFile(ctx context.Context, path, shown string, query grepQuery, results *grepResults) error {
	if query.matcher == nil {
		results.addMatch(grepMatch{path: shown}, true)
		return nil
	}

	// path is confined to the workspace by resolvePath and isSearchableFile; replaced by Broker in Phase 2
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, readBufferBytes)
	head, err := reader.Peek(binaryScanBytes)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return nil
	}

	isFirstMatch := true
	for line := 1; ; line++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		text, readErr := readCappedLine(reader)
		if errors.Is(readErr, io.EOF) && len(text) == 0 {
			return nil
		}
		if query.matcher.Match(text) {
			if query.mode == grepModeFiles {
				results.addMatch(grepMatch{path: shown}, true)
				return nil
			}
			if !results.addMatch(grepMatch{path: shown, line: line, text: buildShownLine(text)}, isFirstMatch) {
				return nil
			}
			isFirstMatch = false
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

// readCappedLine returns the next line without its ending, keeping only the first readBufferBytes
// of an oversize line, so memory stays bounded even for a minified file on one line.
func readCappedLine(reader *bufio.Reader) ([]byte, error) {
	fragment, err := reader.ReadSlice('\n')
	line := bytes.Clone(fragment)
	for errors.Is(err, bufio.ErrBufferFull) {
		_, err = reader.ReadSlice('\n')
	}
	line = bytes.TrimSuffix(line, []byte("\n"))
	return bytes.TrimSuffix(line, []byte("\r")), err
}

func buildShownLine(text []byte) string {
	if len(text) <= maxGrepLineBytes {
		return string(text)
	}
	return cutAtRuneBoundary(text, maxGrepLineBytes) + "…"
}

// addMatch reports false once the result cap is exceeded, which ends the search.
func (r *grepResults) addMatch(match grepMatch, isNewFile bool) bool {
	if len(r.matches) == maxGrepResults {
		r.isTruncated = true
		return false
	}
	r.matches = append(r.matches, match)
	if isNewFile {
		r.fileCount++
	}
	return true
}

func formatGrepResults(results grepResults, mode string) string {
	if len(results.matches) == 0 {
		if mode == grepModeFiles {
			return "[no files found]"
		}
		return "[no matches]"
	}

	var output strings.Builder
	for i, match := range results.matches {
		if i > 0 {
			output.WriteByte('\n')
		}
		if mode == grepModeFiles {
			output.WriteString(match.path)
			continue
		}
		fmt.Fprintf(&output, "%s:%d:%s", match.path, match.line, match.text)
	}
	if results.isTruncated {
		fmt.Fprintf(&output, "\n[stopped after %d results; narrow pattern, path or include to see the rest]", maxGrepResults)
	}
	return output.String()
}

// describeSearch is the card summary, e.g. `"TODO" in notes (*.md): 7 matches in 3 files`.
func describeSearch(args grepArgs, results grepResults) string {
	subject := "files"
	if args.Pattern != "" {
		subject = fmt.Sprintf("%q", args.Pattern)
	}
	if args.Path != "." {
		subject += " in " + args.Path
	}
	if args.Include != "" {
		subject += " (" + args.Include + ")"
	}

	more := ""
	if results.isTruncated {
		more = "+"
	}
	if args.Mode == grepModeFiles {
		return fmt.Sprintf("%s: %d%s %s", subject, results.fileCount, more, pluralize(results.fileCount, "file", "files"))
	}
	return fmt.Sprintf("%s: %d%s %s in %d %s", subject, len(results.matches), more,
		pluralize(len(results.matches), "match", "matches"), results.fileCount, pluralize(results.fileCount, "file", "files"))
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
