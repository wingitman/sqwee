package main

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ── Key handling ──────────────────────────────────────────────────────────────

func (m Model) handleQueryKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	hasScripts := len(m.scripts) > 0

	// ── Scripts pane focused ──────────────────────────────────────────────
	if m.scriptFocus && hasScripts {
		switch {
		case keyMatches(msg, m.keys.Up):
			if m.scriptCursor > 0 {
				m.scriptCursor--
			}
			return m, nil
		case keyMatches(msg, m.keys.Down):
			if m.scriptCursor < len(m.scripts)-1 {
				m.scriptCursor++
			}
			return m, nil
		case keyMatches(msg, m.keys.Right):
			m.scriptFocus = false
			return m, nil
		case keyMatches(msg, m.keys.Enter):
			s := m.scripts[m.scriptCursor]
			content, err := loadScript(s.Path)
			if err != nil {
				m.statusMsg = errorStyle.Render("Load failed: " + err.Error())
				return m, nil
			}
			m.editor.SetValue(content)
			m.scriptFocus = false
			m.statusMsg = dimStyle.Render("Loaded " + s.Name + " — " + m.cfg.Keys.RunQuery + " to run.")
			return m, nil
		}
		return m, nil
	}

	// ── Results grid focused ──────────────────────────────────────────────
	if m.resultsFocus {
		return m.handleResultsKey(msg)
	}

	// ── Editor (default) focused ──────────────────────────────────────────
	switch {
	case keyMatches(msg, m.keys.TabNext):
		// Move focus into the results grid (if there are results).
		if m.queryResult != nil && len(m.queryResult.Rows) > 0 {
			m.resultsFocus = true
			m.renderResults()
		}
		return m, nil

	case keyMatches(msg, m.keys.Left):
		if hasScripts {
			m.scriptFocus = true
		}
		return m, nil

	case keyMatches(msg, m.keys.Enter):
		m.editing = true
		return m, m.editor.Focus()

	case keyMatches(msg, m.keys.RunQuery):
		if m.conn == nil {
			m.statusMsg = errorStyle.Render("Not connected. Connect from the Connections tab first.")
			return m, nil
		}
		sql := strings.TrimSpace(m.editor.Value())
		if sql == "" {
			return m, nil
		}
		m.statusMsg = loadingStyle.Render("Running query...")
		return m, runQueryCmd(m.conn, sql, m.activeDriver())

	case keyMatches(msg, m.keys.OpenEditor):
		return m, openEditorCmd(m.cfg, m.editor.Value(), ".sql")

	case keyMatches(msg, m.keys.Refresh):
		m.scripts = discoverSQLScripts(m.cfg)
		m.statusMsg = dimStyle.Render("Rescanned scripts.")
		return m, nil

	case keyMatches(msg, m.keys.CopyItem):
		return m, copyToClipboardCmd(m.editor.Value(), "Query copied.")

	case keyMatches(msg, m.keys.Up):
		m.results.ScrollUp(1)
	case keyMatches(msg, m.keys.Down):
		m.results.ScrollDown(1)
	}
	return m, nil
}

// handleResultsKey handles keys when the results grid is focused: cell-cursor
// navigation, selection-mode cycling, copy (CSV), copy-with-headers, and export.
func (m Model) handleResultsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	res := m.queryResult
	if res == nil {
		m.resultsFocus = false
		return m, nil
	}
	nRows := len(res.Rows)
	nCols := len(res.Columns)

	switch {
	case keyMatches(msg, m.keys.TabPrev), keyMatches(msg, m.keys.Escape):
		m.resultsFocus = false
		m.renderResults()
		return m, nil

	case keyMatches(msg, m.keys.TabNext):
		// Tab out of the grid back to the editor.
		m.resultsFocus = false
		m.renderResults()
		return m, nil

	case keyMatches(msg, m.keys.Up):
		if m.cellRow > 0 {
			m.cellRow--
		}
	case keyMatches(msg, m.keys.Down):
		if m.cellRow < nRows-1 {
			m.cellRow++
		}
	case keyMatches(msg, m.keys.Left):
		if m.cellCol > 0 {
			m.cellCol--
		}
	case keyMatches(msg, m.keys.Right):
		if m.cellCol < nCols-1 {
			m.cellCol++
		}

	case keyMatches(msg, m.keys.SelectMode):
		m.selMode = m.selMode.next()
		m.statusMsg = dimStyle.Render("Selection: " + m.selMode.String())

	case keyMatches(msg, m.keys.CopyItem):
		cmd := m.copySelectionCmd(false)
		m.renderResults()
		return m, cmd
	case keyMatches(msg, m.keys.CopyHeaders):
		cmd := m.copySelectionCmd(true)
		m.renderResults()
		return m, cmd

	case keyMatches(msg, m.keys.Export):
		mod := NewModal(ModalExport, "Export Results", []ModalField{
			{Label: "Format", Kind: FieldSelect, Options: []string{"csv", "tsv", "json"}},
		}, m.modalWidth(), m.keys)
		m.modal = &mod
		return m, nil
	}

	m.renderResults()
	return m, nil
}

