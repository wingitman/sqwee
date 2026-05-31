package main

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// brandWordmark renders the two-tone "delbysoft" wordmark used across the
// delbysoft family of tools (white "delby" + brand-blue "soft"), followed by
// the app name. Matches teapi/lambit.
func brandWordmark() string {
	delby := titleBrandDelby.Render("delby")
	soft := titleBrandSoft.Render("soft")
	app := titleAppStyle.Render(" / sqwee")
	return delby + soft + app
}

// sqweeASCII is the splash logo shown on the welcome / empty-state screen.
var sqweeASCII = []string{
	"  ___  ___ _   _  _____ ___ ",
	" / __|/ _ \\ | | || __\\ \\ / / ",
	" \\__ \\ (_) | |_| || _| \\ V /  ",
	" |___/\\__\\_\\\\___/ |___| |_|   ",
}

// renderSplash builds the centered welcome screen shown when no connection is
// active: the delbysoft wordmark, the sqwee ASCII art, a tagline and a hint.
func renderSplash(width, height int, hint string) string {
	logoStyle := lipgloss.NewStyle().Foreground(colBrand).Bold(true)

	var b strings.Builder
	b.WriteString(brandWordmark())
	b.WriteString("\n\n")
	for _, line := range sqweeASCII {
		b.WriteString(logoStyle.Render(line))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("a terminal database client"))
	b.WriteString("\n\n")
	b.WriteString(hint)

	block := b.String()

	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		block,
	)
}
