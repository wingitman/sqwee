package main

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"main.go/internal/driver"
)

// Tab identifies one of the three top-level sections.
type Tab int

const (
	TabConnections Tab = iota
	TabSchema
	TabQuery
	numTabs
)

var tabNames = []string{"Connections", "Schema", "Query"}

// connItem is a connection shown in the Connections tab: either a saved
// connection or one discovered from the environment.
type connItem struct {
	info  driver.ConnInfo
	saved bool // true if backed by a SavedConnection in the data store
}

// Model is the root Bubble Tea model.
type Model struct {
	cfg  Config
	keys KeyMap
	data AppData

	tab    Tab
	width  int
	height int

	// Connections tab
	conns      []connItem
	connCursor int
	activeConn *driver.ConnInfo // the connection driving Schema/Query tabs
	conn       driver.Conn      // live connection (nil when disconnected)
	connecting bool
	connErr    string

	// Schema tab
	schemas       []driver.Schema
	objects       []driver.DBObject
	schemaCursor  int // index into the *filtered* object list
	schemaLoading bool
	colCache      map[string][]driver.Column
	metaCache     map[string]driver.TableMetadata
	filtering     bool   // schema filter input is active
	filter        string // current schema filter text

	// Query tab
	editor       textarea.Model
	results      viewport.Model
	queryResult  *driver.QueryResult
	queryErr     string
	execMsg      string
	editing      bool // editor focused for typing
	scripts      []sqlScript
	scriptCursor int
	scriptFocus  bool // true when the script list (not the editor) is focused

	// Results grid interaction
	resultsFocus bool          // true when the results grid (not the editor) is focused
	selMode      SelectionMode // current selection scope
	cellRow      int           // cursor row in the results grid
	cellCol      int           // cursor column in the results grid
	colWidths    []int         // last-rendered column widths (for mouse hit-testing)

	// Modal overlay
	modal *Modal

	// Initialize-database wizard state (carried across the chained modals).
	wizard initWizard

	statusMsg string
}

// initWizard holds state for the multi-step "Initialize database" flow.
type initWizard struct {
	active   bool
	driver   string            // selected driver name
	mode     string            // selected ProvisionMode ID
	modeIdx  int               // index into the driver's ProvisionModes
	connName string            // saved-connection name
	passEnv  string            // password env-var name for the saved connection
	values   map[string]string // collected provisioning field values
}

// activeDriver returns the driver name of the active connection, or "".
func (m Model) activeDriver() string {
	if m.activeConn != nil {
		return m.activeConn.Driver
	}
	return ""
}

// NewModel builds the root model, loading config, data and discovered conns.
func NewModel() (Model, error) {
	cfg, err := LoadConfig()
	if err != nil {
		// Non-fatal: fall back to defaults so the app still launches.
		cfg = defaultConfig()
	}
	data, _ := LoadData()

	ed := textarea.New()
	ed.Placeholder = "SELECT * FROM ..."
	ed.ShowLineNumbers = true
	ed.KeyMap.DeleteWordBackward = key.NewBinding(key.WithKeys("alt+backspace", "ctrl+w", "ctrl+backspace"), key.WithHelp("ctrl+backspace", "delete word backward"))

	m := Model{
		cfg:       cfg,
		keys:      NewKeyMap(cfg),
		data:      data,
		tab:       TabConnections,
		editor:    ed,
		results:   viewport.New(),
		colCache:  map[string][]driver.Column{},
		metaCache: map[string]driver.TableMetadata{},
	}
	m.results.SoftWrap = false
	m.rebuildConnections()
	m.scripts = discoverSQLScripts(cfg)
	return m, nil
}

// rebuildConnections merges saved + discovered connections into m.conns.
// A discovered entry that duplicates a saved connection (same driver+host+port
// or same database file) is dropped so saved connections take precedence.
func (m *Model) rebuildConnections() {
	var items []connItem
	savedKeys := map[string]bool{}
	for _, s := range m.data.Connections {
		info := s.toConnInfo()
		items = append(items, connItem{info: info, saved: true})
		savedKeys[connKey(info)] = true
	}
	for _, ci := range discoverConnections(m.cfg) {
		if savedKeys[connKey(ci)] {
			continue
		}
		items = append(items, connItem{info: ci, saved: false})
	}
	m.conns = items
	if m.connCursor >= len(items) {
		m.connCursor = max(0, len(items)-1)
	}
}

