package driver

import (
	"net/url"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func TestPostgresDSNOmitsEmptyDatabasePath(t *testing.T) {
	dsn := buildPostgresDSN(ConnInfo{Host: "db.example.com", User: "postgres"})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "" {
		t.Fatalf("path = %q, want empty", u.Path)
	}
	if u.Host != "db.example.com:5432" {
		t.Fatalf("host = %q", u.Host)
	}
}

func TestPostgresDSNIncludesDatabasePath(t *testing.T) {
	dsn := buildPostgresDSN(ConnInfo{Host: "db.example.com", Database: "app"})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/app" {
		t.Fatalf("path = %q, want /app", u.Path)
	}
}

func TestMySQLURLToDSN(t *testing.T) {
	dsn, err := mysqlURLToDSN("mysql://user:pass@db.example.com:3307/app?sqwee_opt=ok&parseTime=false", ConnInfo{})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.User != "user" || cfg.Passwd != "pass" || cfg.Addr != "db.example.com:3307" || cfg.DBName != "app" {
		t.Fatalf("unexpected cfg: %+v", cfg)
	}
	if cfg.ParseTime {
		t.Fatal("parseTime should be false from URL query")
	}
	if cfg.Params["sqwee_opt"] != "ok" {
		t.Fatalf("sqwee_opt param = %q", cfg.Params["sqwee_opt"])
	}
}

func TestMSSQLDSNOmitsEmptyDatabase(t *testing.T) {
	dsn := buildMSSQLDSN(ConnInfo{Host: "db.example.com", User: "sa"})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query().Get("database"); got != "" {
		t.Fatalf("database query = %q, want empty", got)
	}
}

func TestStructuredDSNsRejectHTTPHostByProducingNoHTTPScheme(t *testing.T) {
	// Host validation happens in the TUI layer; DSN builders should still never
	// treat an http(s) host as the database URL scheme.
	dsn := buildPostgresDSN(ConnInfo{Host: "https://db.example.com"})
	if strings.HasPrefix(dsn, "https://") {
		t.Fatalf("dsn uses http scheme: %s", dsn)
	}
}
