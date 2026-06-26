package driver

// DynamoDB driver for sqwee.
//
// Schema browser mapping:
//   Schemas    → single entry "default" (DynamoDB has no namespaces)
//   Objects    → DynamoDB tables (KindTable)
//   Columns    → partition key, sort key, and all GSI/LSI key attributes
//   Definition → DescribeTable output as formatted JSON
//
// Connection:
//   Credentials come from ConnInfo.Options:
//     aws_region            (required, or AWS_REGION / AWS_DEFAULT_REGION env var)
//     aws_access_key_id     (optional; falls back to env / ~/.aws/credentials)
//     aws_secret_access_key (optional; same fallback)
//     aws_session_token     (optional)
//     endpoint_url          (optional; use for DynamoDB Local, e.g. http://localhost:8000)
//
//   URL scheme  "dynamodb://region[/]" also accepted:
//     dynamodb://us-east-1
//     dynamodb://localhost:8000  → treated as a local endpoint
//
// Query / Exec language (JSON body):
//   {"Operation":"Scan",  "TableName":"users","Limit":50}
//   {"Operation":"Query", "TableName":"orders","KeyConditionExpression":"pk = :pk",
//    "ExpressionAttributeValues":{":pk":{"S":"user#1"}}}
//   {"Operation":"GetItem","TableName":"users","Key":{"id":{"S":"abc"}}}
//   {"Operation":"PutItem","TableName":"users","Item":{"id":{"S":"abc"},"name":{"S":"Alice"}}}
//   {"Operation":"UpdateItem","TableName":"users","Key":{"id":{"S":"abc"}},
//    "UpdateExpression":"SET #n = :n","ExpressionAttributeNames":{"#n":"name"},
//    "ExpressionAttributeValues":{":n":{"S":"Bob"}}}
//   {"Operation":"DeleteItem","TableName":"users","Key":{"id":{"S":"abc"}}}

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func init() { Register(&dynamoDriver{}) }

type dynamoDriver struct{}

func (d *dynamoDriver) Name() string      { return "dynamodb" }
func (d *dynamoDriver) Schemes() []string { return []string{"dynamodb"} }
func (d *dynamoDriver) DefaultPort() int  { return 0 }

func (d *dynamoDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	opts := info.Options
	if opts == nil {
		opts = map[string]string{}
	}

	// Resolve region.
	region := opts["aws_region"]
	if region == "" {
		// Try to parse from URL: "dynamodb://us-east-1"
		if info.URL != "" {
			after := strings.TrimPrefix(info.URL, "dynamodb://")
			after = strings.TrimSuffix(after, "/")
			if after != "" && !strings.Contains(after, ":") {
				region = after
			}
		}
	}
	if region == "" {
		region = "us-east-1" // will be overridden by env var if set
	}

	// Build the AWS config.
	var cfgOpts []func(*config.LoadOptions) error
	cfgOpts = append(cfgOpts, config.WithRegion(region))

	// Explicit credentials take precedence over env / profile.
	accessKey := opts["aws_access_key_id"]
	secretKey := opts["aws_secret_access_key"]
	sessionToken := opts["aws_session_token"]
	if accessKey != "" && secretKey != "" {
		cfgOpts = append(cfgOpts,
			config.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(accessKey, secretKey, sessionToken),
			),
		)
	}

	cfg, err := config.LoadDefaultConfig(ctx, cfgOpts...)
	if err != nil {
		return nil, fmt.Errorf("dynamodb: load AWS config: %w", err)
	}

	// Build the DynamoDB client.
	var dynamoOpts []func(*dynamodb.Options)

	// Custom endpoint for DynamoDB Local.
	endpoint := opts["endpoint_url"]
	if endpoint == "" && info.URL != "" {
		// "dynamodb://localhost:8000" style
		after := strings.TrimPrefix(info.URL, "dynamodb://")
		if strings.Contains(after, ":") { // has port → local endpoint
			endpoint = "http://" + strings.TrimSuffix(after, "/")
		}
	}
	if endpoint != "" {
		dynamoOpts = append(dynamoOpts, func(o *dynamodb.Options) {
			o.BaseEndpoint = aws.String(endpoint)
		})
	}

	client := dynamodb.NewFromConfig(cfg, dynamoOpts...)

	// Smoke-test: list tables (page size 1) to verify connectivity.
	_, err = client.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(1)})
	if err != nil {
		return nil, fmt.Errorf("dynamodb: connectivity check failed: %w", err)
	}

	return &dynamoConn{client: client}, nil
}

