package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"main.go/internal/driver"
)

// ── Wizard entry ──────────────────────────────────────────────────────────────

// startInitWizard opens the first step of the Initialize-database flow: pick a
// database engine. Only drivers that implement driver.Provisioner are offered.
func (m Model) startInitWizard() (tea.Model, tea.Cmd) {
	provisioners := driver.Provisioners()
	if len(provisioners) == 0 {
		m.statusMsg = errorStyle.Render("No drivers support initializing a database.")
		return m, nil
	}
	m.wizard = initWizard{active: true, values: map[string]string{}}
	mod := NewModal(ModalInitPickDriver, "Initialize Database — Type", []ModalField{
		{Label: "Database type", Kind: FieldSelect, Options: provisioners, Value: provisioners[0]},
	}, m.modalWidth(), m.keys)
	m.modal = &mod
	return m, nil
}

// ── Step transitions (called from handleModalConfirm) ─────────────────────────

// handleInitConfirm advances the wizard based on which step was confirmed.
func (m Model) handleInitConfirm(msg modalConfirmMsg) (tea.Model, tea.Cmd) {
	switch msg.kind {
	case ModalInitPickDriver:
		m.wizard.driver = msg.values[0]
		return m.initPickModeStep()

	case ModalInitPickMode:
		// Resolve the chosen mode label back to its index/ID.
		modes := m.wizardModes()
		label := msg.values[0]
		for i, mode := range modes {
			if mode.Label == label {
				m.wizard.modeIdx = i
				m.wizard.mode = mode.ID
				break
			}
		}
		return m.initConfigureStep()

	case ModalInitConfigure:
		m.captureConfigureValues(msg.values)
		return m.initConfirmStep()

	case ModalInitConfirm:
		// User confirmed — run provisioning.
		dn := m.wizard.driver
		mode := m.wizard.mode
		vals := m.copyValues()
		m.statusMsg = loadingStyle.Render("Provisioning " + dn + " database… (this can take a moment)")
		return m, provisionCmd(dn, mode, vals)
	}
	return m, nil
}

// wizardModes returns the ProvisionModes for the selected driver.
func (m Model) wizardModes() []driver.ProvisionMode {
	p := driver.AsProvisioner(m.wizard.driver)
	if p == nil {
		return nil
	}
	return p.ProvisionModes()
}

// initPickModeStep opens the mode picker, or skips straight to configuration
// when the driver offers a single mode.
func (m Model) initPickModeStep() (tea.Model, tea.Cmd) {
	modes := m.wizardModes()
	if len(modes) == 0 {
		m.statusMsg = errorStyle.Render("Driver " + m.wizard.driver + " offers no provisioning modes.")
		m.wizard.active = false
		return m, nil
	}
	if len(modes) == 1 {
		m.wizard.modeIdx = 0
		m.wizard.mode = modes[0].ID
		return m.initConfigureStep()
	}
	labels := make([]string, len(modes))
	for i, mode := range modes {
		labels[i] = mode.Label
	}
	mod := NewModal(ModalInitPickMode, "Initialize "+m.wizard.driver+" — Mode", []ModalField{
		{Label: "How", Kind: FieldSelect, Options: labels, Value: labels[0]},
	}, m.modalWidth(), m.keys)
	m.modal = &mod
	return m, nil
}

// initConfigureStep builds the dynamic field form from the chosen mode's spec,
// plus the saved-connection Name and password env-var fields.
func (m Model) initConfigureStep() (tea.Model, tea.Cmd) {
	modes := m.wizardModes()
	if m.wizard.modeIdx >= len(modes) {
		m.wizard.active = false
		return m, nil
	}
	mode := modes[m.wizard.modeIdx]

	fields := []ModalField{
		{Label: "Connection name", Placeholder: m.wizard.driver + "-db", Value: m.wizard.connName},
	}
	for _, f := range mode.Fields {
		fields = append(fields, ModalField{
			Label:       f.Label,
			Placeholder: f.Placeholder,
			Value:       f.Default,
			Password:    f.Password,
			Options:     f.Options,
			Kind:        fieldKind(f),
		})
	}
	// For server/docker modes, also collect a password env-var name for the
	// saved connection (consistent with the New Connection modal).
	if m.wizard.mode != "file" {
		fields = append(fields, ModalField{
			Label:       "Password env var (for saved connection)",
			Placeholder: "PGPASSWORD",
			Value:       m.wizard.passEnv,
		})
	}

	title := "Initialize " + m.wizard.driver + " — Configure"
	mod := NewModal(ModalInitConfigure, title, fields, m.modalWidth(), m.keys)
	m.modal = &mod
	return m, nil
}

func fieldKind(f driver.ProvisionField) ModalFieldKind {
	if len(f.Options) > 0 {
		return FieldSelect
	}
	return FieldText
}

// captureConfigureValues maps the modal field values back to the wizard state.
func (m *Model) captureConfigureValues(values []string) {
	if len(values) == 0 {
		return
	}
	// Field 0 is always the connection name.
	m.wizard.connName = strings.TrimSpace(values[0])

	mode := m.wizardModes()[m.wizard.modeIdx]
	idx := 1
	for _, f := range mode.Fields {
		if idx < len(values) {
			m.wizard.values[f.Key] = values[idx]
		}
		idx++
	}
	// Trailing password-env field for server/docker modes.
	if m.wizard.mode != "file" && idx < len(values) {
		m.wizard.passEnv = strings.TrimSpace(values[idx])
	}
}

