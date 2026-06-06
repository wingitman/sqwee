package driver

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDockerDatabaseIntegration(t *testing.T) {
	if os.Getenv("SQWEE_TEST_DOCKER") == "" {
		t.Skip("set SQWEE_TEST_DOCKER=1 to run Docker database integration tests")
	}
	if !dockerAvailable() {
		t.Fatal("SQWEE_TEST_DOCKER is set, but Docker is not available")
	}

	for _, driverName := range integrationDrivers() {
		driverName := driverName
		t.Run(driverName, func(t *testing.T) {
			p := AsProvisioner(driverName)
			if p == nil {
				t.Fatalf("%s is not a provisioner", driverName)
			}
			container := "sqwee-test-" + driverName + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
			port := freeTestPort(t)
			values := map[string]string{
				"container": container,
				"password":  "Sqwee_Test_123!",
				"port":      strconv.Itoa(port),
				"db_name":   "sqwee_test",
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			res, err := p.Provision(ctx, "docker", values)
			if err != nil {
				t.Fatal(err)
			}
			defer removeDockerContainer(res.Container)

			info := res.Info
			info.Password = res.PasswordHint
			smokeTestConn(t, info)
		})
	}
}

func TestLocalDatabaseURLIntegration(t *testing.T) {
	runURLIntegrationTests(t, map[string]string{
		"postgres": "SQWEE_TEST_LOCAL_POSTGRES_URL",
		"mysql":    "SQWEE_TEST_LOCAL_MYSQL_URL",
		"mssql":    "SQWEE_TEST_LOCAL_MSSQL_URL",
	})
}

func TestExternalDatabaseURLIntegration(t *testing.T) {
	runURLIntegrationTests(t, map[string]string{
		"postgres": "SQWEE_TEST_EXTERNAL_POSTGRES_URL",
		"mysql":    "SQWEE_TEST_EXTERNAL_MYSQL_URL",
		"mssql":    "SQWEE_TEST_EXTERNAL_MSSQL_URL",
	})
}

func runURLIntegrationTests(t *testing.T, envByDriver map[string]string) {
	t.Helper()
	var ran bool
	for driverName, env := range envByDriver {
		raw := os.Getenv(env)
		if raw == "" {
			continue
		}
		ran = true
		driverName, raw := driverName, raw
		t.Run(driverName, func(t *testing.T) {
			smokeTestConn(t, ConnInfo{Driver: driverName, URL: raw})
		})
	}
	if !ran {
		t.Skip("set one or more SQWEE_TEST_*_URL env vars to run URL integration tests")
	}
}

func smokeTestConn(t *testing.T, info ConnInfo) {
	t.Helper()
	d := Resolve(info)
	if d == nil {
		t.Fatalf("no driver for %+v", info)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := d.Connect(ctx, info)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Schemas(ctx); err != nil {
		t.Fatalf("schemas: %v", err)
	}

	table := "sqwee_smoke_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	_, _ = conn.Exec(ctx, "DROP TABLE "+table)
	defer conn.Exec(context.Background(), "DROP TABLE "+table)

	if _, err := conn.Exec(ctx, "CREATE TABLE "+table+" (id INT PRIMARY KEY, name VARCHAR(40))"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO "+table+" (id, name) VALUES (1, 'sqwee')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	res, err := conn.Query(ctx, "SELECT name FROM "+table+" WHERE id = 1")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 || res.Rows[0][0] != "sqwee" {
		t.Fatalf("unexpected rows: %+v", res.Rows)
	}
}

func integrationDrivers() []string {
	raw := os.Getenv("SQWEE_TEST_DOCKER_DRIVERS")
	if raw == "" {
		return []string{"postgres", "mysql", "mssql"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected addr: %v", ln.Addr())
	}
	if addr.Port == 0 {
		t.Fatal(fmt.Errorf("allocated port was zero"))
	}
	return addr.Port
}
