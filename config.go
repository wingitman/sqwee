package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"charm.land/bubbles/v2/key"
	"github.com/BurntSushi/toml"
)

// ── Config ────────────────────────────────────────────────────────────────────
//
// Config is loaded from the platform-appropriate path on startup:
//   Linux:   ~/.config/delbysoft/sqwee.toml
//   macOS:   ~/Library/Application Support/delbysoft/sqwee.toml
//   Windows: %AppData%\Roaming\delbysoft\sqwee.toml
//
// If the file doesn't exist it is created with all defaults and comments.
// If the file exists but is missing new keys (migration), it is rewritten
// with the new keys added while preserving all existing user values.

// ConfigKeys holds every configurable key binding. Every key used anywhere in
// sqwee is listed here so the user can remap anything by editing the TOML file.
type ConfigKeys struct {
	// ── Navigation ──────────────────────────────────────────────────────────
	Up     string `toml:"up"`
	Down   string `toml:"down"`
	Left   string `toml:"left"`
	Right  string `toml:"right"`
	Enter  string `toml:"enter"`
	Escape string `toml:"escape"`

	// Section cycling
	TabNext string `toml:"tab_next"`
	TabPrev string `toml:"tab_prev"`

	// ── Actions ─────────────────────────────────────────────────────────────
	RunQuery   string `toml:"run_query"`
	NewItem    string `toml:"new_item"`
	EditItem   string `toml:"edit_item"`
	DeleteItem string `toml:"delete_item"`
	Connect    string `toml:"connect"`
	Refresh    string `toml:"refresh"`
	CopyItem   string `toml:"copy_item"`
	InitDB     string `toml:"init_db"` // initialize/provision a new database

	// ── Results grid ─────────────────────────────────────────────────────────
	SelectMode  string `toml:"select_mode"`  // cycle cell/row/column/all selection
	CopyHeaders string `toml:"copy_headers"` // copy selection including column headers
	Export      string `toml:"export"`       // export results to ~/Downloads

	// ── Open in editor ───────────────────────────────────────────────────────
	OpenEditor string `toml:"open_editor"`
	OpenConfig string `toml:"open_config"`

	// ── App-level ────────────────────────────────────────────────────────────
	Quit string `toml:"quit"`
}

// ConfigUI holds UI preferences.
type ConfigUI struct {
	SidebarWidth int    `toml:"sidebar_width"`
	ResultsSplit int    `toml:"results_split"` // % of the query tab height for the editor
	Theme        string `toml:"theme"`
	Editor       string `toml:"editor"`        // optional command overriding $VISUAL/$EDITOR
	FileExplorer string `toml:"file_explorer"` // optional command for opening folders after export
}

// ConfigDiscovery controls automatic connection discovery on startup.
type ConfigDiscovery struct {
	ScanEnv    bool `toml:"scan_env"`
	ScanDotenv bool `toml:"scan_dotenv"`
	ScanPgpass bool `toml:"scan_pgpass"`
	ScanSQLite bool `toml:"scan_sqlite"`
	ScanSQL    bool `toml:"scan_sql"`
	ScanPorts  bool `toml:"scan_ports"`
}

// Config is the top-level config struct.
type Config struct {
	Keys      ConfigKeys      `toml:"keys"`
	UI        ConfigUI        `toml:"ui"`
	Discovery ConfigDiscovery `toml:"discovery"`
}

// defaultConfig returns the full set of defaults. Navigation defaults are
// vim-style (k/j/h/l), matching the rest of the delbysoft family.
func defaultConfig() Config {
	return Config{
		Keys: ConfigKeys{
			Up:          "k",
			Down:        "j",
			Left:        "h",
			Right:       "l",
			Enter:       "enter",
			Escape:      "esc",
			TabNext:     "tab",
			TabPrev:     "shift+tab",
			RunQuery:    "s",
			NewItem:     "n",
			EditItem:    "e",
			DeleteItem:  "d",
			Connect:     "c",
			Refresh:     "r",
			CopyItem:    "y",
			InitDB:      "i",
			SelectMode:  "v",
			CopyHeaders: "Y",
			Export:      "X",
			OpenEditor:  "E",
			OpenConfig:  "o",
			Quit:        "q",
		},
		UI: ConfigUI{
			SidebarWidth: 32,
			ResultsSplit: 50,
			Theme:        "dark",
			Editor:       "",
			FileExplorer: "",
		},
		Discovery: ConfigDiscovery{
			ScanEnv:    true,
			ScanDotenv: true,
			ScanPgpass: true,
			ScanSQLite: true,
			ScanSQL:    true,
			ScanPorts:  true,
		},
	}
}

// ── Paths ─────────────────────────────────────────────────────────────────────

func configPath() string { return filepath.Join(configDir(), "sqwee.toml") }

func configDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "delbysoft")
}

// ── Load / Save ───────────────────────────────────────────────────────────────