// ─── Conn ────────────────────────────────────────────────────────────────────

type dynamoConn struct {
	client *dynamodb.Client
}

func (c *dynamoConn) Ping(ctx context.Context) error {
	_, err := c.client.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(1)})
	return err
}

func (c *dynamoConn) Close() error { return nil }

func (c *dynamoConn) Schemas(_ context.Context) ([]Schema, error) {
	return []Schema{{Name: "default"}}, nil
}

func (c *dynamoConn) Objects(ctx context.Context, _ string) ([]DBObject, error) {
	var tables []string
	var lastKey *string
	for {
		out, err := c.client.ListTables(ctx, &dynamodb.ListTablesInput{
			ExclusiveStartTableName: lastKey,
		})
		if err != nil {
			return nil, fmt.Errorf("dynamodb: list tables: %w", err)
		}
		tables = append(tables, out.TableNames...)
		if out.LastEvaluatedTableName == nil {
			break
		}
		lastKey = out.LastEvaluatedTableName
	}
	sort.Strings(tables)
	out := make([]DBObject, len(tables))
	for i, t := range tables {
		out[i] = DBObject{Schema: "default", Name: t, Kind: KindTable}
	}
	return out, nil
}

func (c *dynamoConn) Columns(ctx context.Context, _, table string) ([]Column, error) {
	out, err := c.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(table),
	})
	if err != nil {
		return nil, fmt.Errorf("dynamodb: describe table %s: %w", table, err)
	}

	// Build a map of attribute names to key roles.
	keyRoles := map[string]string{}
	for _, k := range out.Table.KeySchema {
		switch k.KeyType {
		case types.KeyTypeHash:
			keyRoles[aws.ToString(k.AttributeName)] = "PK"
		case types.KeyTypeRange:
			keyRoles[aws.ToString(k.AttributeName)] = "SK"
		}
	}
	// GSI / LSI key attributes.
	for _, gsi := range out.Table.GlobalSecondaryIndexes {
		for _, k := range gsi.KeySchema {
			name := aws.ToString(k.AttributeName)
			if _, exists := keyRoles[name]; !exists {
				keyRoles[name] = "UNI"
			}
		}
	}

	var cols []Column
	for _, attr := range out.Table.AttributeDefinitions {
		name := aws.ToString(attr.AttributeName)
		typ := string(attr.AttributeType)
		cols = append(cols, Column{
			Name: name,
			Type: dynamoAttrType(typ),
			Key:  keyRoles[name],
		})
	}
	// Sort: PK first, then SK, then others.
	sort.Slice(cols, func(i, j int) bool {
		order := map[string]int{"PK": 0, "SK": 1, "UNI": 2, "": 3}
		if cols[i].Key != cols[j].Key {
			return order[cols[i].Key] < order[cols[j].Key]
		}
		return cols[i].Name < cols[j].Name
	})
	return cols, nil
}

func dynamoAttrType(t string) string {
	switch t {
	case "S":
		return "String"
	case "N":
		return "Number"
	case "B":
		return "Binary"
	default:
		return t
	}
}

func (c *dynamoConn) Definition(ctx context.Context, obj DBObject) (string, error) {
	out, err := c.client.DescribeTable(ctx, &dynamodb.DescribeTableInput{
		TableName: aws.String(obj.Name),
	})
	if err != nil {
		return "", fmt.Errorf("dynamodb: describe table %s: %w", obj.Name, err)
	}
	b, err := json.MarshalIndent(out.Table, "", "  ")
	if err != nil {
		return fmt.Sprintf("-- Table: %s", obj.Name), nil
	}
	return string(b), nil
}

// ─── Query / Exec ────────────────────────────────────────────────────────────

// dynamoOp is the JSON envelope callers send to Query/Exec.
type dynamoOp struct {
	Operation string `json:"Operation"`
	// All remaining fields are passed verbatim to the SDK input structs.
}

