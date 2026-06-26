package driver

// Redis driver for sqwee.
//
// Schema browser mapping:
//   Schemas    → Redis logical databases (0 … N-1, from CONFIG GET databases).
//                Each schema name is the DB index as a string ("0", "1", …).
//   Objects    → Key-prefix groups discovered by SCAN (split on the first ":").
//                Keys without a colon are listed individually.  Each is shown
//                as KindTable so the schema tree renders them normally.
//   Columns    → Metadata for a key: type, TTL, encoding, size.
//   Definition → Full value dump (GET / HGETALL / LRANGE / SMEMBERS / ZRANGE).
//
// Query / Exec:
//   Type any Redis command, e.g.  GET mykey   HSET user:1 name Alice
//   Commands that return a list render as a single-column result.
//   Commands that return a map/hash render as two columns (field, value).
//   All other commands render their reply in a "result" column.

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// ─── Provisioner ─────────────────────────────────────────────────────────────

func (d *redisDriver) ProvisionModes() []ProvisionMode {
	return []ProvisionMode{
		{
			ID:    "docker",
			Label: "New Docker container (redis:7)",
			Fields: []ProvisionField{
				{Key: "container", Label: "Container name", Default: ""},
				{Key: "password", Label: "Password (optional)", Password: true, Optional: true},
				{Key: "port", Label: "Host port", Default: "6379"},
			},
		},
	}
}

func (d *redisDriver) Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error) {
	if mode != "docker" {
		return ProvisionResult{}, fmt.Errorf("redis: unknown provision mode %q", mode)
	}

	if !dockerAvailable() {
		return ProvisionResult{}, fmt.Errorf("docker is not installed or the daemon is not running")
	}

	name := strings.TrimSpace(values["container"])
	if name == "" {
		name = "sqwee-redis-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	password := values["password"]
	hostPort := freeHostPort(values, 6379)

	spec := dockerSpec{
		Image:         "redis:7",
		Env:           map[string]string{},
		ContainerPort: 6379,
		HostPort:      hostPort,
		Name:          name,
	}
	if password != "" {
		// The official redis image accepts requirepass as a command argument.
		spec.Cmd = []string{"redis-server", "--requirepass", password}
	}

	var steps []string
	container, err := runDockerContainer(ctx, spec)
	if err != nil {
		return ProvisionResult{}, err
	}
	steps = append(steps, "Started Docker container "+container+" (redis:7)")

	info := ConnInfo{
		Driver:   "redis",
		Host:     "localhost",
		Port:     hostPort,
		Password: password,
		Database: "0",
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if err := waitForServer(waitCtx, d, info); err != nil {
		cancel()
		removeDockerContainer(container)
		return ProvisionResult{}, fmt.Errorf("container started but Redis never became ready (removed): %w", err)
	}
	cancel()
	steps = append(steps, "Server is accepting connections")

	result := ConnInfo{
		Driver:   "redis",
		Host:     "localhost",
		Port:     hostPort,
		Database: "0",
	}
	pr := ProvisionResult{Info: result, Steps: steps, Container: container}
	if password != "" {
		pr.PasswordHint = password
	}
	return pr, nil
}

func init() { Register(&redisDriver{}) }

type redisDriver struct{}

func (d *redisDriver) Name() string      { return "redis" }
func (d *redisDriver) Schemes() []string { return []string{"redis", "rediss"} }
func (d *redisDriver) DefaultPort() int  { return 6379 }

func (d *redisDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	var opts *redis.Options
	if info.URL != "" {
		var err error
		opts, err = redis.ParseURL(info.URL)
		if err != nil {
			return nil, fmt.Errorf("redis: parse URL: %w", err)
		}
	} else {
		host := info.Host
		if host == "" {
			host = "localhost"
		}
		port := info.Port
		if port == 0 {
			port = 6379
		}
		db := 0
		if info.Database != "" {
			if n, err := strconv.Atoi(info.Database); err == nil {
				db = n
			}
		}
		opts = &redis.Options{
			Addr:     fmt.Sprintf("%s:%d", host, port),
			Password: info.Password,
			DB:       db,
		}
		if info.User != "" {
			opts.Username = info.User
		}
	}

	client := redis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}
	return &redisConn{client: client}, nil
}

// ─── Conn ────────────────────────────────────────────────────────────────────

type redisConn struct {
	client *redis.Client
}

func (c *redisConn) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *redisConn) Close() error {
	return c.client.Close()
}

// Schemas returns the configured logical Redis databases (0 … N-1).
func (c *redisConn) Schemas(ctx context.Context) ([]Schema, error) {
	n := 16 // Redis default
	vals, err := c.client.ConfigGet(ctx, "databases").Result()
	if err == nil {
		if nStr, ok := vals["databases"]; ok {
			if parsed, err := strconv.Atoi(nStr); err == nil && parsed > 0 {
				n = parsed
			}
		}
	}
	out := make([]Schema, n)
	for i := range out {
		out[i] = Schema{Name: strconv.Itoa(i)}
	}
	return out, nil
}

