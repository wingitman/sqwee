package driver

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // pure-Go SQLite, no CGO
)

func init() { Register(&sqliteDriver{}) }

type sqliteDriver struct{}

func (d *sqliteDriver) Name() string      { return "sqlite" }
func (d *sqliteDriver) Schemes() []string { return []string{"sqlite", "sqlite3", "file"} }
func (d *sqliteDriver) DefaultPort() int  { return 0 }

func (d *sqliteDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	// SQLite is file-backed: the database is a path. Accept it from the URL,
	// the Database field, or the Host field for flexibility.
	path := info.Database
	if info.URL != "" {
		path = strings.TrimPrefix(info.URL, "sqlite://")
		path = strings.TrimPrefix(path, "sqlite3://")
		path = strings.TrimPrefix(path, "file://")
	}
	if path == "" {
		path = info.Host
	}
	if path == "" {
		return nil, fmt.Errorf("sqlite: no database file path provided")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &sqliteConn{sqlConn: sqlConn{db: db}}, nil
}

type sqliteConn struct {
	sqlConn
}

// SQLite has a single implicit schema; we report "main".
func (c *sqliteConn) Schemas(ctx context.Context) ([]Schema, error) {
	return []Schema{{Name: "main"}}, nil
}

func (c *sqliteConn) Objects(ctx context.Context, schema string) ([]DBObject, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT name, type FROM sqlite_master
		 WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%'
		 ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var objs []DBObject
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		kind := KindTable
		if typ == "view" {
			kind = KindView
		}
		objs = append(objs, DBObject{Schema: "main", Name: name, Kind: kind})
	}
	return objs, rows.Err()
}

func (c *sqliteConn) Columns(ctx context.Context, schema, table string) ([]Column, error) {
	rows, err := c.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var (
			cid        int
			name, typ  string
			notNull    int
			dflt       sql.NullString
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &primaryKey); err != nil {
			return nil, err
		}
		key := ""
		if primaryKey > 0 {
			key = "PK"
		}
		cols = append(cols, Column{
			Name:     name,
			Type:     typ,
			Nullable: notNull == 0,
			Key:      key,
			Default:  dflt.String,
		})
	}
	return cols, rows.Err()
}

func (c *sqliteConn) Definition(ctx context.Context, obj DBObject) (string, error) {
	var ddl sql.NullString
	err := c.db.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE name = ?`, obj.Name).Scan(&ddl)
	if err != nil {
		return "", err
	}
	if !ddl.Valid {
		return "", fmt.Errorf("no definition available for %s", obj.Name)
	}
	return ddl.String + ";", nil
}

func (c *sqliteConn) TableMetadata(ctx context.Context, schema, table string) (TableMetadata, error) {
	var meta TableMetadata
	idxRows, err := c.db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_list(%q)", table))
	if err == nil {
		for idxRows.Next() {
			var seq int
			var name, origin string
			var unique, partial int
			if idxRows.Scan(&seq, &name, &unique, &origin, &partial) != nil {
				continue
			}
			idx := Index{Name: name, Unique: unique == 1, Primary: origin == "pk"}
			colRows, cerr := c.db.QueryContext(ctx, fmt.Sprintf("PRAGMA index_info(%q)", name))
			if cerr == nil {
				for colRows.Next() {
					var seqno, cid int
					var col string
					if colRows.Scan(&seqno, &cid, &col) == nil {
						idx.Columns = append(idx.Columns, col)
					}
				}
				colRows.Close()
			}
			meta.Indexes = append(meta.Indexes, idx)
		}
		idxRows.Close()
	}

	fkRows, err := c.db.QueryContext(ctx, fmt.Sprintf("PRAGMA foreign_key_list(%q)", table))
	if err != nil {
		return meta, nil
	}
	defer fkRows.Close()
	byID := map[int]*ForeignKey{}
	var order []int
	for fkRows.Next() {
		var id, seq int
		var refTable, from, to, onUpdate, onDelete, match string
		if fkRows.Scan(&id, &seq, &refTable, &from, &to, &onUpdate, &onDelete, &match) != nil {
			continue
		}
		fk := byID[id]
		if fk == nil {
			name := "fk_" + table + "_" + itoaDriver(id)
			fk = &ForeignKey{Name: name, RefSchema: schema, RefTable: refTable, OnUpdate: onUpdate, OnDelete: onDelete}
			byID[id] = fk
			order = append(order, id)
		}
		fk.Columns = append(fk.Columns, from)
		fk.RefColumns = append(fk.RefColumns, to)
	}
	for _, id := range order {
		meta.ForeignKeys = append(meta.ForeignKeys, *byID[id])
	}
	return meta, nil
}

// Explain implements the optional Explainer capability.
func (c *sqliteConn) Explain(ctx context.Context, query string) (string, error) {
	res, err := c.Query(ctx, "EXPLAIN QUERY PLAN "+query)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, row := range res.Rows {
		b.WriteString(strings.Join(row, " | "))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func itoaDriver(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// ─── Provisioner ────────────────────────────────────────────────────────────

// ProvisionModes implements the optional Provisioner capability. SQLite has a
// single mode: create a new database file.
func (d *sqliteDriver) ProvisionModes() []ProvisionMode {
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, "mydb.sqlite")
	return []ProvisionMode{
		{
			ID:    "file",
			Label: "New SQLite database file",
			Fields: []ProvisionField{
				{Key: "path", Label: "File path", Default: def, Placeholder: "/path/to/db.sqlite"},
			},
		},
	}
}

// Provision creates a new SQLite database file by opening it (modernc.org/sqlite
// creates the file on first open) and returns a connection to it.
func (d *sqliteDriver) Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error) {
	path := strings.TrimSpace(values["path"])
	if path == "" {
		return ProvisionResult{}, fmt.Errorf("sqlite: a file path is required")
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if _, err := os.Stat(path); err == nil {
		return ProvisionResult{}, fmt.Errorf("sqlite: file already exists: %s", path)
	}

	// Ensure the parent directory exists.
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return ProvisionResult{}, fmt.Errorf("sqlite: %w", err)
		}
	}

	info := ConnInfo{Driver: "sqlite", Database: path}
	conn, err := d.Connect(ctx, info)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("sqlite: create failed: %w", err)
	}
	conn.Close()

	return ProvisionResult{
		Info:  info,
		Steps: []string{"Created SQLite database file: " + path},
	}, nil
}