func (c *dynamoConn) Query(ctx context.Context, query string) (QueryResult, error) {
	start := time.Now()
	query = strings.TrimSpace(query)

	var op dynamoOp
	if err := json.Unmarshal([]byte(query), &op); err != nil {
		return QueryResult{}, fmt.Errorf("dynamodb: query must be a JSON object with an \"Operation\" field: %w", err)
	}

	switch strings.ToLower(op.Operation) {
	case "scan":
		var in dynamodb.ScanInput
		if err := json.Unmarshal([]byte(query), &in); err != nil {
			return QueryResult{}, fmt.Errorf("dynamodb: parse Scan input: %w", err)
		}
		out, err := c.client.Scan(ctx, &in)
		if err != nil {
			return QueryResult{}, fmt.Errorf("dynamodb: Scan: %w", err)
		}
		return dynamoItemsToResult(out.Items, start, out.LastEvaluatedKey != nil), nil

	case "query":
		var in dynamodb.QueryInput
		if err := json.Unmarshal([]byte(query), &in); err != nil {
			return QueryResult{}, fmt.Errorf("dynamodb: parse Query input: %w", err)
		}
		out, err := c.client.Query(ctx, &in)
		if err != nil {
			return QueryResult{}, fmt.Errorf("dynamodb: Query: %w", err)
		}
		return dynamoItemsToResult(out.Items, start, out.LastEvaluatedKey != nil), nil

	case "getitem":
		var in dynamodb.GetItemInput
		if err := json.Unmarshal([]byte(query), &in); err != nil {
			return QueryResult{}, fmt.Errorf("dynamodb: parse GetItem input: %w", err)
		}
		out, err := c.client.GetItem(ctx, &in)
		if err != nil {
			return QueryResult{}, fmt.Errorf("dynamodb: GetItem: %w", err)
		}
		if out.Item == nil {
			return QueryResult{Columns: []string{"(no results)"}, Duration: time.Since(start)}, nil
		}
		return dynamoItemsToResult([]map[string]types.AttributeValue{out.Item}, start, false), nil

	default:
		return QueryResult{}, fmt.Errorf(
			"dynamodb: unknown read Operation %q — use Scan, Query, or GetItem", op.Operation)
	}
}

func (c *dynamoConn) Exec(ctx context.Context, query string) (ExecResult, error) {
	start := time.Now()
	query = strings.TrimSpace(query)

	var op dynamoOp
	if err := json.Unmarshal([]byte(query), &op); err != nil {
		return ExecResult{}, fmt.Errorf("dynamodb: exec must be a JSON object with an \"Operation\" field: %w", err)
	}

	switch strings.ToLower(op.Operation) {
	case "putitem":
		var in dynamodb.PutItemInput
		if err := json.Unmarshal([]byte(query), &in); err != nil {
			return ExecResult{}, fmt.Errorf("dynamodb: parse PutItem input: %w", err)
		}
		_, err := c.client.PutItem(ctx, &in)
		if err != nil {
			return ExecResult{}, fmt.Errorf("dynamodb: PutItem: %w", err)
		}
		return ExecResult{RowsAffected: 1, Duration: time.Since(start), Message: "PutItem: ok"}, nil

	case "updateitem":
		var in dynamodb.UpdateItemInput
		if err := json.Unmarshal([]byte(query), &in); err != nil {
			return ExecResult{}, fmt.Errorf("dynamodb: parse UpdateItem input: %w", err)
		}
		_, err := c.client.UpdateItem(ctx, &in)
		if err != nil {
			return ExecResult{}, fmt.Errorf("dynamodb: UpdateItem: %w", err)
		}
		return ExecResult{RowsAffected: 1, Duration: time.Since(start), Message: "UpdateItem: ok"}, nil

	case "deleteitem":
		var in dynamodb.DeleteItemInput
		if err := json.Unmarshal([]byte(query), &in); err != nil {
			return ExecResult{}, fmt.Errorf("dynamodb: parse DeleteItem input: %w", err)
		}
		_, err := c.client.DeleteItem(ctx, &in)
		if err != nil {
			return ExecResult{}, fmt.Errorf("dynamodb: DeleteItem: %w", err)
		}
		return ExecResult{RowsAffected: 1, Duration: time.Since(start), Message: "DeleteItem: ok"}, nil

	default:
		// Try as a read query.
		qr, err := c.Query(ctx, query)
		if err != nil {
			return ExecResult{}, err
		}
		return ExecResult{
			RowsAffected: int64(len(qr.Rows)),
			Duration:     qr.Duration,
			Message:      fmt.Sprintf("%d items", len(qr.Rows)),
		}, nil
	}
}

