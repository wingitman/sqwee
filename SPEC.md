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

### Provisioner — create a new database

A driver MAY implement `Provisioner` to let sqwee create a brand-new database
for that engine (the "Initialize database" wizard, bound to `i`). It is
discovered via `driver.AsProvisioner(name)` / `driver.Provisioners()`:

```go
type Provisioner interface {
    // ProvisionModes returns the strategies this driver offers (>= 1).
    ProvisionModes() []ProvisionMode
    // Provision creates the database for the chosen mode using the collected
    // field values, returning a connection to the new database.
    Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error)
}

type ProvisionMode struct {
    ID     string           // "file", "server", "docker"
    Label  string           // human description shown in the wizard
    Fields []ProvisionField // inputs to collect for this mode
}

type ProvisionField struct {
    Key         string   // map key passed to Provision ("host", "db_name", ...)
    Label       string
    Default     string
    Placeholder string
    Options     []string // non-empty => selector field
    Password    bool     // masked; never persisted
    Optional    bool
}

type ProvisionResult struct {
    Info         ConnInfo // connection to the new DB (Password left empty)
    Steps        []string // human log shown in the summary
    Container    string   // docker container name, or ""
    PasswordHint string   // suggested env-var name / generated docker password
}
```

The wizard builds its form fields from the selected mode's `Fields`, then calls
`Provision(ctx, mode.ID, values)`. On success sqwee saves the returned
`ConnInfo` as a connection (password referenced by env-var name), adds it to the
list, and auto-connects.

**Provisioning helpers** in the `driver` package make this easy to implement:

- `serverProvisionSpec` + `provisionServer` / `provisionDocker` — connect to a
  maintenance database and run `CREATE DATABASE`, or start a Docker container
  first. The three server drivers share this.
- `dockerAvailable()` and `runDockerContainer()` shell out to the `docker` CLI
  (no Docker SDK dependency), and `waitForServer()` polls `Connect`+`Ping` until
  a freshly-started server is ready.
- `quotePostgres` / `quoteMySQL` / `quoteMSSQLIdent` safely quote the new
  database name.

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
- Implements `Provisioner` — `server` (connect to the `postgres` maintenance DB,
  `CREATE DATABASE`) and `docker` (`postgres:16`) modes.

### MySQL / MariaDB (`mysql`)

- **Schemes:** `mysql`, `mariadb`
- **Backend:** `github.com/go-sql-driver/mysql`
- **Connect:** builds a DSN with `parseTime=true`. A "schema" is a database.
- **Introspection:** `information_schema`; definitions via `SHOW CREATE ...`.
- Implements `Explainer`.
- Implements `Provisioner` — `server` (connect with no default DB,
  `CREATE DATABASE`) and `docker` (`mysql:8`) modes.

### SQLite (`sqlite`)

- **Schemes:** `sqlite`, `sqlite3`, `file`
- **Backend:** `modernc.org/sqlite` (pure-Go, no CGO)
- **Connect:** the "database" is a file path (from `info.Database`, `info.URL`,
  or `info.Host`). Reports a single schema, `main`.
- **Introspection:** `sqlite_master` + `PRAGMA table_info`.
- Implements `Explainer` (`EXPLAIN QUERY PLAN`).
- Implements `Provisioner` — a single `file` mode (opening a new path creates
  the file). Refuses to overwrite an existing file.

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
- Implements `Provisioner` — `server` (connect to `master`, `CREATE DATABASE`)
  and `docker` (`mcr.microsoft.com/mssql/server:2022-latest`) modes. Docker
  generates a complexity-compliant SA password.

### MongoDB (`mongodb`)

- **Schemes:** `mongodb`, `mongodb+srv`
- **Default port:** 27017
- **Backend:** `go.mongodb.org/mongo-driver/v2`
- **Connect:** uses `info.URL` if set, otherwise builds `mongodb://[user:pass@]host:port[/db]`.
  `info.Database` sets the default database (falls back to `"test"`).
- **Schema browser:**
  - *Schemas* → databases (`ListDatabaseNames`)
  - *Objects* → collections (`ListCollectionNames`), shown as `KindTable`
  - *Columns* → field names sampled from up to 20 documents (`$sample`); `_id` is always first with `Key="PK"`
  - *Definition* → `collStats` output as formatted JSON
