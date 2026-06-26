package main

import (
	"bufio"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"main.go/internal/driver"
)

// discoverConnections scans the environment and common config files for
// connection details, returning them as driver.ConnInfo values tagged with
// their Source. These are surfaced (read-only) alongside saved connections.
func discoverConnections(cfg Config) []driver.ConnInfo {
	var found []driver.ConnInfo

	if cfg.Discovery.ScanEnv {
		found = append(found, discoverFromEnv()...)
	}
	if cfg.Discovery.ScanDotenv {
		found = append(found, discoverFromDotenv()...)
	}
	if cfg.Discovery.ScanPgpass {
		found = append(found, discoverFromPgpass()...)
	}
	if cfg.Discovery.ScanSQLite {
		found = append(found, discoverSQLiteFiles()...)
	}
	if cfg.Discovery.ScanPorts {
		found = append(found, discoverListeningServers()...)
	}
	return dedupeConns(found)
}

// sqliteExts are the file extensions treated as SQLite databases.
var sqliteExts = map[string]bool{
	".sqlite":  true,
	".sqlite3": true,
	".db":      true,
}

// discoverSQLiteFiles scans the working directory (non-recursively) for SQLite
// database files and returns a connectable entry for each.
func discoverSQLiteFiles() []driver.ConnInfo {
	var out []driver.ConnInfo
	entries, err := os.ReadDir(".")
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !sqliteExts[ext] {
			continue
		}
		abs, err := filepath.Abs(e.Name())
		if err != nil {
			abs = e.Name()
		}
		out = append(out, driver.ConnInfo{
			Name:     e.Name(),
			Driver:   "sqlite",
			Database: abs,
			Source:   "file",
		})
	}
	return out
}

// listeningServers maps a localhost port to the driver that serves it.
var listeningServers = []struct {
	port   int
	driver string
	label  string
}{
	{1433, "mssql", "SQL Server"},
	{5432, "postgres", "PostgreSQL"},
	{3306, "mysql", "MySQL"},
	// NoSQL databases.
	{27017, "mongodb", "MongoDB"},
	{6379, "redis", "Redis"},
	{8000, "dynamodb", "DynamoDB Local"},
	{9042, "cassandra", "Cassandra"},
	{9200, "elasticsearch", "Elasticsearch"},
	// Specialised databases.
	{3000, "tigerbeetle", "TigerBeetle"},
}

// discoverListeningServers probes well-known DB ports on localhost and returns a
// credential-less placeholder for each one that is accepting connections. The
// user supplies credentials via the New Connection modal (NeedsCreds is set).
func discoverListeningServers() []driver.ConnInfo {
	var out []driver.ConnInfo
	for _, s := range listeningServers {
		addr := net.JoinHostPort("localhost", strconv.Itoa(s.port))
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err != nil {
			continue
		}
		conn.Close()
		out = append(out, driver.ConnInfo{
			Name:      s.label + " @ localhost:" + strconv.Itoa(s.port),
			Driver:    s.driver,
			Host:      "localhost",
			Port:      s.port,
			Source:    "detected",
			NeedsCred: true,
		})
	}
	return out
}

