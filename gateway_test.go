package main

import (
	"net/url"
	"strings"
	"testing"

	"main.go/internal/driver"
)

func TestGatewayTargetFromStructuredConnection(t *testing.T) {
	host, port, err := gatewayTarget(driver.ConnInfo{Host: "private.db", Port: 5432}, 5432)
	if err != nil {
		t.Fatal(err)
	}
	if host != "private.db" || port != 5432 {
		t.Fatalf("target = %s:%d", host, port)
	}
}

func TestGatewayEnabledWithKeyFile(t *testing.T) {
	if !gatewayEnabled(driver.GatewayInfo{KeyFile: "~/key.pem"}) {
		t.Fatal("gateway should be enabled by key file")
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	got := shellCommand("sqlite3", "/srv/odd'name.sqlite", "SELECT 'x'")
	if !strings.Contains(got, `'\''`) {
		t.Fatalf("command did not shell-escape single quote: %s", got)
	}
}

func TestGatewayTargetFromURLConnection(t *testing.T) {
	host, port, err := gatewayTarget(driver.ConnInfo{URL: "postgres://user:pass@private.db:15432/app"}, 5432)
	if err != nil {
		t.Fatal(err)
	}
	if host != "private.db" || port != 15432 {
		t.Fatalf("target = %s:%d", host, port)
	}
}

func TestRouteConnInfoThroughTunnelRewritesStructuredConnection(t *testing.T) {
	info := routeConnInfoThroughTunnel(driver.ConnInfo{Host: "private.db", Port: 5432, Gateway: driver.GatewayInfo{Type: "ssh"}}, 40123)
	if info.Host != "127.0.0.1" || info.Port != 40123 {
		t.Fatalf("routed = %+v", info)
	}
	if gatewayEnabled(info.Gateway) {
		t.Fatalf("gateway should be cleared after routing: %+v", info.Gateway)
	}
}

func TestRouteConnInfoThroughTunnelRewritesURLConnection(t *testing.T) {
	info := routeConnInfoThroughTunnel(driver.ConnInfo{URL: "postgres://user:pass@private.db:5432/app?sslmode=require", Gateway: driver.GatewayInfo{Type: "ssh"}}, 40123)
	u, err := url.Parse(info.URL)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "127.0.0.1:40123" {
		t.Fatalf("url host = %q", u.Host)
	}
	if u.Query().Get("sslmode") != "require" {
		t.Fatalf("query was not preserved: %s", info.URL)
	}
}
