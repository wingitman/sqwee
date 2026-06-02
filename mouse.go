package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Screen layout row offsets (see View / renderNormalLayout):
//
//	row 0          title bar
//	row 1          tab bar
//	row 2          content top (panel borders begin here)
//	...            content
//	bottom 2 rows  hint bar (divider + line)
//
// Within a bordered list panel the first selectable item sits 3 rows below the
// panel's top edge: 1 (top border) + 1 (panel title) + 1 (blank line).
const (
	rowTabBar      = 1
	rowContentTop  = 2
	listItemOffset = 3 // rows from panel top to the first list item
	// The schema list adds a filter line, so its items start one row lower.
	schemaListItemOffset = 4
)

// handleMouse routes mouse events: tab-bar clicks switch tabs, wheel events
// scroll the active list/viewport, and clicks inside a left-hand list select
// the item under the pointer.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// A modal swallows mouse input (nothing actionable behind it).
	if m.modal != nil {
		return m, nil
	}

	e := msg.Mouse()

	switch msg.(type) {
	case tea.MouseWheelMsg:
		return m.handleWheel(e)
	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft {
			return m, nil
		}
		return m.handleClick(e)
	}
	return m, nil
}

func (m Model) handleWheel(e tea.Mouse) (tea.Model, tea.Cmd) {
	up := e.Button == tea.MouseWheelUp
	switch m.tab {
	case TabConnections:
		m.moveConnCursor(up)
	case TabSchema:
		m.moveSchemaCursor(up)
		return m, m.ensureColumnsCmd()
	case TabQuery:
		if m.editing {
			var cmd tea.Cmd
			m.editor, cmd = m.editor.Update(tea.MouseWheelMsg(e))
			return m, cmd
		}
		switch {
		case m.scriptFocus:
			m.moveScriptCursor(up)
		case m.resultsFocus:
			// Move the cell cursor and keep it visible.
			if up {
				if m.cellRow > 0 {
					m.cellRow--
				}
			} else if m.queryResult != nil && m.cellRow < len(m.queryResult.Rows)-1 {
				m.cellRow++
			}
			m.renderResults()
		case up:
			m.results.ScrollUp(2)
		default:
			m.results.ScrollDown(2)
		}
	}
	return m, nil
}

func (m Model) handleClick(e tea.Mouse) (tea.Model, tea.Cmd) {
	// Tab bar: pick the tab whose label spans the clicked column.
	if e.Y == rowTabBar {
		if t, ok := m.tabAtColumn(e.X); ok {
			m.tab = t
			return m, nil
		}
	}

	// Left-hand list item clicks.
	switch m.tab {
	case TabConnections:
		if idx, ok := m.listIndexAt(e, len(m.conns)); ok {
			m.connCursor = idx
		}
	case TabSchema:
		if e.X < m.cfg.UI.SidebarWidth {
			// The schema list has an extra filter line before the items.
			rowOffset := e.Y - rowContentTop - schemaListItemOffset
			visible := m.contentHeight() - 5
			if visible < 1 {
				visible = 1
			}
			if idx, ok := m.objectIndexAtRow(visible, rowOffset); ok {
				m.schemaCursor = idx
				return m, m.ensureColumnsCmd()
			}
		}
	case TabQuery:
		// Click inside the built-in editor focuses it and moves the cursor to the
		// nearest available text position.
		if m.editorPointAt(e) {
			m.resultsFocus = false
			m.scriptFocus = false
			m.editing = true
			m.moveEditorCursorToMouse(e)
			return m, m.editor.Focus()
		}

		// Click inside the scripts pane focuses it and selects a script.
		if len(m.scripts) > 0 && e.X < m.cfg.UI.SidebarWidth {
			if idx, ok := m.listIndexAt(e, len(m.scripts)); ok {
				m.scriptFocus = true
				m.scriptCursor = idx
			}
			return m, nil
		}
		// Click inside the results grid focuses it and selects the cell.
		if r, c, ok := m.resultsCellAt(e); ok {
			m.resultsFocus = true
			m.cellRow = r
			m.cellCol = c
			m.renderResults()
			return m, m.ensureColumnsCmd()
		}
	}
	return m, nil
}

