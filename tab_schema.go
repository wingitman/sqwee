package main

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"main.go/internal/driver"
)

// ── Filtering ─────────────────────────────────────────────────────────────────

// filteredObjects returns the objects matching the current filter (matched
// case-insensitively against the object name and its schema). An empty filter
// returns all objects.
func (m Model) filteredObjects() []driver.DBObject {
	if m.filter == "" {
		return m.objects
	}
	needle := strings.ToLower(m.filter)
	var out []driver.DBObject
	for _, o := range m.objects {
		if strings.Contains(strings.ToLower(o.Name), needle) ||
			strings.Contains(strings.ToLower(o.Schema), needle) {
			out = append(out, o)
		}
	}
	return out
}

// selectedObject returns the object under the cursor in the filtered list, or
// false if the list is empty.
func (m Model) selectedObject() (driver.DBObject, bool) {
	objs := m.filteredObjects()
	if len(objs) == 0 || m.schemaCursor < 0 || m.schemaCursor >= len(objs) {
		return driver.DBObject{}, false
	}
	return objs[m.schemaCursor], true
}

// ── Key handling ──────────────────────────────────────────────────────────────

func (m Model) handleSchemaKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// While the filter input is active, capture typing.
	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filter = ""
			m.clampSchemaCursor()
			return m, m.ensureColumnsCmd()
		case "enter":
			m.filtering = false
			return m, nil
		case "backspace":
			if m.filter != "" {
				m.filter = m.filter[:len(m.filter)-1]
			}
			m.clampSchemaCursor()
			return m, m.ensureColumnsCmd()
		default:
			if msg.Text != "" {
				m.filter += msg.Text
				m.clampSchemaCursor()
				return m, m.ensureColumnsCmd()
			}
			return m, nil
		}
	}

	count := len(m.filteredObjects())
	switch {
	case msg.String() == "/":
		m.filtering = true
		return m, nil
	case keyMatches(msg, m.keys.Up):
		if m.schemaCursor > 0 {
			m.schemaCursor--
		}
		return m, m.ensureColumnsCmd()
	case keyMatches(msg, m.keys.Down):
		if m.schemaCursor < count-1 {
			m.schemaCursor++
		}
		return m, m.ensureColumnsCmd()
	case keyMatches(msg, m.keys.Enter):
		obj, ok := m.selectedObject()
		if m.conn == nil || !ok {
			return m, nil
		}
		// Tables/views → SELECT preview; functions/procedures → definition.
		switch obj.Kind {
		case driver.KindTable, driver.KindView:
			preview := previewSelect(obj, m.activeDriver())
			m.editor.SetValue(preview)
			m.tab = TabQuery
			return m, runQueryCmd(m.conn, preview, m.activeDriver())
		default:
			return m, loadDefinitionCmd(m.conn, obj)
		}
	case keyMatches(msg, m.keys.Refresh):
		if m.conn != nil {
			m.schemaLoading = true
			return m, loadSchemaCmd(m.conn)
		}
	case keyMatches(msg, m.keys.NewItem):
		mod := NewModal(ModalNewObject, "New Object", []ModalField{
			{Label: "Kind", Kind: FieldSelect, Options: []string{"table", "view", "function", "procedure"}},
			{Label: "Name", Placeholder: "my_table"},
		}, m.modalWidth(), m.keys)
		m.modal = &mod
		return m, nil
	}
	return m, nil
}

// clampSchemaCursor keeps the cursor within the bounds of the filtered list.
func (m *Model) clampSchemaCursor() {
	count := len(m.filteredObjects())
	if m.schemaCursor >= count {
		m.schemaCursor = count - 1
	}
	if m.schemaCursor < 0 {
		m.schemaCursor = 0
	}
}

// ensureColumnsCmd loads columns for the currently selected table/view if they
// are not already cached.
func (m Model) ensureColumnsCmd() tea.Cmd {
	obj, ok := m.selectedObject()
	if m.conn == nil || !ok {
		return nil
	}
	if obj.Kind != driver.KindTable && obj.Kind != driver.KindView {
		return nil
	}
	if _, ok := m.colCache[obj.Qualified()]; ok {
		return nil
	}
	return loadColumnsCmd(m.conn, obj)
}

