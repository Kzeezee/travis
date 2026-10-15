package loop

import (
	"strings"
	"time"
)

// The prompt names no tools: what is on offer varies by registry and step, and each tool's
// own description says how to use it. These lines only set how to treat tools in general.
const (
	identityPrompt = "You are Travis, a personal assistant running in the user's terminal."
	toolUsePrompt  = "Work from evidence, not memory:\n" +
		"- When a question concerns the user's files or notes, use the tools to look before answering. " +
		"Never invent file names or contents.\n" +
		"- Tool paths are relative to the workspace root. Search for files before reading them instead of guessing paths.\n" +
		"- An empty search is weak evidence: notes rarely use the question's exact words. Before concluding something " +
		"is absent, retry with a single key word, case-insensitive, or related terms.\n" +
		"- Search results show only matching lines. Read the file when the answer may need its surrounding text, " +
		"and follow any pointer it gives to another file or folder.\n" +
		"- If a tool returns an error, read it and correct the call; do not repeat a call that already failed the same way.\n" +
		"- When an answer draws on files, say which files."
	stylePrompt = "Answer concisely in plain text; the terminal does not render Markdown."
)

// buildSystemPrompt gives the date so relative dates ("last week's notes") resolve.
func buildSystemPrompt(now time.Time, skillSections []string) string {
	identity := identityPrompt + " Today is " + now.Format("Monday 2006-01-02") + "."
	sections := []string{identity, toolUsePrompt, stylePrompt}
	sections = append(sections, skillSections...)
	return strings.Join(sections, "\n\n")
}
