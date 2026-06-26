package driver

// MongoDB driver for sqwee.
//
// Schema browser mapping:
//   Schemas  → databases listed on the server
//   Objects  → collections in a database (shown as KindTable)
//   Columns  → fields sampled from up to 20 documents
//   Definition → collStats output as JSON
//
// Query language: MongoDB shell-style
//   collection.find({filter})
//   collection.find({filter}, {projection})
//   collection.findOne({filter})
//   collection.aggregate([pipeline])
//   collection.countDocuments({filter})
//   db.collection.find(...)   ← explicit database prefix
//
// Exec language: same syntax for write operations
//   collection.insertOne({doc})
//   collection.insertMany([docs])
//   collection.updateOne({filter}, {update})
//   collection.updateMany({filter}, {update})
//   collection.deleteOne({filter})
//   collection.deleteMany({filter})

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	mongoopts "go.mongodb.org/mongo-driver/v2/mongo/options"
)

func init() { Register(&mongoDriver{}) }

type mongoDriver struct{}

func (d *mongoDriver) Name() string      { return "mongodb" }
func (d *mongoDriver) Schemes() []string { return []string{"mongodb", "mongodb+srv"} }
func (d *mongoDriver) DefaultPort() int  { return 27017 }

func (d *mongoDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	uri := info.URL
	if uri == "" {
		host := info.Host
		if host == "" {
			host = "localhost"
		}
		port := info.Port
		if port == 0 {
			port = 27017
		}
		if info.User != "" {
			uri = fmt.Sprintf("mongodb://%s:%s@%s:%d", info.User, info.Password, host, port)
		} else {
			uri = fmt.Sprintf("mongodb://%s:%d", host, port)
		}
		if info.Database != "" {
			uri += "/" + info.Database
		}
	}

	clientOpts := mongoopts.Client().ApplyURI(uri)
	client, err := mongo.Connect(clientOpts)
	if err != nil {
		return nil, fmt.Errorf("mongodb: connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		client.Disconnect(ctx) //nolint:errcheck
		return nil, fmt.Errorf("mongodb: ping: %w", err)
	}
	defaultDB := info.Database
	if defaultDB == "" {
		defaultDB = "test"
	}
	return &mongoConn{client: client, defaultDB: defaultDB}, nil
}

// ─── Conn ────────────────────────────────────────────────────────────────────

type mongoConn struct {
	client    *mongo.Client
	defaultDB string
}

func (c *mongoConn) Ping(ctx context.Context) error {
	return c.client.Ping(ctx, nil)
}

func (c *mongoConn) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.client.Disconnect(ctx)
}

func (c *mongoConn) Schemas(ctx context.Context) ([]Schema, error) {
	names, err := c.client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("mongodb: list databases: %w", err)
	}
	sort.Strings(names)
	out := make([]Schema, len(names))
	for i, n := range names {
		out[i] = Schema{Name: n}
	}
	return out, nil
}

func (c *mongoConn) Objects(ctx context.Context, schema string) ([]DBObject, error) {
	if schema == "" {
		schema = c.defaultDB
	}
	names, err := c.client.Database(schema).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("mongodb: list collections: %w", err)
	}
	sort.Strings(names)
	out := make([]DBObject, len(names))
	for i, n := range names {
		out[i] = DBObject{Schema: schema, Name: n, Kind: KindTable}
	}
	return out, nil
}

func (c *mongoConn) Columns(ctx context.Context, schema, table string) ([]Column, error) {
	if schema == "" {
		schema = c.defaultDB
	}
	coll := c.client.Database(schema).Collection(table)

	// Sample up to 20 documents to discover the field names present in the collection.
	cur, err := coll.Aggregate(ctx, bson.A{
		bson.D{{Key: "$sample", Value: bson.D{{Key: "size", Value: 20}}}},
	})
	if err != nil {
		// Collection might be empty or inaccessible — return minimal info.
		return []Column{{Name: "_id", Type: "ObjectID", Key: "PK"}}, nil
	}
	defer cur.Close(ctx)

	seen := map[string]bool{}
	var order []string
	for cur.Next(ctx) {
		var doc bson.M
		if err := cur.Decode(&doc); err != nil {
			continue
		}
		for k := range doc {
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	if len(order) == 0 {
		return []Column{{Name: "_id", Type: "ObjectID", Key: "PK"}}, nil
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i] == "_id" {
			return true
		}
		if order[j] == "_id" {
			return false
		}
		return order[i] < order[j]
	})

	cols := make([]Column, len(order))
	for i, k := range order {
		key := ""
		if k == "_id" {
			key = "PK"
		}
		cols[i] = Column{Name: k, Type: "any", Nullable: true, Key: key}
	}
	return cols, nil
}