// discoverFromEnv reads DATABASE_URL plus the standard PG*/MYSQL* variables.
func discoverFromEnv() []driver.ConnInfo {
	var out []driver.ConnInfo

	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		if ci, ok := parseURLConn(dbURL, "env:DATABASE_URL"); ok {
			out = append(out, ci)
		}
	}

	// Postgres PG* variables.
	if host := os.Getenv("PGHOST"); host != "" {
		port, _ := strconv.Atoi(os.Getenv("PGPORT"))
		if port == 0 {
			port = 5432
		}
		out = append(out, driver.ConnInfo{
			Name:     "PG env",
			Driver:   "postgres",
			Host:     host,
			Port:     port,
			User:     os.Getenv("PGUSER"),
			Password: os.Getenv("PGPASSWORD"),
			Database: os.Getenv("PGDATABASE"),
			Source:   "env:PG*",
		})
	}

	// MySQL MYSQL_* variables.
	if host := os.Getenv("MYSQL_HOST"); host != "" {
		port, _ := strconv.Atoi(os.Getenv("MYSQL_TCP_PORT"))
		if port == 0 {
			port = 3306
		}
		out = append(out, driver.ConnInfo{
			Name:     "MySQL env",
			Driver:   "mysql",
			Host:     host,
			Port:     port,
			User:     os.Getenv("MYSQL_USER"),
			Password: os.Getenv("MYSQL_PWD"),
			Database: os.Getenv("MYSQL_DATABASE"),
			Source:   "env:MYSQL_*",
		})
	}

	// ─── NoSQL env vars ───────────────────────────────────────────────────────

	// MongoDB: MONGODB_URL, MONGO_URL, or MONGO_URI.
	for _, key := range []string{"MONGODB_URL", "MONGO_URL", "MONGO_URI"} {
		if u := os.Getenv(key); u != "" {
			if ci, ok := parseURLConn(u, "env:"+key); ok {
				out = append(out, ci)
			}
			break
		}
	}

	// Redis: REDIS_URL, REDIS_URI, or REDIS_HOST.
	redisHandled := false
	for _, key := range []string{"REDIS_URL", "REDIS_URI"} {
		if u := os.Getenv(key); u != "" {
			if ci, ok := parseURLConn(u, "env:"+key); ok {
				out = append(out, ci)
			}
			redisHandled = true
			break
		}
	}
	if !redisHandled {
		if host := os.Getenv("REDIS_HOST"); host != "" {
			port, _ := strconv.Atoi(os.Getenv("REDIS_PORT"))
			if port == 0 {
				port = 6379
			}
			db := os.Getenv("REDIS_DB")
			if db == "" {
				db = "0"
			}
			out = append(out, driver.ConnInfo{
				Name:     "Redis env",
				Driver:   "redis",
				Host:     host,
				Port:     port,
				Password: os.Getenv("REDIS_PASSWORD"),
				Database: db,
				Source:   "env:REDIS_*",
			})
		}
	}

	// DynamoDB: detected when AWS_REGION (or AWS_DEFAULT_REGION) is set.
	// For local DynamoDB the endpoint is taken from AWS_ENDPOINT_URL or
	// DYNAMODB_ENDPOINT.
	awsRegion := os.Getenv("AWS_REGION")
	if awsRegion == "" {
		awsRegion = os.Getenv("AWS_DEFAULT_REGION")
	}
	if awsRegion != "" {
		opts := map[string]string{"aws_region": awsRegion}
		if ep := os.Getenv("AWS_ENDPOINT_URL"); ep != "" {
			opts["endpoint_url"] = ep
		} else if ep := os.Getenv("DYNAMODB_ENDPOINT"); ep != "" {
			opts["endpoint_url"] = ep
		}
		if k := os.Getenv("AWS_ACCESS_KEY_ID"); k != "" {
			opts["aws_access_key_id"] = k
			opts["aws_secret_access_key"] = os.Getenv("AWS_SECRET_ACCESS_KEY")
			if t := os.Getenv("AWS_SESSION_TOKEN"); t != "" {
				opts["aws_session_token"] = t
			}
		}
		out = append(out, driver.ConnInfo{
			Name:    "DynamoDB env (" + awsRegion + ")",
			Driver:  "dynamodb",
			Options: opts,
			Source:  "env:AWS_*",
		})
	}

	// Elasticsearch: ELASTICSEARCH_URL, ELASTIC_URL.
	for _, key := range []string{"ELASTICSEARCH_URL", "ELASTIC_URL", "OPENSEARCH_URL"} {
		if u := os.Getenv(key); u != "" {
			if ci, ok := parseURLConn(u, "env:"+key); ok {
				out = append(out, ci)
			}
			break
		}
	}

	// TigerBeetle: TB_ADDRESS (the client's own convention, e.g. "127.0.0.1:3000").
	if addr := os.Getenv("TB_ADDRESS"); addr != "" {
		host, portStr, splitErr := net.SplitHostPort(addr)
		port := 3000
		if splitErr == nil {
			if n, err := strconv.Atoi(portStr); err == nil {
				port = n
			}
		} else {
			// Bare port number ("3000") — TigerBeetle's own default format.
			if n, err := strconv.Atoi(addr); err == nil {
				host = "localhost"
				port = n
			} else {
				host = addr
			}
		}
		out = append(out, driver.ConnInfo{
			Name:   "TigerBeetle env",
			Driver: "tigerbeetle",
			Host:   host,
			Port:   port,
			Source: "env:TB_ADDRESS",
		})
	}

	return out
}

