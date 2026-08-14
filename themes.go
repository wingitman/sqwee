package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Themes selects a shared Delbysoft theme and contains optional per-app
// overrides. The shared file is a collection of [themes.<name>] tables.
type Themes struct {
	ThemeName          string `toml:"theme_name"`
	ThemeFile          string `toml:"theme_file"`
	Foreground         string `toml:"foreground"`
	Background         string `toml:"background"`
	Primary            string `toml:"primary"`
	Accent             string `toml:"accent"`
	Muted              string `toml:"muted"`
	Error              string `toml:"error"`
	Success            string `toml:"success"`
	File               string `toml:"file"`
	Border             string `toml:"border"`
	SelectedBackground string `toml:"selected_background"`
	SelectedForeground string `toml:"selected_foreground"`
	HeaderBackground   string `toml:"header_background"`
	HintKey            string `toml:"hint_key"`
	ParentCrumb        string `toml:"parent_crumb"`
	RootDirectory      string `toml:"root_directory"`
	Clipboard          string `toml:"clipboard"`
	BrandPrimary       string `toml:"brand_primary"`
	BrandSecondary     string `toml:"brand_secondary"`
	Selector           string `toml:"selector"`
	ImageBackground    string `toml:"image_background"`
}

// ResolvedTheme describes the palette after applying the shared theme and any
// local overrides. Terminal means no explicit base colors should be used.
type ResolvedTheme struct {
	Colors   map[string]string
	Terminal bool
}

type themeFile struct {
	Themes map[string]Themes `toml:"themes"`
}