func (c *mongoConn) Definition(ctx context.Context, obj DBObject) (string, error) {
	schema := obj.Schema
	if schema == "" {
		schema = c.defaultDB
	}
	var result bson.M
	err := c.client.Database(schema).RunCommand(ctx,
		bson.D{{Key: "collStats", Value: obj.Name}},
	).Decode(&result)
	if err != nil {
		return fmt.Sprintf("-- Collection: %s.%s\n-- (no stats available)", schema, obj.Name), nil
	}
	b, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Sprintf("-- Collection: %s.%s", schema, obj.Name), nil
	}
	return string(b), nil
}

func (c *mongoConn) Query(ctx context.Context, query string) (QueryResult, error) {
	start := time.Now()
	query = strings.TrimSpace(query)

	coll, op, argStr, schema, err := parseMongoShell(query, c.defaultDB)
	if err != nil {
		return QueryResult{}, err
	}
	collection := c.client.Database(schema).Collection(coll)

	switch strings.ToLower(op) {
	case "find":
		filter, projection, err := parseMongoFindArgs(argStr)
		if err != nil {
			return QueryResult{}, err
		}
		opts := mongoopts.Find().SetLimit(int64(maxRows + 1))
		if projection != nil {
			opts.SetProjection(projection)
		}
		cur, err := collection.Find(ctx, filter, opts)
		if err != nil {
			return QueryResult{}, fmt.Errorf("mongodb: find: %w", err)
		}
		defer cur.Close(ctx)
		var docs []bson.M
		if err := cur.All(ctx, &docs); err != nil {
			return QueryResult{}, err
		}
		return mongodocsToResult(docs, start), nil

	case "findone":
		filter, err := parseMongoDocArg(argStr)
		if err != nil {
			return QueryResult{}, err
		}
		var doc bson.M
		if err := collection.FindOne(ctx, filter).Decode(&doc); err != nil {
			if err == mongo.ErrNoDocuments {
				return QueryResult{Columns: []string{"(no results)"}, Duration: time.Since(start)}, nil
			}
			return QueryResult{}, fmt.Errorf("mongodb: findOne: %w", err)
		}
		return mongodocsToResult([]bson.M{doc}, start), nil

	case "aggregate":
		pipeline, err := parseMongoArrayArg(argStr)
		if err != nil {
			return QueryResult{}, err
		}
		cur, err := collection.Aggregate(ctx, pipeline)
		if err != nil {
			return QueryResult{}, fmt.Errorf("mongodb: aggregate: %w", err)
		}
		defer cur.Close(ctx)
		var docs []bson.M
		if err := cur.All(ctx, &docs); err != nil {
			return QueryResult{}, err
		}
		return mongodocsToResult(docs, start), nil

	case "countdocuments":
		filter, _ := parseMongoDocArg(argStr) // empty filter ok
		n, err := collection.CountDocuments(ctx, filter)
		if err != nil {
			return QueryResult{}, fmt.Errorf("mongodb: countDocuments: %w", err)
		}
		return QueryResult{
			Columns:  []string{"count"},
			Rows:     [][]string{{fmt.Sprintf("%d", n)}},
			Nulls:    [][]bool{{false}},
			Duration: time.Since(start),
		}, nil

	default:
		return QueryResult{}, fmt.Errorf(
			"mongodb: unknown read operation %q — use find, findOne, aggregate, countDocuments", op)
	}
}

