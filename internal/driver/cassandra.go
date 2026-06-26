package driver

// Cassandra driver for sqwee.
//
// Schema browser mapping:
//   Schemas    → keyspaces (from system_schema.keyspaces)
//   Objects    → tables + materialized views in a keyspace (KindTable / KindView)
//   Columns    → columns from system_schema.columns
//   Definition → a reconstructed CREATE TABLE CQL statement
//
// Query / Exec:
//   Use standard CQL (Cassandra Query Language), e.g.
//     SELECT * FROM users WHERE id = 'abc'
//     INSERT INTO users (id, name) VALUES ('1', 'Alice')
//   Results are rendered as a flat table (one column per CQL column).

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gocql/gocql"
)

func init() { Register(&cassandraDriver{}) }

type cassandraDriver struct{}

func (d *cassandraDriver) Name() string      { return "cassandra" }
func (d *cassandraDriver) Schemes() []string { return []string{"cassandra", "cql"} }
func (d *cassandraDriver) DefaultPort() int  { return 9042 }

func (d *cassandraDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	hosts := []string{"localhost"}
	if info.Host != "" {
		hosts = []string{info.Host}
	}
	if info.URL != "" {
		raw := strings.TrimPrefix(info.URL, "cassandra://")
		raw = strings.TrimPrefix(raw, "cql://")
		// Strip path (keyspace) to get host:port.
		if idx := strings.Index(raw, "/"); idx > 0 {
			raw = raw[:idx]
		}
		if raw != "" {
			hosts = []string{raw}
		}
	}

	cluster := gocql.NewCluster(hosts...)
	cluster.Port = 9042
	if info.Port > 0 {
		cluster.Port = info.Port
	}
	if info.User != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: info.User,
			Password: info.Password,
		}
	}
	if info.Database != "" {
		cluster.Keyspace = info.Database
	}
	cluster.Consistency = gocql.Quorum
	cluster.ConnectTimeout = 10 * time.Second
	cluster.Timeout = 30 * time.Second

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("cassandra: connect: %w", err)
	}
	return &cassandraConn{session: session}, nil
}

// ─── Conn ────────────────────────────────────────────────────────────────────

type cassandraConn struct {
	session *gocql.Session
}

func (c *cassandraConn) Ping(ctx context.Context) error {
	return c.session.Query("SELECT now() FROM system.local").
		WithContext(ctx).Exec()
}

func (c *cassandraConn) Close() error {
	c.session.Close()
	return nil
}

func (c *cassandraConn) Schemas(ctx context.Context) ([]Schema, error) {
	iter := c.session.Query(
		`SELECT keyspace_name FROM system_schema.keyspaces`).
		WithContext(ctx).Iter()

	var names []string
	var ks string
	for iter.Scan(&ks) {
		names = append(names, ks)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("cassandra: list keyspaces: %w", err)
	}
	sort.Strings(names)
	out := make([]Schema, len(names))
	for i, n := range names {
		out[i] = Schema{Name: n}
	}
	return out, nil
}

func (c *cassandraConn) Objects(ctx context.Context, schema string) ([]DBObject, error) {
	var out []DBObject

	// Tables.
	iterT := c.session.Query(
		`SELECT table_name FROM system_schema.tables WHERE keyspace_name = ?`, schema).
		WithContext(ctx).Iter()
	var name string
	for iterT.Scan(&name) {
		out = append(out, DBObject{Schema: schema, Name: name, Kind: KindTable})
	}
	if err := iterT.Close(); err != nil {
		return nil, fmt.Errorf("cassandra: list tables: %w", err)
	}

	// Materialized views.
	iterV := c.session.Query(
		`SELECT view_name FROM system_schema.views WHERE keyspace_name = ?`, schema).
		WithContext(ctx).Iter()
	for iterV.Scan(&name) {
		out = append(out, DBObject{Schema: schema, Name: name, Kind: KindView})
	}
	iterV.Close() //nolint:errcheck

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *cassandraConn) Columns(ctx context.Context, schema, table string) ([]Column, error) {
	iter := c.session.Query(
		`SELECT column_name, type, kind FROM system_schema.columns
		 WHERE keyspace_name = ? AND table_name = ?`,
		schema, table,
	).WithContext(ctx).Iter()

	var cols []Column
	var colName, colType, colKind string
	for iter.Scan(&colName, &colType, &colKind) {
		key := ""
		switch colKind {
		case "partition_key":
			key = "PK"
		case "clustering":
			key = "SK"
		}
		cols = append(cols, Column{
			Name:     colName,
			Type:     colType,
			Nullable: colKind == "regular",
			Key:      key,
		})
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("cassandra: list columns: %w", err)
	}

	// Sort: PK first, clustering next, then regular.
	order := map[string]int{"PK": 0, "SK": 1, "": 2}
	sort.SliceStable(cols, func(i, j int) bool {
		if cols[i].Key != cols[j].Key {
			return order[cols[i].Key] < order[cols[j].Key]
		}
		return cols[i].Name < cols[j].Name
	})
	return cols, nil
}

