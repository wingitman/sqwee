// Package driver defines the database-driver contract sqwee uses to connect to,
// introspect, and query any database, plus a registry of built-in drivers.
//
// The package is deliberately free of any TUI / Bubble Tea dependency so it can
// be reused and tested on its own. Third-party drivers implement the Driver
// interface and self-register from an init() function. See SPEC.md.
package driver

import (
	"context"
	"time"
)

// ConnInfo holds everything needed to open a connection. Drivers may use the
// structured fields, the raw URL, or both. Exactly how they are interpreted is
// up to each driver (documented per-driver in SPEC.md).
type ConnInfo struct {
	// Name is a human label for the connection (e.g. "local-postgres").
	Name string
	// Driver is the registered driver name ("postgres", "mysql", ...). May be
	// empty, in which case the URL scheme is used to resolve a driver.
	Driver string

	// URL is an optional full connection string / DSN. When set it usually
	// takes precedence over the structured fields below.
	URL string

	// Structured connection fields (used when URL is empty).
	Host     string
	Port     int
	User     string
	Password string
	Database string

	// Options holds extra driver-specific key/value settings (sslmode, etc.).
	Options map[string]string

	// Source records where this connection was found ("saved", "env",
	// ".env", ".pgpass", "file", "detected", ...). Purely informational.
	Source string

	// NeedsCred marks a connection that was detected (e.g. a listening server)
	// but has no credentials yet. The UI prompts for them before connecting.
	NeedsCred bool
}

// Schema is a namespace within a database (a "schema" in Postgres/SQL Server,
// or the database itself in MySQL/SQLite).
type Schema struct {
	Name string
}

// ObjectKind enumerates the kinds of schema objects sqwee surfaces.
type ObjectKind string

const (
	KindTable     ObjectKind = "table"
	KindView      ObjectKind = "view"
	KindFunction  ObjectKind = "function"
	KindProcedure ObjectKind = "procedure"
)

// DBObject is a single schema object (table, view, function, or procedure).
type DBObject struct {
	Schema string
	Name   string
	Kind   ObjectKind
}

// Qualified returns "schema.name" (or just "name" when Schema is empty).
func (o DBObject) Qualified() string {
	if o.Schema == "" {
		return o.Name
	}
	return o.Schema + "." + o.Name
}

// Column describes a single column of a table or view.
type Column struct {
	Name     string
	Type     string
	Nullable bool
	Key      string // "PK", "FK", "UNI", or "" — best-effort, driver-specific
	Default  string
}

// QueryResult is the outcome of a row-returning query (typically SELECT).
type QueryResult struct {
	Columns  []string
	Rows     [][]string // string-rendered cells; nil cell => NULL (see Nulls)
	Nulls    [][]bool   // parallel to Rows; true where the value was NULL
	Duration time.Duration
	// Truncated is true when the driver stopped early at a row cap.
	Truncated bool
}

// ExecResult is the outcome of a non-row statement (INSERT/UPDATE/DDL).
type ExecResult struct {
	RowsAffected int64
	Duration     time.Duration
	Message      string // optional human note (e.g. "CREATE TABLE")
}

// Driver is the interface every database driver must implement. It is a thin
// factory: it identifies itself, advertises the URL schemes it handles, and
// opens live connections.
type Driver interface {
	// Name returns the short, unique identifier ("postgres", "mysql", ...).
	Name() string

	// Schemes returns the URL schemes this driver handles ("postgres",
	// "postgresql"). Used to resolve a driver from a connection URL.
	Schemes() []string

	// DefaultPort returns the conventional TCP port (0 for file/embedded DBs).
	DefaultPort() int

	// Connect opens a live connection. The returned Conn is owned by the
	// caller, which must Close it.
	Connect(ctx context.Context, info ConnInfo) (Conn, error)
}

// Conn is a live connection to a database. All methods take a context so the
// TUI can cancel long-running operations.
type Conn interface {
	// Ping verifies the connection is alive.
	Ping(ctx context.Context) error

	// Schemas lists the namespaces visible on this connection.
	Schemas(ctx context.Context) ([]Schema, error)

	// Objects lists tables, views, functions and procedures in a schema.
	Objects(ctx context.Context, schema string) ([]DBObject, error)

	// Columns lists the columns of a table or view.
	Columns(ctx context.Context, schema, table string) ([]Column, error)

	// Definition returns a CREATE-style DDL / source definition for obj.
	Definition(ctx context.Context, obj DBObject) (string, error)

	// Query runs a row-returning statement and returns the rows.
	Query(ctx context.Context, sql string) (QueryResult, error)

	// Exec runs a non-row statement (DDL/DML) and returns a summary.
	Exec(ctx context.Context, sql string) (ExecResult, error)

	// Close releases the connection.
	Close() error
}

// ─── Optional capability interfaces ─────────────────────────────────────────
// Drivers MAY implement these. sqwee discovers them at runtime via a type
// assertion (e.g. conn.(Explainer)). This keeps the required Driver/Conn
// surface small while letting richer drivers opt into extra features.

// Explainer is implemented by connections that can return a query plan.
type Explainer interface {
	Explain(ctx context.Context, sql string) (string, error)
}

// ProcedureRunner is implemented by connections that can CALL a stored
// procedure with positional arguments.
type ProcedureRunner interface {
	CallProcedure(ctx context.Context, obj DBObject, args []string) (QueryResult, error)
}
