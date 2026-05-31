package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"main.go/internal/driver"
)

// dataPath returns the path to the saved-connections store, alongside the
// config file in the delbysoft config directory.
func dataPath() string { return filepath.Join(configDir(), "sqwee.json") }

// SavedConnection is a connection persisted to sqwee.json.
//
// Passwords are NOT stored in plaintext by default. Instead PasswordEnv names
// an environment variable that holds the password, resolved at connect time.
// Set StorePassword=true (via the UI) to persist the literal Password instead.
type SavedConnection struct {
	Name        string            `json:"name"`
	Driver      string            `json:"driver"`
	URL         string            `json:"url,omitempty"`
	Host        string            `json:"host,omitempty"`
	Port        int               `json:"port,omitempty"`
	User        string            `json:"user,omitempty"`
	Database    string            `json:"database,omitempty"`
	PasswordEnv string            `json:"password_env,omitempty"`
	Password    string            `json:"password,omitempty"` // only when StorePassword
	Options     map[string]string `json:"options,omitempty"`
}

// AppData is the root of the persisted JSON store.
type AppData struct {
	Connections []SavedConnection `json:"connections"`
}

// LoadData reads sqwee.json. A missing file yields empty data (no error).
func LoadData() (AppData, error) {
	var data AppData
	b, err := os.ReadFile(dataPath())
	if err != nil {
		if os.IsNotExist(err) {
			return data, nil
		}
		return data, err
	}
	if len(b) == 0 {
		return data, nil
	}
	err = json.Unmarshal(b, &data)
	return data, err
}

// SaveData writes sqwee.json.
func SaveData(data AppData) error {
	if err := os.MkdirAll(configDir(), 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(dataPath(), b, 0o600)
}

// toConnInfo converts a SavedConnection into a driver.ConnInfo, resolving the
// password from its environment variable when StorePassword was not used.
func (s SavedConnection) toConnInfo() driver.ConnInfo {
	pw := s.Password
	if pw == "" && s.PasswordEnv != "" {
		pw = os.Getenv(s.PasswordEnv)
	}
	return driver.ConnInfo{
		Name:     s.Name,
		Driver:   s.Driver,
		URL:      s.URL,
		Host:     s.Host,
		Port:     s.Port,
		User:     s.User,
		Password: pw,
		Database: s.Database,
		Options:  s.Options,
		Source:   "saved",
	}
}
