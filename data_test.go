package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSavedConnectionURLUsesPasswordEnv(t *testing.T) {
	t.Setenv("PGPASSWORD", "secret")
	s := SavedConnection{
		Name:        "url-postgres",
		Driver:      "postgres",
		URL:         "postgres://user@db.example.com:5432/app?sslmode=require",
		PasswordEnv: "PGPASSWORD",
	}

	info := s.toConnInfoWithEnv(nil)
	if info.Password != "secret" {
		t.Fatalf("password = %q, want env secret", info.Password)
	}
	if info.Host != "db.example.com" || info.Port != 5432 || info.Database != "app" {
		t.Fatalf("unexpected conn info: %+v", info)
	}
	if info.Options["sslmode"] != "require" {
		t.Fatalf("sslmode = %q", info.Options["sslmode"])
	}
}

func TestSavedConnectionResolvesInlineEnvRefs(t *testing.T) {
	sources := []EnvSource{{
		Name:  ".env-dev",
		Alias: "env-dev",
		Values: map[string]string{
			"PGHOST":           "dev.db.local",
			"PGPORT":           "15432",
			"PGUSER":           "dev_user",
			"PGPASSWORD":       "dev_secret",
			"PGDATABASE":       "dev_app",
			"GATEWAY_HOST":     "gateway.local",
			"GATEWAY_PORT":     "2222",
			"GATEWAY_USER":     "gateway_user",
			"GATEWAY_PASSWORD": "gateway_secret",
			"GATEWAY_KEY_FILE": "/keys/dev.pem",
		},
	}}
	s := SavedConnection{
		Name:     "dev",
		Driver:   "postgres",
		Host:     "env-dev:PGHOST",
		PortEnv:  "env-dev:PGPORT",
		User:     "env-dev:PGUSER",
		Password: "env-dev:PGPASSWORD",
		Database: "env-dev:PGDATABASE",
		Gateway: &SavedGateway{
			Type:     "ssh",
			Host:     "env-dev:GATEWAY_HOST",
			PortEnv:  "env-dev:GATEWAY_PORT",
			User:     "env-dev:GATEWAY_USER",
			Password: "env-dev:GATEWAY_PASSWORD",
			KeyFile:  "env-dev:GATEWAY_KEY_FILE",
		},
	}

	info := s.toConnInfoWithEnv(sources)
	if info.Host != "dev.db.local" || info.Port != 15432 || info.User != "dev_user" || info.Password != "dev_secret" || info.Database != "dev_app" {
		t.Fatalf("unexpected conn info: %+v", info)
	}
	if info.Gateway.Type != "ssh" || info.Gateway.Host != "gateway.local" || info.Gateway.Port != 2222 || info.Gateway.User != "gateway_user" || info.Gateway.Password != "gateway_secret" || info.Gateway.KeyFile != "/keys/dev.pem" {
		t.Fatalf("unexpected gateway info: %+v", info.Gateway)
	}
}

func TestUnresolvedInlineEnvRefKeepsRawValue(t *testing.T) {
	s := SavedConnection{Driver: "postgres", Host: "env-prod:PGHOST", Password: "env-prod:PGPASSWORD"}

	info := s.toConnInfoWithEnv(nil)
	if info.Host != "env-prod:PGHOST" || info.Password != "env-prod:PGPASSWORD" {
		t.Fatalf("unresolved refs should remain raw, got %+v", info)
	}
}

func TestCanonicalEnvRefFallsBackToProcessEnv(t *testing.T) {
	t.Setenv("PGPASSWORD", "process_secret")
	s := SavedConnection{Driver: "postgres", Password: "env:PGPASSWORD"}

	info := s.toConnInfoWithEnv(nil)
	if info.Password != "process_secret" {
		t.Fatalf("password = %q, want process_secret", info.Password)
	}
}

func TestLoadDataRejectsCorruptJSON(t *testing.T) {
	setTempConfigHome(t)
	if err := os.MkdirAll(configDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dataPath(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadData(); err == nil {
		t.Fatal("expected corrupt JSON to fail")
	}
}

func TestSaveDataWritesAtomicallyReadableFile(t *testing.T) {
	setTempConfigHome(t)
	data := AppData{Connections: []SavedConnection{{Name: "saved", Driver: "postgres", Host: "localhost"}}}
	if err := SaveData(data); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadData()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Connections) != 1 || loaded.Connections[0].Name != "saved" {
		t.Fatalf("loaded = %+v", loaded.Connections)
	}
	if _, err := os.Stat(filepath.Join(configDir(), "sqwee.json")); err != nil {
		t.Fatal(err)
	}
}
