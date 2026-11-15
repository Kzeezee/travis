package terminal

import (
	"image/color"

	glamouransi "charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

const (
	themeAuto         = "auto"
	themeDark         = "dark"
	themeLight        = "light"
	themeHighContrast = "high_contrast"
	themeMono         = "mono"
)

var themeNames = []string{themeAuto, themeDark, themeLight, themeHighContrast, themeMono}

// terminalTheme gives every renderer semantic styles without exposing palette choices at call sites.
type terminalTheme struct {
	name                                                                                     string
	userLabel, assistantLabel, dim, thought, rule, success, tool, error, warning, failureBar lipgloss.Style
	selectedRow                                                                              lipgloss.Style
	mascotBody, mascotFace, mascotBeacon, mascotMark                                         lipgloss.Style
	cursor                                                                                   color.Color
	markdown                                                                                 glamouransi.StyleConfig
}

type themePalette struct {
	assistant, user, success, error, warning, dim, rule, contrast string
	mascotBody, mascotFace, mascotBeacon, mascotMark              string
	isLight, isMono                                               bool
}

func buildTerminalTheme(name string) terminalTheme {
	palette := themePalette{
		assistant: "#F4A259", user: "#7FB8C9", success: "#8FBF7F", error: "#EF6461",
		warning: "#E9D27A", dim: "#7D8597", rule: "#3E4451", contrast: "#1C1B19",
		mascotBody: "#FFF4D6", mascotFace: "#8BE8F7", mascotBeacon: "#FF7A1A", mascotMark: "#FFFFFF",
	}
	switch name {
	case themeLight:
		palette = themePalette{
			assistant: "#9A4D00", user: "#006B80", success: "#267300", error: "#B42318",
			warning: "#765B00", dim: "#59636E", rule: "#AAB2BD", contrast: "#FFFFFF", isLight: true,
			mascotBody: "#6B5132", mascotFace: "#007C91", mascotBeacon: "#D94A00", mascotMark: "#1C1B19",
		}
	case themeHighContrast:
		palette = themePalette{
			assistant: "#FFFF00", user: "#00FFFF", success: "#00FF00", error: "#FF3030",
			warning: "#FFFF00", dim: "#FFFFFF", rule: "#FFFFFF", contrast: "#000000",
			mascotBody: "#FFFFFF", mascotFace: "#00FFFF", mascotBeacon: "#FFFF00", mascotMark: "#FFFFFF",
		}
	case themeMono:
		palette.isMono = true
	case themeAuto, themeDark, "":
		name = themeDark
	default:
		name = themeDark
	}

	theme := terminalTheme{name: name}
	if palette.isMono {
		theme.userLabel = lipgloss.NewStyle().Bold(true)
		theme.assistantLabel = lipgloss.NewStyle().Bold(true)
		theme.dim = lipgloss.NewStyle().Faint(true)
		theme.thought = lipgloss.NewStyle().Faint(true).Italic(true)
		theme.rule = lipgloss.NewStyle().Faint(true)
		theme.success = lipgloss.NewStyle()
		theme.tool = lipgloss.NewStyle()
		theme.error = lipgloss.NewStyle().Bold(true)
		theme.warning = lipgloss.NewStyle().Bold(true)
		theme.failureBar = lipgloss.NewStyle().Bold(true).Reverse(true)
		theme.selectedRow = lipgloss.NewStyle().Reverse(true)
		theme.mascotBody = lipgloss.NewStyle()
		theme.mascotFace = lipgloss.NewStyle().Bold(true)
		theme.mascotBeacon = lipgloss.NewStyle().Bold(true)
		theme.mascotMark = lipgloss.NewStyle().Bold(true)
		theme.markdown = prepareMarkdownStyle(glamourstyles.ASCIIStyleConfig, palette)
		return theme
	}

	assistant, user := lipgloss.Color(palette.assistant), lipgloss.Color(palette.user)
	success, failure := lipgloss.Color(palette.success), lipgloss.Color(palette.error)
	warning, dim := lipgloss.Color(palette.warning), lipgloss.Color(palette.dim)
	theme.userLabel = lipgloss.NewStyle().Bold(true).Foreground(user)
	theme.assistantLabel = lipgloss.NewStyle().Bold(true).Foreground(assistant)
	theme.dim = lipgloss.NewStyle().Foreground(dim)
	theme.thought = lipgloss.NewStyle().Foreground(dim).Italic(true)
	theme.rule = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.rule))
	theme.success = lipgloss.NewStyle().Foreground(success)
	theme.tool = lipgloss.NewStyle().Foreground(user)
	theme.error = lipgloss.NewStyle().Foreground(failure)
	theme.warning = lipgloss.NewStyle().Foreground(warning)
	theme.failureBar = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.contrast)).Background(failure)
	theme.selectedRow = lipgloss.NewStyle().Reverse(true).Foreground(assistant)
	theme.mascotBody = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.mascotBody))
	theme.mascotFace = lipgloss.NewStyle().Foreground(lipgloss.Color(palette.mascotFace))
	theme.mascotBeacon = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.mascotBeacon))
	theme.mascotMark = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(palette.mascotMark))
	theme.cursor = assistant

	markdownBase := glamourstyles.DarkStyleConfig
	if palette.isLight {
		markdownBase = glamourstyles.LightStyleConfig
	}
	theme.markdown = prepareMarkdownStyle(markdownBase, palette)
	return theme
}

func prepareMarkdownStyle(style glamouransi.StyleConfig, palette themePalette) glamouransi.StyleConfig {
	noMargin := uint(0)
	style.Document.Margin = &noMargin
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	if !palette.isMono {
		style.Heading.Color = &palette.assistant
		style.H1.Color, style.H1.BackgroundColor = &palette.contrast, &palette.assistant
		style.Code.Color = &palette.user
		style.Link.Color = &palette.user
		style.LinkText.Color = &palette.assistant
	}
	return style
}
