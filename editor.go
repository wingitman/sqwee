package main

import (
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editorClosedMsg carries the contents of a temp file edited in $EDITOR.
type editorClosedMsg struct {
	content string
	err     string
}

// openConfigCmd suspends the TUI, opens sqwee.toml in $EDITOR, then resumes and
// reloads the config so keybind changes apply live.
func openConfigCmd(cfg Config) tea.Cmd {
	path := configPath()
	return tea.ExecProcess(commandWithPath(resolveEditor(cfg), path), func(err error) tea.Msg {
		cfg, _ := LoadConfig()
		return configReloadedMsg{cfg: cfg}
	})
}

// openEditorCmd writes text to a temp file, opens it in $EDITOR, and returns the
// edited contents when the editor closes.
func openEditorCmd(cfg Config, text, ext string) tea.Cmd {
	f, err := os.CreateTemp("", "sqwee-*"+ext)
	if err != nil {
		return func() tea.Msg { return editorClosedMsg{err: err.Error()} }
	}
	path := f.Name()
	_, _ = f.WriteString(text)
	f.Close()

	return tea.ExecProcess(commandWithPath(resolveEditor(cfg), path), func(err error) tea.Msg {
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
func resolveEditor(cfg Config) string {
	if e := strings.TrimSpace(cfg.UI.Editor); e != "" {
		return e
	}
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

func openFileExplorerCmd(cfg Config, path string) tea.Cmd {
	cmd := resolveFileExplorer(cfg)
	if cmd == "" {
		return nil
	}
	return tea.ExecProcess(commandWithPath(cmd, path), func(err error) tea.Msg {
		if err != nil {
			return explorerOpenedMsg{err: err.Error()}
		}
		return explorerOpenedMsg{}
	})
}

type explorerOpenedMsg struct{ err string }

func resolveFileExplorer(cfg Config) string {
	if e := strings.TrimSpace(cfg.UI.FileExplorer); e != "" {
		return e
	}
	switch runtime.GOOS {
	case "darwin":
		return "open"
	case "windows":
		return "explorer"
	default:
		return "xdg-open"
	}
}

func commandWithPath(command, path string) *exec.Cmd {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		parts = []string{command}
	}
	args := append([]string{}, parts[1:]...)
	args = append(args, path)
	return exec.Command(parts[0], args...)
}
