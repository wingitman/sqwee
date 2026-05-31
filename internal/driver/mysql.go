package driver

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/go-sql-driver/mysql"
)

func init() { Register(&mysqlDriver{}) }

type mysqlDriver struct{}

func (d *mysqlDriver) Name() string      { return "mysql" }
func (d *mysqlDriver) Schemes() []string { return []string{"mysql", "mariadb"} }
func (d *mysqlDriver) DefaultPort() int  { return 3306 }

func (d *mysqlDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	dsn := info.URL
	if strings.HasPrefix(dsn, "mysql://") || strings.HasPrefix(dsn, "mariadb://") {
		// Convert a URL into the go-sql-driver DSN form.
		dsn = ""
	}
	if dsn == "" {
		dsn = buildMySQLDSN(info)
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &mysqlConn{sqlConn: sqlConn{db: db}, database: info.Database}, nil
}

func buildMySQLDSN(info ConnInfo) string {
	cfg := mysql.NewConfig()
	cfg.User = info.User
	cfg.Passwd = info.Password
	cfg.Net = "tcp"
	host := info.Host
	if host == "" {
		host = "localhost"
	}
	port := info.Port
	if port == 0 {
		port = 3306
	}
	cfg.Addr = fmt.Sprintf("%s:%d", host, port)
	cfg.DBName = info.Database
	cfg.ParseTime = true
	for k, v := range info.Options {
		if cfg.Params == nil {
			cfg.Params = map[string]string{}
		}
		cfg.Params[k] = v
	}
	return cfg.FormatDSN()
}

type mysqlConn struct {
	sqlConn
	database string
}

func (c *mysqlConn) Schemas(ctx context.Context) ([]Schema, error) {
	// In MySQL a "schema" is a database. Report the connected database, or all
	// non-system databases if none was specified.
	if c.database != "" {
		return []Schema{{Name: c.database}}, nil
	}
	rows, err := c.db.QueryContext(ctx,
		`SELECT schema_name FROM information_schema.schemata
		 WHERE schema_name NOT IN ('mysql','information_schema','performance_schema','sys')
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

func (c *mysqlConn) Objects(ctx context.Context, schema string) ([]DBObject, error) {
	var out []DBObject

	tv, err := c.db.QueryContext(ctx,
		`SELECT table_name, table_type FROM information_schema.tables
		 WHERE table_schema = ? ORDER BY table_type, table_name`, schema)
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
		`SELECT routine_name, routine_type FROM information_schema.routines
		 WHERE routine_schema = ? ORDER BY routine_type, routine_name`, schema)
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

func (c *mysqlConn) Columns(ctx context.Context, schema, table string) ([]Column, error) {
	rows, err := c.db.QueryContext(ctx,
		`SELECT column_name, column_type, is_nullable, column_key, COALESCE(column_default,'')
		 FROM information_schema.columns
		 WHERE table_schema = ? AND table_name = ?
		 ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var name, typ, nullable, key, dflt string
		if err := rows.Scan(&name, &typ, &nullable, &key, &dflt); err != nil {
			return nil, err
		}
		k := ""
		switch key {
		case "PRI":
			k = "PK"
		case "MUL":
			k = "FK"
		case "UNI":
			k = "UNI"
		}
		cols = append(cols, Column{
			Name:     name,
			Type:     typ,
			Nullable: nullable == "YES",
			Key:      k,
			Default:  dflt,
		})
	}
	return cols, rows.Err()
}

func (c *mysqlConn) Definition(ctx context.Context, obj DBObject) (string, error) {
	var stmt string
	switch obj.Kind {
	case KindView:
		var name, create, charset, collation string
		err := c.db.QueryRowContext(ctx, "SHOW CREATE VIEW "+quoteMySQL(obj.Name)).
			Scan(&name, &create, &charset, &collation)
		if err != nil {
			return "", err
		}
		return create + ";", nil
	case KindFunction:
		var name, mode, create, charset, coll, dbcoll string
		err := c.db.QueryRowContext(ctx, "SHOW CREATE FUNCTION "+quoteMySQL(obj.Name)).
			Scan(&name, &mode, &create, &charset, &coll, &dbcoll)
		if err != nil {
			return "", err
		}
		return create + ";", nil
	case KindProcedure:
		var name, mode, create, charset, coll, dbcoll string
		err := c.db.QueryRowContext(ctx, "SHOW CREATE PROCEDURE "+quoteMySQL(obj.Name)).
			Scan(&name, &mode, &create, &charset, &coll, &dbcoll)
		if err != nil {
			return "", err
		}
		return create + ";", nil
	default:
		var name string
		err := c.db.QueryRowContext(ctx, "SHOW CREATE TABLE "+quoteMySQL(obj.Name)).
			Scan(&name, &stmt)
		if err != nil {
			return "", err
		}
		return stmt + ";", nil
	}
}

func quoteMySQL(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

// Explain implements the optional Explainer capability.
func (c *mysqlConn) Explain(ctx context.Context, query string) (string, error) {
	res, err := c.Query(ctx, "EXPLAIN "+query)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(strings.Join(res.Columns, " | "))
	b.WriteString("\n")
	for _, row := range res.Rows {
		b.WriteString(strings.Join(row, " | "))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// mysqlProvisionSpec describes how to provision a MySQL/MariaDB database.
func mysqlProvisionSpec() serverProvisionSpec {
	return serverProvisionSpec{
		driverName:    "mysql",
		maintenanceDB: "", // MySQL needs no default database to CREATE DATABASE
		defaultPort:   3306,
		dockerImage:   "mysql:8",
		passwordEnvFn: func(pw string) map[string]string {
			return map[string]string{"MYSQL_ROOT_PASSWORD": pw}
		},
		createSQL: func(name string) string { return "CREATE DATABASE " + quoteMySQL(name) },
	}
}

// ProvisionModes implements the optional Provisioner capability.
func (d *mysqlDriver) ProvisionModes() []ProvisionMode {
	return []ProvisionMode{
		{ID: "server", Label: "CREATE DATABASE on a running MySQL/MariaDB server", Fields: serverFields(3306)},
		{ID: "docker", Label: "Spin up a MySQL Docker container", Fields: dockerFields(3306)},
	}
}

// Provision creates a new MySQL database via the chosen mode.
func (d *mysqlDriver) Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error) {
	spec := mysqlProvisionSpec()
	switch mode {
	case "docker":
		return spec.provisionDocker(ctx, values)
	default:
		return spec.provisionServer(ctx, values)
	}
}
