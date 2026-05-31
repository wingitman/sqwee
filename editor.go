package main

import (
	"os"
	"os/exec"
	"runtime"

	tea "charm.land/bubbletea/v2"
)

// editorClosedMsg carries the contents of a temp file edited in $EDITOR.
type editorClosedMsg struct {
	content string
	err     string
}

// openConfigCmd suspends the TUI, opens sqwee.toml in $EDITOR, then resumes and
// reloads the config so keybind changes apply live.
func openConfigCmd() tea.Cmd {
	path := configPath()
	return tea.ExecProcess(exec.Command(resolveEditor(), path), func(err error) tea.Msg {
		cfg, _ := LoadConfig()
		return configReloadedMsg{cfg: cfg}
	})
}

// openEditorCmd writes text to a temp file, opens it in $EDITOR, and returns the
// edited contents when the editor closes.
func openEditorCmd(text, ext string) tea.Cmd {
	f, err := os.CreateTemp("", "sqwee-*"+ext)
	if err != nil {
		return func() tea.Msg { return editorClosedMsg{err: err.Error()} }
	}
	path := f.Name()
	_, _ = f.WriteString(text)
	f.Close()

	return tea.ExecProcess(exec.Command(resolveEditor(), path), func(err error) tea.Msg {
		defer os.Remove(path)
		if err != nil {
			return editorClosedMsg{err: err.Error()}
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return editorClosedMsg{err: rerr.Error()}
		}
		return editorClosedMsg{content: string(b)}
	})
}

// resolveEditor returns the user's preferred editor, falling back per-OS.
func resolveEditor() string {
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "nano"
}