func (c *cassandraConn) Definition(ctx context.Context, obj DBObject) (string, error) {
	cols, err := c.Columns(ctx, obj.Schema, obj.Name)
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "CREATE TABLE %s.%s (\n", obj.Schema, obj.Name)

	var pks, cks []string
	for _, col := range cols {
		fmt.Fprintf(&sb, "    %s %s", col.Name, col.Type)
		if col.Key == "PK" {
			pks = append(pks, col.Name)
		} else if col.Key == "SK" {
			cks = append(cks, col.Name)
		}
		sb.WriteString(",\n")
	}

	// PRIMARY KEY clause.
	sb.WriteString("    PRIMARY KEY (")
	if len(pks) == 1 && len(cks) == 0 {
		sb.WriteString(pks[0])
	} else {
		sb.WriteString("(")
		sb.WriteString(strings.Join(pks, ", "))
		sb.WriteString(")")
		if len(cks) > 0 {
			sb.WriteString(", ")
			sb.WriteString(strings.Join(cks, ", "))
		}
	}
	sb.WriteString(")\n);\n")
	return sb.String(), nil
}

// Query executes CQL and returns a QueryResult.
func (c *cassandraConn) Query(ctx context.Context, cql string) (QueryResult, error) {
	start := time.Now()
	cql = strings.TrimSpace(cql)

	iter := c.session.Query(cql).WithContext(ctx).Iter()
	cols := iter.Columns()
	if len(cols) == 0 {
		if err := iter.Close(); err != nil {
			return QueryResult{}, fmt.Errorf("cassandra: %w", err)
		}
		return QueryResult{Columns: []string{"(no results)"}, Duration: time.Since(start)}, nil
	}

	colNames := make([]string, len(cols))
	for i, col := range cols {
		colNames[i] = col.Name
	}

	var rows [][]string
	var nulls [][]bool
	truncated := false

	for {
		row := make(map[string]interface{})
		if !iter.MapScan(row) {
			break
		}
		if len(rows) >= maxRows {
			truncated = true
			break
		}
		cells := make([]string, len(colNames))
		null := make([]bool, len(colNames))
		for j, col := range colNames {
			v, ok := row[col]
			if !ok || v == nil {
				null[j] = true
			} else {
				cells[j] = fmt.Sprintf("%v", v)
			}
		}
		rows = append(rows, cells)
		nulls = append(nulls, null)
	}

	if err := iter.Close(); err != nil {
		return QueryResult{}, fmt.Errorf("cassandra: %w", err)
	}

	return QueryResult{
		Columns:   colNames,
		Rows:      rows,
		Nulls:     nulls,
		Duration:  time.Since(start),
		Truncated: truncated,
	}, nil
}

// Exec runs a CQL statement that does not return rows.
func (c *cassandraConn) Exec(ctx context.Context, cql string) (ExecResult, error) {
	start := time.Now()
	cql = strings.TrimSpace(cql)

	if err := c.session.Query(cql).WithContext(ctx).Exec(); err != nil {
		return ExecResult{}, fmt.Errorf("cassandra: %w", err)
	}
	return ExecResult{Duration: time.Since(start)}, nil
}

// ─── Provisioner ─────────────────────────────────────────────────────────────

func (d *cassandraDriver) ProvisionModes() []ProvisionMode {
	return []ProvisionMode{
		{
			ID:    "docker",
			Label: "New Docker container (cassandra:5)",
			Fields: []ProvisionField{
				{Key: "container", Label: "Container name", Default: ""},
				{Key: "port", Label: "Host port", Default: "9042"},
				{Key: "keyspace", Label: "Keyspace name", Default: "myapp", Placeholder: "myapp"},
			},
		},
		{
			ID:    "server",
			Label: "Existing Cassandra cluster",
			Fields: []ProvisionField{
				{Key: "host", Label: "Host", Default: "localhost"},
				{Key: "port", Label: "Port", Default: "9042"},
				{Key: "user", Label: "Username", Optional: true},
				{Key: "password", Label: "Password", Password: true, Optional: true},
				{Key: "keyspace", Label: "New keyspace name", Default: "myapp", Placeholder: "myapp"},
			},
		},
	}
}

