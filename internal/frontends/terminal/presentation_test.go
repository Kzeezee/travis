package terminal

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBuildReportLinesKeepsSummarySectionsAndDetailsReadable(t *testing.T) {
	report := InspectionReport{
		Summary: "Instructions Travis can use",
		Sections: []ReportSection{{
			Title: "Active now",
			Rows: []ReportRow{{
				Label: "Travis built-in rules", Value: "Always active", Status: ReportSuccess,
			}},
		}},
		Details: []ReportRow{{
			Label: "Project source", Value: "/a/very/long/path/AGENTS.md", Kind: ReportValuePath,
		}},
	}

	got := buildReportLines(report)
	want := []string{
		"Instructions Travis can use",
		"Active now:",
		"✓ Travis built-in rules: Always active",
		"Details:",
		"Project source:",
		"  /a/very/long/path/AGENTS.md",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("report lines\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestCommandPanelKeepsHeaderAndLegendAtNarrowWidths(t *testing.T) {
	panel := commandPanel{
		title: "provider",
		options: []commandPanelOption{{
			section: "Providers", label: "a-provider-with-a-long-name",
			detail: "Needs an API key before it can connect", value: "provider", status: ReportWarning,
		}},
	}
	const width, height = 32, 8
	rendered := ansi.Strip(renderCommandPanel(panel, width, height, buildTerminalTheme(themeDark)))

	if !strings.Contains(rendered, "/provider") || !strings.Contains(rendered, "enter · esc") ||
		!strings.Contains(rendered, "Needs an API key") || !strings.Contains(rendered, "can connect") {
		t.Fatalf("panel\n%s\nwant header, wrapped detail, and footer legend", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if ansi.StringWidth(line) > width {
			t.Fatalf("line %q is %d cells wide, limit %d", line, ansi.StringWidth(line), width)
		}
	}
}

func TestSemanticNoticeStylesDoNotChangeCopyableText(t *testing.T) {
	notices := []string{
		"✓ Ready",
		"! Needs attention",
		"✗ Failed",
		"Details:",
		"Source: /workspace/AGENTS.md",
	}
	rendered := ansi.Strip(renderNotices(notices, 80, buildTerminalTheme(themeDark)))
	for _, notice := range notices {
		if !strings.Contains(rendered, notice) {
			t.Fatalf("rendered notices\n%s\nmissing %q", rendered, notice)
		}
	}
}