// previewSelect builds a "first N rows" SELECT appropriate for the dialect
// (SQL Server uses TOP; others use LIMIT).
func previewSelect(obj driver.DBObject, driverName string) string {
	if driverName == "mssql" {
		return "SELECT TOP 100 * FROM " + obj.Qualified() + ";"
	}
	return "SELECT * FROM " + obj.Qualified() + " LIMIT 100;"
}

// scaffoldObject returns a starter CREATE statement for the given kind/name.
func scaffoldObject(kind, name string) string {
	switch kind {
	case "table":
		return "CREATE TABLE " + name + " (\n  id INTEGER PRIMARY KEY,\n  name TEXT NOT NULL\n);"
	case "view":
		return "CREATE VIEW " + name + " AS\nSELECT * FROM some_table;"
	case "function":
		return "-- Function definition (dialect-specific)\nCREATE FUNCTION " + name + "() RETURNS void AS $$\nBEGIN\n  -- ...\nEND;\n$$ LANGUAGE plpgsql;"
	case "procedure":
		return "-- Procedure definition (dialect-specific)\nCREATE PROCEDURE " + name + "()\nBEGIN\n  -- ...\nEND;"
	default:
		return "-- " + kind + " " + name
	}
}

// objectIndexAtRow returns the filtered-object index rendered at the given row
// offset (from the first content row of the list area), or false. It mirrors
// the windowed layout produced by schemaListLines so mouse clicks line up.
func (m Model) objectIndexAtRow(visible, rowOffset int) (int, bool) {
	_, objIdx := m.schemaListLinesAndMap(m.filteredObjects(), visible)
	if rowOffset < 0 || rowOffset >= len(objIdx) {
		return 0, false
	}
	if objIdx[rowOffset] < 0 {
		return 0, false
	}
	return objIdx[rowOffset], true
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) viewSchema() string {
	contentH := m.contentHeight()
	sidebarW := m.cfg.UI.SidebarWidth

	var list strings.Builder
	if m.conn == nil {
		list.WriteString(panelTitleStyle.Render("Schema"))
		list.WriteString("\n\n")
		list.WriteString(itemDimStyle.Render("Not connected."))
		list.WriteString("\n\n")
		list.WriteString(itemDimStyle.Render("Go to Connections and press " + m.cfg.Keys.Connect + "."))
		left := panelBlurredStyle.Width(sidebarW).Height(contentH).Render(list.String())
		right := panelBlurredStyle.Width(m.width - sidebarW - 4).Height(contentH).Render("")
		return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}

	title := "Objects"
	if m.activeConn != nil {
		title = m.activeConn.Name
	}
	header := panelTitleStyle.Render(title)
	// Header second line: filter input or a count/hint.
	objs := m.filteredObjects()
	if m.filtering {
		header += "\n" + labelFocusedStyle.Render("/") + valueStyle.Render(m.filter) + dimStyle.Render("▏")
	} else if m.filter != "" {
		header += "\n" + dimStyle.Render("/"+m.filter+"  ("+itoaSimple(len(objs))+" matches, esc clears)")
	} else {
		header += "\n" + dimStyle.Render("/ to filter")
	}
	list.WriteString(header)
	list.WriteString("\n\n")

	if m.schemaLoading {
		list.WriteString(loadingStyle.Render("Loading schema..."))
	} else if len(m.objects) == 0 {
		list.WriteString(itemDimStyle.Render("No objects found."))
	} else if len(objs) == 0 {
		list.WriteString(itemDimStyle.Render("No objects match \"" + m.filter + "\"."))
	} else {
		// Rows available for the list inside the panel: total height minus the
		// top border (1), title (1), filter line (1), blank (1), bottom border (1).
		visible := contentH - 5
		if visible < 1 {
			visible = 1
		}
		lines := m.schemaListLines(objs, visible)
		list.WriteString(strings.Join(lines, "\n"))
	}

	leftStyle := panelBlurredStyle
	if m.modal == nil {
		leftStyle = panelFocusedStyle
	}
	left := leftStyle.Width(sidebarW).Height(contentH).Render(list.String())

	right := m.objectDetailPanel(m.width-sidebarW-4, contentH)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// schemaListLines builds the windowed, group-headed list of object lines.
func (m Model) schemaListLines(objs []driver.DBObject, visible int) []string {
	lines, _ := m.schemaListLinesAndMap(objs, visible)
	return lines
}

// schemaListLinesAndMap returns the rendered list lines plus a parallel slice
// mapping each rendered line to a filtered-object index (or -1 for a group
// header / scroll indicator). The window always keeps the selected object
// visible. Both the view and mouse hit-testing use this so they stay in sync.
func (m Model) schemaListLinesAndMap(objs []driver.DBObject, visible int) ([]string, []int) {
	type line struct {
		text string
		obj  int // index into objs, or -1 for a header
	}
	var all []line
	selLine := 0
	lastSchema := ""
	for i, obj := range objs {
		if obj.Schema != lastSchema && obj.Schema != "" {
			all = append(all, line{text: itemDimStyle.Render(obj.Schema), obj: -1})
			lastSchema = obj.Schema
		}
		badge := lipgloss.NewStyle().Foreground(objectKindColor(string(obj.Kind))).Render(kindBadge(obj.Kind))
		var text string
		if i == m.schemaCursor {
			text = selectedItemStyle.Render("› "+obj.Name) + " " + badge
			selLine = len(all)
		} else {
			text = itemStyle.Render("  " + badge + " " + obj.Name)
		}
		all = append(all, line{text: text, obj: i})
	}

	emit := func(ls []line) ([]string, []int) {
		texts := make([]string, len(ls))
		idx := make([]int, len(ls))
		for i, l := range ls {
			texts[i] = l.text
			idx[i] = l.obj
		}
		return texts, idx
	}

	if len(all) <= visible {
		return emit(all)
	}

	// Window so the selected line stays visible (with context above).
	start := selLine - visible/2
	if start < 0 {
		start = 0
	}
	if start > len(all)-visible {
		start = len(all) - visible
	}
	end := start + visible

	var win []line
	if start > 0 {
		win = append(win, line{text: dimStyle.Render("  ↑ more"), obj: -1})
		start++
	}
	tail := false
	if end < len(all) {
		tail = true
		end--
	}
	for i := start; i < end && i < len(all); i++ {
		win = append(win, all[i])
	}
	if tail {
		win = append(win, line{text: dimStyle.Render("  ↓ more"), obj: -1})
	}
	return emit(win)
}

func (m Model) objectDetailPanel(w, h int) string {
	var b strings.Builder
	b.WriteString(panelTitleStyle.Render("Columns"))
	b.WriteString("\n\n")

	obj, ok := m.selectedObject()
	if !ok || m.conn == nil {
		b.WriteString(itemDimStyle.Render("Select an object."))
		return panelBlurredStyle.Width(w).Height(h).Render(b.String())
	}

	b.WriteString(labelStyle.Render("Object: ") + valueStyle.Render(obj.Qualified()) + "\n")
	b.WriteString(labelStyle.Render("Kind:   ") + valueStyle.Render(string(obj.Kind)) + "\n\n")

	if obj.Kind == driver.KindTable || obj.Kind == driver.KindView {
		cols := m.colCache[obj.Qualified()]
		if len(cols) == 0 {
			b.WriteString(itemDimStyle.Render("Loading columns..."))
		}
		for _, c := range cols {
			key := ""
			if c.Key != "" {
				key = " " + lipgloss.NewStyle().Foreground(colOrange).Render("["+c.Key+"]")
			}
			null := ""
			if !c.Nullable {
				null = dimStyle.Render(" NOT NULL")
			}
			b.WriteString(valueStyle.Render(c.Name) + " " +
				lipgloss.NewStyle().Foreground(colTeal).Render(c.Type) + key + null + "\n")
		}
	} else {
		b.WriteString(itemDimStyle.Render("Press " + m.cfg.Keys.Enter + " to load its definition into the Query tab."))
	}

	return panelBlurredStyle.Width(w).Height(h).Render(b.String())
}

func kindBadge(k driver.ObjectKind) string {
	switch k {
	case driver.KindTable:
		return "▤"
	case driver.KindView:
		return "◫"
	case driver.KindFunction:
		return "ƒ"
	case driver.KindProcedure:
		return "⚙"
	default:
		return "·"
	}
}
