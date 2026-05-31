package driver

import (
	"context"
	"path/filepath"
	"testing"
)

func TestProvisionersDiscovered(t *testing.T) {
	names := Provisioners()
	want := map[string]bool{"sqlite": false, "postgres": false, "mysql": false, "mssql": false}
	for _, n := range names {
		if _, ok := want[n]; ok {
			want[n] = true
		}
	}
	for n, found := range want {
		if !found {
			t.Errorf("driver %q should be a Provisioner", n)
		}
	}
	if AsProvisioner("sqlite") == nil {
		t.Error("AsProvisioner(sqlite) should not be nil")
	}
	if AsProvisioner("does-not-exist") != nil {
		t.Error("AsProvisioner(unknown) should be nil")
	}
}

func TestSQLiteProvisionFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.sqlite")

	p := AsProvisioner("sqlite")
	if p == nil {
		t.Fatal("sqlite is not a Provisioner")
	}

	// Must offer exactly one "file" mode.
	modes := p.ProvisionModes()
	if len(modes) != 1 || modes[0].ID != "file" {
		t.Fatalf("expected one file mode, got %+v", modes)
	}

	res, err := p.Provision(context.Background(), "file", map[string]string{"path": path})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if res.Info.Driver != "sqlite" || res.Info.Database != path {
		t.Fatalf("unexpected ConnInfo: %+v", res.Info)
	}

	// The file must now exist and be connectable.
	conn, err := ByName("sqlite").Connect(context.Background(), res.Info)
	if err != nil {
		t.Fatalf("connect to provisioned db: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec(context.Background(), "CREATE TABLE t (id INTEGER)"); err != nil {
		t.Fatalf("exec on provisioned db: %v", err)
	}
}

func TestSQLiteProvisionRejectsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dup.sqlite")
	p := AsProvisioner("sqlite")
	if _, err := p.Provision(context.Background(), "file", map[string]string{"path": path}); err != nil {
		t.Fatalf("first provision: %v", err)
	}
	// Second provision to the same path must fail (don't clobber data).
	if _, err := p.Provision(context.Background(), "file", map[string]string{"path": path}); err == nil {
		t.Error("expected error provisioning over an existing file")
	}
}

func TestServerProvisionModes(t *testing.T) {
	for _, name := range []string{"postgres", "mysql", "mssql"} {
		p := AsProvisioner(name)
		if p == nil {
			t.Fatalf("%s is not a Provisioner", name)
		}
		modes := p.ProvisionModes()
		var haveServer, haveDocker bool
		for _, mode := range modes {
			if mode.ID == "server" {
				haveServer = true
			}
			if mode.ID == "docker" {
				haveDocker = true
			}
			if len(mode.Fields) == 0 {
				t.Errorf("%s mode %q has no fields", name, mode.ID)
			}
		}
		if !haveServer || !haveDocker {
			t.Errorf("%s should offer server+docker modes, got %+v", name, modes)
		}
	}
}

func TestQuotePostgres(t *testing.T) {
	cases := map[string]string{
		"app":     `"app"`,
		"my db":   `"my db"`,
		`we"ird`:  `"we""ird"`,
		"drop;--": `"drop;--"`,
	}
	for in, want := range cases {
		if got := quotePostgres(in); got != want {
			t.Errorf("quotePostgres(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServerProvisionRequiresDBName(t *testing.T) {
	spec := postgresProvisionSpec()
	// No db_name → error before any network call.
	if _, err := spec.provisionServer(context.Background(), map[string]string{}); err == nil {
		t.Error("expected error when db_name is missing")
	}
}

func TestCreateSQLQuoting(t *testing.T) {
	if got := postgresProvisionSpec().createSQL("app"); got != `CREATE DATABASE "app"` {
		t.Errorf("postgres createSQL = %q", got)
	}
	if got := mysqlProvisionSpec().createSQL("app"); got != "CREATE DATABASE `app`" {
		t.Errorf("mysql createSQL = %q", got)
	}
	if got := mssqlProvisionSpec().createSQL("app"); got != "CREATE DATABASE [app]" {
		t.Errorf("mssql createSQL = %q", got)
	}
}