// Objects returns key-prefix groups for the given database.
// schema is the DB index as a string ("0", "1", …).
func (c *redisConn) Objects(ctx context.Context, schema string) ([]DBObject, error) {
	dbIdx, err := strconv.Atoi(schema)
	if err != nil {
		dbIdx = 0
	}

	// Switch to the requested database.
	conn := c.client.Conn()
	defer conn.Close()
	if err := conn.Do(ctx, "SELECT", dbIdx).Err(); err != nil {
		return nil, fmt.Errorf("redis: SELECT %d: %w", dbIdx, err)
	}

	// Scan up to 2000 keys to build a prefix picture.
	const scanCount = 2000
	prefixes := map[string]bool{}
	var cursor uint64
	total := 0
	for {
		keys, next, err := conn.Scan(ctx, cursor, "*", 200).Result()
		if err != nil {
			break
		}
		for _, k := range keys {
			if total >= scanCount {
				break
			}
			total++
			if idx := strings.Index(k, ":"); idx > 0 {
				prefixes[k[:idx]+":*"] = true
			} else {
				prefixes[k] = true
			}
		}
		cursor = next
		if cursor == 0 || total >= scanCount {
			break
		}
	}

	names := make([]string, 0, len(prefixes))
	for p := range prefixes {
		names = append(names, p)
	}
	sort.Strings(names)

	out := make([]DBObject, len(names))
	for i, n := range names {
		out[i] = DBObject{Schema: schema, Name: n, Kind: KindTable}
	}
	return out, nil
}

// Columns returns metadata about a key pattern or specific key.
func (c *redisConn) Columns(ctx context.Context, schema, key string) ([]Column, error) {
	// Strip the ":*" wildcard suffix added by Objects if present.
	bare := strings.TrimSuffix(key, ":*")
	if bare == key {
		// It's a concrete key — describe it directly.
		return c.columnsMeta(ctx, key)
	}
	// It's a pattern — describe a representative member.
	conn := c.client.Conn()
	defer conn.Close()
	dbIdx, _ := strconv.Atoi(schema)
	conn.Do(ctx, "SELECT", dbIdx) //nolint:errcheck

	keys, _, _ := conn.Scan(ctx, 0, key, 1).Result()
	if len(keys) > 0 {
		return c.columnsMeta(ctx, keys[0])
	}
	// No keys found — return the schema columns anyway.
	return redisMetaColumns(), nil
}

func redisMetaColumns() []Column {
	return []Column{
		{Name: "key", Type: "string", Key: "PK"},
		{Name: "type", Type: "string"},
		{Name: "ttl", Type: "integer"},
		{Name: "encoding", Type: "string"},
		{Name: "size", Type: "integer"},
	}
}

func (c *redisConn) columnsMeta(ctx context.Context, key string) ([]Column, error) {
	// We don't run a SELECT here; assumes the caller connected to the right DB.
	_ = key
	return redisMetaColumns(), nil
}

// Definition returns the full value of a key (or a sample for patterns).
func (c *redisConn) Definition(ctx context.Context, obj DBObject) (string, error) {
	dbIdx, _ := strconv.Atoi(obj.Schema)
	conn := c.client.Conn()
	defer conn.Close()
	if err := conn.Do(ctx, "SELECT", dbIdx).Err(); err != nil {
		return "", fmt.Errorf("redis: SELECT: %w", err)
	}

	key := obj.Name
	// For patterns, pick one actual key.
	if strings.HasSuffix(key, ":*") {
		keys, _, _ := conn.Scan(ctx, 0, key, 1).Result()
		if len(keys) == 0 {
			return fmt.Sprintf("-- No keys matching %q", key), nil
		}
		key = keys[0]
	}

	keyType, err := conn.Type(ctx, key).Result()
	if err != nil {
		return "", fmt.Errorf("redis: TYPE %s: %w", key, err)
	}
	ttl, _ := conn.TTL(ctx, key).Result()
	enc, _ := conn.ObjectEncoding(ctx, key).Result()

	var sb strings.Builder
	fmt.Fprintf(&sb, "-- Key:      %s\n", key)
	fmt.Fprintf(&sb, "-- Type:     %s\n", keyType)
	fmt.Fprintf(&sb, "-- TTL:      %s\n", ttl)
	fmt.Fprintf(&sb, "-- Encoding: %s\n\n", enc)

	switch keyType {
	case "string":
		val, _ := conn.Get(ctx, key).Result()
		sb.WriteString(val)
	case "hash":
		fields, _ := conn.HGetAll(ctx, key).Result()
		keys2 := make([]string, 0, len(fields))
		for k := range fields {
			keys2 = append(keys2, k)
		}
		sort.Strings(keys2)
		for _, k := range keys2 {
			fmt.Fprintf(&sb, "%s: %s\n", k, fields[k])
		}
	case "list":
		vals, _ := conn.LRange(ctx, key, 0, 99).Result()
		for i, v := range vals {
			fmt.Fprintf(&sb, "[%d] %s\n", i, v)
		}
	case "set":
		vals, _ := conn.SMembers(ctx, key).Result()
		sort.Strings(vals)
		for _, v := range vals {
			fmt.Fprintf(&sb, "%s\n", v)
		}
	case "zset":
		vals, _ := conn.ZRangeWithScores(ctx, key, 0, 99).Result()
		for _, v := range vals {
			fmt.Fprintf(&sb, "%g  %s\n", v.Score, v.Member)
		}
	default:
		fmt.Fprintf(&sb, "(type %q — no preview available)", keyType)
	}
	return sb.String(), nil
}

