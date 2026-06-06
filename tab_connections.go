package main

import (
	"net/url"
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
	return m.connectionModal(ModalAddConnection, "New Connection", SavedConnection{Driver: defaultDriverName()})
}

// newConnectionModalFor builds the New Connection modal pre-filled from a
// (possibly partial) ConnInfo — used when adding a detected server.
func (m Model) newConnectionModalFor(info driver.ConnInfo) *Modal {
	return m.connectionModal(ModalAddConnection, "New Connection", savedConnectionFromInfo(info))
}

func savedConnectionFromInfo(info driver.ConnInfo) SavedConnection {
	drv := info.Driver
	if drv == "" {
		drv = defaultDriverName()
	}
	return SavedConnection{
		Name:     info.Name,
		Driver:   drv,
		URL:      info.URL,
		Host:     info.Host,
		Port:     info.Port,
		User:     info.User,
		Database: info.Database,
		Gateway:  savedGatewayFromInfo(info.Gateway),
	}
}

func savedGatewayFromInfo(info driver.GatewayInfo) *SavedGateway {
	if info.Type == "" && info.Host == "" && info.Port == 0 && info.User == "" && info.Password == "" && info.KeyFile == "" {
		return nil
	}
	return &SavedGateway{
		Type:     info.Type,
		Host:     info.Host,
		Port:     info.Port,
		User:     info.User,
		Password: info.Password,
		KeyFile:  info.KeyFile,
	}
}

func (m Model) connectionModal(kind ModalKind, title string, s SavedConnection) *Modal {
	port := ""
	if s.PortEnv != "" {
		port = s.PortEnv
	} else if s.Port > 0 {
		port = strconv.Itoa(s.Port)
	}
	password := s.Password
	if password == "" && s.PasswordEnv != "" {
		password = "env:" + s.PasswordEnv
	}
	drv := s.Driver
	if drv == "" {
		drv = defaultDriverName()
	}
	gwType, gwHost, gwPort, gwUser, gwPassword, gwKeyFile := gatewayModalValues(s.Gateway)
	gwVisible := &FieldCondition{Field: 8, Value: "ssh"}
	mod := NewModal(kind, title, []ModalField{
		{Label: "Name", Placeholder: "local-postgres", Value: s.Name},
		{Label: "Driver", Kind: FieldSelect, Options: driver.Names(), Value: drv},
		{Label: "URL (optional)", Placeholder: "postgres://user:pass@host:5432/db or env-dev:DATABASE_URL", Value: s.URL},
		{Label: "Host", Placeholder: "hostname/IP or env-dev:PGHOST", Value: s.Host},
		{Label: "Port", Placeholder: "5432 or env-dev:PGPORT", Value: port},
		{Label: "User", Placeholder: "postgres or env-dev:PGUSER", Value: s.User},
		{Label: "Password", Placeholder: "literal password or env-dev:PGPASSWORD", Value: password, Password: true},
		{Label: "Database", Placeholder: "mydb or env-dev:PGDATABASE", Value: s.Database},
		{Label: "Gateway", Kind: FieldSelect, Options: []string{"none", "ssh"}, Value: gwType},
		{Label: "Gateway host", Placeholder: "gateway.example.com or env-prod:GATEWAY_HOST", Value: gwHost, VisibleWhen: gwVisible},
		{Label: "Gateway port", Placeholder: "22 or env-prod:GATEWAY_PORT", Value: gwPort, VisibleWhen: gwVisible},
		{Label: "Gateway user", Placeholder: "administrator or env-prod:GATEWAY_USER", Value: gwUser, VisibleWhen: gwVisible},
		{Label: "Gateway password", Placeholder: "literal password/passphrase or env-prod:GATEWAY_PASSWORD", Value: gwPassword, Password: true, VisibleWhen: gwVisible},
		{Label: "Gateway key file", Placeholder: "~/Work/timid/key.pem or env-prod:GATEWAY_KEY_FILE", Value: gwKeyFile, VisibleWhen: gwVisible},
	}, m.modalWidth(), m.keys)
	return &mod
}