- **Query language** — MongoDB shell syntax:

  ```
  collection.find({filter})
  collection.find({filter}, {projection})
  collection.findOne({filter})
  collection.aggregate([pipeline])
  collection.countDocuments({filter})
  mydb.collection.find({})          ← explicit database prefix
  ```

- **Exec language** — write operations in the same syntax:

  ```
  collection.insertOne({doc})
  collection.insertMany([docs])
  collection.updateOne({filter}, {update})
  collection.updateMany({filter}, {update})
  collection.deleteOne({filter})
  collection.deleteMany({filter})
  ```

- **Discovery:** reads `MONGODB_URL`, `MONGO_URL`, `MONGO_URI` env vars; probes port 27017.

---

### Redis (`redis`)

- **Schemes:** `redis`, `rediss` (TLS)
- **Default port:** 6379
- **Backend:** `github.com/redis/go-redis/v9`
- **Connect:** uses `info.URL` if set (parsed by `redis.ParseURL`), otherwise builds from
  `host:port`, `info.Password`, and `info.Database` (DB index as a string, e.g. `"0"`).
- **Schema browser:**
  - *Schemas* → logical databases 0 … N-1 (N from `CONFIG GET databases`, default 16)
  - *Objects* → key-prefix groups: keys with a `:` are grouped as `prefix:*`; bare keys are listed individually (SCAN, up to 2000 keys)
  - *Columns* → metadata columns: `key`, `type`, `ttl`, `encoding`, `size`
  - *Definition* → full value dump: `GET` (string), `HGETALL` (hash), `LRANGE 0 99` (list), `SMEMBERS` (set), `ZRANGE 0 99 WITHSCORES` (sorted set)
- **Query / Exec:** raw Redis commands, e.g. `GET mykey`, `HSET user:1 name Alice`.
  List and hash replies are rendered as a two-column table; scalar replies as a single cell.
- **Discovery:** reads `REDIS_URL`, `REDIS_URI`, `REDIS_HOST`/`REDIS_PORT` env vars; probes port 6379.

---

### DynamoDB (`dynamodb`)

- **Schemes:** `dynamodb`
- **Default port:** 0 (AWS-managed; 8000 for DynamoDB Local)
- **Backend:** `github.com/aws/aws-sdk-go-v2/service/dynamodb`
- **Connect:** loads AWS credentials from `ConnInfo.Options` (explicit keys) or falls back to
  the standard AWS credential chain (env vars, `~/.aws/credentials`, IAM role).

  | Option key              | Meaning |
  |-------------------------|---------|
  | `aws_region`            | AWS region (required; falls back to `AWS_REGION` / `AWS_DEFAULT_REGION`) |
  | `aws_access_key_id`     | Access key (optional; env fallback) |
  | `aws_secret_access_key` | Secret key (optional; env fallback) |
  | `aws_session_token`     | Session token (optional) |
  | `endpoint_url`          | Custom endpoint, e.g. `http://localhost:8000` for DynamoDB Local |

  A URL of the form `dynamodb://localhost:8000` is treated as a local endpoint.

- **Schema browser:**
  - *Schemas* → single entry `"default"` (DynamoDB has no namespaces)
  - *Objects* → all tables (`ListTables`), shown as `KindTable`
  - *Columns* → key schema attributes (PK, SK) + GSI/LSI key attributes from `DescribeTable`
  - *Definition* → `DescribeTable` output as formatted JSON
- **Query / Exec language** — JSON operation envelope:

  ```json
  {"Operation":"Scan",  "TableName":"users","Limit":50}
  {"Operation":"Query", "TableName":"orders",
   "KeyConditionExpression":"pk = :pk",
   "ExpressionAttributeValues":{":pk":{"S":"user#1"}}}
  {"Operation":"GetItem","TableName":"users","Key":{"id":{"S":"abc"}}}
  {"Operation":"PutItem","TableName":"users","Item":{"id":{"S":"abc"},"name":{"S":"Alice"}}}
  {"Operation":"UpdateItem","TableName":"users","Key":{"id":{"S":"abc"}},
   "UpdateExpression":"SET #n = :n",
   "ExpressionAttributeNames":{"#n":"name"},
   "ExpressionAttributeValues":{":n":{"S":"Bob"}}}
  {"Operation":"DeleteItem","TableName":"users","Key":{"id":{"S":"abc"}}}
  ```

  All fields beyond `"Operation"` are passed verbatim to the AWS SDK input struct.