// connKey identifies a connection for de-duplication between saved and
// discovered entries.
func connKey(ci driver.ConnInfo) string {
	if ci.Driver == "sqlite" {
		return "sqlite|" + ci.Database
	}
	host := ci.Host
	if host == "" && ci.URL != "" {
		host = ci.URL
	}
	return ci.Driver + "|" + host + "|" + itoaSimple(ci.Port)
}

func (m Model) Init() tea.Cmd { return nil }

// ── Messages ────────────────────────────────────────────────────────────────

type connectedMsg struct {
	info driver.ConnInfo
	conn driver.Conn
}
type connectErrMsg struct{ err string }
type schemaLoadedMsg struct {
	schemas []driver.Schema
	objects []driver.DBObject
}
type schemaErrMsg struct{ err string }
type queryDoneMsg struct{ result driver.QueryResult }
type execDoneMsg struct {
	result driver.ExecResult
}
type queryErrMsg struct{ err string }
type definitionLoadedMsg struct{ ddl string }
type columnsLoadedMsg struct {
	key  string
	cols []driver.Column
}
type tableMetadataLoadedMsg struct {
	key  string
	meta driver.TableMetadata
}
type configReloadedMsg struct{ cfg Config }

// ── Commands ────────────────────────────────────────────────────────────────

func connectCmd(info driver.ConnInfo) tea.Cmd {
	return func() tea.Msg {
		d := driver.Resolve(info)
		if d == nil {
			return connectErrMsg{err: "no driver for connection (driver=" + info.Driver + ")"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn, err := d.Connect(ctx, info)
		if err != nil {
			return connectErrMsg{err: err.Error()}
		}
		return connectedMsg{info: info, conn: conn}
	}
}

func loadSchemaCmd(conn driver.Conn) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		schemas, err := conn.Schemas(ctx)
		if err != nil {
			return schemaErrMsg{err: err.Error()}
		}
		var objs []driver.DBObject
		for _, s := range schemas {
			o, err := conn.Objects(ctx, s.Name)
			if err != nil {
				return schemaErrMsg{err: err.Error()}
			}
			objs = append(objs, o...)
		}
		return schemaLoadedMsg{schemas: schemas, objects: objs}
	}
}

func runQueryCmd(conn driver.Conn, sql, driverName string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		batches := splitSQLBatches(sql, driverName)
		// Single statement → behave exactly as before (Query vs Exec).
		if len(batches) <= 1 {
			one := sql
			if len(batches) == 1 {
				one = batches[0]
			}
			return runOneStatement(ctx, conn, one)
		}

		// Multiple batches (e.g. a T-SQL script with GO separators): run each
		// in order, keep the last row-returning result, and summarise.
		var lastResult *driver.QueryResult
		var execCount, affected int64
		for _, b := range batches {
			msg := runOneStatement(ctx, conn, b)
			switch v := msg.(type) {
			case queryErrMsg:
				return v // stop on first error, report it
			case queryDoneMsg:
				r := v.result
				lastResult = &r
			case execDoneMsg:
				execCount++
				affected += v.result.RowsAffected
			}
		}
		if lastResult != nil {
			return queryDoneMsg{result: *lastResult}
		}
		return execDoneMsg{result: driver.ExecResult{
			RowsAffected: affected,
			Message:      itoaSimple(int(execCount)) + " batches executed",
		}}
	}
}

