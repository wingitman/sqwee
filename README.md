# sqwee

A terminal database client for connecting to any database — browse schemas, edit objects, and run queries from a fast, keyboard-driven TUI.

`sqwee` auto-discovers connection details from your environment, lets you save and manage connections, browses tables / views / functions / stored procedures for the connected database, and runs arbitrary SQL with a results grid. It ships with built-in **Postgres**, **MySQL / MariaDB**, **SQLite**, and **SQL Server** drivers, and its driver system is pluggable — add support for any database by implementing one Go interface (see [SPEC.md](SPEC.md)).

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Lip Gloss](https://github.com/charmbracelet/lipgloss). A delbysoft project.

---

## Features

- **Three-tab UI** — Connections, Schema, and Query, cycled with `Tab` / `Shift+Tab`
- **Auto-discovery** — finds connections in `DATABASE_URL`, `PG*` / `MYSQL_*` env vars, `.env*` files (incl. `DATABASE_PATH` SQLite paths), `~/.pgpass`, **local `*.sqlite` / `*.db` files in the working directory**, and **DB servers listening on localhost**
- **SQL script library** — discovers `*.sql` files in the working directory and lets you load and run them (with T-SQL `GO` batch support)
- **Initialize a database** — spin up a new database from scratch: a SQLite file, a `CREATE DATABASE` on a running server, or a fresh **Docker container** — then auto-save and connect to it
- **Saved connections** — persisted to `~/.config/delbysoft/sqwee.json`; fields can be literals or `.env*` references like `env-dev:PGHOST`
- **Schema browser** — tables, views, functions, and stored procedures with column details; **browse every database on a SQL Server instance**
- **Query editor** — run arbitrary SQL with a scrollable, NULL-aware results grid; open the editor in `$EDITOR`
- **Object definitions** — load `CREATE` DDL for any object straight into the editor
- **Pluggable drivers** — built-in Postgres / MySQL / SQLite / SQL Server; add your own in Go (see [SPEC.md](SPEC.md))
- **Fully remappable keybinds** via a `.toml` config you can reload live with `o`
- **CGO-free** — pure-Go drivers, so it cross-compiles cleanly

---

## Installation

### macOS / Linux

```bash
git clone https://github.com/delbysoft/sqwee
cd sqwee
make install
```

`make install` installs `sqwee` to `~/.local/bin/sqwee` and tells you if `~/.local/bin` needs to be added to your `PATH`. If Go is installed, it builds from source; otherwise it installs the matching pre-built binary from `releases/`.

### Windows

```powershell
git clone https://github.com/delbysoft/sqwee
cd sqwee
.\install.ps1
```

`install.ps1` installs `sqwee` to `%LOCALAPPDATA%\Programs\sqwee\sqwee.exe` and adds that directory to your user `PATH` via the registry (no admin required). If Go is installed, it builds from source; otherwise it installs the pre-built binary from `releases\windows\sqwee.exe`.

> **Execution policy:** if you see a policy error, run once as your user:
> ```powershell
> Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
> ```

No Go install is required on supported pre-built platforms unless you want to build from source or refresh the pre-built binaries.

### Quick run (no install)

```bash
make run     # or: ./run
```

---

## Usage

```bash
sqwee
```

On launch sqwee opens the **Connections** tab. Any connections it discovered from your environment appear automatically, alongside connections you've saved.

### The three tabs

| Tab | What it does |
|-----|--------------|
| **Connections** | Pick / add / edit / delete connections. Press `c` (or `Enter`) to connect — that connection becomes the active context for the other two tabs. Press `i` to **initialize a new database** (see below), or `y` to copy the selected connection's config. |
| **Schema** | Browse tables, views, functions and stored procedures of the active connection. `Enter` previews a table/view or loads an object's definition into the Query tab. |
| **Query** | Edit and run SQL. `Enter` enters edit mode, `s` runs the statement, `E` opens the editor in `$EDITOR`. Press `Tab` to focus the **results grid** and select cells/rows/columns to copy or export (see below). If `*.sql` files were found, a **Scripts** pane appears on the left (`h` to focus it, `Enter` to load a script). |

### Default keybinds

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Next / previous tab |
| `k` / `j` | Move up / down |
| `h` / `l` | Move left / right |
| `Enter` | Select / open / enter edit mode |
| `Esc` | Cancel / back |
| `c` | Connect to the selected connection |
| `i` | Initialize / provision a new database |
| `n` | New connection / object |
| `e` | Edit selected connection |
| `d` | Delete selected connection |
| `r` | Refresh schema / re-scan connections |
| `s` | Run the SQL in the query editor |
| `/` | Filter the schema object list |
| `E` | Open the query / definition in `$EDITOR` |
| `y` | Copy to clipboard (query, or the results selection as CSV) |
| `Y` | Copy the results selection **with headers** |
| `v` | Cycle the results selection scope (cell → row → column → all) |
| `X` | Export the full result set to `~/Downloads` (CSV / TSV / JSON) |
| `o` | Open the config file in `$EDITOR` (reloads live) |
| `q` | Quit (`Ctrl+C` always works too) |

All of these are configurable — see **Config** below.

### The results grid

After running a query, press `Tab` to move focus from the editor into the
results grid. Then:

- Move the cell cursor with the navigation keys (`h`/`j`/`k`/`l` by default),
  or click a cell with the mouse.
- Press `v` to cycle the **selection scope**: a single **cell**, the whole
  **row**, the whole **column**, or **all** rows. For example, select the
  `name` column to copy every user's name.
- Press `y` to copy the selection, or `Y` to copy it **with column headers**.
  Copies are **RFC 4180 CSV** (fields containing commas, quotes, or newlines
  are properly quoted), so they paste straight into a spreadsheet or `.csv`
  file with no manual splitting.
- Press `X` to export the **entire** result set to your `~/Downloads` folder
  as CSV, TSV, or JSON.
- Press `Shift+Tab` (or `Esc`) to return focus to the editor.

---

## Initializing a database

From the **Connections** tab, press `i` to create a brand-new database from scratch. A short wizard walks you through:

1. **Type** — pick a database engine (only engines whose driver supports provisioning are listed).
2. **Mode** — how to create it:
   - **SQLite** → a new database **file**.
   - **Postgres / MySQL / SQL Server** → either run `CREATE DATABASE` on a **running server**, or spin up a fresh **Docker container** and create the database inside it.
3. **Configure** — fill in the connection settings (file path, or host/port/admin-user/password/new-database-name).
4. **Confirm** — review a summary of what will be created. Press `y` to **copy the config** to your clipboard, then `Enter` to proceed.

sqwee then creates the database, **saves a connection** to it in `sqwee.json`, adds it to your connection list, and **auto-connects** so its schema appears in the Schema tab.

**Docker mode** requires the `docker` CLI and a running daemon. sqwee runs `docker run` with the official image for the engine (`postgres:16`, `mysql:8`, `mcr.microsoft.com/mssql/server:2022-latest`), waits for the server to accept connections, then issues `CREATE DATABASE`. The container name and generated password are shown in the result. If Docker isn't available, the file/server modes still work.

> Provisioning a server or Docker database **connects to an existing/just-started server** and runs `CREATE DATABASE`. It does not install a database engine on your machine.

---

## Connection discovery

On startup (and on `r` in the Connections tab) sqwee scans, non-destructively:

- `DATABASE_URL` — any `postgres://`, `mysql://`, `sqlserver://`, or `sqlite://` URL
- `PGHOST` / `PGPORT` / `PGUSER` / `PGPASSWORD` / `PGDATABASE`
- `MYSQL_HOST` / `MYSQL_TCP_PORT` / `MYSQL_USER` / `MYSQL_PWD` / `MYSQL_DATABASE`
- `.env*` files in the working directory — `*DATABASE_URL` / `*DB_URL` URLs **and** SQLite paths from `*DATABASE_PATH` / `*DB_PATH` / `SQLITE*` keys
- `~/.pgpass` (`host:port:database:user:password`)
- **`*.sqlite` / `*.sqlite3` / `*.db` files** in the working directory
- **listening DB servers** on `localhost` (`1433` SQL Server, `5432` Postgres, `3306` MySQL)

Discovered connections are read-only in the UI (they reflect your environment). To make one permanent, add it with `n`.

A **detected server** has no credentials, so it's shown as "credentials required". Pressing connect (`c`) on it opens the New Connection form pre-filled with its host/port/driver — fill in the user/password and connect. For SQL Server, once connected you can browse **every database on the instance**, not just the one in the connection string.

Each scan can be toggled in the config `[discovery]` section.

---

## Config

sqwee reads a TOML config from the platform config directory:

| OS | Path |
|----|------|
| Linux | `~/.config/delbysoft/sqwee.toml` |
| macOS | `~/Library/Application Support/delbysoft/sqwee.toml` |
| Windows | `%AppData%\Roaming\delbysoft\sqwee.toml` |

It is created with sensible defaults on first launch. Press `o` inside sqwee to open it in `$EDITOR` — changes are reloaded live when you close the editor.

```toml
[keys]
up           = "k"
down         = "j"
left         = "h"
right        = "l"
enter        = "enter"
escape       = "esc"
tab_next     = "tab"
tab_prev     = "shift+tab"
run_query    = "s"
new_item     = "n"
edit_item    = "e"
delete_item  = "d"
connect      = "c"
refresh      = "r"
copy_item    = "y"
init_db      = "i"   # initialize/provision a new database
select_mode  = "v"   # cycle results selection: cell -> row -> column -> all
copy_headers = "Y"   # copy the results selection including column headers
export       = "X"   # export results to ~/Downloads (CSV / TSV / JSON)
open_editor  = "E"
open_config  = "o"
quit         = "q"

[ui]
sidebar_width = 32   # width of the left list in columns
results_split = 50   # % of the Query tab height for the editor
theme         = "dark"
editor        = ""   # optional editor command; empty uses $VISUAL, $EDITOR, then OS default
file_explorer = ""   # optional folder opener; empty uses OS default

[discovery]
scan_env    = true   # scan DATABASE_URL / PG* / MYSQL_* env vars
scan_dotenv = true   # scan .env* files in the working directory
scan_pgpass = true   # scan ~/.pgpass for Postgres connections
scan_sqlite = true   # scan the working directory for *.sqlite / *.db files
scan_sql    = true   # import *.sql script files from the working directory
scan_ports  = true   # detect DB servers listening on localhost (1433/5432/3306)
```

### Saved connections (`sqwee.json`)

Connections you add are stored next to the config in `sqwee.json`. Connection fields are literal by default. To resolve a value from a project env file, use `<env-file-alias>:<KEY>`, where the alias is the `.env*` filename without the leading dot:

- `env:PGPASSWORD` resolves from `.env`, then falls back to the process environment.
- `env-dev:PGPASSWORD` resolves from `.env-dev`.
- `env.example:PGPASSWORD` resolves from `.env.example`.

Unresolved env references are highlighted in the Connections detail panel but do not block saving; sqwee keeps the raw value you entered.

SSH gateways support password auth or private-key auth via `gateway.key_file`. For SQLite connections, a gateway runs the remote `sqlite3` CLI against the remote database file, so the EC2/server host must have `sqlite3` installed.

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
      "password": "env-dev:PGPASSWORD"
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
      "name": "timid-ec2",
      "driver": "sqlite",
      "database": "/srv/timid/timid.sqlite",
      "gateway": {
        "type": "ssh",
        "host": "ec2.example.com",
        "user": "ubuntu",
        "key_file": "~/Work/timid/timid.pem"
      }
    }
  ]
}
```

---

## Custom drivers

sqwee's driver system is pluggable. See **[SPEC.md](SPEC.md)** for the full interface specification — you can add support for any database (Oracle, ClickHouse, DuckDB, etc.) by implementing the `Driver` interface in Go and dropping a file into `internal/driver/`.

---

## Tests

The default suite is self-contained and safe to run without Docker or network access:

```bash
go test ./...
```

Live database tests are opt-in because they start containers or connect to real services:

```bash
# Docker-hosted Postgres/MySQL/SQL Server smoke tests.
SQWEE_TEST_DOCKER=1 go test ./internal/driver -run DockerDatabaseIntegration

