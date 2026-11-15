package terminal

import "strings"

type ReportStatus string

const (
	ReportNeutral     ReportStatus = ""
	ReportCurrent     ReportStatus = "current"
	ReportLoading     ReportStatus = "loading"
	ReportSuccess     ReportStatus = "success"
	ReportWarning     ReportStatus = "warning"
	ReportError       ReportStatus = "error"
	ReportUnavailable ReportStatus = "unavailable"
)

type ReportValueKind string

const (
	ReportValueText       ReportValueKind = ""
	ReportValuePath       ReportValueKind = "path"
	ReportValueIdentifier ReportValueKind = "identifier"
)

type ReportRow struct {
	Label  string
	Value  string
	Detail string
	Status ReportStatus
	Kind   ReportValueKind
}

type ReportSection struct {
	Title string
	Rows  []ReportRow
}

type InspectionReport struct {
	Summary  string
	Sections []ReportSection
	Details  []ReportRow
}

func buildReportLines(report InspectionReport) []string {
	var lines []string
	if report.Summary != "" {
		lines = append(lines, report.Summary)
	}
	for _, section := range report.Sections {
		if section.Title != "" {
			lines = append(lines, section.Title+":")
		}
		for _, row := range section.Rows {
			lines = append(lines, buildReportRowLines(row)...)
		}
	}
	if len(report.Details) > 0 {
		lines = append(lines, "Details:")
		for _, row := range report.Details {
			lines = append(lines, buildReportRowLines(row)...)
		}
	}
	return lines
}

func (m *chatModel) appendReport(report InspectionReport) {
	for _, line := range buildReportLines(report) {
		m.transcript.addNotice(line)
	}
}

func buildReportRowLines(row ReportRow) []string {
	prefix := reportStatusPrefix(row.Status)
	if row.Label != "" && row.Value != "" && row.Kind != ReportValueText {
		lines := []string{prefix + row.Label + ":", "  " + row.Value}
		if row.Detail != "" {
			for detailLine := range strings.SplitSeq(row.Detail, "\n") {
				lines = append(lines, "  "+detailLine)
			}
		}
		return lines
	}
	line := prefix + row.Label
	if row.Value != "" {
		if row.Label != "" {
			line += ": "
		}
		line += row.Value
	}
	lines := []string{line}
	if row.Detail != "" {
		for detailLine := range strings.SplitSeq(row.Detail, "\n") {
			lines = append(lines, "  "+detailLine)
		}
	}
	return lines
}

func reportStatusPrefix(status ReportStatus) string {
	switch status {
	case ReportCurrent, ReportSuccess:
		return "✓ "
	case ReportLoading:
		return "… "
	case ReportWarning:
		return "! "
	case ReportError:
		return "✗ "
	case ReportUnavailable:
		return "○ "
	case ReportNeutral:
		return ""
	}
	return ""
}
