# sqwee Driver Interface Specification

This document describes the `Driver` interface sqwee uses to connect to,
introspect, and query a database. If you want to use a database not covered by
the built-in **Postgres**, **MySQL**, **SQLite**, and **SQL Server** drivers,
you can add support by implementing this interface in Go.

---

## Overview

sqwee talks to databases through a small, dependency-free contract package,
`internal/driver`. A driver is responsible for:

1. **Identifying** itself (a unique name and the URL schemes it handles).
2. **Connecting** — opening a live connection from a `ConnInfo`.
3. **Introspecting** — listing schemas, objects (tables / views / functions /
   procedures), and columns.
4. **Defining** — returning a `CREATE`-style DDL for an object.
5. **Querying** — running row-returning statements and non-row statements.

The package has **no dependency on the TUI**, so drivers can be written and
unit-tested in isolation.

---

## The Go Interface

A driver implements `Driver` (a thin factory) and returns a `Conn` (a live
connection). Both live in `internal/driver`:

```go
package driver

import "context"

type Driver interface {
    // Name returns the short, unique identifier ("postgres", "mysql", ...).
    Name() string

    // Schemes returns the URL schemes this driver handles ("postgres",
    // "postgresql"). Used to resolve a driver from a connection URL.
    Schemes() []string

    // DefaultPort returns the conventional TCP port (0 for file/embedded DBs).
    DefaultPort() int

    // Connect opens a live connection. The caller owns and must Close it.
    Connect(ctx context.Context, info ConnInfo) (Conn, error)
}

type Conn interface {
    Ping(ctx context.Context) error
    Schemas(ctx context.Context) ([]Schema, error)
    Objects(ctx context.Context, schema string) ([]DBObject, error)
    Columns(ctx context.Context, schema, table string) ([]Column, error)
    Definition(ctx context.Context, obj DBObject) (string, error)
    Query(ctx context.Context, sql string) (QueryResult, error)
    Exec(ctx context.Context, sql string) (ExecResult, error)
    Close() error
}
```

The data types the interface traffics in:

```go
type ConnInfo struct {
    Name     string
    Driver   string            // registered driver name, may be empty
    URL      string            // full DSN/URL; usually wins over fields below
    Host     string
    Port     int
    User     string
    Password string
    Database string
    Options   map[string]string // driver-specific extras (sslmode, ...)
    Source    string            // where it was found ("saved", "env", ...)
    NeedsCred bool              // detected server with no credentials yet
}

// A Schema is a namespace. Most drivers use a plain schema name; a driver MAY
// use a compound "namespace.schema" form (the SQL Server driver uses
// "database.schema" so the whole instance is browsable). Whatever a driver puts
// in Schema.Name is passed back verbatim to Objects()/Columns().
type Schema   struct{ Name string }

type ObjectKind string
const (
    KindTable     ObjectKind = "table"
    KindView      ObjectKind = "view"
    KindFunction  ObjectKind = "function"
    KindProcedure ObjectKind = "procedure"
)

type DBObject struct {
    Schema string
    Name   string
    Kind   ObjectKind
}

type Column struct {
    Name     string
    Type     string
    Nullable bool
    Key      string // "PK", "FK", "UNI", or "" — best-effort
    Default  string
}

type QueryResult struct {
    Columns   []string
    Rows      [][]string // string-rendered cells
    Nulls     [][]bool   // parallel to Rows; true where the value was NULL
    Duration  time.Duration
    Truncated bool       // true when stopped early at the row cap
}

type ExecResult struct {
    RowsAffected int64
    Duration     time.Duration
    Message      string
}
```

---

## Registering Your Driver

Place your implementation in a `.go` file inside `internal/driver/` and register
it from an `init()` function:

```go
package driver

func init() {
    Register(&duckdbDriver{})
}

type duckdbDriver struct{}

func (d *duckdbDriver) Name() string      { return "duckdb" }
func (d *duckdbDriver) Schemes() []string { return []string{"duckdb"} }
func (d *duckdbDriver) DefaultPort() int  { return 0 }

func (d *duckdbDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
    // ... open a *sql.DB or native handle, ping it, return your Conn ...
}
```

`main.go` blank-imports the package, so every `init()` runs at startup:

```go
import _ "main.go/internal/driver"
```

Registering two drivers with the same `Name()` panics — names must be unique.

### How a driver is chosen for a connection

When you connect, sqwee calls `driver.Resolve(info)`:

1. If `info.Driver` is set, the driver with that **name** is used.
2. Otherwise, the **scheme** of `info.URL` is matched against each driver's
   `Schemes()`.

So a saved connection with `"driver": "duckdb"` or a `DATABASE_URL` of
`duckdb://...` will both resolve to your driver.

---

## Building on `database/sql`

