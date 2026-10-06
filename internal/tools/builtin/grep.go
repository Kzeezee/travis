// Search flow: Invoke → decodeGrepArgs (content: compiles the regex; files: turns pattern into a name glob) →
// workspace.resolvePath → searchTree walks files in lexical order, skipping hidden entries, non-regular files and
// escaping symlinks → each file is filtered by its globs, then listed (files) or scanned line by line (content) →
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
	"regexp/syntax"
	"slices"
	"strings"
	"unicode"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/Kzeezee/travis/internal/core"
)

const (
	grepToolName    = "grep"
	grepDescription = "Search the workspace; this is also how to find and list files. " +
		"mode=content (default): pattern is an RE2 regular expression matched against file contents, " +
		"and matching lines come back as path:line:text. " +
		"mode=files: pattern matches file names, not contents, and matching paths come back one per line; " +
		"words without wildcards match names containing them in order (clinic confirm finds clinic-confirmation.md), " +
		"a glob such as **/*.md works too, and an empty pattern lists every file under path. " +
		"Both modes ignore case unless pattern contains an uppercase letter. " +
		"path narrows either mode to a folder or file relative to the workspace root. " +
		"include is an extra glob that file paths must match, such as *.md. " +
		"Globs are relative to path; one without a slash matches names at any depth. " +
		"Hidden files and folders are skipped. Returned paths are relative to the workspace root."
	grepSchema = `{"type":"object","properties":{` +
		`"pattern":{"type":"string","description":"content mode: RE2 regex over file contents (required). files mode: file-name word or glob; empty lists every file"},` +
		`"path":{"type":"string","description":"Folder or file to search, relative to the workspace root; defaults to the root"},` +
		`"include":{"type":"string","description":"Glob that file paths must match, relative to path, e.g. **/*.md"},` +
		`"mode":{"type":"string","enum":["content","files"],"description":"content searches inside files; files finds or lists files by name"}},` +
		`"additionalProperties":false}`

	grepModeContent = "content"
	grepModeFiles   = "files"

	// maxGrepResults counts lines in content mode and files in files mode.
	maxGrepResults   = 200
	maxGrepLineBytes = 300
)

var expectedGrepArgs = map[string]string{
	"pattern": "string (content mode: RE2 regex, required; files mode: name word or glob)",
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
	mode string
	// contentMatcher is set only in content mode.
	contentMatcher *regexp.Regexp
	// pathGlobs must all match a file's path relative to the search path; none means every file.
	pathGlobs []pathGlob
}

type pathGlob struct {
	glob string
	// isCaseInsensitive is set by smart case: the glob was written without uppercase letters.
	isCaseInsensitive bool
}

type grepMatch struct {
	path string
	// line is 0 in files mode, where only the path is reported.
	line int
	text string
}

type grepResults struct {
	matches     []grepMatch
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
	case args.Mode == grepModeContent && args.Pattern == "":
		return grepArgs{}, grepQuery{}, errors.New("pattern is required in content mode; use mode=files to list files")
	case args.Mode == grepModeContent:
		matcher, err := compileSmartCaseRegexp(args.Pattern)
		if err != nil {
			return grepArgs{}, grepQuery{}, fmt.Errorf("pattern is not a valid RE2 regular expression: %w", err)
		}
		query.contentMatcher = matcher
	case strings.TrimSpace(args.Pattern) != "":
		// Bare words are substrings of the name in order, as in fd, because that is how models ask
		// for files: "clinic confirmation" has to find clinic-confirmation.md.
		namePattern := strings.TrimSpace(args.Pattern)
		if !strings.ContainsAny(namePattern, "*?[{") {
			namePattern = "*" + strings.Join(strings.Fields(namePattern), "*") + "*"
		}
		glob, err := normalizePathGlob(namePattern)
		if err != nil {
			return grepArgs{}, grepQuery{}, fmt.Errorf("in files mode pattern matches file names: %w", err)
		}
		query.pathGlobs = append(query.pathGlobs, glob)
	}

	if strings.TrimSpace(args.Include) != "" {
		glob, err := normalizePathGlob(args.Include)
		if err != nil {
			return grepArgs{}, grepQuery{}, fmt.Errorf("include: %w", err)
		}
		query.pathGlobs = append(query.pathGlobs, glob)
	}
	return args, query, nil
}