- **Discovery:** reads `AWS_REGION` / `AWS_DEFAULT_REGION`; if `AWS_ENDPOINT_URL` or
  `DYNAMODB_ENDPOINT` is set that endpoint is used. Probes port 8000 (DynamoDB Local).

---

### Cassandra (`cassandra`)

- **Schemes:** `cassandra`, `cql`
- **Default port:** 9042
- **Backend:** `github.com/gocql/gocql`
- **Connect:** parses host from `info.URL` or `info.Host`; `info.Database` sets the default keyspace.
  Username/password authentication is supported via `info.User` / `info.Password`.
- **Schema browser:**
  - *Schemas* → keyspaces (`system_schema.keyspaces`)
  - *Objects* → tables (`system_schema.tables`) + materialized views (`system_schema.views`), as `KindTable` / `KindView`
  - *Columns* → columns from `system_schema.columns`; partition key → `Key="PK"`, clustering key → `Key="SK"`
  - *Definition* → reconstructed `CREATE TABLE` CQL with `PRIMARY KEY` clause
- **Query / Exec:** standard CQL — the existing query editor works unchanged.

  ```cql
  SELECT * FROM users WHERE id = 'abc'
  INSERT INTO users (id, name) VALUES ('1', 'Alice')
  CREATE TABLE events (id uuid PRIMARY KEY, ts timestamp, msg text)
  ```

- **Discovery:** probes port 9042.

---

### Elasticsearch (`elasticsearch`)

- **Schemes:** `elasticsearch`, `es`
- **Default port:** 9200
- **Backend:** `github.com/elastic/go-elasticsearch/v8`
- **Connect:** builds the client from `info.URL` (scheme normalised to `http://`) or
  `info.Host` / `info.Port`. Extra options in `info.Options`:

  | Option key | Meaning |
  |------------|---------|
  | `api_key`  | Elastic API key authentication |
  | `cloud_id` | Elastic Cloud ID (overrides address) |
  | `tls`      | `"true"` to use HTTPS for host/port connections |

- **Schema browser:**
  - *Schemas* → single entry `"default"`
  - *Objects* → non-system indices (system indices starting with `.` are hidden), shown as `KindTable`
  - *Columns* → top-level field mappings from the index mapping (`properties`), with field type
  - *Definition* → `GET /<index>/_mapping` + `GET /<index>/_settings` merged as JSON
- **Query language:**

  ```
  index_name {"query":{"match_all":{}}}
  logs {"query":{"range":{"@timestamp":{"gte":"now-1h"}}}}
  {"query":{"match":{"message":"error"}}}    ← searches all indices
  ```

  Results show `_index`, `_id`, `_score` meta columns followed by all `_source` fields.

- **Exec language:**

  ```
  index  <index_name> <json-document>
  delete <index_name> <doc_id>
  create_index <name> [<settings-json>]
  delete_index <name>
  refresh [<index_name>]
  ```

- **Discovery:** reads `ELASTICSEARCH_URL`, `ELASTIC_URL`, `OPENSEARCH_URL` env vars; probes port 9200.

---

## Saved Connection Format (`sqwee.json`)

Connections sqwee saves live in `~/.config/delbysoft/sqwee.json`. String fields
are literal by default, but may use inline env-file references. The prefix is the
`.env*` filename without its leading dot: `env:PGHOST` maps to `.env`,
`env-dev:PGHOST` maps to `.env-dev`, and `env.example:PGHOST` maps to
`.env.example`. The canonical `env:KEY` form also falls back to the process
environment. Unresolved references are highlighted in the UI but remain valid raw
values.

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
      "password": "env:PGPASSWORD"
    },
    {
      "name": "dev-postgres",
      "driver": "postgres",
      "host": "env-dev:PGHOST",
      "port_env": "env-dev:PGPORT",
      "user": "env-dev:PGUSER",
      "database": "env-dev:PGDATABASE",
      "password": "env-dev:PGPASSWORD"
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

A saved entry is turned into a `driver.ConnInfo` at connect time, resolving
inline env references on URL, host, port, user, password and database. `port_env`
is used for env-backed ports because `port` remains a JSON number for existing
saved connections. Legacy `password_env` is still supported when `password` is
empty.

---

*sqwee is a delbysoft project.*
