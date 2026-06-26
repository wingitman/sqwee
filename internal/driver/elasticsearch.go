package driver

// Elasticsearch driver for sqwee.
//
// Schema browser mapping:
//   Schemas    → single entry "default" (Elasticsearch has no schema nesting)
//   Objects    → indices (KindTable); system indices (starting with ".") are hidden
//   Columns    → top-level field mappings from the index mapping
//   Definition → index mapping + settings as formatted JSON
//
// Connection:
//   URL scheme "elasticsearch" or "es":
//     elasticsearch://localhost:9200
//     elasticsearch://user:pass@my-cluster.es.io:9243
//   info.Options:
//     api_key        → Elastic API key authentication
//     cloud_id       → Elastic Cloud ID (takes precedence over URL/host)
//
// Query language (sent to Query()):
//   <index> <json-dsl-body>
//     e.g.  users {"query":{"match_all":{}}}
//     e.g.  logs  {"query":{"range":{"@timestamp":{"gte":"now-1h"}}}}
//
//   Omit the index to search all indices:
//     {"query":{"match":{"message":"error"}}}
//
// Exec language:
//   index <index_name> <json-document>   → index a document
//   delete <index_name> <doc_id>         → delete a document
//   create_index <index_name> [<settings-json>]
//   delete_index <index_name>
//   refresh [<index_name>]

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

func init() { Register(&esDriver{}) }

type esDriver struct{}

func (d *esDriver) Name() string      { return "elasticsearch" }
func (d *esDriver) Schemes() []string { return []string{"elasticsearch", "es"} }
func (d *esDriver) DefaultPort() int  { return 9200 }

func (d *esDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	opts := info.Options
	if opts == nil {
		opts = map[string]string{}
	}

	cfg := elasticsearch.Config{}

	// Elastic Cloud.
	if cloud := opts["cloud_id"]; cloud != "" {
		cfg.CloudID = cloud
	}

	// API key.
	if key := opts["api_key"]; key != "" {
		cfg.APIKey = key
	}

	// Username / password.
	if info.User != "" {
		cfg.Username = info.User
		cfg.Password = info.Password
	}

	// Address list.
	if cfg.CloudID == "" {
		addr := info.URL
		if addr == "" {
			host := info.Host
			if host == "" {
				host = "localhost"
			}
			port := info.Port
			if port == 0 {
				port = 9200
			}
			scheme := "http"
			if opts["tls"] == "true" {
				scheme = "https"
			}
			addr = fmt.Sprintf("%s://%s:%d", scheme, host, port)
		} else {
			// Normalise scheme: "elasticsearch://" → "http://"
			addr = strings.Replace(addr, "elasticsearch://", "http://", 1)
			addr = strings.Replace(addr, "es://", "http://", 1)
		}
		cfg.Addresses = []string{addr}
	}

	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: create client: %w", err)
	}

	// Ping.
	res, err := client.Ping(client.Ping.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: ping: %w", err)
	}
	res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch: ping returned %s", res.Status())
	}

	return &esConn{client: client}, nil
}

// ─── Conn ────────────────────────────────────────────────────────────────────

type esConn struct {
	client *elasticsearch.Client
}

func (c *esConn) Ping(ctx context.Context) error {
	res, err := c.client.Ping(c.client.Ping.WithContext(ctx))
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.IsError() {
		return fmt.Errorf("elasticsearch: ping %s", res.Status())
	}
	return nil
}

func (c *esConn) Close() error { return nil }

func (c *esConn) Schemas(_ context.Context) ([]Schema, error) {
	return []Schema{{Name: "default"}}, nil
}