// copySelectionCmd returns an async command that copies the current results
// selection to the clipboard as CSV.
func (m Model) copySelectionCmd(withHeaders bool) tea.Cmd {
	if m.queryResult == nil {
		return nil
	}
	headers, rows := selectionData(*m.queryResult, m.selMode, m.cellRow, m.cellCol, withHeaders)
	csvText := toCSV(headers, rows, ',')
	hdr := ""
	if withHeaders {
		hdr = " with headers"
	}
	return copyToClipboardCmd(csvText, "Copied "+m.selMode.String()+hdr+" as CSV.")
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) viewQuery() string {
	hasScripts := len(m.scripts) > 0

	// Main column width depends on whether the scripts pane is shown.
	mainW := m.width - 2
	if hasScripts {
		mainW = m.width - m.cfg.UI.SidebarWidth - 4
	}

	editorStyle := panelBlurredStyle
	if m.editing {
		editorStyle = panelFocusedStyle
	}
	editorTitle := panelTitleStyle.Render("Query Editor")
	if m.conn == nil {
		editorTitle += dimStyle.Render("  (not connected)")
	} else if m.editing {
		editorTitle += dimStyle.Render("  (ctrl+s to run · esc to stop editing)")
	} else {
		editorTitle += dimStyle.Render("  (" + m.cfg.Keys.Enter + " to edit · " + m.cfg.Keys.RunQuery + " to run)")
	}
	editorView := m.editor.View()
	if !m.editing {
		editorView = highlightSQLView(editorView)
	}
	editorBox := editorStyle.Width(mainW).Render(
		editorTitle + "\n" + editorView,
	)

	resultsTitle := panelTitleStyle.Render("Results")
	resultsStyle := panelBlurredStyle
	if m.resultsFocus {
		resultsStyle = panelFocusedStyle
		resultsTitle += dimStyle.Render("  (" + m.cfg.Keys.SelectMode + ":select " +
			m.cfg.Keys.CopyItem + ":copy " + m.cfg.Keys.CopyHeaders + ":copy+hdr " +
			m.cfg.Keys.Export + ":export · " + m.cfg.Keys.TabPrev + " to leave)")
	} else if m.queryResult != nil && len(m.queryResult.Rows) > 0 {
		resultsTitle += dimStyle.Render("  (" + m.cfg.Keys.TabNext + " to select cells)")
	}
	resultsBox := resultsStyle.Width(mainW).Render(
		resultsTitle + "\n" + m.resultsBody(),
	)
	mainCol := lipgloss.JoinVertical(lipgloss.Left, editorBox, resultsBox)

	if !hasScripts {
		return mainCol
	}

	// Scripts pane on the left.
	scriptsPane := m.scriptsPane(m.cfg.UI.SidebarWidth, m.contentHeight())
	return lipgloss.JoinHorizontal(lipgloss.Top, scriptsPane, mainCol)
}

