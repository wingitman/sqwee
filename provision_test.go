package main

import (
	"path/filepath"
	"testing"

	"main.go/internal/driver"
)

// newTestModelForWizard builds a minimal Model with config/keys for wizard tests.
func newTestModelForWizard(t *testing.T) Model {
	t.Helper()
	cfg := defaultConfig()
	return Model{
		cfg:      cfg,
		keys:     NewKeyMap(cfg),
		colCache: map[string][]driver.Column{},
		width:    100,
		height:   40,
	}
}

func TestWizardStartPicksProvisioners(t *testing.T) {
	m := newTestModelForWizard(t)
	model, _ := m.startInitWizard()
	m = model.(Model)
	if !m.wizard.active {
		t.Fatal("wizard should be active after start")
	}
	if m.modal == nil || m.modal.Kind != ModalInitPickDriver {
		t.Fatalf("expected ModalInitPickDriver, got %+v", m.modal)
	}
}

func TestWizardSQLiteSingleModeSkipsToConfigure(t *testing.T) {
	m := newTestModelForWizard(t)
	model, _ := m.startInitWizard()
	m = model.(Model)

	// Pick sqlite → single mode → should jump straight to the Configure step.
	model, _ = m.handleInitConfirm(modalConfirmMsg{kind: ModalInitPickDriver, values: []string{"sqlite"}})
	m = model.(Model)
	if m.wizard.driver != "sqlite" {
		t.Fatalf("driver = %q, want sqlite", m.wizard.driver)
	}
	if m.modal == nil || m.modal.Kind != ModalInitConfigure {
		t.Fatalf("expected ModalInitConfigure (single mode skips picker), got %+v", m.modal)
	}
	if m.wizard.mode != "file" {
		t.Fatalf("mode = %q, want file", m.wizard.mode)
	}
}

func TestWizardConfigureToConfirm(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wizard.sqlite")

	m := newTestModelForWizard(t)
	m.wizard = initWizard{active: true, driver: "sqlite", mode: "file", modeIdx: 0, values: map[string]string{}}

	// Configure step values: [connection name, path]
	model, _ := m.handleInitConfirm(modalConfirmMsg{
		kind:   ModalInitConfigure,
		values: []string{"my-sqlite", path},
	})
	m = model.(Model)

	if m.wizard.connName != "my-sqlite" {
		t.Errorf("connName = %q", m.wizard.connName)
	}
	if m.wizard.values["path"] != path {
		t.Errorf("path value = %q, want %q", m.wizard.values["path"], path)
	}
	if m.modal == nil || m.modal.Kind != ModalInitConfirm {
		t.Fatalf("expected ModalInitConfirm, got %+v", m.modal)
	}
	if m.modal.Body == "" || !m.modal.CopyHint {
		t.Error("confirm modal should have a body and a copy hint")
	}
}

func TestFinishProvisionSavesAndSelects(t *testing.T) {
	// Use a temp config dir so SaveData writes somewhere harmless.
	t.Setenv("HOME", t.TempDir())

	m := newTestModelForWizard(t)
	m.wizard = initWizard{active: true, driver: "sqlite", connName: "prov-test", values: map[string]string{}}

	dir := t.TempDir()
	info := driver.ConnInfo{Driver: "sqlite", Database: filepath.Join(dir, "p.sqlite")}
	msg := provisionDoneMsg{
		driver: "sqlite",
		result: driver.ProvisionResult{Info: info, Steps: []string{"Created file"}},
	}

	model, cmd := m.finishProvision(msg)
	m = model.(Model)

	if m.wizard.active {
		t.Error("wizard should be inactive after finish")
	}
	// The new connection should be saved and present in the list.
	found := false
	for _, c := range m.conns {
		if c.saved && c.info.Name == "prov-test" {
			found = true
		}
	}
	if !found {
		t.Fatalf("provisioned connection not in list: %+v", m.conns)
	}
	// Auto-connect command should have been returned.
	if cmd == nil {
		t.Error("expected an auto-connect command after provisioning")
	}
}

func TestFinishProvisionError(t *testing.T) {
	m := newTestModelForWizard(t)
	m.wizard = initWizard{active: true, driver: "postgres", values: map[string]string{}}
	model, cmd := m.finishProvision(provisionDoneMsg{
		driver: "postgres",
		err:    errTest,
	})
	m = model.(Model)
	if m.wizard.active {
		t.Error("wizard should be inactive after an error")
	}
	if cmd != nil {
		t.Error("no auto-connect command should run on error")
	}
}

// errTest is a sentinel error for table tests.
var errTest = &simpleErr{"boom"}

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