// ─── DynamoDB result helpers ─────────────────────────────────────────────────

// ─── Provisioner ─────────────────────────────────────────────────────────────

func (d *dynamoDriver) ProvisionModes() []ProvisionMode {
	return []ProvisionMode{
		{
			ID:    "docker",
			Label: "New Docker container (DynamoDB Local)",
			Fields: []ProvisionField{
				{Key: "container", Label: "Container name", Default: ""},
				{Key: "port", Label: "Host port", Default: "8000"},
			},
		},
	}
}

func (d *dynamoDriver) Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error) {
	if mode != "docker" {
		return ProvisionResult{}, fmt.Errorf("dynamodb: unknown provision mode %q", mode)
	}

	if !dockerAvailable() {
		return ProvisionResult{}, fmt.Errorf("docker is not installed or the daemon is not running")
	}

	name := strings.TrimSpace(values["container"])
	if name == "" {
		name = "sqwee-dynamodb-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	hostPort := freeHostPort(values, 8000)

	spec := dockerSpec{
		Image:         "amazon/dynamodb-local",
		Env:           map[string]string{},
		ContainerPort: 8000,
		HostPort:      hostPort,
		Name:          name,
	}

	var steps []string
	container, err := runDockerContainer(ctx, spec)
	if err != nil {
		return ProvisionResult{}, err
	}
	steps = append(steps, "Started Docker container "+container+" (amazon/dynamodb-local)")

	// DynamoDB Local accepts any credentials — use static dummies.
	localInfo := ConnInfo{
		Driver: "dynamodb",
		Options: map[string]string{
			"aws_region":            "us-east-1",
			"aws_access_key_id":     "local",
			"aws_secret_access_key": "local",
			"endpoint_url":          fmt.Sprintf("http://localhost:%d", hostPort),
		},
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if err := waitForServer(waitCtx, d, localInfo); err != nil {
		cancel()
		removeDockerContainer(container)
		return ProvisionResult{}, fmt.Errorf("container started but DynamoDB Local never became ready (removed): %w", err)
	}
	cancel()
	steps = append(steps, "DynamoDB Local is accepting connections")

	return ProvisionResult{
		Info:      localInfo,
		Steps:     steps,
		Container: container,
	}, nil
}

// dynamoItemsToResult converts DynamoDB items (map[string]AttributeValue) to
// a flat QueryResult table with one column per attribute.
func dynamoItemsToResult(items []map[string]types.AttributeValue, start time.Time, truncated bool) QueryResult {
	if len(items) == 0 {
		return QueryResult{Columns: []string{"(no results)"}, Duration: time.Since(start)}
	}

	// Collect all attribute names.
	seen := map[string]bool{}
	var cols []string
	for _, item := range items {
		for k := range item {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}
	sort.Strings(cols)

	rows := make([][]string, len(items))
	nulls := make([][]bool, len(items))
	for i, item := range items {
		row := make([]string, len(cols))
		null := make([]bool, len(cols))
		for j, col := range cols {
			av, ok := item[col]
			if !ok {
				null[j] = true
			} else {
				row[j] = dynamoAVToString(av)
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

// dynamoAVToString renders a DynamoDB AttributeValue as a compact string.
func dynamoAVToString(av types.AttributeValue) string {
	switch v := av.(type) {
	case *types.AttributeValueMemberS:
		return v.Value
	case *types.AttributeValueMemberN:
		return v.Value
	case *types.AttributeValueMemberBOOL:
		if v.Value {
			return "true"
		}
		return "false"
	case *types.AttributeValueMemberNULL:
		return ""
	case *types.AttributeValueMemberB:
		return fmt.Sprintf("<binary %d bytes>", len(v.Value))
	case *types.AttributeValueMemberSS:
		return "[" + strings.Join(v.Value, ", ") + "]"
	case *types.AttributeValueMemberNS:
		return "[" + strings.Join(v.Value, ", ") + "]"
	case *types.AttributeValueMemberL:
		parts := make([]string, len(v.Value))
		for i, item := range v.Value {
			parts[i] = dynamoAVToString(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *types.AttributeValueMemberM:
		keys := make([]string, 0, len(v.Value))
		for k := range v.Value {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ": " + dynamoAVToString(v.Value[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	default:
		b, err := json.Marshal(av)
		if err != nil {
			return fmt.Sprintf("%v", av)
		}
		return string(b)
	}
}
