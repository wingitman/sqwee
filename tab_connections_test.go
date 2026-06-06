package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"main.go/internal/driver"
)

func testConnectionModel(t *testing.T) Model {
	t.Helper()
	cfg := defaultConfig()
	cfg.Discovery = ConfigDiscovery{}
	return Model{
		cfg:      cfg,
		keys:     NewKeyMap(cfg),
		colCache: map[string][]driver.Column{},
		width:    100,
		height:   40,
	}
}

func setTempConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestAddConnectionPersistsBeforeConnect(t *testing.T) {
	setTempConfigHome(t)
	m := testConnectionModel(t)

	model, cmd := m.handleModalConfirm(modalConfirmMsg{
		kind:   ModalAddConnection,
		values: []string{"aws-postgres", "postgres", "", "db.example.rds.amazonaws.com", "5432", "postgres", "PGPASSWORD", ""},
	})
	m = model.(Model)

	if cmd != nil {
		t.Fatal("adding a connection should not try to connect")
	}
	if len(m.data.Connections) != 1 {
		t.Fatalf("in-memory connections = %d, want 1", len(m.data.Connections))
	}
	if m.data.Connections[0].Database != "" {
		t.Fatalf("database = %q, want empty", m.data.Connections[0].Database)
	}

	data, err := LoadData()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Connections) != 1 || data.Connections[0].Name != "aws-postgres" {
		t.Fatalf("saved data = %+v", data.Connections)
	}
	if data.Connections[0].Password != "PGPASSWORD" || data.Connections[0].PasswordEnv != "" {
		t.Fatalf("password fields = password %q password_env %q, want literal password", data.Connections[0].Password, data.Connections[0].PasswordEnv)
	}
}

func TestSavedConnectionFromModalValuesKeepsInlineEnvRefs(t *testing.T) {
	s := savedConnectionFromModalValues([]string{
		"dev-postgres",
		"postgres",
		"env-dev:DATABASE_URL",
		"env-dev:PGHOST",
		"env-dev:PGPORT",
		"env-dev:PGUSER",
		"env-dev:PGPASSWORD",
		"env-dev:PGDATABASE",
		"ssh",
		"env-dev:GATEWAY_HOST",
		"env-dev:GATEWAY_PORT",
		"env-dev:GATEWAY_USER",
		"env-dev:GATEWAY_PASSWORD",
	})

	if s.URL != "env-dev:DATABASE_URL" || s.Host != "env-dev:PGHOST" || s.PortEnv != "env-dev:PGPORT" || s.User != "env-dev:PGUSER" || s.Password != "env-dev:PGPASSWORD" || s.Database != "env-dev:PGDATABASE" {
		t.Fatalf("unexpected saved connection: %+v", s)
	}
	if s.Gateway == nil || s.Gateway.Host != "env-dev:GATEWAY_HOST" || s.Gateway.PortEnv != "env-dev:GATEWAY_PORT" || s.Gateway.User != "env-dev:GATEWAY_USER" || s.Gateway.Password != "env-dev:GATEWAY_PASSWORD" {
		t.Fatalf("unexpected gateway: %+v", s.Gateway)
	}
	if s.Port != 0 || s.PasswordEnv != "" {
		t.Fatalf("legacy fields should stay empty for inline refs: %+v", s)
	}
}

func TestConnectionModalShowsLegacyPasswordEnvAsInlineRef(t *testing.T) {
	m := testConnectionModel(t)
	mod := m.connectionModal(ModalEditConnection, "Edit Connection", SavedConnection{Driver: "postgres", PasswordEnv: "PGPASSWORD"})

	if got := mod.inputs[6].Value(); got != "env:PGPASSWORD" {
		t.Fatalf("password field = %q, want env:PGPASSWORD", got)
	}
}