func (c *esConn) Objects(ctx context.Context, _ string) ([]DBObject, error) {
	res, err := c.client.Cat.Indices(
		c.client.Cat.Indices.WithContext(ctx),
		c.client.Cat.Indices.WithH("index"),
		c.client.Cat.Indices.WithFormat("json"),
	)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: cat indices: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch: cat indices: %s", res.Status())
	}

	var entries []struct {
		Index string `json:"index"`
	}
	if err := json.NewDecoder(res.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("elasticsearch: decode indices: %w", err)
	}

	var out []DBObject
	for _, e := range entries {
		if strings.HasPrefix(e.Index, ".") {
			continue // skip system indices
		}
		out = append(out, DBObject{Schema: "default", Name: e.Index, Kind: KindTable})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (c *esConn) Columns(ctx context.Context, _, index string) ([]Column, error) {
	res, err := c.client.Indices.GetMapping(
		c.client.Indices.GetMapping.WithContext(ctx),
		c.client.Indices.GetMapping.WithIndex(index),
	)
	if err != nil {
		return nil, fmt.Errorf("elasticsearch: get mapping: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch: get mapping: %s", res.Status())
	}

	// Response: { "<index>": { "mappings": { "properties": { "field": { "type": "..." } } } } }
	var resp map[string]struct {
		Mappings struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"mappings"`
	}
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("elasticsearch: decode mapping: %w", err)
	}

	var cols []Column
	for idx, m := range resp {
		_ = idx
		for fieldName, raw := range m.Mappings.Properties {
			var fieldMeta struct {
				Type string `json:"type"`
			}
			json.Unmarshal(raw, &fieldMeta) //nolint:errcheck
			typ := fieldMeta.Type
			if typ == "" {
				typ = "object"
			}
			cols = append(cols, Column{Name: fieldName, Type: typ, Nullable: true})
		}
		break // only the first (and only) matching index
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].Name < cols[j].Name })
	return cols, nil
}

func (c *esConn) Definition(ctx context.Context, obj DBObject) (string, error) {
	// Fetch mapping.
	resM, err := c.client.Indices.GetMapping(
		c.client.Indices.GetMapping.WithContext(ctx),
		c.client.Indices.GetMapping.WithIndex(obj.Name),
	)
	if err != nil {
		return "", fmt.Errorf("elasticsearch: get mapping: %w", err)
	}
	defer resM.Body.Close()
	mappingBody, _ := io.ReadAll(resM.Body)

	// Fetch settings.
	resS, err := c.client.Indices.GetSettings(
		c.client.Indices.GetSettings.WithContext(ctx),
		c.client.Indices.GetSettings.WithIndex(obj.Name),
	)
	if err != nil {
		return prettyJSON(mappingBody), nil
	}
	defer resS.Body.Close()
	settingsBody, _ := io.ReadAll(resS.Body)

	// Merge into one JSON object.
	combined := map[string]json.RawMessage{
		"mappings": json.RawMessage(mappingBody),
		"settings": json.RawMessage(settingsBody),
	}
	b, err := json.MarshalIndent(combined, "", "  ")
	if err != nil {
		return prettyJSON(mappingBody), nil
	}
	return string(b), nil
}

// ─── Query / Exec ─────────────────────────────────────────────────────────────

// Query runs an Elasticsearch search.
// Syntax: [index] {dsl-json}
func (c *esConn) Query(ctx context.Context, query string) (QueryResult, error) {
	start := time.Now()
	query = strings.TrimSpace(query)

	index, body := splitESQuery(query)

	opts := []func(*esapi.SearchRequest){
		c.client.Search.WithContext(ctx),
		c.client.Search.WithBody(strings.NewReader(body)),
	}
	if index != "" {
		opts = append(opts, c.client.Search.WithIndex(index))
	}

	res, err := c.client.Search(opts...)
	if err != nil {
		return QueryResult{}, fmt.Errorf("elasticsearch: search: %w", err)
	}
	defer res.Body.Close()

	rawBody, _ := io.ReadAll(res.Body)
	if res.IsError() {
		return QueryResult{}, fmt.Errorf("elasticsearch: search %s: %s", res.Status(), string(rawBody))
	}

	return esResponseToResult(rawBody, start)
}

// Exec handles index/delete/admin commands.
func (c *esConn) Exec(ctx context.Context, query string) (ExecResult, error) {
	start := time.Now()
	query = strings.TrimSpace(query)

	tokens := strings.SplitN(query, " ", 3)
	cmd := strings.ToLower(tokens[0])

	switch cmd {
	case "index":
		if len(tokens) < 3 {
			return ExecResult{}, fmt.Errorf("elasticsearch: usage: index <index_name> <json-doc>")
		}
		res, err := c.client.Index(tokens[1],
			strings.NewReader(tokens[2]),
			c.client.Index.WithContext(ctx),
		)
		if err != nil {
			return ExecResult{}, fmt.Errorf("elasticsearch: index: %w", err)
		}
		res.Body.Close()
		if res.IsError() {
			return ExecResult{}, fmt.Errorf("elasticsearch: index %s", res.Status())
		}
		return ExecResult{RowsAffected: 1, Duration: time.Since(start), Message: "indexed: ok"}, nil

	case "delete":
		if len(tokens) < 3 {
			return ExecResult{}, fmt.Errorf("elasticsearch: usage: delete <index_name> <doc_id>")
		}
		res, err := c.client.Delete(tokens[1], tokens[2],
			c.client.Delete.WithContext(ctx),
		)
		if err != nil {
			return ExecResult{}, fmt.Errorf("elasticsearch: delete: %w", err)
		}
		res.Body.Close()
		if res.IsError() {
			return ExecResult{}, fmt.Errorf("elasticsearch: delete %s", res.Status())
		}
		return ExecResult{RowsAffected: 1, Duration: time.Since(start), Message: "deleted: ok"}, nil

	case "create_index":
		if len(tokens) < 2 {
			return ExecResult{}, fmt.Errorf("elasticsearch: usage: create_index <name> [<settings>]")
		}
		var body io.Reader
		if len(tokens) == 3 {
			body = strings.NewReader(tokens[2])
		}
		var opts []func(*esapi.IndicesCreateRequest)
		opts = append(opts, c.client.Indices.Create.WithContext(ctx))
		if body != nil {
			opts = append(opts, c.client.Indices.Create.WithBody(body))
		}
		res, err := c.client.Indices.Create(tokens[1], opts...)
		if err != nil {
			return ExecResult{}, fmt.Errorf("elasticsearch: create index: %w", err)
		}
		res.Body.Close()
		if res.IsError() {
			return ExecResult{}, fmt.Errorf("elasticsearch: create index %s", res.Status())
		}
		return ExecResult{Duration: time.Since(start), Message: "index created: " + tokens[1]}, nil

	case "delete_index":
		if len(tokens) < 2 {
			return ExecResult{}, fmt.Errorf("elasticsearch: usage: delete_index <name>")
		}
		res, err := c.client.Indices.Delete([]string{tokens[1]},
			c.client.Indices.Delete.WithContext(ctx),
		)
		if err != nil {
			return ExecResult{}, fmt.Errorf("elasticsearch: delete index: %w", err)
		}
		res.Body.Close()
		if res.IsError() {
			return ExecResult{}, fmt.Errorf("elasticsearch: delete index %s", res.Status())
		}
		return ExecResult{Duration: time.Since(start), Message: "index deleted: " + tokens[1]}, nil

	case "refresh":
		var opts []func(*esapi.IndicesRefreshRequest)
		opts = append(opts, c.client.Indices.Refresh.WithContext(ctx))
		if len(tokens) >= 2 {
			opts = append(opts, c.client.Indices.Refresh.WithIndex(tokens[1]))
		}
		res, err := c.client.Indices.Refresh(opts...)
		if err != nil {
			return ExecResult{}, fmt.Errorf("elasticsearch: refresh: %w", err)
		}
		res.Body.Close()
		return ExecResult{Duration: time.Since(start), Message: "refreshed"}, nil

	default:
		// Fall through to Query.
		qr, err := c.Query(ctx, query)
		if err != nil {
			return ExecResult{}, err
		}
		return ExecResult{RowsAffected: int64(len(qr.Rows)), Duration: qr.Duration,
			Message: fmt.Sprintf("%d hits", len(qr.Rows))}, nil
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// splitESQuery splits an ES query string into an optional index name and the
// JSON DSL body.  If the first non-whitespace token is not "{", it is treated
// as the index name; everything after it is the body.
func splitESQuery(s string) (index, body string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		return "", s
	}
	idx := strings.IndexAny(s, " \t\n")
	if idx < 0 {
		return s, "{}"
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
}

// esResponseToResult parses an Elasticsearch search response and returns a
// flat QueryResult where each row is a _source document.
func esResponseToResult(body []byte, start time.Time) (QueryResult, error) {
	var resp struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				Index  string          `json:"_index"`
				ID     string          `json:"_id"`
				Score  float64         `json:"_score"`
				Source json.RawMessage `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		// Return raw body as a single cell.
		pretty := prettyJSON(body)
		return QueryResult{
			Columns: []string{"response"},
			Rows:    [][]string{{pretty}},
			Nulls:   [][]bool{{false}},
		}, nil
	}

	if len(resp.Hits.Hits) == 0 {
		return QueryResult{
			Columns:  []string{"(no results)"},
			Duration: time.Since(start),
		}, nil
	}

	// Collect all source field names.
	seen := map[string]bool{}
	var cols []string
	type rowDoc struct {
		meta   map[string]string
		source map[string]interface{}
	}
	docs := make([]rowDoc, len(resp.Hits.Hits))

	for i, hit := range resp.Hits.Hits {
		var src map[string]interface{}
		json.Unmarshal(hit.Source, &src) //nolint:errcheck
		if src == nil {
			src = map[string]interface{}{}
		}
		docs[i] = rowDoc{
			meta: map[string]string{
				"_index": hit.Index,
				"_id":    hit.ID,
				"_score": fmt.Sprintf("%g", hit.Score),
			},
			source: src,
		}
		for k := range src {
			if !seen[k] {
				seen[k] = true
				cols = append(cols, k)
			}
		}
	}

	// Prepend meta columns.
	metaCols := []string{"_index", "_id", "_score"}
	allCols := append(metaCols, cols...)
	sort.Strings(allCols[len(metaCols):])

	truncated := len(resp.Hits.Hits) > maxRows

	rows := make([][]string, len(docs))
	nulls := make([][]bool, len(docs))
	for i, doc := range docs {
		row := make([]string, len(allCols))
		null := make([]bool, len(allCols))
		for j, col := range allCols {
			if v, ok := doc.meta[col]; ok {
				row[j] = v
			} else if v, ok := doc.source[col]; ok {
				row[j] = esValueToString(v)
			} else {
				null[j] = true
			}
		}
		rows[i] = row
		nulls[i] = null
	}

	return QueryResult{
		Columns:   allCols,
		Rows:      rows,
		Nulls:     nulls,
		Duration:  time.Since(start),
		Truncated: truncated,
	}, nil
}

func esValueToString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%g", t)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// ─── Provisioner ─────────────────────────────────────────────────────────────

func (d *esDriver) ProvisionModes() []ProvisionMode {
	return []ProvisionMode{
		{
			ID:    "docker",
			Label: "New Docker container (elasticsearch:8.17.0)",
			Fields: []ProvisionField{
				{Key: "container", Label: "Container name", Default: ""},
				{Key: "port", Label: "Host port", Default: "9200"},
			},
		},
	}
}

func (d *esDriver) Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error) {
	if mode != "docker" {
		return ProvisionResult{}, fmt.Errorf("elasticsearch: unknown provision mode %q", mode)
	}

	if !dockerAvailable() {
		return ProvisionResult{}, fmt.Errorf("docker is not installed or the daemon is not running")
	}

	name := strings.TrimSpace(values["container"])
	if name == "" {
		name = "sqwee-elasticsearch-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	hostPort := freeHostPort(values, 9200)

	spec := dockerSpec{
		Image: "elasticsearch:8.17.0",
		Env: map[string]string{
			// Single-node dev setup with security disabled for simplicity.
			"discovery.type":          "single-node",
			"xpack.security.enabled":  "false",
			"ES_JAVA_OPTS":            "-Xms512m -Xmx512m",
		},
		ContainerPort: 9200,
		HostPort:      hostPort,
		Name:          name,
	}

	var steps []string
	container, err := runDockerContainer(ctx, spec)
	if err != nil {
		return ProvisionResult{}, err
	}
	steps = append(steps, "Started Docker container "+container+" (elasticsearch:8.17.0)")

	info := ConnInfo{
		Driver: "elasticsearch",
		Host:   "localhost",
		Port:   hostPort,
	}
	// Elasticsearch JVM startup takes ~30s; allow 90s.
	waitCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	if err := waitForServer(waitCtx, d, info); err != nil {
		cancel()
		removeDockerContainer(container)
		return ProvisionResult{}, fmt.Errorf("container started but Elasticsearch never became ready (removed): %w", err)
	}
	cancel()
	steps = append(steps, "Elasticsearch is accepting connections")

	return ProvisionResult{
		Info:      info,
		Steps:     steps,
		Container: container,
	}, nil
}

func prettyJSON(raw []byte) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}
