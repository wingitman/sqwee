package main

import (
	"os"
	"path/filepath"
	"strings"
)

// sqlScript is a *.sql file discovered in the working directory and made
// available to load into the query editor.
type sqlScript struct {
	Name string // file name (display)
	Path string // absolute path
}

// discoverSQLScripts scans the working directory (non-recursively) for *.sql
// files. Disabled by the [discovery] scan_sql config toggle.
func discoverSQLScripts(cfg Config) []sqlScript {
	if !cfg.Discovery.ScanSQL {
		return nil
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		return nil
	}
	var out []sqlScript
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.ToLower(filepath.Ext(e.Name())) != ".sql" {
			continue
		}
		abs, err := filepath.Abs(e.Name())
		if err != nil {
			abs = e.Name()
		}
		out = append(out, sqlScript{Name: e.Name(), Path: abs})
	}
	return out
}

// loadScript reads a script file's contents.
func loadScript(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// splitSQLBatches splits a script into executable batches.
//
// For SQL Server (driverName == "mssql") it splits on lines consisting only of
// the T-SQL "GO" batch separator (optionally followed by a repeat count), which
// is not valid SQL and must be removed before sending to the server. For other
// drivers the whole text is returned as a single batch (the driver/db handles
// multiple statements itself).
func splitSQLBatches(script, driverName string) []string {
	if driverName != "mssql" {
		trimmed := strings.TrimSpace(script)
		if trimmed == "" {
			return nil
		}
		return []string{script}
	}

	var batches []string
	var cur strings.Builder
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			batches = append(batches, s)
		}
		cur.Reset()
	}

	for _, line := range strings.Split(script, "\n") {
		if isGoSeparator(line) {
			flush()
			continue
		}
		cur.WriteString(line)
		cur.WriteString("\n")
	}
	flush()
	return batches
}

// isGoSeparator reports whether a line is a standalone T-SQL GO batch separator
// (case-insensitive, optionally "GO 5" with a repeat count, ignoring trailing
// comments).
func isGoSeparator(line string) bool {
	s := strings.TrimSpace(line)
	if i := strings.Index(s, "--"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "" {
		return false
	}
	fields := strings.Fields(s)
	if !strings.EqualFold(fields[0], "GO") {
		return false
	}
	// "GO" or "GO <count>".
	return len(fields) == 1 || (len(fields) == 2 && isAllDigits(fields[1]))
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// stripLeadingComments removes leading -- line comments and blank lines so the
// statement-verb sniffing in runOneStatement sees the real first keyword.
func stripLeadingComments(sql string) string {
	lines := strings.Split(sql, "\n")
	i := 0
	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "--") {
			i++
			continue
		}
		break
	}
	return strings.Join(lines[i:], "\n")
}