func (d *cassandraDriver) Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error) {
	switch mode {
	case "docker":
		return cassandraProvisionDocker(ctx, d, values)
	case "server":
		return cassandraProvisionServer(ctx, values)
	default:
		return ProvisionResult{}, fmt.Errorf("cassandra: unknown provision mode %q", mode)
	}
}

func cassandraProvisionDocker(ctx context.Context, d *cassandraDriver, values map[string]string) (ProvisionResult, error) {
	if !dockerAvailable() {
		return ProvisionResult{}, fmt.Errorf("docker is not installed or the daemon is not running")
	}
	keyspace := strings.TrimSpace(values["keyspace"])
	if keyspace == "" {
		keyspace = "myapp"
	}
	name := strings.TrimSpace(values["container"])
	if name == "" {
		name = "sqwee-cassandra-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	hostPort := freeHostPort(values, 9042)

	spec := dockerSpec{
		Image:         "cassandra:5",
		Env:           map[string]string{},
		ContainerPort: 9042,
		HostPort:      hostPort,
		Name:          name,
	}

	var steps []string
	container, err := runDockerContainer(ctx, spec)
	if err != nil {
		return ProvisionResult{}, err
	}
	steps = append(steps, "Started Docker container "+container+" (cassandra:5)")
	steps = append(steps, "Waiting for Cassandra to initialise — this may take up to 2 minutes…")

	pingInfo := ConnInfo{
		Driver: "cassandra",
		Host:   "localhost",
		Port:   hostPort,
	}
	// Cassandra JVM startup is slow; allow 2 minutes.
	waitCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	if err := waitForServer(waitCtx, d, pingInfo); err != nil {
		cancel()
		removeDockerContainer(container)
		return ProvisionResult{}, fmt.Errorf("container started but Cassandra never became ready (removed): %w", err)
	}
	cancel()
	steps = append(steps, "Server is accepting connections")

	// Create the keyspace.
	createKs := fmt.Sprintf(
		"CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class':'SimpleStrategy','replication_factor':1}",
		keyspace,
	)
	connCtx, connCancel := context.WithTimeout(ctx, 20*time.Second)
	defer connCancel()
	conn, err := d.Connect(connCtx, pingInfo)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("cassandra: cannot connect after start: %w", err)
	}
	_, execErr := conn.Exec(connCtx, createKs)
	conn.Close()
	if execErr != nil {
		return ProvisionResult{}, fmt.Errorf("cassandra: CREATE KEYSPACE failed: %w", execErr)
	}
	steps = append(steps, fmt.Sprintf("Created keyspace %q", keyspace))

	return ProvisionResult{
		Info: ConnInfo{
			Driver:   "cassandra",
			Host:     "localhost",
			Port:     hostPort,
			Database: keyspace,
		},
		Steps:     steps,
		Container: container,
	}, nil
}

func cassandraProvisionServer(ctx context.Context, values map[string]string) (ProvisionResult, error) {
	keyspace := strings.TrimSpace(values["keyspace"])
	if keyspace == "" {
		return ProvisionResult{}, fmt.Errorf("cassandra: a keyspace name is required")
	}
	host := strings.TrimSpace(values["host"])
	if host == "" {
		host = "localhost"
	}
	port := 9042
	if p := strings.TrimSpace(values["port"]); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	info := ConnInfo{
		Driver:   "cassandra",
		Host:     host,
		Port:     port,
		User:     strings.TrimSpace(values["user"]),
		Password: values["password"],
	}

	createKs := fmt.Sprintf(
		"CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {'class':'SimpleStrategy','replication_factor':1}",
		keyspace,
	)
	connCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	drv := ByName("cassandra")
	conn, err := drv.Connect(connCtx, info)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("cassandra: cannot reach cluster: %w", err)
	}
	_, execErr := conn.Exec(connCtx, createKs)
	conn.Close()
	if execErr != nil {
		return ProvisionResult{}, fmt.Errorf("cassandra: CREATE KEYSPACE failed: %w", execErr)
	}

	result := info
	result.Database = keyspace
	result.Password = ""
	return ProvisionResult{
		Info:  result,
		Steps: []string{fmt.Sprintf("Created keyspace %q on %s:%d", keyspace, host, port)},
	}, nil
}
