package main

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"main.go/internal/driver"
)

// dataPath returns the path to the saved-connections store, alongside the
// config file in the delbysoft config directory.
func dataPath() string { return filepath.Join(configDir(), "sqwee.json") }

// SavedConnection is a connection persisted to sqwee.json.
//
// String fields may be literal values or inline env references such as
// env:PGHOST, env-dev:PGHOST or env.example:PGHOST. PasswordEnv is legacy and is
// still resolved as env:PASSWORD_KEY when Password is empty.
type SavedConnection struct {
	Name        string            `json:"name"`
	Driver      string            `json:"driver"`
	URL         string            `json:"url,omitempty"`
	Host        string            `json:"host,omitempty"`
	Port        int               `json:"port,omitempty"`
	PortEnv     string            `json:"port_env,omitempty"`
	User        string            `json:"user,omitempty"`
	Database    string            `json:"database,omitempty"`
	PasswordEnv string            `json:"password_env,omitempty"`
	Password    string            `json:"password,omitempty"`
	Gateway     *SavedGateway     `json:"gateway,omitempty"`
	Options     map[string]string `json:"options,omitempty"`
}

// SavedGateway is an optional SSH gateway persisted with a connection. String
// fields may be literal values or inline env refs, matching SavedConnection.
type SavedGateway struct {
	Type     string `json:"type,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	PortEnv  string `json:"port_env,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
	KeyFile  string `json:"key_file,omitempty"`
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
	dir := configDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "sqwee-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, dataPath())
}

// toConnInfo converts a SavedConnection into a driver.ConnInfo, resolving inline
// env references from project .env* files and the process environment.
func (s SavedConnection) toConnInfo() driver.ConnInfo {
	return s.toConnInfoWithEnv(discoverEnvSources())
}

func (s SavedConnection) toConnInfoWithEnv(sources []EnvSource) driver.ConnInfo {
	name := resolveEnvValue(s.Name, sources).Value
	drv := resolveEnvValue(s.Driver, sources).Value
	rawURL := resolveEnvValue(s.URL, sources).Value
	host := resolveEnvValue(s.Host, sources).Value
	user := resolveEnvValue(s.User, sources).Value
	database := resolveEnvValue(s.Database, sources).Value
	pw := resolveEnvValue(s.Password, sources).Value
	if pw == "" && s.PasswordEnv != "" {
		pw = resolveEnvValue("env:"+s.PasswordEnv, sources).Value
	}
	port := s.Port
	if s.PortEnv != "" {
		if p, err := strconv.Atoi(resolveEnvValue(s.PortEnv, sources).Value); err == nil {
			port = p
		}
	}
	if strings.TrimSpace(rawURL) != "" {
		raw := normalizeSavedURL(rawURL, user, database, pw)
		if info, ok := parseURLConn(raw, "saved"); ok {
			if name != "" {
				info.Name = name
			}
			if drv != "" {
				info.Driver = drv
			}
			if info.Password == "" {
				info.Password = pw
			}
			if info.Options == nil && len(s.Options) > 0 {
				info.Options = map[string]string{}
			}
			for k, v := range s.Options {
				info.Options[k] = v
			}
			info.Gateway = savedGatewayInfoWithEnv(s.Gateway, sources)
			return info
		}
	}
	return driver.ConnInfo{
		Name:     name,
		Driver:   drv,
		URL:      rawURL,
		Host:     host,
		Port:     port,
		User:     user,
		Password: pw,
		Database: database,
		Options:  s.Options,
		Gateway:  savedGatewayInfoWithEnv(s.Gateway, sources),
		Source:   "saved",
	}
}

func savedGatewayInfoWithEnv(g *SavedGateway, sources []EnvSource) driver.GatewayInfo {
	if g == nil {
		return driver.GatewayInfo{}
	}
	typ := resolveEnvValue(g.Type, sources).Value
	host := resolveEnvValue(g.Host, sources).Value
	user := resolveEnvValue(g.User, sources).Value
	password := resolveEnvValue(g.Password, sources).Value
	keyFile := resolveEnvValue(g.KeyFile, sources).Value
	port := g.Port
	if g.PortEnv != "" {
		if p, err := strconv.Atoi(resolveEnvValue(g.PortEnv, sources).Value); err == nil {
			port = p
		}
	}
	if typ == "" && (host != "" || user != "" || password != "" || keyFile != "" || port > 0) {
		typ = "ssh"
	}
	return driver.GatewayInfo{
		Type:     typ,
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		KeyFile:  keyFile,
	}
}

func normalizeSavedURL(rawURL, user, database, password string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return rawURL
	}
	username := u.User.Username()
	if username == "" {
		username = user
	}
	urlPassword, hasURLPassword := u.User.Password()
	if !hasURLPassword {
		urlPassword = password
	}
	if username != "" {
		if urlPassword != "" {
			u.User = url.UserPassword(username, urlPassword)
		} else {
			u.User = url.User(username)
		}
	}
	if u.Path == "" && database != "" {
		u.Path = "/" + database
	}
	return u.String()
}
