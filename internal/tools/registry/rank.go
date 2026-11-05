package registry

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Kzeezee/travis/internal/core"
)

// queryWordPattern splits a query into the words scored against each tool's name and
// description; punctuation and case carry no signal here.
var queryWordPattern = regexp.MustCompile(`[a-zA-Z0-9]+`)

// estimateToolSpecTokens is a rough, documented heuristic (~4 characters per token, a
// common rule of thumb) over the bytes a tool's spec actually costs the model: there is no
// tokenizer dependency in this repo, and an estimate is enough to keep the exposed set
// inside budget without being exact.
func estimateToolSpecTokens(spec core.ToolSpec) int {
	size := len(spec.Name) + len(spec.Description) + len(spec.InputSchema)
	return size / 4
}

// rankToolsByQueryOverlap returns the tools sharing at least one of query's words with their own
// name or description, most shared words first; ties keep tools' relative order (sort.SliceStable).
// Pure: no registry state, independently testable.
func rankToolsByQueryOverlap(tools []core.Tool, query string) []core.Tool {
	words := queryWordPattern.FindAllString(strings.ToLower(query), -1)
	scores := make(map[string]int, len(tools))
	ranked := make([]core.Tool, 0, len(tools))
	for _, tool := range tools {
		spec := tool.Describe()
		haystack := strings.ToLower(spec.Name + " " + spec.Description)
		score := 0
		for _, word := range words {
			if strings.Contains(haystack, word) {
				score++
			}
		}
		if score > 0 {
			scores[tool.Name()] = score
			ranked = append(ranked, tool)
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		return scores[ranked[i].Name()] > scores[ranked[j].Name()]
	})
	return ranked
}
