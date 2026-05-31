package main

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// ─── Palette ────────────────────────────────────────────────────────────────
// Shared with the rest of the delbysoft family (teapi/lambit) so sqwee feels
// like a sibling tool. Brand blue (#5865F2) is the "soft" half of the wordmark.
var (
	colPurple  = lipgloss.Color("#7D56F4")
	colBrand   = lipgloss.Color("#5865F2") // delbysoft brand blue
	colTeal    = lipgloss.Color("#00D7AF")
	colOrange  = lipgloss.Color("#F0A500")
	colRed     = lipgloss.Color("#FF4672")
	colGreen   = lipgloss.Color("#2ECC71")
	colWhite   = lipgloss.Color("#FAFAFA")
	colGray    = lipgloss.Color("#888888")
	colDimGray = lipgloss.Color("#444444")
	colBorder  = lipgloss.Color("#333355")
)

// ─── App chrome ─────────────────────────────────────────────────────────────
var (
	dimStyle = lipgloss.NewStyle().Foreground(colGray)

	titleBrandDelby = lipgloss.NewStyle().Foreground(colWhite).Bold(true)
	titleBrandSoft  = lipgloss.NewStyle().Foreground(colBrand).Bold(true)
	titleAppStyle   = lipgloss.NewStyle().Foreground(colGray)

	tabActiveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#1a1a1a")).
			Background(colTeal).
			Bold(true).
			Padding(0, 2)
	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(colGray).
				Padding(0, 2)
)

// ─── Panels ─────────────────────────────────────────────────────────────────
var (
	panelFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colPurple)
	panelBlurredStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colBorder)

	panelTitleStyle = lipgloss.NewStyle().Foreground(colTeal).Bold(true)

	selectedItemStyle = lipgloss.NewStyle().
				Foreground(colWhite).
				Background(colPurple).
				Bold(true)
	itemStyle    = lipgloss.NewStyle().Foreground(colWhite)
	itemDimStyle = lipgloss.NewStyle().Foreground(colGray)

	labelStyle        = lipgloss.NewStyle().Foreground(colGray)
	labelFocusedStyle = lipgloss.NewStyle().Foreground(colTeal).Bold(true)
	valueStyle        = lipgloss.NewStyle().Foreground(colWhite)
)

// ─── Status / feedback ──────────────────────────────────────────────────────
var (
	errorStyle   = lipgloss.NewStyle().Foreground(colRed)
	successStyle = lipgloss.NewStyle().Foreground(colGreen)
	loadingStyle = lipgloss.NewStyle().Foreground(colOrange)

	connectedDot    = lipgloss.NewStyle().Foreground(colGreen).Render("●")
	disconnectedDot = lipgloss.NewStyle().Foreground(colDimGray).Render("○")
	errorDot        = lipgloss.NewStyle().Foreground(colRed).Render("●")
)

// ─── Results table ──────────────────────────────────────────────────────────
var (
	tableHeaderStyle = lipgloss.NewStyle().
				Foreground(colTeal).
				Bold(true)
	tableCellStyle    = lipgloss.NewStyle().Foreground(colWhite)
	tableNullStyle    = lipgloss.NewStyle().Foreground(colDimGray).Italic(true)
	tableRowSelStyle  = lipgloss.NewStyle().Background(lipgloss.Color("#222244"))
	tableDividerStyle = lipgloss.NewStyle().Foreground(colBorder)
	// Cell within the active selection (dim) and the single cursor cell (bold).
	tableSelStyle = lipgloss.NewStyle().Foreground(colWhite).Background(lipgloss.Color("#2A2A55"))
	tableCurStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1a1a")).Background(colTeal).Bold(true)
)

// ─── Hint bar ───────────────────────────────────────────────────────────────
var (
	hintDividerStyle = lipgloss.NewStyle().Foreground(colBorder)
	hintStyle        = lipgloss.NewStyle().Foreground(colGray)
	hintKeyStyle     = lipgloss.NewStyle().Foreground(colTeal).Bold(true)
)

// ─── Modal ──────────────────────────────────────────────────────────────────
var (
	modalBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(colPurple).
				Padding(1, 2)
	modalTitleStyle = lipgloss.NewStyle().Foreground(colPurple).Bold(true)
)

// objectKindColor maps a schema-object kind to a colour for the schema tree.
func objectKindColor(kind string) color.Color {
	switch kind {
	case "table":
		return colTeal
	case "view":
		return colBrand
	case "function":
		return colPurple
	case "procedure":
		return colOrange
	default:
		return colGray
	}
}
