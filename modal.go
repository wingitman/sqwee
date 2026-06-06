package main

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ── Modal overlay ─────────────────────────────────────────────────────────────
//
// Modal is a simple overlay dialog for collecting user input. It captures all
// key input until confirmed (Enter on the last field) or cancelled (Esc).
//
// A field may be a free-text input or a selector (cycled with ←/→), used e.g.
// for choosing the database driver when adding a connection.

// ModalFieldKind distinguishes text inputs from selector fields.
type ModalFieldKind int

const (
	FieldText ModalFieldKind = iota
	FieldSelect
)

// ModalField defines one field in a modal.
type ModalField struct {
	Label       string
	Placeholder string
	Value       string         // pre-filled value (edit mode)
	Kind        ModalFieldKind // text or selector
	Options     []string       // selector options (Kind == FieldSelect)
	Password    bool           // mask input
	VisibleWhen *FieldCondition
}

// FieldCondition hides a field unless another field has the required value.
type FieldCondition struct {
	Field int
	Value string
}

// ModalKind identifies what the modal is for, used to route the confirm message.
type ModalKind int

const (
	ModalAddConnection ModalKind = iota
	ModalEditConnection
	ModalNewObject
	ModalExport
	ModalConfirm

	// Initialize-database wizard steps.
	ModalInitPickDriver
	ModalInitPickMode
	ModalInitConfigure
	ModalInitConfirm
)

// modalConfirmMsg is dispatched when a modal is confirmed.
type modalConfirmMsg struct {
	kind   ModalKind
	values []string
}

// modalCancelMsg is dispatched when a modal is cancelled.
type modalCancelMsg struct{}

// modalCopyMsg is dispatched when the user presses the copy key on a modal that
// has CopyHint enabled (e.g. the provisioning summary).
type modalCopyMsg struct{ kind ModalKind }

// Modal is the overlay dialog model.
type Modal struct {
	Kind    ModalKind
	Title   string
	inputs  []textinput.Model
	labels  []string
	kinds   []ModalFieldKind
	options [][]string
	selIdx  []int // current selection index per field (selectors only)
	visible []*FieldCondition
	focused int
	Width   int
	keys    KeyMap // honoured for navigation/confirm/cancel; remappable

	// Body is optional read-only content rendered above the fields (used by
	// the provisioning summary/confirm step). CopyHint, when true, shows a
	// "copy config" hint that maps to the configured copy key.
	Body     string
	CopyHint bool
}

// NewModal creates a modal from the given fields. The KeyMap is honoured for
// field navigation, selector cycling, confirm and cancel, so modals respect the
// user's configured (and live-reloaded) keybindings.
func NewModal(kind ModalKind, title string, fields []ModalField, width int, keys KeyMap) Modal {
	n := len(fields)
	inputs := make([]textinput.Model, n)
	labels := make([]string, n)
	kinds := make([]ModalFieldKind, n)
	options := make([][]string, n)
	selIdx := make([]int, n)
	visible := make([]*FieldCondition, n)

	for i, f := range fields {
		ti := textinput.New()
		ti.Placeholder = f.Placeholder
		ti.CharLimit = 2048
		ti.SetWidth(width - 18)
		if f.Value != "" {
			ti.SetValue(f.Value)
		}
		if f.Password {
			ti.EchoMode = textinput.EchoPassword
		}
		inputs[i] = ti
		labels[i] = f.Label
		kinds[i] = f.Kind
		options[i] = f.Options
		visible[i] = f.VisibleWhen
		// Resolve initial selector index from Value if present.
		if f.Kind == FieldSelect {
			for j, opt := range f.Options {
				if opt == f.Value {
					selIdx[i] = j
				}
			}
		}
	}

	m := Modal{
		Kind:    kind,
		Title:   title,
		inputs:  inputs,
		labels:  labels,
		kinds:   kinds,
		options: options,
		selIdx:  selIdx,
		visible: visible,
		Width:   width,
		keys:    keys,
	}
	m.focusField(m.firstVisibleField())
	return m
}