// copyValues returns a copy of the collected provisioning values.
func (m Model) copyValues() map[string]string {
	out := make(map[string]string, len(m.wizard.values))
	for k, v := range m.wizard.values {
		out[k] = v
	}
	return out
}

// initConfirmStep shows a read-only summary with a "copy config" hint.
func (m Model) initConfirmStep() (tea.Model, tea.Cmd) {
	mod := NewModal(ModalInitConfirm, "Initialize Database — Confirm", nil, m.modalWidth(), m.keys)
	mod.Body = m.wizardSummary()
	mod.CopyHint = true
	m.modal = &mod
	return m, nil
}

// wizardSummary renders a human-readable summary of the pending provisioning.
func (m Model) wizardSummary() string {
	var b strings.Builder
	line := func(label, val string) {
		if val == "" {
			return
		}
		b.WriteString(labelStyle.Render(label+": ") + valueStyle.Render(val) + "\n")
	}
	line("Name", m.wizard.connName)
	line("Engine", m.wizard.driver)
	line("Mode", m.wizardModeLabel())
	v := m.wizard.values
	if m.wizard.mode == "file" {
		line("File", v["path"])
	} else {
		line("Host", firstNonEmpty(v["host"], "localhost"))
		line("Port", v["port"])
		line("Admin user", v["user"])
		line("New database", v["db_name"])
		if m.wizard.mode == "docker" {
			line("Container", firstNonEmpty(v["container"], "(auto-generated)"))
			b.WriteString(dimStyle.Render("A fresh Docker container will be started.\n"))
		}
		if m.wizard.passEnv != "" {
			line("Password env", m.wizard.passEnv)
		}
	}
	return b.String()
}

func (m Model) wizardModeLabel() string {
	modes := m.wizardModes()
	if m.wizard.modeIdx < len(modes) {
		return modes[m.wizard.modeIdx].Label
	}
	return m.wizard.mode
}

// wizardConfigText returns the summary as plain text for clipboard copy.
func (m Model) wizardConfigText() string {
	v := m.wizard.values
	var b strings.Builder
	fmt.Fprintf(&b, "name = %s\n", m.wizard.connName)
	fmt.Fprintf(&b, "driver = %s\n", m.wizard.driver)
	fmt.Fprintf(&b, "mode = %s\n", m.wizard.mode)
	if m.wizard.mode == "file" {
		fmt.Fprintf(&b, "path = %s\n", v["path"])
	} else {
		fmt.Fprintf(&b, "host = %s\n", firstNonEmpty(v["host"], "localhost"))
		fmt.Fprintf(&b, "port = %s\n", v["port"])
		fmt.Fprintf(&b, "user = %s\n", v["user"])
		fmt.Fprintf(&b, "database = %s\n", v["db_name"])
		if m.wizard.passEnv != "" {
			fmt.Fprintf(&b, "password_env = %s\n", m.wizard.passEnv)
		}
	}
	return b.String()
}

// ── Command + result ──────────────────────────────────────────────────────────

type provisionDoneMsg struct {
	result driver.ProvisionResult
	driver string
	err    error
}

// provisionCmd runs the driver's Provision off the UI goroutine.
func provisionCmd(driverName, mode string, values map[string]string) tea.Cmd {
	return func() tea.Msg {
		p := driver.AsProvisioner(driverName)
		if p == nil {
			return provisionDoneMsg{driver: driverName, err: fmt.Errorf("driver %q cannot provision", driverName)}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		res, err := p.Provision(ctx, mode, values)
		return provisionDoneMsg{result: res, driver: driverName, err: err}
	}
}

// finishProvision saves the new connection, refreshes the list, and auto-connects.
func (m Model) finishProvision(msg provisionDoneMsg) (tea.Model, tea.Cmd) {
	m.wizard.active = false
	if msg.err != nil {
		m.statusMsg = errorStyle.Render("Initialize failed: " + msg.err.Error())
		return m, nil
	}

	info := msg.result.Info
	name := m.wizard.connName
	if name == "" {
		name = info.Name
	}
	if name == "" {
		name = msg.driver + "-db"
	}

	saved := SavedConnection{
		Name:        name,
		Driver:      info.Driver,
		Host:        info.Host,
		Port:        info.Port,
		User:        info.User,
		Database:    info.Database,
		PasswordEnv: m.wizard.passEnv,
	}
	if msg.result.Container != "" {
		saved.Options = map[string]string{"docker_container": msg.result.Container}
	}

	next := AppData{Connections: append([]SavedConnection(nil), m.data.Connections...)}
	next.Connections = append(next.Connections, saved)
	if err := SaveData(next); err != nil {
		m.statusMsg = errorStyle.Render("Database created, but saving connection failed: " + err.Error())
		return m, nil
	}
	m.data = next
	m.rebuildConnections()

	// Select the new connection.
	for i, c := range m.conns {
		if c.saved && c.info.Name == name && c.info.Driver == info.Driver {
			m.connCursor = i
			break
		}
	}

	steps := strings.Join(msg.result.Steps, " · ")
	if msg.result.PasswordHint != "" {
		steps += "  (password: " + msg.result.PasswordHint + ")"
	}
	m.statusMsg = successStyle.Render("Database ready — " + steps)

	// Auto-connect so its schema loads.
	connInfo := saved.toConnInfo()
	// For Docker, the freshly-generated password is in the hint; use it for the
	// immediate connect (it isn't persisted to disk).
	if msg.result.PasswordHint != "" {
		connInfo.Password = msg.result.PasswordHint
	}
	m.connecting = true
	return m, connectCmd(connInfo)
}

// ── helpers ───────────────────────────────────────────────────────────────────

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
