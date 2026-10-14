package registry

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// hintWordPattern splits a hint into the words scored against each tool's name and
// description; punctuation and case carry no signal here.
var hintWordPattern = regexp.MustCompile(`[a-zA-Z0-9]+`)

// estimateToolSpecTokens is a rough, documented heuristic (~4 characters per token, a
// common rule of thumb) over the bytes a tool's spec actually costs the model: there is no
// tokenizer dependency in this repo, and an estimate is enough to keep the exposed set
// inside budget without being exact.
func estimateToolSpecTokens(spec core.ToolSpec) int {
	size := len(spec.Name) + len(spec.Description) + len(spec.InputSchema)
	return size / 4
}

// rankToolsByHintOverlap orders tools by how many of hint's words appear in their own name
// or description, highest first; ties keep tools' relative order (sort.SliceStable), so a
// zero-overlap hint — today's production default, hint="" — leaves tools exactly as List
// already sorted them. Pure: no registry state, independently testable.
func rankToolsByHintOverlap(tools []core.Tool, hint string) []core.Tool {
	ranked := make([]core.Tool, len(tools))
	copy(ranked, tools)
	if hint == "" {
		return ranked
	}

	words := hintWordPattern.FindAllString(strings.ToLower(hint), -1)
	scores := make(map[string]int, len(ranked))
	for _, tool := range ranked {
		spec := tool.Describe()
		haystack := strings.ToLower(spec.Name + " " + spec.Description)
		score := 0
		for _, word := range words {
			if strings.Contains(haystack, word) {
				score++
			}
		}
		scores[tool.Name()] = score
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		return scores[ranked[i].Name()] > scores[ranked[j].Name()]
	})
	return ranked
}