func (m *Modal) fieldVisible(i int) bool {
	if i < 0 || i >= len(m.inputs) {
		return false
	}
	cond := m.visible[i]
	if cond == nil {
		return true
	}
	if cond.Field < 0 || cond.Field >= len(m.inputs) {
		return true
	}
	return m.valueAt(cond.Field) == cond.Value
}

func (m *Modal) valueAt(i int) string {
	if m.kinds[i] == FieldSelect {
		if len(m.options[i]) > 0 {
			return m.options[i][m.selIdx[i]]
		}
		return ""
	}
	return m.inputs[i].Value()
}

func (m *Modal) firstVisibleField() int {
	for i := range m.inputs {
		if m.fieldVisible(i) {
			return i
		}
	}
	return 0
}

func (m *Modal) lastVisibleField() int {
	for i := len(m.inputs) - 1; i >= 0; i-- {
		if m.fieldVisible(i) {
			return i
		}
	}
	return 0
}

func (m *Modal) focusField(i int) {
	for j := range m.inputs {
		if j == i && m.kinds[j] == FieldText {
			m.inputs[j].Focus()
		} else {
			m.inputs[j].Blur()
		}
	}
	m.focused = i
}

func (m *Modal) advance(delta int) {
	n := len(m.inputs)
	if n == 0 {
		return
	}
	for step := 1; step <= n; step++ {
		next := (m.focused + delta*step + n*step) % n
		if m.fieldVisible(next) {
			m.focusField(next)
			return
		}
	}
}

// values returns the current field values (selector → selected option string).
func (m *Modal) values() []string {
	out := make([]string, len(m.inputs))
	for i := range m.inputs {
		out[i] = m.valueAt(i)
	}
	return out
}

// Update handles a key message. The bool return is true when the modal should
// close; the returned cmd carries the confirm/cancel message.
//
// Field movement is driven by Tab / Shift+Tab (and the configured tab_next /
// tab_prev keys); Enter confirms on the last field or advances otherwise; the
// configured Escape cancels. Crucially, when a TEXT field is focused, every
// other key — including remapped navigation keys like vim h/j/k/l — is passed
// straight through to the input so you can type those characters. Configured
// Up/Down/Left/Right only act as navigation on SELECTOR fields, which have no
// text to type.
func (m Modal) Update(msg tea.Msg) (Modal, tea.Cmd, bool) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		// Non-key messages (e.g. bracketed PasteMsg) go to the focused text
		// input so paste works.
		if m.kinds[m.focused] == FieldText {
			var cmd tea.Cmd
			m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
			return m, cmd, false
		}
		return m, nil, false
	}
	s := key.String()

	// Cancel.
	if keyMatches(key, m.keys.Escape) {
		return m, func() tea.Msg { return modalCancelMsg{} }, true
	}

	// Info/summary modal (no input fields): Enter confirms, copy key copies.
	if len(m.inputs) == 0 {
		if keyMatches(key, m.keys.Enter) {
			kind := m.Kind
			return m, func() tea.Msg { return modalConfirmMsg{kind: kind, values: nil} }, true
		}
		if m.CopyHint && keyMatches(key, m.keys.CopyItem) {
			kind := m.Kind
			return m, func() tea.Msg { return modalCopyMsg{kind: kind} }, false
		}
		return m, nil, false
	}

	onText := m.kinds[m.focused] == FieldText
	onSelect := m.kinds[m.focused] == FieldSelect

	// Confirm / advance (Enter).
	if keyMatches(key, m.keys.Enter) {
		if m.focused == m.lastVisibleField() {
			vals := m.values()
			kind := m.Kind
			return m, func() tea.Msg { return modalConfirmMsg{kind: kind, values: vals} }, true
		}
		m.advance(1)
		return m, nil, false
	}

	// Field navigation is always via Tab / Shift+Tab (plus the configured
	// tab_next / tab_prev, which default to those). This never conflicts with
	// typing into a text field.
	if s == "tab" || keyMatches(key, m.keys.TabNext) {
		m.advance(1)
		return m, nil, false
	}
	if s == "shift+tab" || keyMatches(key, m.keys.TabPrev) {
		m.advance(-1)
		return m, nil, false
	}

	// On a SELECTOR field there is nothing to type, so use up/down to move
	// between fields and left/right (configured or bare arrows) to cycle.
	if onSelect {
		if keyMatches(key, m.keys.Down) || s == "down" {
			m.advance(1)
			return m, nil, false
		}
		if keyMatches(key, m.keys.Up) || s == "up" {
			m.advance(-1)
			return m, nil, false
		}
		if keyMatches(key, m.keys.Left) || s == "left" {
			opts := m.options[m.focused]
			if len(opts) > 0 {
				m.selIdx[m.focused] = (m.selIdx[m.focused] - 1 + len(opts)) % len(opts)
			}
			return m, nil, false
		}
		if keyMatches(key, m.keys.Right) || s == "right" {
			opts := m.options[m.focused]
			if len(opts) > 0 {
				m.selIdx[m.focused] = (m.selIdx[m.focused] + 1) % len(opts)
			}
			return m, nil, false
		}
		return m, nil, false
	}

	// TEXT field: forward everything (typing + cursor keys) to the input.
	if onText {
		var cmd tea.Cmd
		m.inputs[m.focused], cmd = m.inputs[m.focused].Update(msg)
		return m, cmd, false
	}
	return m, nil, false
}

