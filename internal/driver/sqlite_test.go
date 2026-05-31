package driver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// setupTestDB creates a temporary SQLite database with a table and a view.
func setupTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")

	d := ByName("sqlite")
	if d == nil {
		t.Fatal("sqlite driver not registered")
	}
	conn, err := d.Connect(context.Background(), ConnInfo{Database: path})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	stmts := []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT)`,
		`INSERT INTO users (name, email) VALUES ('Alice','alice@x.com')`,
		`INSERT INTO users (name, email) VALUES ('Bob', NULL)`,
		`CREATE VIEW v_users AS SELECT id, name FROM users`,
	}
	for _, s := range stmts {
		if _, err := conn.Exec(context.Background(), s); err != nil {
			t.Fatalf("exec %q: %v", s, err)
		}
	}
	return path
}

func TestSQLiteRegistered(t *testing.T) {
	if ByName("sqlite") == nil {
		t.Fatal("sqlite not registered")
	}
	if ForScheme("sqlite") == nil {
		t.Fatal("sqlite scheme not resolvable")
	}
}

func TestSQLiteIntrospectionAndQuery(t *testing.T) {
	path := setupTestDB(t)
	d := ByName("sqlite")
	conn, err := d.Connect(context.Background(), ConnInfo{Database: path})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	ctx := context.Background()

	// Schemas.
	schemas, err := conn.Schemas(ctx)
	if err != nil || len(schemas) != 1 || schemas[0].Name != "main" {
		t.Fatalf("schemas = %v, err = %v", schemas, err)
	}

	// Objects: expect a table and a view.
	objs, err := conn.Objects(ctx, "main")
	if err != nil {
		t.Fatalf("objects: %v", err)
	}
	var haveTable, haveView bool
	for _, o := range objs {
		if o.Name == "users" && o.Kind == KindTable {
			haveTable = true
		}
		if o.Name == "v_users" && o.Kind == KindView {
			haveView = true
		}
	}
	if !haveTable || !haveView {
		t.Fatalf("expected table+view, got %+v", objs)
	}

	// Columns.
	cols, err := conn.Columns(ctx, "main", "users")
	if err != nil || len(cols) != 3 {
		t.Fatalf("columns = %v, err = %v", cols, err)
	}
	if cols[0].Key != "PK" {
		t.Errorf("expected id to be PK, got %q", cols[0].Key)
	}
	if cols[1].Nullable {
		t.Errorf("expected name NOT NULL")
	}

	// Definition.
	ddl, err := conn.Definition(ctx, DBObject{Schema: "main", Name: "users", Kind: KindTable})
	if err != nil || ddl == "" {
		t.Fatalf("definition = %q, err = %v", ddl, err)
	}

	// Query with a NULL value.
	res, err := conn.Query(ctx, "SELECT name, email FROM users ORDER BY name")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(res.Rows))
	}
	// Bob's email is NULL.
	if !res.Nulls[1][1] {
		t.Errorf("expected Bob.email to be NULL")
	}

	// Explain capability.
	if ex, ok := conn.(Explainer); ok {
		if _, err := ex.Explain(ctx, "SELECT * FROM users"); err != nil {
			t.Errorf("explain: %v", err)
		}
	} else {
		t.Error("sqlite conn should implement Explainer")
	}
}

func TestSQLiteMissingPath(t *testing.T) {
	d := ByName("sqlite")
	if _, err := d.Connect(context.Background(), ConnInfo{}); err == nil {
		t.Error("expected error for missing path")
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}

func TestAllBuiltinsRegistered(t *testing.T) {
	want := []string{"mssql", "mysql", "postgres", "sqlite"}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i, n := range want {
		if got[i] != n {
			t.Errorf("driver[%d] = %q, want %q", i, got[i], n)
		}
	}
	// Scheme resolution.
	cases := map[string]string{
		"postgresql": "postgres",
		"mysql":      "mysql",
		"sqlserver":  "mssql",
		"sqlite3":    "sqlite",
	}
	for scheme, drv := range cases {
		d := ForScheme(scheme)
		if d == nil || d.Name() != drv {
			t.Errorf("ForScheme(%q) = %v, want %q", scheme, d, drv)
		}
	}
}
