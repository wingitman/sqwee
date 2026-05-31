package driver

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver name "pgx"
)

func init() { Register(&postgresDriver{}) }

type postgresDriver struct{}

func (d *postgresDriver) Name() string      { return "postgres" }
func (d *postgresDriver) Schemes() []string { return []string{"postgres", "postgresql"} }
func (d *postgresDriver) DefaultPort() int  { return 5432 }

func (d *postgresDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	dsn := info.URL
	if dsn == "" {
		dsn = buildPostgresDSN(info)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &postgresConn{sqlConn: sqlConn{db: db}}, nil
}

func buildPostgresDSN(info ConnInfo) string {
	port := info.Port
	if port == 0 {
		port = 5432
	}
	host := info.Host
	if host == "" {
		host = "localhost"
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   fmt.Sprintf("%s:%d", host, port),
		Path:   "/" + info.Database,
	}
	if info.User != "" {
		if info.Password != "" {
			u.User = url.UserPassword(info.User, info.Password)
		} else {
			u.User = url.User(info.User)
		}
	}
	q := url.Values{}
	if _, ok := info.Options["sslmode"]; !ok {
		q.Set("sslmode", "prefer")
	}
	for k, v := range info.Options {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

type postgresConn struct {
	sqlConn
}

func (c *postgresConn) Schemas(ctx context.Context) ([]Schema, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT schema_name FROM information_schema.schemata
		 WHERE schema_name NOT IN ('pg_catalog','information_schema')
		   AND schema_name NOT LIKE 'pg_toast%' AND schema_name NOT LIKE 'pg_temp%'
		 ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schema
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, Schema{Name: n})
	}
	return out, rows.Err()
}

func (c *postgresConn) Objects(ctx context.Context, schema string) ([]DBObject, error) {
	var out []DBObject

	// Tables and views.
	tv, err := c.db.QueryContext(ctx,
		`SELECT table_name, table_type FROM information_schema.tables
		 WHERE table_schema = $1 ORDER BY table_type, table_name`, schema)
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

	// Functions and procedures.
	fp, err := c.db.QueryContext(ctx,
		`SELECT routine_name, routine_type FROM information_schema.routines
		 WHERE routine_schema = $1 ORDER BY routine_type, routine_name`, schema)
	if err != nil {
		return out, nil // best-effort; tables already gathered
	}
	for fp.Next() {
		var name, typ string
		if err := fp.Scan(&name, &typ); err != nil {
			fp.Close()
			return out, nil
		}
		kind := KindFunction
		if strings.EqualFold(typ, "PROCEDURE") {
			kind = KindProcedure
		}
		out = append(out, DBObject{Schema: schema, Name: name, Kind: kind})
	}
	fp.Close()

	return out, nil
}

func (c *postgresConn) Columns(ctx context.Context, schema, table string) ([]Column, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT column_name, data_type, is_nullable, COALESCE(column_default,'')
		 FROM information_schema.columns
		 WHERE table_schema = $1 AND table_name = $2
		 ORDER BY ordinal_position`, schema, table)
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

	// Best-effort primary-key detection.
	pk, _ := c.db.QueryContext(ctx,
		`SELECT a.attname FROM pg_index i
		 JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		 WHERE i.indrelid = ($1||'.'||$2)::regclass AND i.indisprimary`, schema, table)
	if pk != nil {
		pkset := map[string]bool{}
		for pk.Next() {
			var n string
			if pk.Scan(&n) == nil {
				pkset[n] = true
			}
		}
		pk.Close()
		for i := range cols {
			if pkset[cols[i].Name] {
				cols[i].Key = "PK"
			}
		}
	}

	return cols, nil
}

func (c *postgresConn) Definition(ctx context.Context, obj DBObject) (string, error) {
	switch obj.Kind {
	case KindView:
		var def string
		err := c.db.QueryRowContext(ctx,
			`SELECT pg_get_viewdef(($1||'.'||$2)::regclass, true)`, obj.Schema, obj.Name).Scan(&def)
		if err != nil {
			return "", err
		}
		return "CREATE VIEW " + obj.Qualified() + " AS\n" + def, nil
	case KindFunction, KindProcedure:
		var def string
		err := c.db.QueryRowContext(ctx,
			`SELECT pg_get_functiondef(p.oid)
			 FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			 WHERE n.nspname = $1 AND p.proname = $2 LIMIT 1`, obj.Schema, obj.Name).Scan(&def)
		if err != nil {
			return "", err
		}
		return def, nil
	default:
		// Reconstruct a CREATE TABLE from column metadata.
		cols, err := c.Columns(ctx, obj.Schema, obj.Name)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString("CREATE TABLE " + obj.Qualified() + " (\n")
		for i, col := range cols {
			b.WriteString("  " + col.Name + " " + col.Type)
			if !col.Nullable {
				b.WriteString(" NOT NULL")
			}
			if col.Default != "" {
				b.WriteString(" DEFAULT " + col.Default)
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

// Explain implements the optional Explainer capability.
func (c *postgresConn) Explain(ctx context.Context, query string) (string, error) {
	res, err := c.Query(ctx, "EXPLAIN "+query)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, row := range res.Rows {
		b.WriteString(strings.Join(row, " "))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// quotePostgres wraps an identifier in double quotes, escaping embedded quotes.
func quotePostgres(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// postgresProvisionSpec describes how to provision a Postgres database.
func postgresProvisionSpec() serverProvisionSpec {
	return serverProvisionSpec{
		driverName:    "postgres",
		maintenanceDB: "postgres", // can't connect to a DB that doesn't exist yet
		defaultPort:   5432,
		dockerImage:   "postgres:16",
		passwordEnvFn: func(pw string) map[string]string {
			return map[string]string{"POSTGRES_PASSWORD": pw}
		},
		createSQL: func(name string) string { return "CREATE DATABASE " + quotePostgres(name) },
	}
}

// ProvisionModes implements the optional Provisioner capability.
func (d *postgresDriver) ProvisionModes() []ProvisionMode {
	return []ProvisionMode{
		{ID: "server", Label: "CREATE DATABASE on a running PostgreSQL server", Fields: serverFields(5432)},
		{ID: "docker", Label: "Spin up a PostgreSQL Docker container", Fields: dockerFields(5432)},
	}
}

// Provision creates a new Postgres database via the chosen mode.
func (d *postgresDriver) Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error) {
	spec := postgresProvisionSpec()
	switch mode {
	case "docker":
		return spec.provisionDocker(ctx, values)
	default:
		return spec.provisionServer(ctx, values)
	}
}