// Query executes a raw Redis command and returns the result as a QueryResult.
func (c *redisConn) Query(ctx context.Context, query string) (QueryResult, error) {
	start := time.Now()
	args, err := splitRedisCommand(query)
	if err != nil || len(args) == 0 {
		return QueryResult{}, fmt.Errorf("redis: %w", err)
	}
	reply, err := c.client.Do(ctx, args...).Result()
	if err != nil {
		return QueryResult{}, fmt.Errorf("redis: %w", err)
	}
	return redisReplyToResult(reply, start), nil
}

// Exec executes a Redis command and returns an ExecResult.
func (c *redisConn) Exec(ctx context.Context, query string) (ExecResult, error) {
	start := time.Now()
	args, err := splitRedisCommand(query)
	if err != nil || len(args) == 0 {
		return ExecResult{}, fmt.Errorf("redis: %w", err)
	}
	reply, err := c.client.Do(ctx, args...).Result()
	if err != nil {
		return ExecResult{}, fmt.Errorf("redis: %w", err)
	}
	msg := fmt.Sprintf("%v", reply)
	var affected int64
	switch v := reply.(type) {
	case int64:
		affected = v
	}
	return ExecResult{RowsAffected: affected, Duration: time.Since(start), Message: msg}, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// splitRedisCommand splits a Redis command string into tokens, respecting
// double-quoted strings.
func splitRedisCommand(s string) ([]interface{}, error) {
	s = strings.TrimSpace(s)
	var tokens []string
	var current strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"' && !inQuote:
			inQuote = true
		case ch == '"' && inQuote:
			inQuote = false
		case ch == ' ' && !inQuote:
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	args := make([]interface{}, len(tokens))
	for i, t := range tokens {
		args[i] = t
	}
	return args, nil
}

// redisReplyToResult converts a Redis reply into a QueryResult table.
func redisReplyToResult(reply interface{}, start time.Time) QueryResult {
	dur := time.Since(start)
	switch v := reply.(type) {
	case nil:
		return QueryResult{Columns: []string{"result"}, Rows: [][]string{{"(nil)"}}, Nulls: [][]bool{{false}}, Duration: dur}

	case string:
		return QueryResult{Columns: []string{"result"}, Rows: [][]string{{v}}, Nulls: [][]bool{{false}}, Duration: dur}

	case int64:
		return QueryResult{Columns: []string{"result"}, Rows: [][]string{{strconv.FormatInt(v, 10)}}, Nulls: [][]bool{{false}}, Duration: dur}

	case []interface{}:
		// Could be a list or a flat interleaved hash (HGETALL).
		// Detect as hash if even length and all odd-indexed items look like field names.
		if len(v)%2 == 0 && len(v) > 0 {
			isPair := true
			for i := 0; i < len(v); i += 2 {
				if _, ok := v[i].(string); !ok {
					isPair = false
					break
				}
			}
			if isPair {
				rows := make([][]string, len(v)/2)
				nulls := make([][]bool, len(v)/2)
				for i := 0; i < len(v); i += 2 {
					field := fmt.Sprintf("%v", v[i])
					val := fmt.Sprintf("%v", v[i+1])
					rows[i/2] = []string{field, val}
					nulls[i/2] = []bool{false, false}
				}
				return QueryResult{Columns: []string{"field", "value"}, Rows: rows, Nulls: nulls, Duration: dur}
			}
		}
		// Simple list.
		rows := make([][]string, len(v))
		nulls := make([][]bool, len(v))
		for i, item := range v {
			rows[i] = []string{fmt.Sprintf("%v", item)}
			nulls[i] = []bool{false}
		}
		return QueryResult{Columns: []string{"value"}, Rows: rows, Nulls: nulls, Duration: dur}

	case map[interface{}]interface{}:
		keys := make([]string, 0, len(v))
		strs := map[string]string{}
		for k, val := range v {
			ks := fmt.Sprintf("%v", k)
			strs[ks] = fmt.Sprintf("%v", val)
			keys = append(keys, ks)
		}
		sort.Strings(keys)
		rows := make([][]string, len(keys))
		nulls := make([][]bool, len(keys))
		for i, k := range keys {
			rows[i] = []string{k, strs[k]}
			nulls[i] = []bool{false, false}
		}
		return QueryResult{Columns: []string{"field", "value"}, Rows: rows, Nulls: nulls, Duration: dur}

	default:
		s := fmt.Sprintf("%v", v)
		return QueryResult{Columns: []string{"result"}, Rows: [][]string{{s}}, Nulls: [][]bool{{false}}, Duration: dur}
	}
}