// LoadConfig reads the config file.
// • First launch: creates the file with all defaults + comments, returns defaults.
// • Existing file: decodes user values, then migrates missing keys if needed.
func LoadConfig() (Config, error) {
	cfg := defaultConfig()
	path := configPath()

	if _, err := os.Stat(path); os.IsNotExist(err) {
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o750); mkErr != nil {
			return cfg, mkErr
		}
		if wErr := writeConfigFile(path, cfg); wErr != nil {
			return cfg, wErr
		}
		return cfg, nil
	}

	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return cfg, err
	}

	if configNeedsMigration(path) {
		_ = writeConfigFile(path, cfg) // non-fatal
	}

	return cfg, nil
}

// SaveConfig writes the current config back to disk (used after live-reload).
func SaveConfig(cfg Config) error {
	return writeConfigFile(configPath(), cfg)
}

// configNeedsMigration returns true if the file is missing any key that ships
// with this version of sqwee. Required key names are derived from the struct
// TOML tags so this stays in sync as new fields are added.
func configNeedsMigration(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(data)
	for _, key := range tomlKeys(reflect.TypeOf(ConfigKeys{}), true) {
		if !strings.Contains(s, key+" =") {
			return true
		}
	}
	for _, key := range tomlKeys(reflect.TypeOf(ConfigDiscovery{}), false) {
		if !strings.Contains(s, key+" =") {
			return true
		}
	}
	for _, key := range tomlKeys(reflect.TypeOf(ConfigUI{}), false) {
		if !strings.Contains(s, key+" =") {
			return true
		}
	}
	if !strings.Contains(s, "[discovery]") {
		return true
	}
	return false
}

func tomlKeys(t reflect.Type, skipOmitEmpty bool) []string {
	keys := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		tag := t.Field(i).Tag.Get("toml")
		if tag == "" || (skipOmitEmpty && strings.Contains(tag, "omitempty")) {
			continue
		}
		keys = append(keys, strings.Split(tag, ",")[0])
	}
	return keys
}

func writeConfigFile(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(buildConfigTOML(cfg))
	return err
}