Most drivers wrap a `database/sql` backend. sqwee provides an unexported
`sqlConn` base in `internal/driver/sqlconn.go` that implements `Ping`, `Close`,
`Query` (with NULL detection and a row cap), and `Exec` for you. Embed it and
supply only the dialect-specific introspection methods:

```go
type duckdbConn struct {
    sqlConn // gives you Ping/Close/Query/Exec
}

func (c *duckdbConn) Schemas(ctx context.Context) ([]Schema, error)         { /* ... */ }
func (c *duckdbConn) Objects(ctx context.Context, schema string) ([]DBObject, error) { /* ... */ }
func (c *duckdbConn) Columns(ctx context.Context, schema, table string) ([]Column, error) { /* ... */ }
func (c *duckdbConn) Definition(ctx context.Context, obj DBObject) (string, error) { /* ... */ }
```

`Query` renders every cell to a string and records which cells were `NULL`, so
the results grid can style them. Keep results bounded — the base caps at 1000
rows and sets `Truncated`.

The four built-in drivers (`postgres.go`, `mysql.go`, `sqlite.go`, `mssql.go`)
all follow this pattern and are good reference implementations.

---

## Optional Capabilities

A `Conn` MAY implement extra interfaces. sqwee discovers them with a type
assertion (`conn.(Explainer)`), so the required surface stays small:

```go
// Explainer returns a query plan.
type Explainer interface {
    Explain(ctx context.Context, sql string) (string, error)
}

// ProcedureRunner CALLs a stored procedure with positional arguments.
type ProcedureRunner interface {
    CallProcedure(ctx context.Context, obj DBObject, args []string) (QueryResult, error)
}
```

For example, the SQLite and Postgres drivers implement `Explainer`. To add a new
optional capability, define the interface here and type-assert it at the call
site — existing drivers don't need to change.

---

## Built-in Drivers

### Postgres (`postgres`)

- **Schemes:** `postgres`, `postgresql`
- **Backend:** `github.com/jackc/pgx/v5/stdlib` (`database/sql` name `pgx`)
- **Connect:** uses `info.URL` if set, else builds a `postgres://` DSN with
  `sslmode=prefer`.
- **Introspection:** `information_schema` + `pg_catalog`; views via
  `pg_get_viewdef`, routines via `pg_get_functiondef`.
- Implements `Explainer`.

### MySQL / MariaDB (`mysql`)

- **Schemes:** `mysql`, `mariadb`
- **Backend:** `github.com/go-sql-driver/mysql`
- **Connect:** builds a DSN with `parseTime=true`. A "schema" is a database.
- **Introspection:** `information_schema`; definitions via `SHOW CREATE ...`.
- Implements `Explainer`.

### SQLite (`sqlite`)

- **Schemes:** `sqlite`, `sqlite3`, `file`
- **Backend:** `modernc.org/sqlite` (pure-Go, no CGO)
- **Connect:** the "database" is a file path (from `info.Database`, `info.URL`,
  or `info.Host`). Reports a single schema, `main`.
- **Introspection:** `sqlite_master` + `PRAGMA table_info`.
- Implements `Explainer` (`EXPLAIN QUERY PLAN`).

### SQL Server (`mssql`)

- **Schemes:** `sqlserver`, `mssql`
- **Backend:** `github.com/microsoft/go-mssqldb`
- **Connect:** builds a `sqlserver://` URL with `database=` parameter.
- **Whole-instance browsing:** `Schemas()` enumerates **every non-system
  database** on the instance via `sys.databases`, returning one entry per
  `database.schema` pair (e.g. `VipPracticeStock.dbo`). `Objects()`,
  `Columns()`, and `Definition()` split that prefix and query the target
  database using three-part names, so a database created after you connected
  (or one you don't have in the connection string) is still browsable.
- **Multi-batch scripts:** the Query tab splits scripts on standalone `GO`
  batch separators before sending them, so T-SQL setup scripts run as-is.
- **Introspection:** `sys.databases` + `information_schema`; definitions via
  `OBJECT_DEFINITION`.

---

## Saved Connection Format (`sqwee.json`)

Connections sqwee saves live in `~/.config/delbysoft/sqwee.json`. This is the
exact shape the app reads and writes (passwords reference an env var by default;
set `password` directly only if you accept plaintext storage):

```json
{
  "connections": [
    {
      "name": "local-postgres",
      "driver": "postgres",
      "host": "localhost",
      "port": 5432,
      "user": "postgres",
      "database": "app_dev",
      "password_env": "PGPASSWORD"
    },
    {
      "name": "scratch",
      "driver": "sqlite",
      "database": "/home/me/scratch.sqlite"
    },
    {
      "name": "via-url",
      "driver": "mysql",
      "url": "mysql://user:pass@localhost:3306/shop"
    }
  ]
}
```

A saved entry is turned into a `driver.ConnInfo` at connect time, resolving the
password from `password_env` when `password` is empty.

---

*sqwee is a delbysoft project.*