func (c *mongoConn) Exec(ctx context.Context, query string) (ExecResult, error) {
	start := time.Now()
	query = strings.TrimSpace(query)

	coll, op, argStr, schema, err := parseMongoShell(query, c.defaultDB)
	if err != nil {
		return ExecResult{}, err
	}
	collection := c.client.Database(schema).Collection(coll)

	switch strings.ToLower(op) {
	case "insertone":
		doc, err := parseMongoDocArg(argStr)
		if err != nil {
			return ExecResult{}, err
		}
		_, err = collection.InsertOne(ctx, doc)
		if err != nil {
			return ExecResult{}, fmt.Errorf("mongodb: insertOne: %w", err)
		}
		return ExecResult{RowsAffected: 1, Duration: time.Since(start), Message: "insertOne: ok"}, nil

	case "insertmany":
		arr, err := parseMongoArrayArg(argStr)
		if err != nil {
			return ExecResult{}, err
		}
		docs := make([]interface{}, len(arr))
		for i, v := range arr {
			docs[i] = v
		}
		res, err := collection.InsertMany(ctx, docs)
		if err != nil {
			return ExecResult{}, fmt.Errorf("mongodb: insertMany: %w", err)
		}
		n := int64(len(res.InsertedIDs))
		return ExecResult{RowsAffected: n, Duration: time.Since(start),
			Message: fmt.Sprintf("insertMany: %d inserted", n)}, nil

	case "updateone":
		filter, update, err := parseMongoPairArgs(argStr)
		if err != nil {
			return ExecResult{}, err
		}
		res, err := collection.UpdateOne(ctx, filter, update)
		if err != nil {
			return ExecResult{}, fmt.Errorf("mongodb: updateOne: %w", err)
		}
		return ExecResult{RowsAffected: res.ModifiedCount, Duration: time.Since(start),
			Message: fmt.Sprintf("updateOne: matched=%d modified=%d", res.MatchedCount, res.ModifiedCount)}, nil

	case "updatemany":
		filter, update, err := parseMongoPairArgs(argStr)
		if err != nil {
			return ExecResult{}, err
		}
		res, err := collection.UpdateMany(ctx, filter, update)
		if err != nil {
			return ExecResult{}, fmt.Errorf("mongodb: updateMany: %w", err)
		}
		return ExecResult{RowsAffected: res.ModifiedCount, Duration: time.Since(start),
			Message: fmt.Sprintf("updateMany: matched=%d modified=%d", res.MatchedCount, res.ModifiedCount)}, nil

	case "deleteone":
		filter, err := parseMongoDocArg(argStr)
		if err != nil {
			return ExecResult{}, err
		}
		res, err := collection.DeleteOne(ctx, filter)
		if err != nil {
			return ExecResult{}, fmt.Errorf("mongodb: deleteOne: %w", err)
		}
		return ExecResult{RowsAffected: res.DeletedCount, Duration: time.Since(start),
			Message: fmt.Sprintf("deleteOne: %d deleted", res.DeletedCount)}, nil

	case "deletemany":
		filter, err := parseMongoDocArg(argStr)
		if err != nil {
			return ExecResult{}, err
		}
		res, err := collection.DeleteMany(ctx, filter)
		if err != nil {
			return ExecResult{}, fmt.Errorf("mongodb: deleteMany: %w", err)
		}
		return ExecResult{RowsAffected: res.DeletedCount, Duration: time.Since(start),
			Message: fmt.Sprintf("deleteMany: %d deleted", res.DeletedCount)}, nil

	default:
		// Fall through: try as a read Query and report the row count.
		qr, err := c.Query(ctx, query)
		if err != nil {
			return ExecResult{}, err
		}
		return ExecResult{
			RowsAffected: int64(len(qr.Rows)),
			Duration:     qr.Duration,
			Message:      fmt.Sprintf("%d documents", len(qr.Rows)),
		}, nil
	}
}

// ─── Shell-syntax parser ─────────────────────────────────────────────────────
//
// Accepts:
//   collection.operation(args)
//   database.collection.operation(args)

func parseMongoShell(q, defaultDB string) (coll, op, args, schema string, err error) {
	dotIdx := strings.Index(q, ".")
	if dotIdx < 0 {
		return "", "", "", "", fmt.Errorf(
			"mongodb: query must be in the form collection.operation({...})")
	}
	// Find the opening paren — it comes after the op name.
	parenIdx := strings.Index(q, "(")
	if parenIdx < 0 {
		return "", "", "", "", fmt.Errorf("mongodb: missing '(' in query")
	}
	if !strings.HasSuffix(strings.TrimSpace(q), ")") {
		return "", "", "", "", fmt.Errorf("mongodb: missing closing ')'")
	}

	// Everything before the first paren is "a.b" or "a.b.c".
	head := q[:parenIdx]
	args = q[parenIdx+1 : strings.LastIndex(q, ")")]

	parts := strings.SplitN(head, ".", 3)
	switch len(parts) {
	case 2:
		// collection.operation
		schema = defaultDB
		coll = parts[0]
		op = parts[1]
	case 3:
		// database.collection.operation
		schema = parts[0]
		coll = parts[1]
		op = parts[2]
	default:
		return "", "", "", "", fmt.Errorf("mongodb: cannot parse %q", head)
	}
	return coll, op, args, schema, nil
}