// discoverFromDotenv reads .env* files in the working directory looking for a
// connection URL (*DATABASE_URL / *DB_URL) or a SQLite file path
// (*DATABASE_PATH / *DB_PATH / *SQLITE* / any value ending in a sqlite ext).
func discoverFromDotenv() []driver.ConnInfo {
	return discoverFromEnvSources(discoverEnvSources())
}

func discoverFromEnvSources(sources []EnvSource) []driver.ConnInfo {
	var out []driver.ConnInfo
	for _, src := range sources {
		for key, val := range src.Values {
			ukey := strings.ToUpper(key)
			if val == "" {
				continue
			}

			switch {
			case strings.HasSuffix(ukey, "DATABASE_URL") || strings.HasSuffix(ukey, "DB_URL") ||
				strings.HasSuffix(ukey, "MONGODB_URL") || strings.HasSuffix(ukey, "MONGO_URI") ||
				strings.HasSuffix(ukey, "REDIS_URL") || strings.HasSuffix(ukey, "REDIS_URI") ||
				strings.HasSuffix(ukey, "ELASTICSEARCH_URL") || strings.HasSuffix(ukey, "ELASTIC_URL"):
				if ci, ok := parseURLConn(val, src.Name+":"+key); ok {
					out = append(out, ci)
				}
			case strings.HasSuffix(ukey, "DATABASE_PATH") || strings.HasSuffix(ukey, "DB_PATH") ||
				strings.Contains(ukey, "SQLITE") || sqliteExts[strings.ToLower(filepath.Ext(val))]:
				out = append(out, driver.ConnInfo{
					Name:     filepath.Base(val),
					Driver:   "sqlite",
					Database: absFromWorkingDir(val),
					Source:   src.Name + ":" + key,
				})
			}
		}
	}
	return out
}

// discoverFromPgpass reads ~/.pgpass (host:port:database:user:password).
func discoverFromPgpass() []driver.ConnInfo {
	var out []driver.ConnInfo
	home, err := os.UserHomeDir()
	if err != nil {
		return out
	}
	f, err := os.Open(filepath.Join(home, ".pgpass"))
	if err != nil {
		return out
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) < 5 {
			continue
		}
		port, _ := strconv.Atoi(parts[1])
		if port == 0 {
			port = 5432
		}
		host := parts[0]
		if host == "*" {
			host = "localhost"
		}
		out = append(out, driver.ConnInfo{
			Name:     "pgpass " + parts[3] + "@" + host,
			Driver:   "postgres",
			Host:     host,
			Port:     port,
			User:     parts[3],
			Password: parts[4],
			Database: strings.TrimPrefix(parts[2], "*"),
			Source:   ".pgpass",
		})
	}
	return out
}

// parseURLConn parses a connection URL into a ConnInfo, resolving the driver
// from the scheme so the entry is connectable.
func parseURLConn(raw, source string) (driver.ConnInfo, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		return driver.ConnInfo{}, false
	}
	d := driver.ForScheme(u.Scheme)
	drvName := ""
	if d != nil {
		drvName = d.Name()
	}
	port, _ := strconv.Atoi(u.Port())
	pw, _ := u.User.Password()
	options := map[string]string{}
	for k, vals := range u.Query() {
		if len(vals) > 0 {
			options[k] = vals[len(vals)-1]
		}
	}
	name := u.Scheme
	if u.Host != "" {
		name = u.Scheme + " " + u.Host
	}
	return driver.ConnInfo{
		Name:     name,
		Driver:   drvName,
		URL:      raw,
		Host:     u.Hostname(),
		Port:     port,
		User:     u.User.Username(),
		Password: pw,
		Database: strings.TrimPrefix(u.Path, "/"),
		Options:  options,
		Source:   source,
	}, true
}

func dedupeConns(in []driver.ConnInfo) []driver.ConnInfo {
	seen := map[string]bool{}
	var out []driver.ConnInfo
	for _, ci := range in {
		key := ci.Driver + "|" + ci.URL + "|" + ci.Host + "|" + strconv.Itoa(ci.Port) + "|" + ci.Database + "|" + ci.User
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ci)
	}
	return out
}