// resultsCellAt maps a click to a (row, col) in the results grid, or false if
// the click is outside the data rows. It accounts for the editor box height,
// the results panel chrome, the header rows, and the viewport scroll offset.
func (m Model) resultsCellAt(e tea.Mouse) (int, int, bool) {
	if m.queryResult == nil || len(m.queryResult.Rows) == 0 {
		return 0, 0, false
	}
	xBase := m.queryMainX()
	if e.X < xBase {
		return 0, 0, false
	}

	resultsTop := m.queryResultsTop()
	// Inside the results box: border(1) + title(1) + header(1) + separator(1)
	// before the first data row.
	firstDataRow := resultsTop + 4
	rowInView := e.Y - firstDataRow
	if rowInView < 0 {
		return 0, 0, false
	}
	row := rowInView + m.results.YOffset()
	if row >= len(m.queryResult.Rows) {
		return 0, 0, false
	}

	// Horizontal: map x to a column using the cached widths. Columns are
	// separated by " │ " (3 cols). The panel adds a 1-col left border + nothing
	// else before the first cell (lipgloss border).
	x := e.X - xBase - 1 + m.results.XOffset() // account for border and horizontal scroll
	col := 0
	acc := 0
	for i, w := range m.colWidths {
		end := acc + w
		if x >= acc && x < end {
			col = i
			return row, col, true
		}
		acc = end + 3 // " │ " separator
	}
	// Past the last column → clamp to last.
	if len(m.colWidths) > 0 {
		return row, len(m.colWidths) - 1, true
	}
	return 0, 0, false
}

func (m Model) queryMainX() int {
	if len(m.scripts) == 0 {
		return 0
	}
	// The scripts panel width includes its left/right border around the
	// configured content width.
	return m.cfg.UI.SidebarWidth + 2
}

func (m Model) queryEditorHeight() int {
	contentH := m.contentHeight()
	editorH := contentH * m.cfg.UI.ResultsSplit / 100
	if editorH < 4 {
		editorH = 4
	}
	return editorH
}

func (m Model) queryResultsTop() int {
	// Query editor panel content is title + editor view, then a bordered frame.
	return rowContentTop + m.queryEditorHeight() + 3
}

func (m Model) editorPointAt(e tea.Mouse) bool {
	xBase := m.queryMainX()
	if e.X < xBase || e.X >= m.width {
		return false
	}
	if e.Y < rowContentTop+2 {
		return false
	}
	return e.Y < rowContentTop+2+m.queryEditorHeight()
}

func (m *Model) moveEditorCursorToMouse(e tea.Mouse) {
	row := e.Y - (rowContentTop + 2) + m.editor.ScrollYOffset()
	lines := strings.Split(m.editor.Value(), "\n")
	if len(lines) == 0 {
		lines = []string{""}
	}
	if row < 0 {
		row = 0
	}
	if row >= len(lines) {
		row = len(lines) - 1
	}
	for m.editor.Line() < row {
		m.editor.CursorDown()
	}
	for m.editor.Line() > row {
		m.editor.CursorUp()
	}
	lineNumberWidth := 0
	if m.editor.ShowLineNumbers {
		lineNumberWidth = 4
	}
	col := e.X - m.queryMainX() - 1 - lineNumberWidth
	if col < 0 {
		col = 0
	}
	if col > len([]rune(lines[row])) {
		col = len([]rune(lines[row]))
	}
	m.editor.SetCursorColumn(col)
}

// tabAtColumn returns the tab whose label occupies column x in the tab bar.
// Each label is rendered with horizontal padding of 2 on each side.
func (m Model) tabAtColumn(x int) (Tab, bool) {
	col := 0
	for i, name := range tabNames {
		w := lipgloss.Width(name) + 4 // Padding(0,2)
		if x >= col && x < col+w {
			return Tab(i), true
		}
		col += w
	}
	return 0, false
}

// listIndexAt maps a click to a list index within a left-hand panel, or false
// if the click is outside the item rows / list bounds.
func (m Model) listIndexAt(e tea.Mouse, count int) (int, bool) {
	if e.X >= m.cfg.UI.SidebarWidth {
		return 0, false
	}
	idx := e.Y - rowContentTop - listItemOffset
	if idx < 0 || idx >= count {
		return 0, false
	}
	return idx, true
}

// ── cursor movement helpers (shared with keyboard) ──────────────────────────

func (m *Model) moveConnCursor(up bool) {
	if up {
		if m.connCursor > 0 {
			m.connCursor--
		}
		return
	}
	if m.connCursor < len(m.conns)-1 {
		m.connCursor++
	}
}

func (m *Model) moveSchemaCursor(up bool) {
	if up {
		if m.schemaCursor > 0 {
			m.schemaCursor--
		}
		return
	}
	if m.schemaCursor < len(m.filteredObjects())-1 {
		m.schemaCursor++
	}
}

func (m *Model) moveScriptCursor(up bool) {
	if up {
		if m.scriptCursor > 0 {
			m.scriptCursor--
		}
		return
	}
	if m.scriptCursor < len(m.scripts)-1 {
		m.scriptCursor++
	}
}