// scriptsPane renders the discovered *.sql file list.
func (m Model) scriptsPane(w, h int) string {
	var b strings.Builder
	b.WriteString(panelTitleStyle.Render("Scripts"))
	b.WriteString("\n\n")
	for i, s := range m.scripts {
		if i == m.scriptCursor && m.scriptFocus {
			b.WriteString(selectedItemStyle.Render("› " + s.Name))
		} else if i == m.scriptCursor {
			b.WriteString(itemStyle.Render("› " + s.Name))
		} else {
			b.WriteString(itemStyle.Render("  " + s.Name))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(itemDimStyle.Render(m.cfg.Keys.Enter + " load · " + m.cfg.Keys.Right + " editor"))

	style := panelBlurredStyle
	if m.scriptFocus {
		style = panelFocusedStyle
	}
	return style.Width(w).Height(h).Render(b.String())
}

func (m Model) resultsBody() string {
	switch {
	case m.queryErr != "":
		return errorStyle.Render("Error:\n" + m.queryErr)
	case m.execMsg != "":
		return m.execMsg
	case m.queryResult != nil:
		return m.resultsHeaderView() + "\n" + m.results.View()
	default:
		return itemDimStyle.Render("Run a query to see results here.")
	}
}

// renderResults formats the current QueryResult into the results viewport.
func (m *Model) renderResults() {
	if m.queryResult == nil {
		return
	}
	res := *m.queryResult
	if len(res.Columns) == 0 {
		m.results.SetContent(itemDimStyle.Render("(no columns returned)"))
		return
	}

	avail := m.results.Width()
	if avail <= 0 {
		avail = m.width - 4
	}

	// Compute per-column widths, capped so the table fits.
	widths := make([]int, len(res.Columns))
	for i, c := range res.Columns {
		widths[i] = len(c)
	}
	for _, row := range res.Rows {
		for i, cell := range row {
			if l := displayLen(cell); l > widths[i] {
				widths[i] = l
			}
		}
	}
	const maxCol = 40
	for i := range widths {
		if widths[i] > maxCol {
			widths[i] = maxCol
		}
		if widths[i] < 3 {
			widths[i] = 3
		}
	}
	m.colWidths = widths

	var b strings.Builder

	// Rows. When the results grid is focused, highlight the current selection
	// (and the cursor cell within it).
	divider := tableDividerStyle.Render(" │ ")
	for r, row := range res.Rows {
		var cells []string
		for i := range res.Columns {
			isNull := res.Nulls != nil && r < len(res.Nulls) && i < len(res.Nulls[r]) && res.Nulls[r][i]
			var raw string
			if isNull {
				raw = "NULL"
			} else if i < len(row) {
				raw = row[i]
			}
			padded := padTrunc(raw, widths[i])

			var styled string
			switch {
			case m.resultsFocus && r == m.cellRow && i == m.cellCol:
				styled = tableCurStyle.Render(padded)
			case m.resultsFocus && cellSelected(m.selMode, m.cellRow, m.cellCol, r, i):
				styled = tableSelStyle.Render(padded)
			case isNull:
				styled = tableNullStyle.Render(padded)
			default:
				styled = tableCellStyle.Render(padded)
			}
			cells = append(cells, styled)
		}
		b.WriteString(strings.Join(cells, divider))
		b.WriteString("\n")
	}

	// Footer summary.
	b.WriteString("\n")
	summary := itoaSimple(len(res.Rows)) + " rows"
	if res.Truncated {
		summary += " (truncated)"
	}
	if res.Duration > 0 {
		summary += "  ·  " + res.Duration.Round(time.Millisecond).String()
	}
	if m.resultsFocus {
		summary += "  ·  sel: " + m.selMode.String()
	}
	b.WriteString(dimStyle.Render(summary))

	m.results.SetContent(b.String())
	m.ensureCellVisible()
}

func (m Model) resultsHeaderView() string {
	if m.queryResult == nil || len(m.queryResult.Columns) == 0 || len(m.colWidths) == 0 {
		return ""
	}
	header, sep := m.resultsHeaderLines()
	x := m.results.XOffset()
	w := m.results.Width()
	if w <= 0 {
		w = m.width - 4
	}
	return ansi.Cut(header, x, x+w) + "\n" + ansi.Cut(sep, x, x+w)
}

func (m Model) resultsHeaderLines() (string, string) {
	res := *m.queryResult
	var hdr []string
	for i, c := range res.Columns {
		hdr = append(hdr, tableHeaderStyle.Render(padTrunc(c, m.colWidths[i])))
	}
	var sep []string
	for _, w := range m.colWidths {
		sep = append(sep, strings.Repeat("─", w))
	}
	return strings.Join(hdr, tableDividerStyle.Render(" │ ")), tableDividerStyle.Render(strings.Join(sep, "─┼─"))
}

// ensureCellVisible scrolls the results viewport so the cursor cell stays in
// view in both axes. Header rows are rendered outside the viewport, so the
// vertical target is the data row itself.
func (m *Model) ensureCellVisible() {
	target := m.cellRow
	top := m.results.YOffset()
	h := m.results.Height()
	if h > 0 {
		if target < top {
			m.results.SetYOffset(target)
		} else if target >= top+h {
			m.results.SetYOffset(target - h + 1)
		}
	}

	if len(m.colWidths) == 0 || m.cellCol < 0 || m.cellCol >= len(m.colWidths) {
		return
	}
	start := m.columnStart(m.cellCol)
	end := start + m.colWidths[m.cellCol]
	left := m.results.XOffset()
	w := m.results.Width()
	if w <= 0 {
		return
	}
	if start < left {
		m.results.SetXOffset(start)
	} else if end > left+w {
		m.results.SetXOffset(end - w)
	}
}

func (m Model) columnStart(col int) int {
	start := 0
	for i := 0; i < col && i < len(m.colWidths); i++ {
		start += m.colWidths[i] + 3
	}
	return start
}

// displayLen returns a rune count for width calculations.
func displayLen(s string) int { return len([]rune(s)) }

// padTrunc pads or truncates s to exactly w display columns.
func padTrunc(s string, w int) string {
	r := []rune(s)
	if len(r) > w {
		if w <= 1 {
			return string(r[:w])
		}
		return string(r[:w-1]) + "…"
	}
	return s + strings.Repeat(" ", w-len(r))
}
