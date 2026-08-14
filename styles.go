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
	itemStyle        = lipgloss.NewStyle().Foreground(colWhite)
	itemDimStyle     = lipgloss.NewStyle().Foreground(colGray)
	schemaGroupStyle = lipgloss.NewStyle().Foreground(colOrange).Bold(true)

	labelStyle        = lipgloss.NewStyle().Foreground(colGray)
	labelFocusedStyle = lipgloss.NewStyle().Foreground(colTeal).Bold(true)
	valueStyle        = lipgloss.NewStyle().Foreground(colWhite)
	sqlKeywordStyle   = lipgloss.NewStyle().Foreground(colPurple).Bold(true)
	sqlCommentStyle   = lipgloss.NewStyle().Foreground(colGray).Italic(true)
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
	hintLabelStyle   = lipgloss.NewStyle().Foreground(colOrange).Bold(true)
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

// Selector renders the active theme-picker row marker.
var Selector = lipgloss.NewStyle().Foreground(colWhite).Bold(true)

// ConfigureTheme applies a complete semantic palette. Terminal mode omits
// explicit colors so the terminal's normal foreground and background inherit.
func ConfigureTheme(colors map[string]string, terminal bool) {
	colPurple = themedColor(colors, terminal, "primary", "#7D56F4")
	colBrand = themedColor(colors, terminal, "brand_secondary", "#5865F2")
	colTeal = themedColor(colors, terminal, "accent", "#00D7AF")
	colOrange = themedColor(colors, terminal, "clipboard", "#F0A500")
	colRed = themedColor(colors, terminal, "error", "#FF4672")
	colGreen = themedColor(colors, terminal, "success", "#2ECC71")
	colWhite = themedColor(colors, terminal, "foreground", "#FAFAFA")
	colGray = themedColor(colors, terminal, "muted", "#888888")
	colDimGray = themedColor(colors, terminal, "muted", "#444444")
	colBorder = themedColor(colors, terminal, "border", "#333355")

	dimStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "muted", "#888888")
	titleBrandDelby = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "brand_primary", "#FAFAFA")
	titleBrandSoft = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "brand_secondary", "#5865F2")
	titleAppStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "muted", "#888888")
	tabActiveStyle = themedBackground(themedStyle(lipgloss.NewStyle().Bold(true).Padding(0, 2), colors, terminal, "foreground", "#1a1a1a"), colors, terminal, "accent", "#00D7AF")
	tabInactiveStyle = themedStyle(lipgloss.NewStyle().Padding(0, 2), colors, terminal, "muted", "#888888")
	panelFocusedStyle = themedBorder(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()), colors, terminal, "primary", "#7D56F4")
	panelBlurredStyle = themedBorder(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()), colors, terminal, "border", "#333355")
	panelTitleStyle = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "accent", "#00D7AF")
	selectedItemStyle = themedBackground(themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "selected_foreground", "#FAFAFA"), colors, terminal, "selected_background", "#7D56F4")
	itemStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "foreground", "#FAFAFA")
	itemDimStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "muted", "#888888")
	schemaGroupStyle = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "clipboard", "#F0A500")
	labelStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "muted", "#888888")
	labelFocusedStyle = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "accent", "#00D7AF")
	valueStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "foreground", "#FAFAFA")
	sqlKeywordStyle = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "primary", "#7D56F4")
	sqlCommentStyle = themedStyle(lipgloss.NewStyle().Italic(true), colors, terminal, "muted", "#888888")
	errorStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "error", "#FF4672")
	successStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "success", "#2ECC71")
	loadingStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "clipboard", "#F0A500")

	connectedDot = lipgloss.NewStyle().Foreground(colGreen).Render("●")
	disconnectedDot = lipgloss.NewStyle().Foreground(colDimGray).Render("○")
	errorDot = lipgloss.NewStyle().Foreground(colRed).Render("●")

	tableHeaderStyle = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "accent", "#00D7AF")
	tableCellStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "foreground", "#FAFAFA")
	tableNullStyle = themedStyle(lipgloss.NewStyle().Italic(true), colors, terminal, "muted", "#444444")
	tableRowSelStyle = themedBackground(lipgloss.NewStyle(), colors, terminal, "selected_background", "#222244")
	tableDividerStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "border", "#333355")
	tableSelStyle = themedBackground(themedStyle(lipgloss.NewStyle(), colors, terminal, "selected_foreground", "#FAFAFA"), colors, terminal, "selected_background", "#2A2A55")
	tableCurStyle = themedBackground(themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "foreground", "#1a1a1a"), colors, terminal, "accent", "#00D7AF")
	hintDividerStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "border", "#333355")
	hintStyle = themedStyle(lipgloss.NewStyle(), colors, terminal, "muted", "#888888")
	hintKeyStyle = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "accent", "#00D7AF")
	hintLabelStyle = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "clipboard", "#F0A500")
	modalBorderStyle = themedBorder(lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2), colors, terminal, "primary", "#7D56F4")
	modalTitleStyle = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "primary", "#7D56F4")
	Selector = themedStyle(lipgloss.NewStyle().Bold(true), colors, terminal, "selector", "#FAFAFA")
}

func themedColor(colors map[string]string, terminal bool, key, fallback string) color.Color {
	if value, ok := themedValue(colors, terminal, key, fallback); ok {
		return lipgloss.Color(value)
	}
	return nil
}

func themedStyle(style lipgloss.Style, colors map[string]string, terminal bool, key, fallback string) lipgloss.Style {
	if value, ok := themedValue(colors, terminal, key, fallback); ok {
		return style.Foreground(lipgloss.Color(value))
	}
	return style
}

func themedBackground(style lipgloss.Style, colors map[string]string, terminal bool, key, fallback string) lipgloss.Style {
	if value, ok := themedValue(colors, terminal, key, fallback); ok {
		return style.Background(lipgloss.Color(value))
	}
	return style
}

func themedBorder(style lipgloss.Style, colors map[string]string, terminal bool, key, fallback string) lipgloss.Style {
	if value, ok := themedValue(colors, terminal, key, fallback); ok {
		return style.BorderForeground(lipgloss.Color(value))
	}
	return style
}

func themedValue(colors map[string]string, terminal bool, key, fallback string) (string, bool) {
	if value := colors[key]; value != "" {
		return value, true
	}
	if terminal {
		return "", false
	}
	return fallback, fallback != ""
}
