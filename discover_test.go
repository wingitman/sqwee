package main

import (
	"path/filepath"
	"testing"
)

func TestDiscoverSQLiteFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "app.sqlite"), "")
	writeFile(t, filepath.Join(dir, "cache.db"), "")
	writeFile(t, filepath.Join(dir, "data.sqlite3"), "")
	writeFile(t, filepath.Join(dir, "readme.md"), "nope")

	withWD(t, dir, func() {
		got := discoverSQLiteFiles()
		if len(got) != 3 {
			t.Fatalf("expected 3 sqlite files, got %d: %v", len(got), got)
		}
		for _, ci := range got {
			if ci.Driver != "sqlite" {
				t.Errorf("driver = %q, want sqlite", ci.Driver)
			}
			if !filepath.IsAbs(ci.Database) {
				t.Errorf("database path %q is not absolute", ci.Database)
			}
		}
	})
}

func TestDiscoverFromDotenvSQLitePath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"),
		"# Database\nDATABASE_PATH=./timid.sqlite\nOTHER=1\n")

	withWD(t, dir, func() {
		got := discoverFromDotenv()
		if len(got) != 1 {
			t.Fatalf("expected 1 discovered conn, got %d: %v", len(got), got)
		}
		ci := got[0]
		if ci.Driver != "sqlite" {
			t.Fatalf("driver = %q, want sqlite", ci.Driver)
		}
		if filepath.Base(ci.Database) != "timid.sqlite" {
			t.Errorf("database = %q", ci.Database)
		}
		if !filepath.IsAbs(ci.Database) {
			t.Errorf("expected absolute path, got %q", ci.Database)
		}
	})
}

func TestDiscoverFromDotenvURL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"),
		"DATABASE_URL=postgres://u:p@localhost:5432/app?sslmode=require\n")

	withWD(t, dir, func() {
		got := discoverFromDotenv()
		if len(got) != 1 || got[0].Driver != "postgres" {
			t.Fatalf("expected postgres conn, got %v", got)
		}
		if got[0].Options["sslmode"] != "require" {
			t.Fatalf("sslmode option = %q", got[0].Options["sslmode"])
		}
	})
}

func TestDiscoverFromDotenvScansEnvStarFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"), "DATABASE_URL=postgres://u:p@localhost:5432/app\n")
	writeFile(t, filepath.Join(dir, ".env-dev"), "DATABASE_URL=mysql://u:p@localhost:3306/dev\n")
	writeFile(t, filepath.Join(dir, ".env.example"), "DATABASE_PATH=./example.sqlite\n")

	withWD(t, dir, func() {
		got := discoverFromDotenv()
		if len(got) != 3 {
			t.Fatalf("expected 3 discovered conns, got %d: %v", len(got), got)
		}
		sources := map[string]bool{}
		for _, ci := range got {
			sources[ci.Source] = true
		}
		for _, want := range []string{".env:DATABASE_URL", ".env-dev:DATABASE_URL", ".env.example:DATABASE_PATH"} {
			if !sources[want] {
				t.Fatalf("missing source %q in %v", want, sources)
			}
		}
	})
}