// buildConfigTOML produces the full commented TOML string for writing.
func buildConfigTOML(cfg Config) string {
	k := cfg.Keys
	ui := cfg.UI
	d := cfg.Discovery
	q := func(s string) string { return `"` + s + `"` }
	boolStr := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	itoa := func(n int) string {
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

	return "# sqwee configuration file\n" +
		"# Edit any value below and press o inside sqwee to reload it live.\n" +
		"# Key names: letters, \"up\", \"down\", \"left\", \"right\", \"enter\", \"esc\",\n" +
		"#             \"tab\", \"shift+tab\", \"ctrl+x\", \"alt+x\", etc.\n" +
		"\n" +
		"[keys]\n" +
		"\n" +
		"# ── Navigation ─────────────────────────────────────────────────────────\n" +
		"up           = " + q(k.Up) + "       # move cursor / scroll up\n" +
		"down         = " + q(k.Down) + "       # move cursor / scroll down\n" +
		"left         = " + q(k.Left) + "       # move left / previous panel\n" +
		"right        = " + q(k.Right) + "       # move right / next panel\n" +
		"enter        = " + q(k.Enter) + "    # open / select\n" +
		"escape       = " + q(k.Escape) + "      # cancel / back\n" +
		"\n" +
		"# ── Section cycling (Tab / Shift+Tab by default) ────────────────────────\n" +
		"tab_next     = " + q(k.TabNext) + "      # next tab\n" +
		"tab_prev     = " + q(k.TabPrev) + "  # previous tab\n" +
		"\n" +
		"# ── Actions ─────────────────────────────────────────────────────────────\n" +
		"run_query    = " + q(k.RunQuery) + "       # run the SQL in the query editor\n" +
		"new_item     = " + q(k.NewItem) + "       # new connection / object\n" +
		"edit_item    = " + q(k.EditItem) + "       # edit selected item\n" +
		"delete_item  = " + q(k.DeleteItem) + "       # delete selected item\n" +
		"connect      = " + q(k.Connect) + "       # connect to selected connection\n" +
		"refresh      = " + q(k.Refresh) + "       # refresh schema / reconnect\n" +
		"copy_item    = " + q(k.CopyItem) + "       # copy focused content to clipboard\n" +
		"init_db      = " + q(k.InitDB) + "       # initialize/provision a new database\n" +
		"\n" +
		"# ── Results grid (Query tab) ────────────────────────────────────────────\n" +
		"select_mode  = " + q(k.SelectMode) + "       # cycle selection: cell -> row -> column -> all\n" +
		"copy_headers = " + q(k.CopyHeaders) + "       # copy the current selection including column headers\n" +
		"export       = " + q(k.Export) + "       # export results to ~/Downloads (CSV / TSV / JSON)\n" +
		"\n" +
		"# ── Open in editor ──────────────────────────────────────────────────────\n" +
		"open_editor  = " + q(k.OpenEditor) + "       # open the query / definition in $EDITOR\n" +
		"open_config  = " + q(k.OpenConfig) + "       # open this config file in $EDITOR\n" +
		"\n" +
		"# ── App-level ────────────────────────────────────────────────────────────\n" +
		"quit         = " + q(k.Quit) + "       # quit sqwee (Ctrl+C always works too)\n" +
		"\n" +
		"[ui]\n" +
		"sidebar_width = " + itoa(ui.SidebarWidth) + "   # width of the left list in columns\n" +
		"results_split = " + itoa(ui.ResultsSplit) + "   # % of the query tab height for the editor\n" +
		"theme         = " + q(ui.Theme) + "     # colour theme (currently only \"dark\" is supported)\n" +
		"editor        = " + q(ui.Editor) + "        # optional editor command; empty uses $VISUAL, $EDITOR, then OS default\n" +
		"file_explorer = " + q(ui.FileExplorer) + "        # optional folder opener; empty uses OS default\n" +
		"\n" +
		"[discovery]\n" +
		"scan_env    = " + boolStr(d.ScanEnv) + "   # scan DATABASE_URL / PG* / MYSQL_* env vars\n" +
		"scan_dotenv = " + boolStr(d.ScanDotenv) + "   # scan .env files in the working directory\n" +
		"scan_pgpass = " + boolStr(d.ScanPgpass) + "   # scan ~/.pgpass for Postgres connections\n" +
		"scan_sqlite = " + boolStr(d.ScanSQLite) + "   # scan the working directory for *.sqlite / *.db files\n" +
		"scan_sql    = " + boolStr(d.ScanSQL) + "   # import *.sql script files from the working directory\n" +
		"scan_ports  = " + boolStr(d.ScanPorts) + "   # detect DB servers listening on localhost (1433/5432/3306)\n"
}

// ── KeyMap ────────────────────────────────────────────────────────────────────

type KeyMap struct {
	Up     key.Binding
	Down   key.Binding
	Left   key.Binding
	Right  key.Binding
	Enter  key.Binding
	Escape key.Binding

	TabNext key.Binding
	TabPrev key.Binding

	RunQuery    key.Binding
	NewItem     key.Binding
	EditItem    key.Binding
	DeleteItem  key.Binding
	Connect     key.Binding
	Refresh     key.Binding
	CopyItem    key.Binding
	InitDB      key.Binding
	SelectMode  key.Binding
	CopyHeaders key.Binding
	Export      key.Binding
	OpenEditor  key.Binding
	OpenConfig  key.Binding

	Quit key.Binding
}

func bindingFor(k, helpText string) key.Binding {
	if k == "" {
		return key.NewBinding()
	}
	return key.NewBinding(key.WithKeys(k), key.WithHelp(k, helpText))
}

func bindingForWithExtra(k, helpText string, extra ...string) key.Binding {
	if k == "" && len(extra) == 0 {
		return key.NewBinding()
	}
	keys := extra
	if k != "" {
		keys = append([]string{k}, extra...)
	}
	return key.NewBinding(key.WithKeys(keys...), key.WithHelp(k, helpText))
}

// NewKeyMap builds a KeyMap from a Config.
func NewKeyMap(cfg Config) KeyMap {
	k := cfg.Keys
	return KeyMap{
		Up:          bindingFor(k.Up, "up"),
		Down:        bindingFor(k.Down, "down"),
		Left:        bindingFor(k.Left, "left"),
		Right:       bindingFor(k.Right, "right"),
		Enter:       bindingFor(k.Enter, "select"),
		Escape:      bindingFor(k.Escape, "back"),
		TabNext:     bindingFor(k.TabNext, "next tab"),
		TabPrev:     bindingFor(k.TabPrev, "prev tab"),
		RunQuery:    bindingFor(k.RunQuery, "run query"),
		NewItem:     bindingFor(k.NewItem, "new"),
		EditItem:    bindingFor(k.EditItem, "edit"),
		DeleteItem:  bindingFor(k.DeleteItem, "delete"),
		Connect:     bindingFor(k.Connect, "connect"),
		Refresh:     bindingFor(k.Refresh, "refresh"),
		CopyItem:    bindingFor(k.CopyItem, "copy"),
		InitDB:      bindingFor(k.InitDB, "init db"),
		SelectMode:  bindingFor(k.SelectMode, "select mode"),
		CopyHeaders: bindingFor(k.CopyHeaders, "copy w/ headers"),
		Export:      bindingFor(k.Export, "export"),
		OpenEditor:  bindingFor(k.OpenEditor, "editor"),
		OpenConfig:  bindingFor(k.OpenConfig, "config"),
		Quit:        bindingForWithExtra(k.Quit, "quit", "ctrl+c"),
	}
}

func keyMatches(msg interface{ String() string }, binding key.Binding) bool {
	for _, k := range binding.Keys() {
		if msg.String() == k {
			return true
		}
	}
	return false
}