// runOneStatement runs a single statement, choosing Query vs Exec by its verb.
func runOneStatement(ctx context.Context, conn driver.Conn, sql string) tea.Msg {
	trimmed := strings.ToLower(strings.TrimSpace(stripLeadingComments(sql)))
	isRowReturning := strings.HasPrefix(trimmed, "select") ||
		strings.HasPrefix(trimmed, "with") ||
		strings.HasPrefix(trimmed, "show") ||
		strings.HasPrefix(trimmed, "pragma") ||
		strings.HasPrefix(trimmed, "explain")
	if isRowReturning {
		res, err := conn.Query(ctx, sql)
		if err != nil {
			return queryErrMsg{err: err.Error()}
		}
		return queryDoneMsg{result: res}
	}
	res, err := conn.Exec(ctx, sql)
	if err != nil {
		return queryErrMsg{err: err.Error()}
	}
	return execDoneMsg{result: res}
}

func loadColumnsCmd(conn driver.Conn, obj driver.DBObject) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cols, err := conn.Columns(ctx, obj.Schema, obj.Name)
		if err != nil {
			return columnsLoadedMsg{key: obj.Qualified(), cols: nil}
		}
		return columnsLoadedMsg{key: obj.Qualified(), cols: cols}
	}
}

func loadTableMetadataCmd(conn driver.Conn, obj driver.DBObject) tea.Cmd {
	return func() tea.Msg {
		provider, ok := conn.(driver.TableMetadataProvider)
		if !ok {
			return tableMetadataLoadedMsg{key: obj.Qualified()}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		meta, err := provider.TableMetadata(ctx, obj.Schema, obj.Name)
		if err != nil {
			return tableMetadataLoadedMsg{key: obj.Qualified()}
		}
		return tableMetadataLoadedMsg{key: obj.Qualified(), meta: meta}
	}
}

func loadDefinitionCmd(conn driver.Conn, obj driver.DBObject) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ddl, err := conn.Definition(ctx, obj)
		if err != nil {
			return queryErrMsg{err: err.Error()}
		}
		return definitionLoadedMsg{ddl: ddl}
	}
}

