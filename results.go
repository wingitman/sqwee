package main

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"

	"main.go/internal/driver"
)

// clipboardCopiedMsg is emitted after an async clipboard write completes.
type clipboardCopiedMsg struct {
	note string
	err  error
}

// copyToClipboardCmd writes text to the system clipboard off the UI goroutine.
// Some clipboard backends (e.g. wl-copy) can block, so this never runs inline
// in Update.
func copyToClipboardCmd(text, note string) tea.Cmd {
	return func() tea.Msg {
		err := clipboard.WriteAll(text)
		return clipboardCopiedMsg{note: note, err: err}
	}
}

// SelectionMode is the scope of the current results-grid selection.
type SelectionMode int

const (
	SelectCell SelectionMode = iota
	SelectRow
	SelectColumn
	SelectAll
)

func (s SelectionMode) String() string {
	switch s {
	case SelectCell:
		return "cell"
	case SelectRow:
		return "row"
	case SelectColumn:
		return "column"
	case SelectAll:
		return "all"
	default:
		return "?"
	}
}

// next cycles to the following selection mode.
func (s SelectionMode) next() SelectionMode {
	return (s + 1) % 4
}

// cellSelected reports whether the cell at (row, col) is inside the current
// selection given the cursor position and mode.
func cellSelected(mode SelectionMode, curRow, curCol, row, col int) bool {
	switch mode {
	case SelectCell:
		return row == curRow && col == curCol
	case SelectRow:
		return row == curRow
	case SelectColumn:
		return col == curCol
	case SelectAll:
		return true
	}
	return false
}

// ── Extraction (for copy) ─────────────────────────────────────────────────────

// selectionRows returns the rows (each a slice of cell strings) and the column
// header labels covered by the current selection. includeHeaders controls
// whether the header row is prepended.
func selectionData(res driver.QueryResult, mode SelectionMode, curRow, curCol int, includeHeaders bool) ([]string, [][]string) {
	var cols []int
	switch mode {
	case SelectColumn, SelectCell:
		cols = []int{curCol}
	default: // row / all
		for i := range res.Columns {
			cols = append(cols, i)
		}
	}

	var rowIdxs []int
	switch mode {
	case SelectRow, SelectCell:
		rowIdxs = []int{curRow}
	default: // column / all
		for i := range res.Rows {
			rowIdxs = append(rowIdxs, i)
		}
	}

	headers := make([]string, 0, len(cols))
	for _, c := range cols {
		if c >= 0 && c < len(res.Columns) {
			headers = append(headers, res.Columns[c])
		}
	}

	var out [][]string
	for _, r := range rowIdxs {
		if r < 0 || r >= len(res.Rows) {
			continue
		}
		var cells []string
		for _, c := range cols {
			cells = append(cells, cellValue(res, r, c))
		}
		out = append(out, cells)
	}

	if !includeHeaders {
		headers = nil
	}
	return headers, out
}

// cellValue returns the display value of a cell, rendering NULLs as an empty
// string (so CSV round-trips cleanly).
func cellValue(res driver.QueryResult, row, col int) string {
	if row < 0 || row >= len(res.Rows) || col < 0 || col >= len(res.Rows[row]) {
		return ""
	}
	if res.Nulls != nil && row < len(res.Nulls) && col < len(res.Nulls[row]) && res.Nulls[row][col] {
		return ""
	}
	return res.Rows[row][col]
}

// ── CSV / TSV / JSON encoding ─────────────────────────────────────────────────

// toCSV encodes rows (with optional header) as RFC 4180 CSV. The encoding/csv
// package quotes fields containing commas, quotes, or newlines, so the output
// pastes into a spreadsheet/CSV file without further escaping.
func toCSV(headers []string, rows [][]string, comma rune) string {
	var b strings.Builder
	w := csv.NewWriter(&b)
	w.Comma = comma
	w.UseCRLF = true // RFC 4180 / Excel-friendly line endings
	if len(headers) > 0 {
		_ = w.Write(headers)
	}
	for _, r := range rows {
		_ = w.Write(r)
	}
	w.Flush()
	return b.String()
}

// fullCSV encodes the whole result set as CSV (always with headers).
func fullCSV(res driver.QueryResult, comma rune) string {
	rows := make([][]string, len(res.Rows))
	for r := range res.Rows {
		cells := make([]string, len(res.Columns))
		for c := range res.Columns {
			cells[c] = cellValue(res, r, c)
		}
		rows[r] = cells
	}
	return toCSV(res.Columns, rows, comma)
}

// fullJSON encodes the whole result set as a JSON array of objects.
func fullJSON(res driver.QueryResult) (string, error) {
	records := make([]map[string]any, 0, len(res.Rows))
	for r := range res.Rows {
		obj := make(map[string]any, len(res.Columns))
		for c, name := range res.Columns {
			if res.Nulls != nil && r < len(res.Nulls) && c < len(res.Nulls[r]) && res.Nulls[r][c] {
				obj[name] = nil
			} else if c < len(res.Rows[r]) {
				obj[name] = res.Rows[r][c]
			} else {
				obj[name] = nil
			}
		}
		records = append(records, obj)
	}
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ── Export to disk ────────────────────────────────────────────────────────────

// exportFormat enumerates the file formats the export modal offers.
type exportFormat string

const (
	exportCSV  exportFormat = "csv"
	exportTSV  exportFormat = "tsv"
	exportJSON exportFormat = "json"
)

// downloadsDir returns the user's Downloads directory, falling back to $HOME.
func downloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	dl := filepath.Join(home, "Downloads")
	if fi, err := os.Stat(dl); err == nil && fi.IsDir() {
		return dl
	}
	return home
}

// exportResults writes the result set to ~/Downloads in the given format and
// returns the path written.
func exportResults(res driver.QueryResult, format exportFormat) (string, error) {
	ts := time.Now().Format("20060102-150405")
	name := "sqwee-export-" + ts + "." + string(format)
	path := filepath.Join(downloadsDir(), name)

	var content string
	var err error
	switch format {
	case exportTSV:
		content = fullCSV(res, '\t')
	case exportJSON:
		content, err = fullJSON(res)
	default:
		content = fullCSV(res, ',')
	}
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