// parseMongoDocArg parses a single Extended-JSON object from a string.
// Returns an empty bson.M on blank/empty input.
func parseMongoDocArg(s string) (bson.M, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" {
		return bson.M{}, nil
	}
	var m bson.M
	if err := bson.UnmarshalExtJSON([]byte(s), false, &m); err != nil {
		return nil, fmt.Errorf("mongodb: invalid filter/document JSON: %w", err)
	}
	return m, nil
}

// parseMongoArrayArg parses a JSON array (for pipeline / insertMany).
func parseMongoArrayArg(s string) (bson.A, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "[]" {
		return bson.A{}, nil
	}
	var a bson.A
	if err := bson.UnmarshalExtJSON([]byte(s), false, &a); err != nil {
		return nil, fmt.Errorf("mongodb: invalid JSON array: %w", err)
	}
	return a, nil
}

// parseMongoFindArgs splits "find({filter})" or "find({filter},{projection})"
// argument strings into their two parts.
func parseMongoFindArgs(s string) (filter, projection bson.M, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return bson.M{}, nil, nil
	}
	// Find split point: end of the first top-level JSON object.
	split := topLevelObjectEnd(s)
	if split < 0 || split >= len(s) {
		f, err := parseMongoDocArg(s)
		return f, nil, err
	}
	filterStr := strings.TrimSpace(s[:split+1])
	rest := strings.TrimSpace(s[split+1:])
	rest = strings.TrimPrefix(rest, ",")
	rest = strings.TrimSpace(rest)

	filter, err = parseMongoDocArg(filterStr)
	if err != nil {
		return nil, nil, err
	}
	if rest != "" {
		projection, err = parseMongoDocArg(rest)
		if err != nil {
			return nil, nil, err
		}
	}
	return filter, projection, nil
}

// parseMongoPairArgs splits "update({filter},{update})" argument string.
func parseMongoPairArgs(s string) (filter, update bson.M, err error) {
	s = strings.TrimSpace(s)
	split := topLevelObjectEnd(s)
	if split < 0 {
		return nil, nil, fmt.Errorf("mongodb: expected two JSON objects separated by comma")
	}
	filterStr := strings.TrimSpace(s[:split+1])
	rest := strings.TrimSpace(s[split+1:])
	rest = strings.TrimPrefix(rest, ",")
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return nil, nil, fmt.Errorf("mongodb: update requires both a filter and an update document")
	}
	filter, err = parseMongoDocArg(filterStr)
	if err != nil {
		return nil, nil, err
	}
	update, err = parseMongoDocArg(rest)
	return filter, update, err
}

