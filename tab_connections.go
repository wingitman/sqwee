package main

import (
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"main.go/internal/driver"
)

// ── Key handling ──────────────────────────────────────────────────────────────

func (m Model) handleConnectionsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case keyMatches(msg, m.keys.Up):
		if m.connCursor > 0 {
			m.connCursor--
		}
	case keyMatches(msg, m.keys.Down):
		if m.connCursor < len(m.conns)-1 {
			m.connCursor++
		}
	case keyMatches(msg, m.keys.Connect), keyMatches(msg, m.keys.Enter):
		if len(m.conns) == 0 {
			return m, nil
		}
		info := m.conns[m.connCursor].info
		// A detected server has no credentials yet — open the New Connection
		// modal pre-filled with what we know instead of failing to connect.
		if info.NeedsCred {
			m.modal = m.newConnectionModalFor(info)
			m.statusMsg = dimStyle.Render("Detected server — add credentials to connect.")
			return m, nil
		}
		m.connecting = true
		m.connErr = ""
		m.statusMsg = loadingStyle.Render("Connecting to " + info.Name + "...")
		return m, connectCmd(info)
	case keyMatches(msg, m.keys.NewItem):
		m.modal = m.newConnectionModal()
		return m, nil
	case keyMatches(msg, m.keys.InitDB):
		return m.startInitWizard()
	case keyMatches(msg, m.keys.CopyItem):
		return m.copySelectedConnectionConfig()
	case keyMatches(msg, m.keys.EditItem):
		if mod := m.editConnectionModal(); mod != nil {
			m.modal = mod
		}
		return m, nil
	case keyMatches(msg, m.keys.DeleteItem):
		m.deleteSelectedConnection()
		return m, nil
	case keyMatches(msg, m.keys.Refresh):
		m.rebuildConnections()
		m.statusMsg = dimStyle.Render("Connections refreshed.")
		return m, nil
	}
	return m, nil
}

// ── Modals ────────────────────────────────────────────────────────────────────

func (m Model) newConnectionModal() *Modal {
	return m.newConnectionModalFor(driver.ConnInfo{Driver: defaultDriverName()})
}

// newConnectionModalFor builds the New Connection modal pre-filled from a
// (possibly partial) ConnInfo — used when adding a detected server.
func (m Model) newConnectionModalFor(info driver.ConnInfo) *Modal {
	drv := info.Driver
	if drv == "" {
		drv = defaultDriverName()
	}
	port := ""
	if info.Port > 0 {
		port = strconv.Itoa(info.Port)
	}
	mod := NewModal(ModalAddConnection, "New Connection", []ModalField{
		{Label: "Name", Placeholder: "local-postgres", Value: info.Name},
		{Label: "Driver", Kind: FieldSelect, Options: driver.Names(), Value: drv},
		{Label: "Host", Placeholder: "localhost (or file path for sqlite)", Value: info.Host},
		{Label: "Port", Placeholder: "5432", Value: port},
		{Label: "User", Placeholder: "postgres", Value: info.User},
		{Label: "Password (env var name)", Placeholder: "PGPASSWORD"},
		{Label: "Database", Placeholder: "mydb", Value: info.Database},
	}, m.modalWidth(), m.keys)
	return &mod
}

func (m Model) editConnectionModal() *Modal {
	if len(m.conns) == 0 || !m.conns[m.connCursor].saved {
		m.statusMsg = dimStyle.Render("Only saved connections can be edited.")
		return nil
	}
	s := m.savedForCursor()
	if s == nil {
		return nil
	}
	port := ""
	if s.Port > 0 {
		port = strconv.Itoa(s.Port)
	}
	mod := NewModal(ModalEditConnection, "Edit Connection", []ModalField{
		{Label: "Name", Value: s.Name},
		{Label: "Driver", Kind: FieldSelect, Options: driver.Names(), Value: s.Driver},
		{Label: "Host", Value: s.Host},
		{Label: "Port", Value: port},
		{Label: "User", Value: s.User},
		{Label: "Password (env var name)", Value: s.PasswordEnv},
		{Label: "Database", Value: s.Database},
	}, m.modalWidth(), m.keys)
	return &mod
}

