package driver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/microsoft/go-mssqldb" // database/sql driver name "sqlserver"
)

func init() { Register(&mssqlDriver{}) }

type mssqlDriver struct{}

func (d *mssqlDriver) Name() string      { return "mssql" }
func (d *mssqlDriver) Schemes() []string { return []string{"sqlserver", "mssql"} }
func (d *mssqlDriver) DefaultPort() int  { return 1433 }

func (d *mssqlDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	dsn := info.URL
	if dsn == "" {
		dsn = buildMSSQLDSN(info)
	}
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &mssqlConn{sqlConn: sqlConn{db: db}}, nil
}

func buildMSSQLDSN(info ConnInfo) string {
	host := info.Host
	if host == "" {
		host = "localhost"
	}
	port := info.Port
	if port == 0 {
		port = 1433
	}
	u := url.URL{
		Scheme: "sqlserver",
		Host:   fmt.Sprintf("%s:%d", host, port),
	}
	if info.User != "" {
		if info.Password != "" {
			u.User = url.UserPassword(info.User, info.Password)
		} else {
			u.User = url.User(info.User)
		}
	}
	q := url.Values{}
	if info.Database != "" {
		q.Set("database", info.Database)
	}
	for k, v := range info.Options {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type mssqlConn struct {
	sqlConn
}

// systemDatabases are excluded from the all-databases listing.
var mssqlSystemDBs = map[string]bool{
	"master": true, "tempdb": true, "model": true, "msdb": true,
}

// Schemas enumerates every (non-system) database on the instance and its
// schemas, returning one entry per "database.schema" so the user can browse the
// whole server (not just the connected database). This is why a freshly created
// DB like VipPracticeStock shows up even when connected to master.
func (c *mssqlConn) Schemas(ctx context.Context) ([]Schema, error) {
	dbRows, err := c.db.QueryContext(ctx, `SELECT name FROM sys.databases ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var dbs []string
	for dbRows.Next() {
		var n string
		if err := dbRows.Scan(&n); err != nil {
			dbRows.Close()
			return nil, err
		}
		if !mssqlSystemDBs[strings.ToLower(n)] {
			dbs = append(dbs, n)
		}
	}
	dbRows.Close()

	var out []Schema
	for _, db := range dbs {
		q := `SELECT name FROM ` + quoteMSSQLIdent(db) + `.sys.schemas
		      WHERE name NOT IN ('sys','INFORMATION_SCHEMA','guest','db_owner',
		        'db_accessadmin','db_securityadmin','db_ddladmin','db_backupoperator',
		        'db_datareader','db_datawriter','db_denydatareader','db_denydatawriter')
		      ORDER BY name`
		sr, err := c.db.QueryContext(ctx, q)
		if err != nil {
			// Can't read this DB (permissions/offline) — skip it.
			continue
		}
		for sr.Next() {
			var s string
			if sr.Scan(&s) == nil {
				out = append(out, Schema{Name: db + "." + s})
			}
		}
		sr.Close()
	}
	return out, nil
}

// splitDBSchema splits a "database.schema" qualifier into its parts. When no
// database prefix is present, db is empty and the connected database is used.
func splitDBSchema(qualifier string) (db, schema string) {
	if i := strings.Index(qualifier, "."); i >= 0 {
		return qualifier[:i], qualifier[i+1:]
	}
	return "", qualifier
}

func (c *mssqlConn) Objects(ctx context.Context, schema string) ([]DBObject, error) {
	db, sch := splitDBSchema(schema)
	prefix := ""
	if db != "" {
		prefix = quoteMSSQLIdent(db) + "."
	}
	var out []DBObject

	tv, err := c.db.QueryContext(ctx,
		`SELECT table_name, table_type FROM `+prefix+`information_schema.tables
		 WHERE table_schema = @p1 ORDER BY table_type, table_name`, sch)
	if err != nil {
		return nil, err
	}
	for tv.Next() {
		var name, typ string
		if err := tv.Scan(&name, &typ); err != nil {
			tv.Close()
			return nil, err
		}
		kind := KindTable
		if strings.Contains(typ, "VIEW") {
			kind = KindView
		}
		out = append(out, DBObject{Schema: schema, Name: name, Kind: kind})
	}
	tv.Close()

	fp, err := c.db.QueryContext(ctx,
		`SELECT routine_name, routine_type FROM `+prefix+`information_schema.routines
		 WHERE routine_schema = @p1 ORDER BY routine_type, routine_name`, sch)
	if err == nil {
		for fp.Next() {
			var name, typ string
			if fp.Scan(&name, &typ) == nil {
				kind := KindFunction
				if strings.EqualFold(typ, "PROCEDURE") {
					kind = KindProcedure
				}
				out = append(out, DBObject{Schema: schema, Name: name, Kind: kind})
			}
		}
		fp.Close()
	}

	return out, nil
}

func (c *mssqlConn) Columns(ctx context.Context, schema, table string) ([]Column, error) {
	db, sch := splitDBSchema(schema)
	prefix := ""
	if db != "" {
		prefix = quoteMSSQLIdent(db) + "."
	}
	rows, err := c.db.QueryContext(ctx,
		`SELECT column_name, data_type, is_nullable, COALESCE(column_default,'')
		 FROM `+prefix+`information_schema.columns
		 WHERE table_schema = @p1 AND table_name = @p2
		 ORDER BY ordinal_position`, sch, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var name, typ, nullable, dflt string
		if err := rows.Scan(&name, &typ, &nullable, &dflt); err != nil {
			return nil, err
		}
		cols = append(cols, Column{
			Name:     name,
			Type:     typ,
			Nullable: nullable == "YES",
			Default:  dflt,
		})
	}
	return cols, rows.Err()
}

func (c *mssqlConn) Definition(ctx context.Context, obj DBObject) (string, error) {
	db, sch := splitDBSchema(obj.Schema)
	switch obj.Kind {
	case KindView, KindFunction, KindProcedure:
		// OBJECT_DEFINITION resolves in the current database, so pass a
		// fully-qualified [db].[schema].[obj] name to OBJECT_ID.
		objIDArg := "[" + sch + "].[" + obj.Name + "]"
		if db != "" {
			objIDArg = quoteMSSQLIdent(db) + "." + objIDArg
		}
		var def sql.NullString
		err := c.db.QueryRowContext(ctx,
			`SELECT OBJECT_DEFINITION(OBJECT_ID(@p1))`, objIDArg).Scan(&def)
		if err != nil {
			return "", err
		}
		if !def.Valid {
			return "", fmt.Errorf("no definition available for %s", obj.Qualified())
		}
		return def.String, nil
	default:
		cols, err := c.Columns(ctx, obj.Schema, obj.Name)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString("CREATE TABLE " + obj.Qualified() + " (\n")
		for i, col := range cols {
			b.WriteString("  [" + col.Name + "] " + col.Type)
			if !col.Nullable {
				b.WriteString(" NOT NULL")
			}
			if i < len(cols)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(");")
		return b.String(), nil
	}
}

// quoteMSSQLIdent wraps an identifier in [brackets], escaping embedded ].
func quoteMSSQLIdent(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}