// ThemeFilePath expands the configured shared theme file path.
func ThemeFilePath(cfg Config) string {
	if strings.TrimSpace(cfg.Themes.ThemeFile) == "" {
		return filepath.Join(configDir(), "themes.toml")
	}
	path := strings.TrimSpace(cfg.Themes.ThemeFile)
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, strings.TrimLeft(path[1:], `/\`))
		}
	}
	return filepath.Clean(path)
}

// EnsureThemesFile creates the shared theme file if missing and appends any
// missing starter themes without overwriting existing ones.
func EnsureThemesFile(cfg Config) error {
	path := ThemeFilePath(cfg)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		updated := appendMissingStarterThemes(string(data))
		if updated == string(data) {
			return nil
		}
		return os.WriteFile(path, []byte(updated), 0644)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(defaultThemesTOML), 0644)
}

// ThemeNames returns terminal plus every named theme in the shared file.
func ThemeNames(cfg Config) ([]string, error) {
	var file themeFile
	if _, err := toml.DecodeFile(ThemeFilePath(cfg), &file); err != nil {
		return []string{"terminal"}, err
	}
	names := []string{"terminal"}
	for name := range file.Themes {
		names = append(names, name)
	}
	sort.Strings(names[1:])
	return names, nil
}

// SetThemeName updates only the selected theme in the sqwee config file.
func SetThemeName(name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("theme name cannot be empty")
	}
	path := configPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := writeConfigFile(path, defaultConfig()); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content := setSectionKey(string(data), "themes", "theme_name", quote(name))
	return os.WriteFile(path, []byte(content), 0644)
}

// ResolveTheme loads the selected shared theme and applies local overrides.
// A zero-value result means the caller should use its built-in fallback.
func ResolveTheme(cfg Config) ResolvedTheme {
	result := ResolvedTheme{Colors: map[string]string{}, Terminal: cfg.Themes.ThemeName == "terminal"}
	if !result.Terminal {
		var file themeFile
		if _, err := toml.DecodeFile(ThemeFilePath(cfg), &file); err != nil {
			return ResolvedTheme{}
		}
		selected, ok := file.Themes[cfg.Themes.ThemeName]
		if !ok {
			return ResolvedTheme{}
		}
		result.Colors = themeColors(selected)
		if len(result.Colors) == 0 {
			return ResolvedTheme{}
		}
	}
	for key, value := range themeColors(cfg.Themes) {
		result.Colors[key] = value
	}
	return result
}

// ValidateThemeFile checks the shared theme file without changing it.
func ValidateThemeFile(cfg Config) error {
	path := ThemeFilePath(cfg)
	var file themeFile
	if _, err := toml.DecodeFile(path, &file); err != nil {
		return fmt.Errorf("invalid themes file %q: %w", path, err)
	}
	if len(file.Themes) == 0 {
		return fmt.Errorf("themes file %q contains no [themes.<name>] entries", path)
	}
	return nil
}

func themeColors(t Themes) map[string]string {
	values := map[string]string{
		"foreground":          t.Foreground,
		"background":          t.Background,
		"primary":             t.Primary,
		"accent":              t.Accent,
		"muted":               t.Muted,
		"error":               t.Error,
		"success":             t.Success,
		"file":                t.File,
		"border":              t.Border,
		"selected_background": t.SelectedBackground,
		"selected_foreground": t.SelectedForeground,
		"header_background":   t.HeaderBackground,
		"hint_key":            t.HintKey,
		"parent_crumb":        t.ParentCrumb,
		"root_directory":      t.RootDirectory,
		"clipboard":           t.Clipboard,
		"brand_primary":       t.BrandPrimary,
		"brand_secondary":     t.BrandSecondary,
		"selector":            t.Selector,
		"image_background":    t.ImageBackground,
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		if validThemeColor(value) {
			result[key] = value
		}
	}
	return result
}

func validThemeColor(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if len(value) != 4 && len(value) != 7 {
		return false
	}
	if value[0] != '#' {
		return false
	}
	for _, c := range value[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func appendMissingStarterThemes(content string) string {
	for _, name := range starterThemeNames {
		header := "[themes." + name + "]"
		if strings.Contains(content, header) {
			continue
		}
		block := starterThemeBlock(name)
		if block == "" {
			continue
		}
		if strings.TrimSpace(content) != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "\n" + block + "\n"
	}
	return content
}

func starterThemeBlock(name string) string {
	start := strings.Index(defaultThemesTOML, "[themes."+name+"]")
	if start < 0 {
		return ""
	}
	end := strings.Index(defaultThemesTOML[start:], "\n\n[themes.")
	if end < 0 {
		return strings.TrimSpace(defaultThemesTOML[start:])
	}
	return strings.TrimSpace(defaultThemesTOML[start : start+end])
}

func setSectionKey(content, section, key, value string) string {
	if !sectionContainsKey(content, section, key) {
		return insertSectionLine(content, section, key+" = "+value)
	}
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(content, newline)
	inSection := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inSection = trimmed == "["+section+"]"
			continue
		}
		if !inSection || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, key+"=") || strings.HasPrefix(trimmed, key+" ") {
			indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			comment := ""
			if idx := strings.Index(line, "#"); idx >= 0 {
				comment = " " + strings.TrimSpace(line[idx:])
			}
			lines[i] = indent + key + " = " + value + comment
			break
		}
	}
	return strings.Join(lines, newline)
}

func sectionContainsKey(content, section, key string) bool {
	inSection := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inSection = trimmed == "["+section+"]"
			continue
		}
		if inSection && !strings.HasPrefix(trimmed, "#") &&
			(strings.HasPrefix(trimmed, key+"=") || strings.HasPrefix(trimmed, key+" ")) {
			return true
		}
	}
	return false
}

func insertSectionLine(content, section, line string) string {
	newline := "\n"
	if strings.Contains(content, "\r\n") {
		newline = "\r\n"
	}
	lines := strings.Split(content, newline)
	sectionHeader := "[" + section + "]"
	sectionIdx := -1
	insertIdx := len(lines)
	for i, text := range lines {
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if trimmed == sectionHeader {
				sectionIdx = i
				insertIdx = len(lines)
				continue
			}
			if sectionIdx >= 0 {
				insertIdx = i
				break
			}
		}
	}
	if sectionIdx < 0 {
		if strings.TrimSpace(content) != "" && !strings.HasSuffix(content, newline) {
			content += newline
		}
		return content + newline + sectionHeader + newline + line + newline
	}
	lines = append(lines[:insertIdx], append([]string{line}, lines[insertIdx:]...)...)
	return strings.Join(lines, newline)
}

func quote(s string) string { return fmt.Sprintf("%q", s) }

var starterThemeNames = []string{
	"ocean", "high_contrast", "redteam", "blueteam", "vim", "neovim",
	"monotone", "cyberpunk", "sands",
}

const defaultThemesTOML = `# Shared themes for Delbysoft terminal applications.
# Add themes as [themes.name] tables. Colors use #RGB or #RRGGBB values.
# Supported colors: foreground, background, primary, accent, muted, error,
# success, file, border, selected_background, selected_foreground,
# header_background, hint_key, parent_crumb, root_directory, clipboard,
# brand_primary, brand_secondary, selector, image_background.

[themes.ocean]
foreground = "#D7E3FF"
background = "#101522"
primary = "#7C9EF0"
accent = "#F0A47C"
muted = "#66708F"
error = "#F07C7C"
success = "#7CF09C"
file = "#B0B0CC"
border = "#35415F"
selected_background = "#3568B8"
selected_foreground = "#FFFFFF"
header_background = "#17213A"
hint_key = "#FFE66D"
parent_crumb = "#58627F"
root_directory = "#7D88A8"
clipboard = "#F0E07C"
brand_primary = "#FFFFFF"
brand_secondary = "#6F86FF"
selector = "#FFFFFF"
image_background = "#101522"

[themes.high_contrast]
foreground = "#FFFFFF"
background = "#000000"
primary = "#00FFFF"
accent = "#FFFF00"
muted = "#C0C0C0"
error = "#FF5555"
success = "#00FF00"
file = "#FFFFFF"
border = "#FFFFFF"
selected_background = "#FFFF00"
selected_foreground = "#000000"
header_background = "#000000"
hint_key = "#FFFF00"
parent_crumb = "#C0C0C0"
root_directory = "#FFFFFF"
clipboard = "#FFFF00"
brand_primary = "#FFFFFF"
brand_secondary = "#00FFFF"
selector = "#FFFF00"
image_background = "#000000"

[themes.redteam]
foreground = "#FFE8E8"
background = "#210B0B"
primary = "#FF6B6B"
accent = "#FFB86B"
muted = "#A97878"
error = "#FF3333"
success = "#8BE28B"
file = "#F2CACA"
border = "#713333"
selected_background = "#9E2020"
selected_foreground = "#FFFFFF"
header_background = "#3A1010"
hint_key = "#FFD166"
parent_crumb = "#805555"
root_directory = "#C88A8A"
clipboard = "#FFD166"
brand_primary = "#FFFFFF"
brand_secondary = "#FF4D4D"
selector = "#FFFFFF"
image_background = "#210B0B"

[themes.blueteam]
foreground = "#E7F1FF"
background = "#081525"
primary = "#69A7FF"
accent = "#72E0D1"
muted = "#6D86A5"
error = "#FF7B8B"
success = "#7DDEB3"
file = "#C4D8F2"
border = "#294C75"
selected_background = "#1557A5"
selected_foreground = "#FFFFFF"
header_background = "#0D223D"
hint_key = "#F4D35E"
parent_crumb = "#4D6888"
root_directory = "#8BA9CB"
clipboard = "#F4D35E"
brand_primary = "#FFFFFF"
brand_secondary = "#69A7FF"
selector = "#FFFFFF"
image_background = "#081525"

[themes.vim]
foreground = "#D7D7AF"
background = "#1C1C1C"
primary = "#87AF87"
accent = "#D7AF5F"
muted = "#808080"
error = "#AF5F5F"
success = "#87AF87"
file = "#D7D7AF"
border = "#5F5F5F"
selected_background = "#5F5F00"
selected_foreground = "#FFFFAF"
header_background = "#262626"
hint_key = "#FFFF87"
parent_crumb = "#5F875F"
root_directory = "#AFAF87"
clipboard = "#D7AF5F"
brand_primary = "#FFFFFF"
brand_secondary = "#87AF87"
selector = "#FFFFAF"
image_background = "#1C1C1C"

[themes.neovim]
foreground = "#C8D3F5"
background = "#1B1D2B"
primary = "#82AAFF"
accent = "#FFC777"
muted = "#828BB8"
error = "#FF757F"
success = "#C3E88D"
file = "#C8D3F5"
border = "#444A73"
selected_background = "#394B70"
selected_foreground = "#FFFFFF"
header_background = "#222436"
hint_key = "#FFCB6B"
parent_crumb = "#545C8C"
root_directory = "#A9B8E8"
clipboard = "#C3E88D"
brand_primary = "#FFFFFF"
brand_secondary = "#82AAFF"
selector = "#FFFFFF"
image_background = "#1B1D2B"

[themes.monotone]
foreground = "#D0D0D0"
background = "#202020"
primary = "#E0E0E0"
accent = "#FFFFFF"
muted = "#808080"
error = "#B0B0B0"
success = "#D8D8D8"
file = "#C0C0C0"
border = "#606060"
selected_background = "#D0D0D0"
selected_foreground = "#101010"
header_background = "#303030"
hint_key = "#FFFFFF"
parent_crumb = "#707070"
root_directory = "#A0A0A0"
clipboard = "#FFFFFF"
brand_primary = "#FFFFFF"
brand_secondary = "#A0A0A0"
selector = "#FFFFFF"
image_background = "#202020"

[themes.cyberpunk]
foreground = "#F4E8FF"
background = "#170D24"
primary = "#00E5FF"
accent = "#FFEA00"
muted = "#9B75B5"
error = "#FF3864"
success = "#39FF14"
file = "#E6CFFF"
border = "#7A2F9B"
selected_background = "#D100A8"
selected_foreground = "#FFFFFF"
header_background = "#28113C"
hint_key = "#FFEA00"
parent_crumb = "#754D91"
root_directory = "#C68AF0"
clipboard = "#FFEA00"
brand_primary = "#FFFFFF"
brand_secondary = "#00E5FF"
selector = "#FFFFFF"
image_background = "#170D24"

[themes.sands]
foreground = "#F3E7CE"
background = "#282016"
primary = "#E4B96A"
accent = "#F2D06B"
muted = "#9F8B6D"
error = "#D9795B"
success = "#A8B875"
file = "#E8D6B5"
border = "#6D583C"
selected_background = "#A66A2C"
selected_foreground = "#FFF4D6"
header_background = "#382A1B"
hint_key = "#F2D06B"
parent_crumb = "#806B4E"
root_directory = "#CBAE7A"
clipboard = "#F2D06B"
brand_primary = "#FFF4D6"
brand_secondary = "#E4B96A"
selector = "#FFF4D6"
image_background = "#282016"
`