// topLevelObjectEnd returns the index of the closing '}' of the first
// top-level JSON object in s, or -1 if not found.
func topLevelObjectEnd(s string) int {
	depth := 0
	for i, ch := range s {
		switch ch {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// ─── Result builder ──────────────────────────────────────────────────────────

func mongodocsToResult(docs []bson.M, start time.Time) QueryResult {
	if len(docs) == 0 {
		return QueryResult{
			Columns:  []string{"(no results)"},
			Duration: time.Since(start),
		}
	}

	truncated := false
	if len(docs) > maxRows {
		docs = docs[:maxRows]
		truncated = true
	}

	// Collect all column names from every document.
	seen := map[string]bool{}
	var cols []string
	for _, doc := range docs {
		for k := range doc {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	sort.Slice(cols, func(i, j int) bool {
		if cols[i] == "_id" {
			return true
		}
		if cols[j] == "_id" {
			return false
		}
		return cols[i] < cols[j]
	})

	rows := make([][]string, len(docs))
	nulls := make([][]bool, len(docs))
	for i, doc := range docs {
		row := make([]string, len(cols))
		null := make([]bool, len(cols))
		for j, col := range cols {
			v, ok := doc[col]
			if !ok {
				null[j] = true
			} else {
				row[j] = mongoBSONToString(v)
			}
		}
		rows[i] = row
		nulls[i] = null
	}

	return QueryResult{
		Columns:   cols,
		Rows:      rows,
		Nulls:     nulls,
		Duration:  time.Since(start),
		Truncated: truncated,
	}
}

// ─── Provisioner ─────────────────────────────────────────────────────────────

func (d *mongoDriver) ProvisionModes() []ProvisionMode {
	return []ProvisionMode{
		{
			ID:    "docker",
			Label: "New Docker container (mongo:7)",
			Fields: []ProvisionField{
				{Key: "container", Label: "Container name", Default: ""},
				{Key: "password", Label: "Root password", Password: true},
				{Key: "port", Label: "Host port", Default: "27017"},
				{Key: "db_name", Label: "Database name", Default: "myapp", Placeholder: "myapp"},
			},
		},
		{
			ID:    "server",
			Label: "Existing MongoDB server",
			Fields: []ProvisionField{
				{Key: "host", Label: "Host", Default: "localhost"},
				{Key: "port", Label: "Port", Default: "27017"},
				{Key: "user", Label: "Admin user", Optional: true, Placeholder: "root"},
				{Key: "password", Label: "Admin password", Password: true, Optional: true},
				{Key: "db_name", Label: "Database name", Default: "myapp", Placeholder: "myapp"},
			},
		},
	}
}

func (d *mongoDriver) Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error) {
	switch mode {
	case "docker":
		return mongoProvisionDocker(ctx, values)
	case "server":
		return mongoProvisionServer(ctx, values)
	default:
		return ProvisionResult{}, fmt.Errorf("mongodb: unknown provision mode %q", mode)
	}
}

func mongoProvisionDocker(ctx context.Context, values map[string]string) (ProvisionResult, error) {
	if !dockerAvailable() {
		return ProvisionResult{}, fmt.Errorf("docker is not installed or the daemon is not running")
	}
	dbName := strings.TrimSpace(values["db_name"])
	if dbName == "" {
		dbName = "myapp"
	}
	name := strings.TrimSpace(values["container"])
	if name == "" {
		name = "sqwee-mongodb-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	password := values["password"]
	if password == "" {
		password = "Sqwee_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "9!"
	}
	hostPort := freeHostPort(values, 27017)

	spec := dockerSpec{
		Image: "mongo:7",
		Env: map[string]string{
			"MONGO_INITDB_ROOT_USERNAME": "sqwee",
			"MONGO_INITDB_ROOT_PASSWORD": password,
		},
		ContainerPort: 27017,
		HostPort:      hostPort,
		Name:          name,
	}

	var steps []string
	container, err := runDockerContainer(ctx, spec)
	if err != nil {
		return ProvisionResult{}, err
	}
	steps = append(steps, "Started Docker container "+container+" (mongo:7)")

	admin := ConnInfo{
		Driver:   "mongodb",
		Host:     "localhost",
		Port:     hostPort,
		User:     "sqwee",
		Password: password,
	}
	waitCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	if err := waitForServer(waitCtx, ByName("mongodb"), admin); err != nil {
		cancel()
		removeDockerContainer(container)
		return ProvisionResult{}, fmt.Errorf("container started but MongoDB never became ready (removed): %w", err)
	}
	cancel()
	steps = append(steps, "Server is accepting connections")
	steps = append(steps, fmt.Sprintf("Database %q will be created on first use", dbName))

	return ProvisionResult{
		Info: ConnInfo{
			Driver:   "mongodb",
			Host:     "localhost",
			Port:     hostPort,
			User:     "sqwee",
			Database: dbName,
		},
		Steps:        steps,
		Container:    container,
		PasswordHint: password,
	}, nil
}

func mongoProvisionServer(ctx context.Context, values map[string]string) (ProvisionResult, error) {
	dbName := strings.TrimSpace(values["db_name"])
	if dbName == "" {
		return ProvisionResult{}, fmt.Errorf("mongodb: a database name is required")
	}
	host := strings.TrimSpace(values["host"])
	if host == "" {
		host = "localhost"
	}
	port := 27017
	if p := strings.TrimSpace(values["port"]); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			port = n
		}
	}
	info := ConnInfo{
		Driver:   "mongodb",
		Host:     host,
		Port:     port,
		User:     strings.TrimSpace(values["user"]),
		Password: values["password"],
		Database: dbName,
	}

	// MongoDB creates databases lazily — just verify the connection.
	connCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	drv := ByName("mongodb")
	conn, err := drv.Connect(connCtx, info)
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("mongodb: cannot reach server: %w", err)
	}
	conn.Close()

	result := info
	result.Password = ""
	return ProvisionResult{
		Info:  result,
		Steps: []string{fmt.Sprintf("Confirmed connection to %s:%d — database %q will be created on first use", host, port, dbName)},
	}, nil
}

func mongoBSONToString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case int32:
		return fmt.Sprintf("%d", t)
	case int64:
		return fmt.Sprintf("%d", t)
	case float64:
		return fmt.Sprintf("%g", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		// For nested documents, arrays, ObjectIDs, dates, etc., render as JSON.
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}
