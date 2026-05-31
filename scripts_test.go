package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitSQLBatchesNonMSSQL(t *testing.T) {
	// Non-mssql drivers get the whole script as one batch.
	got := splitSQLBatches("SELECT 1;\nGO\nSELECT 2;", "postgres")
	if len(got) != 1 {
		t.Fatalf("expected 1 batch for postgres, got %d: %v", len(got), got)
	}
}

func TestSplitSQLBatchesMSSQL(t *testing.T) {
	script := "CREATE DATABASE Foo;\nGO\nUSE Foo;\nGO\nSELECT 1;\ngo 1\nSELECT 2;"
	got := splitSQLBatches(script, "mssql")
	if len(got) != 4 {
		t.Fatalf("expected 4 batches, got %d: %#v", len(got), got)
	}
	if got[0] != "CREATE DATABASE Foo;" {
		t.Errorf("batch 0 = %q", got[0])
	}
	if got[3] != "SELECT 2;" {
		t.Errorf("batch 3 = %q", got[3])
	}
}

func TestIsGoSeparator(t *testing.T) {
	cases := map[string]bool{
		"GO":         true,
		"go":         true,
		"  GO  ":     true,
		"GO 5":       true,
		"GO -- run":  true,
		"GONE":       false,
		"SELECT GO":  false,
		"GO BACK":    false,
		"":           false,
		"-- comment": false,
	}
	for in, want := range cases {
		if got := isGoSeparator(in); got != want {
			t.Errorf("isGoSeparator(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestStripLeadingComments(t *testing.T) {
	in := "-- a comment\n\n  -- another\nSELECT 1;"
	got := stripLeadingComments(in)
	if got != "SELECT 1;" {
		t.Errorf("stripLeadingComments = %q", got)
	}
}

func TestDiscoverSQLScripts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "01_schema.sql"), "SELECT 1;")
	writeFile(t, filepath.Join(dir, "notes.txt"), "ignore me")
	writeFile(t, filepath.Join(dir, "02_seed.sql"), "SELECT 2;")

	withWD(t, dir, func() {
		cfg := defaultConfig()
		scripts := discoverSQLScripts(cfg)
		if len(scripts) != 2 {
			t.Fatalf("expected 2 scripts, got %d: %v", len(scripts), scripts)
		}

		cfg.Discovery.ScanSQL = false
		if s := discoverSQLScripts(cfg); s != nil {
			t.Errorf("expected nil when scan_sql disabled, got %v", s)
		}
	})
}

// ── helpers ──────────────────────────────────────────────────────────────────

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func withWD(t *testing.T, dir string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(orig)
	fn()
}