// View renders the modal box.
func (m Modal) View() string {
	var b strings.Builder
	b.WriteString(modalTitleStyle.Render(m.Title))
	b.WriteString("\n\n")

	if m.Body != "" {
		b.WriteString(m.Body)
		b.WriteString("\n\n")
	}

	// Info/summary modal (no fields): show confirm/copy/cancel hints only.
	if len(m.inputs) == 0 {
		confirm := firstKey(m.keys.Enter, "enter")
		cancel := firstKey(m.keys.Escape, "esc")
		hint := confirm + ": confirm   "
		if m.CopyHint {
			hint += firstKey(m.keys.CopyItem, "y") + ": copy config   "
		}
		hint += cancel + ": cancel"
		b.WriteString(dimStyle.Render(hint))
		return modalBorderStyle.Width(m.Width - 4).Render(b.String())
	}

	for i := range m.inputs {
		if !m.fieldVisible(i) {
			continue
		}
		label := m.labels[i]
		if i == m.focused {
			b.WriteString(labelFocusedStyle.Render(label + ": "))
		} else {
			b.WriteString(labelStyle.Render(label + ": "))
		}
		if m.kinds[i] == FieldSelect {
			opt := ""
			if len(m.options[i]) > 0 {
				opt = m.options[i][m.selIdx[i]]
			}
			arrows := dimStyle.Render("◀ ") + valueStyle.Render(opt) + dimStyle.Render(" ▶")
			b.WriteString(arrows)
		} else {
			b.WriteString(m.inputs[i].View())
		}
		b.WriteString("\n\n")
	}

	confirm := firstKey(m.keys.Enter, "enter")
	cancel := firstKey(m.keys.Escape, "esc")
	b.WriteString(dimStyle.Render("tab/shift+tab: move   " + confirm + ": next/confirm   ←/→: change   " + cancel + ": cancel"))
	return modalBorderStyle.Width(m.Width - 4).Render(b.String())
}

// firstKey returns the first bound key for a binding, or a fallback.
func firstKey(b interface{ Keys() []string }, fallback string) string {
	if ks := b.Keys(); len(ks) > 0 {
		return ks[0]
	}
	return fallback
}

// OverlayModal centers the modal on top of the background.
func OverlayModal(background, modal string, termWidth, termHeight int) string {
	return lipgloss.Place(
		termWidth, termHeight,
		lipgloss.Center, lipgloss.Center,
		modal,
		lipgloss.WithWhitespaceChars(" "),
		lipgloss.WithWhitespaceStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#111122"))),
	)
}