func TestSavedConnectionConfigOmitsLiteralPasswordButCopiesEnvRef(t *testing.T) {
	literal := savedConnectionConfigText(SavedConnection{Name: "prod", Driver: "postgres", Password: "secret", Gateway: &SavedGateway{Type: "ssh", Host: "gateway", User: "admin", Password: "gateway-secret"}})
	if strings.Contains(literal, "secret") || strings.Contains(literal, "password =") {
		t.Fatalf("literal password leaked in config text: %q", literal)
	}

	envRef := savedConnectionConfigText(SavedConnection{Name: "dev", Driver: "postgres", Password: "env-dev:PGPASSWORD", Gateway: &SavedGateway{Type: "ssh", Host: "env-dev:GATEWAY_HOST", User: "env-dev:GATEWAY_USER", Password: "env-dev:GATEWAY_PASSWORD"}})
	if !strings.Contains(envRef, "password = env-dev:PGPASSWORD") {
		t.Fatalf("env ref password missing from config text: %q", envRef)
	}
	if !strings.Contains(envRef, "gateway.password = env-dev:GATEWAY_PASSWORD") {
		t.Fatalf("gateway env ref missing from config text: %q", envRef)
	}
	keyRef := savedConnectionConfigText(SavedConnection{Name: "dev", Driver: "sqlite", Database: "/srv/app.sqlite", Gateway: &SavedGateway{Type: "ssh", Host: "host", User: "ubuntu", KeyFile: "~/Work/timid/key.pem"}})
	if !strings.Contains(keyRef, "gateway.key_file = ~/Work/timid/key.pem") {
		t.Fatalf("gateway key file missing from config text: %q", keyRef)
	}
}

func TestConnectionModalHidesGatewayFieldsWhenNone(t *testing.T) {
	m := testConnectionModel(t)
	mod := m.connectionModal(ModalAddConnection, "New Connection", SavedConnection{Driver: "sqlite"})

	out := mod.View()
	if strings.Contains(out, "Gateway host") || strings.Contains(out, "Gateway key file") {
		t.Fatalf("gateway detail fields should be hidden when gateway is none: %q", out)
	}

	mod = m.connectionModal(ModalAddConnection, "New Connection", SavedConnection{Driver: "sqlite", Gateway: &SavedGateway{Type: "ssh"}})
	out = mod.View()
	if !strings.Contains(out, "Gateway host") || !strings.Contains(out, "Gateway key file") {
		t.Fatalf("gateway detail fields should be visible when gateway is ssh: %q", out)
	}
}

func TestEnvDiagnosticsHighlightMissingSource(t *testing.T) {
	var b strings.Builder
	writeEnvDiagnostics(&b, SavedConnection{Host: "env-prod:PGHOST"}, nil)

	out := b.String()
	if !strings.Contains(out, "x Host: env-prod:PGHOST") || !strings.Contains(out, "missing source .env-prod") {
		t.Fatalf("diagnostics = %q", out)
	}
}

func TestConnectionValidationRejectsHTTPHost(t *testing.T) {
	err := validateSavedConnection(SavedConnection{Driver: "postgres", Host: "https://db.example.com"})
	if err == "" || !strings.Contains(err, "Host must be a hostname") {
		t.Fatalf("validation error = %q", err)
	}
}

func TestConnectionValidationRejectsHTTPURL(t *testing.T) {
	err := validateSavedConnection(SavedConnection{Driver: "postgres", URL: "https://db.example.com"})
	if err == "" || !strings.Contains(err, "database scheme") {
		t.Fatalf("validation error = %q", err)
	}
}

func TestConnectionValidationRequiresGatewayHostAndUser(t *testing.T) {
	err := validateSavedConnection(SavedConnection{Driver: "postgres", Host: "db", Gateway: &SavedGateway{Type: "ssh", User: "admin"}})
	if err == "" || !strings.Contains(err, "Gateway host") {
		t.Fatalf("validation error = %q", err)
	}
	err = validateSavedConnection(SavedConnection{Driver: "postgres", Host: "db", Gateway: &SavedGateway{Type: "ssh", Host: "gateway"}})
	if err == "" || !strings.Contains(err, "Gateway user") {
		t.Fatalf("validation error = %q", err)
	}
}

func TestAddConnectionSaveFailureKeepsModalAndMemoryClean(t *testing.T) {
	dir := t.TempDir()
	fileConfigHome := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(fileConfigHome, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", fileConfigHome)
	t.Setenv("HOME", dir)
	m := testConnectionModel(t)

	model, _ := m.handleModalConfirm(modalConfirmMsg{
		kind:   ModalAddConnection,
		values: []string{"bad-save", "postgres", "", "localhost", "5432", "postgres", "PGPASSWORD", "app"},
	})
	m = model.(Model)

	if len(m.data.Connections) != 0 {
		t.Fatalf("connection should not be added in-memory on save failure: %+v", m.data.Connections)
	}
	if m.modal == nil || m.modal.Kind != ModalAddConnection {
		t.Fatalf("modal should stay open on save failure, got %+v", m.modal)
	}
	if !strings.Contains(m.statusMsg, "Save failed") {
		t.Fatalf("status = %q, want save failure", m.statusMsg)
	}
}