// handleModalConfirm applies a confirmed modal across all tabs.
func (m Model) handleModalConfirm(msg modalConfirmMsg) (tea.Model, tea.Cmd) {
	switch msg.kind {
	case ModalInitPickDriver, ModalInitPickMode, ModalInitConfigure, ModalInitConfirm:
		return m.handleInitConfirm(msg)

	case ModalAddConnection:
		v := msg.values
		port, _ := strconv.Atoi(strings.TrimSpace(v[3]))
		s := SavedConnection{
			Name:        v[0],
			Driver:      v[1],
			Host:        v[2],
			Port:        port,
			User:        v[4],
			PasswordEnv: v[5],
			Database:    v[6],
		}
		m.data.Connections = append(m.data.Connections, s)
		_ = SaveData(m.data)
		m.rebuildConnections()
		m.statusMsg = successStyle.Render("Connection saved.")
		return m, nil

	case ModalEditConnection:
		v := msg.values
		if s := m.savedForCursor(); s != nil {
			port, _ := strconv.Atoi(strings.TrimSpace(v[3]))
			s.Name = v[0]
			s.Driver = v[1]
			s.Host = v[2]
			s.Port = port
			s.User = v[4]
			s.PasswordEnv = v[5]
			s.Database = v[6]
			_ = SaveData(m.data)
			m.rebuildConnections()
			m.statusMsg = successStyle.Render("Connection updated.")
		}
		return m, nil

	case ModalNewObject:
		// Scaffold a CREATE statement into the query editor.
		v := msg.values
		ddl := scaffoldObject(v[0], v[1])
		m.editor.SetValue(ddl)
		m.tab = TabQuery
		m.statusMsg = dimStyle.Render("Edit the statement, then press " + m.cfg.Keys.RunQuery + " to run.")
		return m, nil

	case ModalExport:
		if m.queryResult == nil {
			return m, nil
		}
		format := exportFormat(msg.values[0])
		path, err := exportResults(*m.queryResult, format)
		if err != nil {
			m.statusMsg = errorStyle.Render("Export failed: " + err.Error())
			return m, nil
		}
		m.statusMsg = successStyle.Render("Exported to " + path)
		return m, openFileExplorerCmd(m.cfg, filepath.Dir(path))
	}
	return m, nil
}

// savedForCursor returns a pointer into m.data.Connections for the selected
// connection, or nil if the selection is not a saved connection.
func (m *Model) savedForCursor() *SavedConnection {
	if len(m.conns) == 0 || !m.conns[m.connCursor].saved {
		return nil
	}
	target := m.conns[m.connCursor].info
	for i := range m.data.Connections {
		s := &m.data.Connections[i]
		if s.Name == target.Name && s.Driver == target.Driver {
			return s
		}
	}
	return nil
}

func (m *Model) deleteSelectedConnection() {
	s := m.savedForCursor()
	if s == nil {
		m.statusMsg = dimStyle.Render("Only saved connections can be deleted.")
		return
	}
	name := s.Name
	out := m.data.Connections[:0]
	for _, c := range m.data.Connections {
		if c.Name == name && c.Driver == s.Driver {
			continue
		}
		out = append(out, c)
	}
	m.data.Connections = out
	_ = SaveData(m.data)
	m.rebuildConnections()
	m.statusMsg = dimStyle.Render("Deleted connection " + name)
}

// copySelectedConnectionConfig copies the selected connection's config to the
// clipboard (password is referenced by env-var name, never the literal secret).
func (m Model) copySelectedConnectionConfig() (tea.Model, tea.Cmd) {
	if len(m.conns) == 0 {
		return m, nil
	}
	info := m.conns[m.connCursor].info
	var b strings.Builder
	b.WriteString("name = " + info.Name + "\n")
	b.WriteString("driver = " + info.Driver + "\n")
	if info.URL != "" {
		b.WriteString("url = " + maskURL(info.URL) + "\n")
	} else {
		if info.Host != "" {
			b.WriteString("host = " + info.Host + "\n")
		}
		if info.Port > 0 {
			b.WriteString("port = " + strconv.Itoa(info.Port) + "\n")
		}
		if info.User != "" {
			b.WriteString("user = " + info.User + "\n")
		}
		if info.Database != "" {
			b.WriteString("database = " + info.Database + "\n")
		}
	}
	if s := m.savedForCursor(); s != nil && s.PasswordEnv != "" {
		b.WriteString("password_env = " + s.PasswordEnv + "\n")
	}
	return m, copyToClipboardCmd(b.String(), "Connection config copied.")
}

