package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	default:
		return tea.KeyPressMsg{Code: []rune(s)[0], Text: s}
	}
}

func newTestModal(keys KeyMap) Modal {
	return NewModal(ModalAddConnection, "Test", []ModalField{
		{Label: "A"},
		{Label: "B", Kind: FieldSelect, Options: []string{"x", "y", "z"}},
	}, 60, keys)
}

func TestModalNavigatesWithTab(t *testing.T) {
	keys := NewKeyMap(defaultConfig())
	m := newTestModal(keys)

	if m.focused != 0 {
		t.Fatalf("expected initial focus 0, got %d", m.focused)
	}

	// Tab moves to the next field.
	m, _, done := m.Update(keyMsg("tab"))
	if done {
		t.Fatal("modal closed unexpectedly")
	}
	if m.focused != 1 {
		t.Fatalf("after tab, expected focus 1, got %d", m.focused)
	}

	// Shift+Tab moves back.
	m, _, _ = m.Update(tea.KeyPressMsg{Mod: tea.ModShift, Code: tea.KeyTab})
	if m.focused != 0 {
		t.Fatalf("after shift+tab, expected focus 0, got %d", m.focused)
	}
}

func TestModalVimKeysTypeIntoTextField(t *testing.T) {
	// With vim nav keys (h/j/k/l), typing those characters into a TEXT field
	// must insert them, not navigate.
	keys := NewKeyMap(defaultConfig())
	m := newTestModal(keys) // field 0 is a text field, focused

	for _, ch := range []string{"h", "j", "k", "l"} {
		var done bool
		m, _, done = m.Update(keyMsg(ch))
		if done {
			t.Fatalf("modal closed while typing %q", ch)
		}
		if m.focused != 0 {
			t.Fatalf("typing %q moved focus to %d; nav keys must not navigate in a text field", ch, m.focused)
		}
	}
	if got := m.inputs[0].Value(); got != "hjkl" {
		t.Fatalf("expected typed value %q, got %q", "hjkl", got)
	}
}

func TestModalHonoursRemappedEscape(t *testing.T) {
	cfg := defaultConfig()
	cfg.Keys.Escape = "ctrl+g" // remap cancel
	keys := NewKeyMap(cfg)
	m := newTestModal(keys)

	// The new escape key cancels.
	_, cmd, done := m.Update(tea.KeyPressMsg{Mod: tea.ModCtrl, Code: 'g', Text: "ctrl+g"})
	if !done {
		t.Fatal("expected modal to close on remapped escape")
	}
	if cmd == nil {
		t.Fatal("expected a cancel command")
	}
	if _, ok := cmd().(modalCancelMsg); !ok {
		t.Fatalf("expected modalCancelMsg, got %T", cmd())
	}

	// The default "esc" should no longer cancel.
	m2 := newTestModal(keys)
	_, _, done2 := m2.Update(keyMsg("esc"))
	if done2 {
		t.Error("plain esc should not cancel after remap")
	}
}

func TestModalPasteIntoTextField(t *testing.T) {
	keys := NewKeyMap(defaultConfig())
	m := newTestModal(keys) // field 0 is text, focused

	m, _, done := m.Update(tea.PasteMsg{Content: "pasted-value"})
	if done {
		t.Fatal("modal closed on paste")
	}
	if got := m.inputs[0].Value(); got != "pasted-value" {
		t.Fatalf("expected pasted value in input, got %q", got)
	}
}

func TestModalSelectorCycles(t *testing.T) {
	keys := NewKeyMap(defaultConfig())
	m := newTestModal(keys)
	// Move to the selector field (index 1) via Tab.
	m, _, _ = m.Update(keyMsg("tab"))
	if m.kinds[m.focused] != FieldSelect {
		t.Fatalf("expected to be on selector field")
	}
	start := m.selIdx[1]
	// On a selector, "l" (configured Right) cycles forward.
	m, _, _ = m.Update(keyMsg("l"))
	if m.selIdx[1] == start {
		t.Errorf("expected selector to advance from %d", start)
	}
	// "j"/"k" on a selector navigate fields (nothing to type here).
	m, _, _ = m.Update(keyMsg("k"))
	if m.focused != 0 {
		t.Errorf("expected k on selector to move to field 0, got %d", m.focused)
	}
}

func TestModalSkipsConditionallyHiddenFields(t *testing.T) {
	keys := NewKeyMap(defaultConfig())
	m := NewModal(ModalAddConnection, "Test", []ModalField{
		{Label: "Gateway", Kind: FieldSelect, Options: []string{"none", "ssh"}, Value: "none"},
		{Label: "Gateway host", VisibleWhen: &FieldCondition{Field: 0, Value: "ssh"}},
		{Label: "Name"},
	}, 60, keys)

	if strings.Contains(m.View(), "Gateway host") {
		t.Fatalf("hidden field rendered: %q", m.View())
	}
	m, _, _ = m.Update(keyMsg("tab"))
	if m.focused != 2 {
		t.Fatalf("tab should skip hidden field, focused %d", m.focused)
	}
	m, _, _ = m.Update(tea.KeyPressMsg{Mod: tea.ModShift, Code: tea.KeyTab})
	m, _, _ = m.Update(keyMsg("l"))
	if !strings.Contains(m.View(), "Gateway host") {
		t.Fatalf("conditional field did not render after selector change: %q", m.View())
	}
}