// normalizePathGlob widens a slash-free glob to any depth, as in ripgrep and .gitignore,
// and applies smart case.
func normalizePathGlob(raw string) (pathGlob, error) {
	glob := strings.TrimPrefix(strings.TrimSpace(raw), "./")
	if !strings.Contains(glob, "/") {
		glob = "**/" + glob
	}
	if !doublestar.ValidatePattern(glob) {
		return pathGlob{}, fmt.Errorf("%q is not a valid glob", raw)
	}
	return pathGlob{glob: glob, isCaseInsensitive: strings.ToLower(glob) == glob}, nil
}

// compileSmartCaseRegexp ignores case unless a literal in the pattern is uppercase, as ripgrep's
// --smart-case does; classes such as [A-Z] or \S never count, since models search for words.
func compileSmartCaseRegexp(pattern string) (*regexp.Regexp, error) {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	if !hasUppercaseLiteral(parsed) {
		pattern = "(?i)" + pattern
	}
	return regexp.Compile(pattern)
}

func hasUppercaseLiteral(node *syntax.Regexp) bool {
	if node.Op == syntax.OpLiteral && slices.ContainsFunc(node.Rune, unicode.IsUpper) {
		return true
	}
	for _, sub := range node.Sub {
		if hasUppercaseLiteral(sub) {
			return true
		}
	}
	return false
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
		if entry.IsDir() || !t.isSearchableFile(path, entry) || !matchesPathGlobs(query.pathGlobs, start, path) {
			return nil
		}

		shown := t.workspace.reportDisplayPath(path, path)
		if query.mode == grepModeFiles {
			results.addMatch(grepMatch{path: shown})
		} else if err := searchFileContents(ctx, path, shown, query.contentMatcher, &results); err != nil && ctx.Err() != nil {
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

func matchesPathGlobs(globs []pathGlob, start, path string) bool {
	if len(globs) == 0 {
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
	rel = filepath.ToSlash(rel)
	for _, glob := range globs {
		name := rel
		if glob.isCaseInsensitive {
			name = strings.ToLower(rel)
		}
		if isMatch, err := doublestar.Match(glob.glob, name); err != nil || !isMatch {
			return false
		}
	}
	return true
}

// searchFileContents adds this file's matching lines to results; binary files are skipped like unreadable ones.
func searchFileContents(ctx context.Context, path, shown string, matcher *regexp.Regexp, results *grepResults) error {
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

	for line := 1; ; line++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		text, readErr := readCappedLine(reader)
		if errors.Is(readErr, io.EOF) && len(text) == 0 {
			return nil
		}
		if matcher.Match(text) {
			if !results.addMatch(grepMatch{path: shown, line: line, text: buildShownLine(text)}) {
				return nil
			}
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
func (r *grepResults) addMatch(match grepMatch) bool {
	if len(r.matches) == maxGrepResults {
		r.isTruncated = true
		return false
	}
	r.matches = append(r.matches, match)
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

// describeSearch is the card summary, e.g. `"TODO" in notes (*.md): 7 results`.
func describeSearch(args grepArgs, results grepResults) string {
	var subject string
	switch {
	case args.Mode == grepModeContent:
		subject = fmt.Sprintf("%q", args.Pattern)
	case args.Pattern != "":
		subject = fmt.Sprintf("files named %q", args.Pattern)
	default:
		subject = "files"
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
	return fmt.Sprintf("%s: %d%s results", subject, len(results.matches), more)
}
