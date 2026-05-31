package driver

import (
	"context"
	"database/sql"
	"fmt"
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