func (m Model) modalWidth() int {
	w := m.width * 6 / 10
	if w < 50 {
		w = 50
	}
	if w > 80 {
		w = 80
	}
	return w
}

func defaultDriverName() string {
	names := driver.Names()
	for _, n := range names {
		if n == "postgres" {
			return n
		}
	}
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// ── View ──────────────────────────────────────────────────────────────────────

func (m Model) viewConnections() string {
	contentH := m.contentHeight()
	sidebarW := m.cfg.UI.SidebarWidth

	// Left: connection list.
	var list strings.Builder
	list.WriteString(panelTitleStyle.Render("Connections"))
	list.WriteString("\n\n")
	if len(m.conns) == 0 {
		list.WriteString(itemDimStyle.Render("No connections found."))
		list.WriteString("\n\n")
		list.WriteString(itemDimStyle.Render("Press " + m.cfg.Keys.NewItem + " to add one."))
	}
	for i, c := range m.conns {
		dot := disconnectedDot
		if m.activeConn != nil && m.activeConn.Name == c.info.Name {
			dot = connectedDot
		}
		label := c.info.Name
		if label == "" {
			label = c.info.Driver
		}
		line := dot + " " + label
		if i == m.connCursor {
			list.WriteString(selectedItemStyle.Render("› " + line))
		} else {
			list.WriteString(itemStyle.Render("  " + line))
		}
		list.WriteString("\n")
	}

	leftStyle := panelBlurredStyle
	if m.modal == nil {
		leftStyle = panelFocusedStyle
	}
	left := leftStyle.Width(sidebarW).Height(contentH).Render(list.String())

	// Right: connection details.
	right := m.connDetailPanel(m.width-sidebarW-4, contentH)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func (m Model) connDetailPanel(w, h int) string {
	var b strings.Builder
	b.WriteString(panelTitleStyle.Render("Details"))
	b.WriteString("\n\n")

	if len(m.conns) == 0 {
		b.WriteString(itemDimStyle.Render("Select a connection to see its details."))
		return panelBlurredStyle.Width(w).Height(h).Render(b.String())
	}

	c := m.conns[m.connCursor]
	field := func(label, val string) {
		if val == "" {
			val = itemDimStyle.Render("—")
		}
		b.WriteString(labelStyle.Render(label+": ") + valueStyle.Render(val) + "\n")
	}
	field("Name", c.info.Name)
	field("Driver", c.info.Driver)
	if c.info.URL != "" {
		field("URL", maskURL(c.info.URL))
	} else {
		field("Host", c.info.Host)
		if c.info.Port > 0 {
			field("Port", strconv.Itoa(c.info.Port))
		}
		field("User", c.info.User)
		field("Database", c.info.Database)
	}
	b.WriteString("\n")
	src := c.info.Source
	if c.saved {
		src = "saved (sqwee.json)"
	}
	b.WriteString(labelStyle.Render("Source: ") + itemDimStyle.Render(src) + "\n")
	if c.info.NeedsCred {
		b.WriteString("\n")
		b.WriteString(loadingStyle.Render("Detected server — credentials required."))
		b.WriteString("\n")
		b.WriteString(itemDimStyle.Render("Press " + m.cfg.Keys.Connect + " to add credentials."))
	} else if !c.saved {
		b.WriteString("\n")
		b.WriteString(itemDimStyle.Render("Discovered connection (read-only)."))
		b.WriteString("\n")
		b.WriteString(itemDimStyle.Render("Press " + m.cfg.Keys.Connect + " to connect."))
	}
	if m.connErr != "" {
		b.WriteString("\n\n")
		b.WriteString(errorStyle.Render("Error: " + m.connErr))
	}

	return panelBlurredStyle.Width(w).Height(h).Render(b.String())
}

func (m Model) contentHeight() int {
	h := m.height - titleBarHeight - tabBarHeight - hintBarHeight - 2
	if h < 4 {
		h = 4
	}
	return h
}

// maskURL hides the password component of a connection URL for display.
func maskURL(raw string) string {
	at := strings.Index(raw, "@")
	scheme := strings.Index(raw, "://")
	if scheme < 0 || at < 0 || at < scheme {
		return raw
	}
	creds := raw[scheme+3 : at]
	if i := strings.Index(creds, ":"); i >= 0 {
		creds = creds[:i] + ":****"
	}
	return raw[:scheme+3] + creds + raw[at:]
}