func gatewayModalValues(g *SavedGateway) (typ, host, port, user, password, keyFile string) {
	typ = "none"
	if g == nil {
		return typ, "", "", "", "", ""
	}
	if g.Type != "" {
		typ = g.Type
	} else {
		typ = "ssh"
	}
	port = g.PortEnv
	if port == "" && g.Port > 0 {
		port = strconv.Itoa(g.Port)
	}
	return typ, g.Host, port, g.User, g.Password, g.KeyFile
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
	return m.connectionModal(ModalEditConnection, "Edit Connection", *s)
}

// handleModalConfirm applies a confirmed modal across all tabs.
func (m Model) handleModalConfirm(msg modalConfirmMsg) (tea.Model, tea.Cmd) {
	switch msg.kind {
	case ModalInitPickDriver, ModalInitPickMode, ModalInitConfigure, ModalInitConfirm:
		return m.handleInitConfirm(msg)

	case ModalAddConnection:
		s := savedConnectionFromModalValues(msg.values)
		if err := validateSavedConnection(s); err != "" {
			m.modal = m.connectionModal(ModalAddConnection, "New Connection", s)
			m.statusMsg = errorStyle.Render(err)
			return m, nil
		}
		next := AppData{Connections: append([]SavedConnection(nil), m.data.Connections...)}
		next.Connections = append(next.Connections, s)
		if err := SaveData(next); err != nil {
			m.modal = m.connectionModal(ModalAddConnection, "New Connection", s)
			m.statusMsg = errorStyle.Render("Save failed: " + err.Error())
			return m, nil
		}
		m.data = next
		m.rebuildConnections()
		m.statusMsg = successStyle.Render("Connection saved.")
		return m, nil

	case ModalEditConnection:
		if m.savedForCursor() != nil {
			next := savedConnectionFromModalValues(msg.values)
			if err := validateSavedConnection(next); err != "" {
				m.modal = m.connectionModal(ModalEditConnection, "Edit Connection", next)
				m.statusMsg = errorStyle.Render(err)
				return m, nil
			}
			data := AppData{Connections: append([]SavedConnection(nil), m.data.Connections...)}
			target := m.conns[m.connCursor].info
			found := false
			for i := range data.Connections {
				s := data.Connections[i]
				if s.Name == target.Name && s.Driver == target.Driver {
					data.Connections[i] = next
					found = true
					break
				}
			}
			if !found {
				m.statusMsg = errorStyle.Render("Save failed: selected connection was not found.")
				return m, nil
			}
			if err := SaveData(data); err != nil {
				m.modal = m.connectionModal(ModalEditConnection, "Edit Connection", next)
				m.statusMsg = errorStyle.Render("Save failed: " + err.Error())
				return m, nil
			}
			m.data = data
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

func savedConnectionFromModalValues(v []string) SavedConnection {
	port := 0
	portEnv := ""
	value := func(i int) string {
		if i >= len(v) {
			return ""
		}
		return strings.TrimSpace(v[i])
	}
	if rawPort := value(4); rawPort != "" {
		if _, _, ok := splitEnvRef(rawPort); ok {
			portEnv = rawPort
		} else {
			port, _ = strconv.Atoi(rawPort)
		}
	}
	return SavedConnection{
		Name:     value(0),
		Driver:   value(1),
		URL:      value(2),
		Host:     value(3),
		Port:     port,
		PortEnv:  portEnv,
		User:     value(5),
		Password: value(6),
		Database: value(7),
		Gateway:  savedGatewayFromModalValues(v),
	}
}

func savedGatewayFromModalValues(v []string) *SavedGateway {
	value := func(i int) string {
		if i >= len(v) {
			return ""
		}
		return strings.TrimSpace(v[i])
	}
	typ := value(8)
	if typ == "" || typ == "none" {
		return nil
	}
	port := 0
	portEnv := ""
	if rawPort := value(10); rawPort != "" {
		if _, _, ok := splitEnvRef(rawPort); ok {
			portEnv = rawPort
		} else {
			port, _ = strconv.Atoi(rawPort)
		}
	}
	return &SavedGateway{
		Type:     typ,
		Host:     value(9),
		Port:     port,
		PortEnv:  portEnv,
		User:     value(11),
		Password: value(12),
		KeyFile:  value(13),
	}
}

func validateSavedConnection(s SavedConnection) string {
	if hasHTTPScheme(s.Host) {
		return "Host must be a hostname or IP, not an http:// or https:// URL. Use the database endpoint without the scheme."
	}
	if s.Gateway != nil {
		if s.Gateway.Type != "" && s.Gateway.Type != "ssh" {
			return "Gateway type must be none or ssh."
		}
		if strings.TrimSpace(s.Gateway.Host) == "" {
			return "Gateway host is required when gateway routing is enabled."
		}
		if hasHTTPScheme(s.Gateway.Host) {
			return "Gateway host must be a hostname or IP, not an http:// or https:// URL."
		}
		if strings.TrimSpace(s.Gateway.User) == "" {
			return "Gateway user is required when gateway routing is enabled."
		}
	}
	if s.URL == "" {
		return ""
	}
	u, err := url.Parse(s.URL)
	if err != nil || u.Scheme == "" {
		return "Connection URL could not be parsed. Use a database URL such as postgres://user:pass@host:5432/db."
	}
	d := driver.ForScheme(u.Scheme)
	if d == nil {
		return "Connection URL scheme must be a database scheme supported by sqwee, not " + u.Scheme + "."
	}
	if s.Driver != "" && s.Driver != d.Name() {
		return "Connection URL scheme " + u.Scheme + " does not match selected driver " + s.Driver + "."
	}
	return ""
}

func hasHTTPScheme(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
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
	out := make([]SavedConnection, 0, len(m.data.Connections))
	for _, c := range m.data.Connections {
		if c.Name == name && c.Driver == s.Driver {
			continue
		}
		out = append(out, c)
	}
	next := AppData{Connections: out}
	if err := SaveData(next); err != nil {
		m.statusMsg = errorStyle.Render("Delete failed: " + err.Error())
		return
	}
	m.data = next
	m.rebuildConnections()
	m.statusMsg = dimStyle.Render("Deleted connection " + name)
}

// copySelectedConnectionConfig copies the selected connection's config to the
// clipboard. Literal passwords are intentionally omitted; env refs are safe to
// copy because they do not reveal the secret value.
func (m Model) copySelectedConnectionConfig() (tea.Model, tea.Cmd) {
	if len(m.conns) == 0 {
		return m, nil
	}
	if s := m.savedForCursor(); s != nil {
		return m, copyToClipboardCmd(savedConnectionConfigText(*s), "Connection config copied.")
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
	return m, copyToClipboardCmd(b.String(), "Connection config copied.")
}

func savedConnectionConfigText(s SavedConnection) string {
	var b strings.Builder
	b.WriteString("name = " + s.Name + "\n")
	b.WriteString("driver = " + s.Driver + "\n")
	if s.URL != "" {
		b.WriteString("url = " + maskURL(s.URL) + "\n")
	} else {
		if s.Host != "" {
			b.WriteString("host = " + s.Host + "\n")
		}
		if s.PortEnv != "" {
			b.WriteString("port = " + s.PortEnv + "\n")
		} else if s.Port > 0 {
			b.WriteString("port = " + strconv.Itoa(s.Port) + "\n")
		}
		if s.User != "" {
			b.WriteString("user = " + s.User + "\n")
		}
		if s.Database != "" {
			b.WriteString("database = " + s.Database + "\n")
		}
	}
	if _, _, ok := splitEnvRef(s.Password); ok {
		b.WriteString("password = " + s.Password + "\n")
	} else if s.PasswordEnv != "" {
		b.WriteString("password = env:" + s.PasswordEnv + "\n")
	}
	if s.Gateway != nil {
		b.WriteString("gateway.type = " + firstNonEmpty(s.Gateway.Type, "ssh") + "\n")
		if s.Gateway.Host != "" {
			b.WriteString("gateway.host = " + s.Gateway.Host + "\n")
		}
		if s.Gateway.PortEnv != "" {
			b.WriteString("gateway.port = " + s.Gateway.PortEnv + "\n")
		} else if s.Gateway.Port > 0 {
			b.WriteString("gateway.port = " + strconv.Itoa(s.Gateway.Port) + "\n")
		}
		if s.Gateway.User != "" {
			b.WriteString("gateway.user = " + s.Gateway.User + "\n")
		}
		if _, _, ok := splitEnvRef(s.Gateway.Password); ok {
			b.WriteString("gateway.password = " + s.Gateway.Password + "\n")
		}
		if s.Gateway.KeyFile != "" {
			b.WriteString("gateway.key_file = " + s.Gateway.KeyFile + "\n")
		}
	}
	return b.String()
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
	if c.info.Gateway.Type != "" {
		field("Gateway", c.info.Gateway.Type+" @ "+c.info.Gateway.Host)
		if c.info.Gateway.KeyFile != "" {
			field("Gateway key file", c.info.Gateway.KeyFile)
		}
	}
	b.WriteString("\n")
	src := c.info.Source
	if c.saved {
		src = "saved (sqwee.json)"
	}
	b.WriteString(labelStyle.Render("Source: ") + itemDimStyle.Render(src) + "\n")
	if len(m.envSources) > 0 {
		b.WriteString(labelStyle.Render("Env files: ") + itemDimStyle.Render(strings.Join(envSourceNames(m.envSources), ", ")) + "\n")
	}
	if c.saved {
		if s := m.savedForCursor(); s != nil {
			writeEnvDiagnostics(&b, *s, m.envSources)
		}
	}
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

func writeEnvDiagnostics(b *strings.Builder, s SavedConnection, sources []EnvSource) {
	fields := []struct {
		label  string
		value  string
		secret bool
	}{
		{"URL", s.URL, false},
		{"Host", s.Host, false},
		{"Port", s.PortEnv, false},
		{"User", s.User, false},
		{"Password", s.Password, true},
		{"Database", s.Database, false},
	}
	if s.Gateway != nil {
		fields = append(fields,
			struct {
				label  string
				value  string
				secret bool
			}{"Gateway host", s.Gateway.Host, false},
			struct {
				label  string
				value  string
				secret bool
			}{"Gateway port", s.Gateway.PortEnv, false},
			struct {
				label  string
				value  string
				secret bool
			}{"Gateway user", s.Gateway.User, false},
			struct {
				label  string
				value  string
				secret bool
			}{"Gateway password", s.Gateway.Password, true},
			struct {
				label  string
				value  string
				secret bool
			}{"Gateway key file", s.Gateway.KeyFile, false},
		)
	}
	if s.Password == "" && s.PasswordEnv != "" {
		fields = append(fields, struct {
			label  string
			value  string
			secret bool
		}{"Password", "env:" + s.PasswordEnv, true})
	}
	var lines []string
	for _, f := range fields {
		res := resolveEnvValue(f.value, sources)
		if !res.IsRef {
			continue
		}
		status := "ok"
		detail := res.SourceName
		style := itemDimStyle
		if res.MissingSource {
			status = "x"
			detail = "missing source ." + res.Alias
			style = errorStyle
		} else if res.MissingKey {
			status = "x"
			detail = "missing key in " + res.SourceName
			style = errorStyle
		}
		preview := ""
		if res.Resolved && !f.secret && res.Value != "" {
			preview = " -> " + res.Value
		}
		lines = append(lines, style.Render(status+" "+f.label+": "+res.Raw+preview+" ("+detail+")"))
	}
	if len(lines) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(labelStyle.Render("Env refs:") + "\n")
	for _, line := range lines {
		b.WriteString(line + "\n")
	}
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