# Limit Docker tests to a subset.
SQWEE_TEST_DOCKER=1 SQWEE_TEST_DOCKER_DRIVERS=postgres,mysql go test ./internal/driver -run DockerDatabaseIntegration

# Locally hosted databases.
SQWEE_TEST_LOCAL_POSTGRES_URL='postgres://user:pass@localhost:5432/app?sslmode=disable' go test ./internal/driver -run LocalDatabaseURLIntegration
SQWEE_TEST_LOCAL_MYSQL_URL='mysql://user:pass@localhost:3306/app' go test ./internal/driver -run LocalDatabaseURLIntegration
SQWEE_TEST_LOCAL_MSSQL_URL='sqlserver://user:pass@localhost:1433?database=app' go test ./internal/driver -run LocalDatabaseURLIntegration

# Externally hosted databases, such as AWS RDS.
SQWEE_TEST_EXTERNAL_POSTGRES_URL='postgres://user:pass@db.example.rds.amazonaws.com:5432/app?sslmode=require' go test ./internal/driver -run ExternalDatabaseURLIntegration
```

URL integration tests create and drop a temporary `sqwee_smoke_*` table in the target database, so point them at a disposable test database/user.

---

## Building from source

```bash
make build      # builds bin/sqwee with the current commit baked in
make clean
```

Building from source requires [Go 1.26+](https://go.dev/dl/).

### Refreshing release binaries

Run this before committing when source has changed, to refresh the pre-built binaries that users without Go install from:

```bash
# Linux/macOS
make build-all

# Windows PowerShell
.\install.ps1 -BuildAll
```

This cross-compiles to `releases/linux/amd64/`, `releases/linux/arm64/`, `releases/darwin/amd64/`, `releases/darwin/arm64/`, and `releases/windows/`. Commit the results.

---

## Uninstall

### macOS / Linux

```bash
make uninstall            # removes the binary; leaves config/data
rm -rf ~/.config/delbysoft/sqwee.toml ~/.config/delbysoft/sqwee.json   # to fully remove
```

### Windows

```powershell
.\uninstall.ps1           # removes the binary and PATH entry
.\uninstall.ps1 -Purge    # also removes config + data
```

---

## License

MIT © 2026 delbysoft. See [LICENSE](LICENSE).