// ── Update ──────────────────────────────────────────────────────────────────

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.applyLayout()
		m.renderResults()
		return m, nil

	case connectedMsg:
		if m.conn != nil {
			m.conn.Close()
		}
		m.connecting = false
		m.connErr = ""
		info := msg.info
		m.activeConn = &info
		m.conn = msg.conn
		m.schemaLoading = true
		m.statusMsg = successStyle.Render("Connected to " + info.Name)
		return m, loadSchemaCmd(msg.conn)

	case connectErrMsg:
		m.connecting = false
		m.connErr = msg.err
		m.statusMsg = errorStyle.Render("Connect failed: " + msg.err)
		return m, nil

	case schemaLoadedMsg:
		m.schemaLoading = false
		m.schemas = msg.schemas
		m.objects = msg.objects
		m.schemaCursor = 0
		m.filter = ""
		m.filtering = false
		m.colCache = map[string][]driver.Column{}
		m.metaCache = map[string]driver.TableMetadata{}
		return m, m.ensureColumnsCmd()

	case columnsLoadedMsg:
		m.colCache[msg.key] = msg.cols
		return m, nil

	case tableMetadataLoadedMsg:
		m.metaCache[msg.key] = msg.meta
		return m, nil

	case schemaErrMsg:
		m.schemaLoading = false
		m.statusMsg = errorStyle.Render("Schema load failed: " + msg.err)
		return m, nil

	case queryDoneMsg:
		res := msg.result
		m.queryResult = &res
		m.queryErr = ""
		m.execMsg = ""
		// Reset grid cursor/selection for the new result set.
		m.resultsFocus = false
		m.cellRow = 0
		m.cellCol = 0
		m.selMode = SelectCell
		m.renderResults()
		return m, nil

	case execDoneMsg:
		m.queryResult = nil
		m.queryErr = ""
		m.execMsg = successStyle.Render(formatExec(msg.result))
		return m, nil

	case queryErrMsg:
		m.queryResult = nil
		m.queryErr = msg.err
		m.execMsg = ""
		return m, nil

	case definitionLoadedMsg:
		m.editor.SetValue(msg.ddl)
		m.tab = TabQuery
		return m, nil

	case configReloadedMsg:
		m.cfg = msg.cfg
		m.keys = NewKeyMap(msg.cfg)
		// Propagate new keys to an open modal so remaps apply live.
		if m.modal != nil {
			m.modal.keys = m.keys
		}
		m.rebuildConnections()
		m.scripts = discoverSQLScripts(msg.cfg)
		m.applyLayout()
		m.statusMsg = dimStyle.Render("Config reloaded.")
		return m, nil

	case editorClosedMsg:
		if msg.err != "" {
			m.statusMsg = errorStyle.Render("Editor: " + msg.err)
		} else if msg.content != "" {
			m.editor.SetValue(strings.TrimRight(msg.content, "\n"))
			m.tab = TabQuery
		}
		return m, nil

	case clipboardCopiedMsg:
		if msg.err != nil {
			m.statusMsg = errorStyle.Render("Copy failed: " + msg.err.Error())
		} else {
			m.statusMsg = successStyle.Render(msg.note)
		}
		return m, nil

	case explorerOpenedMsg:
		if msg.err != "" {
			m.statusMsg = errorStyle.Render("Open export folder failed: " + msg.err)
		}
		return m, nil

	case modalConfirmMsg:
		m.modal = nil
		return m.handleModalConfirm(msg)

	case modalCancelMsg:
		m.modal = nil
		m.wizard.active = false
		return m, nil

	case modalCopyMsg:
		if msg.kind == ModalInitConfirm {
			return m, copyToClipboardCmd(m.wizardConfigText(), "Config copied.")
		}
		return m, nil

	case provisionDoneMsg:
		return m.finishProvision(msg)

	case tea.PasteMsg:
		return m.handlePaste(msg)

	case tea.MouseClickMsg:
		return m.handleMouse(msg)
	case tea.MouseWheelMsg:
		return m.handleMouse(msg)

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handlePaste routes bracketed-paste content to whichever text input is active:
// an open modal's focused field, or the query editor when editing.
func (m Model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil {
		updated, cmd, done := m.modal.Update(msg)
		if done {
			m.modal = nil
			return m, cmd
		}
		m.modal = &updated
		return m, cmd
	}
	if m.tab == TabQuery && m.editing {
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Modal takes precedence.
	if m.modal != nil {
		updated, cmd, done := m.modal.Update(msg)
		if done {
			m.modal = nil
			return m, cmd
		}
		m.modal = &updated
		return m, cmd
	}

	// Editor edit-mode in the Query tab: typing goes to the textarea. Esc
	// leaves edit mode; Ctrl+S runs the query without leaving the editor.
	if m.tab == TabQuery && m.editing {
		switch msg.String() {
		case "esc":
			m.editing = false
			m.editor.Blur()
			return m, nil
		case "tab":
			m.editor.InsertString("  ")
			return m, nil
		case "ctrl+s":
			if m.conn != nil {
				if sql := strings.TrimSpace(m.editor.Value()); sql != "" {
					m.statusMsg = loadingStyle.Render("Running query...")
					return m, runQueryCmd(m.conn, sql, m.activeDriver())
				}
			}
			return m, nil
		default:
			var cmd tea.Cmd
			m.editor, cmd = m.editor.Update(msg)
			return m, cmd
		}
	}

	// Active schema filtering owns printable keys before global shortcuts. This
	// lets filter text include keys that are otherwise mapped globally, such as
	// the default "o" for opening config.
	if m.tab == TabSchema && m.filtering {
		return m.handleSchemaKey(msg)
	}

	// On the Query tab, Tab / Shift+Tab move focus between the editor and the
	// results grid rather than switching top-level tabs:
	//  • Tab is owned by the Query handler when there's a grid to focus into,
	//    or when the grid is already focused (to tab back out).
	//  • Shift+Tab is only owned when the grid is focused (to return to editor).
	haveGrid := m.queryResult != nil && len(m.queryResult.Rows) > 0
	tabNextOwned := m.tab == TabQuery && (m.resultsFocus || haveGrid)
	tabPrevOwned := m.tab == TabQuery && m.resultsFocus

	switch {
	case keyMatches(msg, m.keys.Quit):
		if m.conn != nil {
			m.conn.Close()
		}
		return m, tea.Quit

	case keyMatches(msg, m.keys.TabNext) && !tabNextOwned:
		m.tab = (m.tab + 1) % numTabs
		return m, nil

	case keyMatches(msg, m.keys.TabPrev) && !tabPrevOwned:
		m.tab = (m.tab - 1 + numTabs) % numTabs
		return m, nil

	case keyMatches(msg, m.keys.OpenConfig):
		return m, openConfigCmd(m.cfg)
	}

	// Per-tab handling.
	switch m.tab {
	case TabConnections:
		return m.handleConnectionsKey(msg)
	case TabSchema:
		return m.handleSchemaKey(msg)
	case TabQuery:
		return m.handleQueryKey(msg)
	}
	return m, nil
}

// ── Layout ──────────────────────────────────────────────────────────────────

func (m *Model) applyLayout() {
	if m.width == 0 {
		return
	}
	contentH := m.height - titleBarHeight - tabBarHeight - hintBarHeight
	if contentH < 4 {
		contentH = 4
	}

	// Query tab: editor on top, results below.
	mainW := m.width - 2
	editorH := contentH * m.cfg.UI.ResultsSplit / 100
	if editorH < 4 {
		editorH = 4
	}
	resultsH := contentH - editorH - 2
	if resultsH < 3 {
		resultsH = 3
	}
	m.editor.SetWidth(mainW - 2)
	m.editor.SetHeight(editorH)
	m.results.SetWidth(mainW - 2)
	// The results panel renders a fixed header + separator outside the viewport.
	m.results.SetHeight(max(1, resultsH-2))
}

const (
	titleBarHeight = 1
	tabBarHeight   = 2
	hintBarHeight  = 2
)

// ── View ────────────────────────────────────────────────────────────────────

func (m Model) View() tea.View {
	if m.width == 0 {
		v := tea.NewView("Loading sqwee...")
		v.AltScreen = true
		v.MouseMode = tea.MouseModeCellMotion
		return v
	}

	title := m.renderTitleBar()
	tabs := m.renderTabBar()

	var body string
	switch m.tab {
	case TabConnections:
		body = m.viewConnections()
	case TabSchema:
		body = m.viewSchema()
	case TabQuery:
		body = m.viewQuery()
	}

	hint := m.renderHintBar()
	screen := lipgloss.JoinVertical(lipgloss.Left, title, tabs, body, hint)

	if m.modal != nil {
		screen = OverlayModal(screen, m.modal.View(), m.width, m.height)
	}

	v := tea.NewView(screen)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

func (m Model) renderTitleBar() string {
	brand := " " + brandWordmark() + " "
	var ctx string
	if m.activeConn != nil {
		ctx = connectedDot + " " + m.activeConn.Name
	} else {
		ctx = disconnectedDot + dimStyle.Render(" no active connection")
	}
	right := dimStyle.Render("o:config  q:quit")
	pad := m.width - lipgloss.Width(brand) - lipgloss.Width(ctx) - lipgloss.Width(right) - 2
	if pad < 1 {
		pad = 1
	}
	return brand + ctx + strings.Repeat(" ", pad) + right + " "
}

func (m Model) renderTabBar() string {
	var parts []string
	for i, name := range tabNames {
		if Tab(i) == m.tab {
			parts = append(parts, tabActiveStyle.Render(name))
		} else {
			parts = append(parts, tabInactiveStyle.Render(name))
		}
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Top, parts...)
	return bar + "\n"
}

func (m Model) renderHintBar() string {
	divider := hintDividerStyle.Render(strings.Repeat("─", m.width))
	k := func(b interface{ Keys() []string }) string {
		ks := b.Keys()
		if len(ks) == 0 {
			return "?"
		}
		return ks[0]
	}
	pair := func(b interface{ Keys() []string }, label string) string {
		return hintKeyStyle.Render(k(b)) + hintStyle.Render(":"+label)
	}

	var hints []string
	switch m.tab {
	case TabConnections:
		hints = append(hints,
			pair(m.keys.Up, "up"),
			pair(m.keys.Down, "down"),
			pair(m.keys.Connect, "connect"),
			pair(m.keys.InitDB, "init db"),
			pair(m.keys.NewItem, "new"),
			pair(m.keys.EditItem, "edit"),
			pair(m.keys.DeleteItem, "delete"),
			pair(m.keys.CopyItem, "copy cfg"),
			pair(m.keys.TabNext, "schema"),
		)
	case TabSchema:
		if m.filtering {
			line := hintLabelStyle.Render("Hints ") + hintStyle.Render("│ ") +
				hintKeyStyle.Render("type") + hintStyle.Render(" to filter   ") +
				hintKeyStyle.Render("enter") + hintStyle.Render(" apply   ") +
				hintKeyStyle.Render("esc") + hintStyle.Render(" clear")
			divider := hintDividerStyle.Render(strings.Repeat("─", m.width))
			return divider + "\n" + line
		}
		hints = append(hints,
			pair(m.keys.Up, "up"),
			pair(m.keys.Down, "down"),
			hintKeyStyle.Render("/")+hintStyle.Render(":filter"),
			pair(m.keys.Enter, "open"),
			pair(m.keys.Refresh, "refresh"),
			pair(m.keys.NewItem, "new object"),
			pair(m.keys.TabNext, "query"),
		)
	case TabQuery:
		if m.editing {
			hints = append(hints,
				hintKeyStyle.Render("tab")+hintStyle.Render(":indent"),
				hintKeyStyle.Render("ctrl+backspace")+hintStyle.Render(":delete word"),
				hintKeyStyle.Render("ctrl+s")+hintStyle.Render(":run"),
				hintKeyStyle.Render("esc")+hintStyle.Render(":done"),
			)
		} else if m.resultsFocus {
			hints = append(hints,
				pair(m.keys.Up, "row up"),
				pair(m.keys.Down, "row down"),
				pair(m.keys.Left, "col left"),
				pair(m.keys.Right, "col right"),
				pair(m.keys.SelectMode, "select:"+m.selMode.String()),
				pair(m.keys.CopyItem, "copy csv"),
				pair(m.keys.CopyHeaders, "copy+hdr"),
				pair(m.keys.Export, "export"),
				pair(m.keys.TabPrev, "editor"),
			)
		} else {
			if m.scriptFocus {
				hints = append(hints,
					pair(m.keys.Up, "script up"),
					pair(m.keys.Down, "script down"),
					pair(m.keys.Enter, "load"),
					pair(m.keys.Right, "editor"),
				)
			} else {
				hints = append(hints,
					pair(m.keys.Enter, "edit"),
					pair(m.keys.RunQuery, "run"),
				)
			}
			if len(m.scripts) > 0 {
				hints = append(hints, pair(m.keys.Left, "scripts"))
			}
			if m.queryResult != nil && len(m.queryResult.Rows) > 0 {
				hints = append(hints, pair(m.keys.TabNext, "results"))
			}
			hints = append(hints,
				pair(m.keys.OpenEditor, "editor"),
				pair(m.keys.CopyItem, "copy"),
			)
		}
	}
	if !m.editing && !m.filtering {
		hints = append(hints, pair(m.keys.OpenConfig, "config"), pair(m.keys.Quit, "quit"))
	}

	line := hintLabelStyle.Render("Hints ") + hintStyle.Render("│ ") + strings.Join(hints, hintStyle.Render("  "))
	if m.statusMsg != "" {
		line = m.statusMsg + hintStyle.Render("   ") + line
	}
	return divider + "\n" + line
}

func formatExec(r driver.ExecResult) string {
	parts := []string{"OK"}
	if r.RowsAffected >= 0 {
		parts = append(parts, itoaSimple(int(r.RowsAffected))+" rows affected")
	}
	if r.Duration > 0 {
		parts = append(parts, r.Duration.Round(time.Millisecond).String())
	}
	return strings.Join(parts, "  ·  ")
}

func itoaSimple(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	if neg {
		s = "-" + s
	}
	return s
}
