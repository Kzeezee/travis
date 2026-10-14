package core

import "fmt"

// WrapUntrustedContent marks content whose provenance travis cannot vouch for — a tool
// result, a fetched page, a cloned repo's AGENTS.md — so the model can tell it apart from
// travis's own trusted instructions (§5.12.4). source names where it came from for a human
// reading the transcript; it is not a trust level, and every source is wrapped the same way.
func WrapUntrustedContent(source, content string) string {
	return fmt.Sprintf("<!-- BEGIN UNTRUSTED source=%q -->\n%s\n<!-- END UNTRUSTED -->", source, content)
}
