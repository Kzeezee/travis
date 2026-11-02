package terminal

import (
	glamouransi "charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
)

// Lantern palette: travis speaks in warm amber, the user in cool tide, chrome recedes to steel.
// Hues tint glyphs and labels only, never backgrounds, so any terminal background still reads;
// Bubble Tea downsamples them, and every state also carries a glyph or word so none relies on colour.
const (
	lanternHex = "#F4A259"
	tideHex    = "#7FB8C9"
	mossHex    = "#8FBF7F"
	emberHex   = "#EF6461"
	strawHex   = "#E9D27A"
	steelHex   = "#7D8597"
	ruleHex    = "#3E4451"
	inkHex     = "#1C1B19"
)

var (
	lantern = lipgloss.Color(lanternHex)
	tide    = lipgloss.Color(tideHex)
	moss    = lipgloss.Color(mossHex)
	ember   = lipgloss.Color(emberHex)
	straw   = lipgloss.Color(strawHex)
	steel   = lipgloss.Color(steelHex)
	rule    = lipgloss.Color(ruleHex)
	ink     = lipgloss.Color(inkHex)
)

var (
	userLabelStyle      = lipgloss.NewStyle().Bold(true).Foreground(tide)
	assistantLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(lantern)
	dimStyle            = lipgloss.NewStyle().Foreground(steel)
	thoughtStyle        = lipgloss.NewStyle().Foreground(steel).Italic(true)
	ruleStyle           = lipgloss.NewStyle().Foreground(rule)
	successStyle        = lipgloss.NewStyle().Foreground(moss)
	toolStyle           = lipgloss.NewStyle().Foreground(tide)
	errorStyle          = lipgloss.NewStyle().Foreground(ember)
	warningStyle        = lipgloss.NewStyle().Foreground(straw)
	failureBarStyle     = lipgloss.NewStyle().Bold(true).Foreground(ink).Background(ember)
	// Reverse keeps the highlight visible without colour; the lantern foreground becomes its background.
	selectedRowStyle = lipgloss.NewStyle().Reverse(true).Foreground(lantern)
)

// replyMarkdownStyle is glamour's dark style retinted to the palette, with the document margin and
// surrounding blank lines removed so a reply fits beside the label. It is fixed, never
// auto-detected, so frames are reproducible.
var replyMarkdownStyle = func() glamouransi.StyleConfig {
	style := glamourstyles.DarkStyleConfig
	noMargin := uint(0)
	style.Document.Margin = &noMargin
	style.Document.BlockPrefix = ""
	style.Document.BlockSuffix = ""
	lanternColor, tideColor, inkColor := lanternHex, tideHex, inkHex
	style.Heading.Color = &lanternColor
	style.H1.Color, style.H1.BackgroundColor = &inkColor, &lanternColor
	style.Code.Color = &tideColor
	style.Link.Color = &tideColor
	style.LinkText.Color = &lanternColor
	return style
}()
