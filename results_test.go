package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"main.go/internal/driver"
)

func sampleResult() driver.QueryResult {
	return driver.QueryResult{
		Columns: []string{"id", "name", "note"},
		Rows: [][]string{
			{"1", "Alice", "hi, there"},     // comma → must be quoted
			{"2", "Bob", ""},                // NULL note
			{"3", "Eve \"the\"", "line\nb"}, // quote + newline → quoted
		},
		Nulls: [][]bool{
			{false, false, false},
			{false, false, true},
			{false, false, false},
		},
	}
}

func TestToCSVQuotingRFC4180(t *testing.T) {
	res := sampleResult()
	out := fullCSV(res, ',')
	// Header present.
	if !strings.HasPrefix(out, "id,name,note\r\n") {
		t.Fatalf("missing/incorrect header line: %q", firstLine(out))
	}
	// Field with a comma must be quoted.
	if !strings.Contains(out, `"hi, there"`) {
		t.Errorf("comma field not quoted: %q", out)
	}
	// Field with embedded quotes must be doubled and quoted.
	if !strings.Contains(out, `"Eve ""the"""`) {
		t.Errorf("quote field not escaped: %q", out)
	}
	// Field with a newline must be quoted (CRLF-normalized).
	if !strings.Contains(out, "\"line\r\nb\"") {
		t.Errorf("newline field not quoted: %q", out)
	}
}

func TestSelectionCell(t *testing.T) {
	res := sampleResult()
	// Cell (row 0, col 1) = "Alice".
	headers, rows := selectionData(res, SelectCell, 0, 1, false)
	if headers != nil {
		t.Errorf("cell copy without headers should have no headers, got %v", headers)
	}
	if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != "Alice" {
		t.Fatalf("cell selection = %v, want [[Alice]]", rows)
	}
}

func TestSelectionColumnWithHeaders(t *testing.T) {
	res := sampleResult()
	// Column 1 (name), with headers.
	headers, rows := selectionData(res, SelectColumn, 0, 1, true)
	if len(headers) != 1 || headers[0] != "name" {
		t.Fatalf("headers = %v, want [name]", headers)
	}
	got := []string{rows[0][0], rows[1][0], rows[2][0]}
	want := []string{"Alice", "Bob", "Eve \"the\""}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSelectionRowNullIsEmpty(t *testing.T) {
	res := sampleResult()
	// Row 1 (Bob) has a NULL note → empty string in CSV.
	_, rows := selectionData(res, SelectRow, 1, 0, false)
	if len(rows) != 1 || rows[0][2] != "" {
		t.Fatalf("NULL cell should be empty, got %v", rows)
	}
}

func TestSelectionAll(t *testing.T) {
	res := sampleResult()
	headers, rows := selectionData(res, SelectAll, 0, 0, true)
	if len(headers) != 3 || len(rows) != 3 {
		t.Fatalf("select-all = %d headers, %d rows; want 3,3", len(headers), len(rows))
	}
}

func TestCellSelectedModes(t *testing.T) {
	if !cellSelected(SelectAll, 0, 0, 5, 5) {
		t.Error("SelectAll should cover any cell")
	}
	if !cellSelected(SelectRow, 2, 1, 2, 9) {
		t.Error("SelectRow should cover same row")
	}
	if cellSelected(SelectRow, 2, 1, 3, 1) {
		t.Error("SelectRow should not cover a different row")
	}
	if !cellSelected(SelectColumn, 2, 1, 9, 1) {
		t.Error("SelectColumn should cover same column")
	}
	if !cellSelected(SelectCell, 2, 1, 2, 1) {
		t.Error("SelectCell should cover exact cell")
	}
	if cellSelected(SelectCell, 2, 1, 2, 2) {
		t.Error("SelectCell should not cover neighbour")
	}
}

func TestFullJSONNulls(t *testing.T) {
	res := sampleResult()
	out, err := fullJSON(res)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	if err := json.Unmarshal([]byte(out), &records); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(records) != 3 {
		t.Fatalf("expected 3 records, got %d", len(records))
	}
	// Bob's note is NULL → JSON null.
	if records[1]["note"] != nil {
		t.Errorf("expected null note for Bob, got %v", records[1]["note"])
	}
	if records[0]["name"] != "Alice" {
		t.Errorf("expected Alice, got %v", records[0]["name"])
	}
}

func TestExportResultsWritesFile(t *testing.T) {
	// Point Downloads discovery at a temp HOME.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := sampleResult()
	for _, f := range []exportFormat{exportCSV, exportTSV, exportJSON} {
		path, err := exportResults(res, f)
		if err != nil {
			t.Fatalf("export %s: %v", f, err)
		}
		if filepath.Dir(path) != filepath.Join(tmp, "Downloads") {
			t.Errorf("%s written to %s, expected Downloads dir", f, path)
		}
		if !strings.HasSuffix(path, "."+string(f)) {
			t.Errorf("export path %q missing .%s suffix", path, f)
		}
		b, err := os.ReadFile(path)
		if err != nil || len(b) == 0 {
			t.Errorf("export %s produced empty/unreadable file: %v", f, err)
		}
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i+1]
	}
	return s
}
