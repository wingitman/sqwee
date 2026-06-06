package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"main.go/internal/driver"
)

type sshTunnel struct {
	client   *ssh.Client
	listener net.Listener
	local    string
	once     sync.Once
}

func connectThroughGateway(ctx context.Context, d driver.Driver, info driver.ConnInfo) (driver.Conn, error) {
	if !gatewayEnabled(info.Gateway) {
		return d.Connect(ctx, info)
	}
	if strings.ToLower(info.Gateway.Type) != "ssh" {
		return nil, fmt.Errorf("gateway type %q is not supported", info.Gateway.Type)
	}
	if d.Name() == "sqlite" {
		return connectSQLiteThroughGateway(ctx, info)
	}
	targetHost, targetPort, err := gatewayTarget(info, d.DefaultPort())
	if err != nil {
		return nil, err
	}
	tunnel, err := startSSHTunnel(ctx, info.Gateway, targetHost, targetPort)
	if err != nil {
		return nil, err
	}

	routed := routeConnInfoThroughTunnel(info, tunnel.localPort())
	conn, err := d.Connect(ctx, routed)
	if err != nil {
		tunnel.Close()
		return nil, err
	}
	return &gatewayConn{Conn: conn, tunnel: tunnel}, nil
}

func gatewayEnabled(g driver.GatewayInfo) bool {
	return g.Type != "" || g.Host != "" || g.User != "" || g.Password != "" || g.KeyFile != "" || g.Port > 0
}

func gatewayTarget(info driver.ConnInfo, defaultPort int) (string, int, error) {
	host := info.Host
	port := info.Port
	if info.URL != "" {
		u, err := url.Parse(info.URL)
		if err != nil || u.Scheme == "" {
			return "", 0, fmt.Errorf("gateway routing requires a database URL with a host")
		}
		host = u.Hostname()
		if p := u.Port(); p != "" {
			port, _ = strconv.Atoi(p)
		}
	}
	if host == "" {
		return "", 0, fmt.Errorf("gateway routing requires a database host")
	}
	if port == 0 {
		port = defaultPort
	}
	if port == 0 {
		return "", 0, fmt.Errorf("gateway routing is not supported for file-based connections")
	}
	return host, port, nil
}

func routeConnInfoThroughTunnel(info driver.ConnInfo, localPort int) driver.ConnInfo {
	info.Gateway = driver.GatewayInfo{}
	info.Host = "127.0.0.1"
	info.Port = localPort
	if info.URL != "" {
		u, err := url.Parse(info.URL)
		if err == nil && u.Scheme != "" {
			u.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
			info.URL = u.String()
		}
	}
	return info
}

func startSSHTunnel(ctx context.Context, gw driver.GatewayInfo, targetHost string, targetPort int) (*sshTunnel, error) {
	host := strings.TrimSpace(gw.Host)
	if host == "" {
		return nil, fmt.Errorf("gateway host is required")
	}
	port := gw.Port
	if port == 0 {
		port = 22
	}
	if strings.TrimSpace(gw.User) == "" {
		return nil, fmt.Errorf("gateway user is required")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	client, err := dialSSH(ctx, gw)
	if err != nil {
		listener.Close()
		return nil, err
	}

	t := &sshTunnel{client: client, listener: listener, local: listener.Addr().String()}
	go t.acceptLoop(net.JoinHostPort(targetHost, strconv.Itoa(targetPort)))
	return t, nil
}

func dialSSH(ctx context.Context, gw driver.GatewayInfo) (*ssh.Client, error) {
	host := strings.TrimSpace(gw.Host)
	if host == "" {
		return nil, fmt.Errorf("gateway host is required")
	}
	port := gw.Port
	if port == 0 {
		port = 22
	}
	user := strings.TrimSpace(gw.User)
	if user == "" {
		return nil, fmt.Errorf("gateway user is required")
	}
	auth, err := sshAuthMethods(gw)
	if err != nil {
		return nil, err
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("gateway password or key file is required")
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	dialer := net.Dialer{Timeout: 15 * time.Second}
	netConn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	config := &ssh.ClientConfig{
		User:            user,
		Auth:            auth,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, config)
	if err != nil {
		netConn.Close()
		return nil, err
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

func sshAuthMethods(gw driver.GatewayInfo) ([]ssh.AuthMethod, error) {
	var auth []ssh.AuthMethod
	keyFile := expandHome(strings.TrimSpace(gw.KeyFile))
	if keyFile != "" {
		pem, err := os.ReadFile(keyFile)
		if err != nil {
			return nil, fmt.Errorf("gateway key file: %w", err)
		}
		var signer ssh.Signer
		if gw.Password != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(pem, []byte(gw.Password))
			if err != nil {
				signer, err = ssh.ParsePrivateKey(pem)
			}
		} else {
			signer, err = ssh.ParsePrivateKey(pem)
		}
		if err != nil {
			return nil, fmt.Errorf("gateway key file: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if gw.Password != "" {
		auth = append(auth, ssh.Password(gw.Password))
	}
	return auth, nil
}

func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func (t *sshTunnel) acceptLoop(target string) {
	for {
		localConn, err := t.listener.Accept()
		if err != nil {
			return
		}
		go t.forward(localConn, target)
	}
}

func (t *sshTunnel) forward(localConn net.Conn, target string) {
	defer localConn.Close()
	remoteConn, err := t.client.Dial("tcp", target)
	if err != nil {
		return
	}
	defer remoteConn.Close()

	done := make(chan struct{}, 2)
	go copyAndClose(remoteConn, localConn, done)
	go copyAndClose(localConn, remoteConn, done)
	<-done
}

func copyAndClose(dst net.Conn, src net.Conn, done chan<- struct{}) {
	_, _ = io.Copy(dst, src)
	_ = dst.Close()
	_ = src.Close()
	done <- struct{}{}
}

func (t *sshTunnel) localPort() int {
	_, port, err := net.SplitHostPort(t.local)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(port)
	return n
}

func (t *sshTunnel) Close() error {
	var err error
	t.once.Do(func() {
		err = errors.Join(t.listener.Close(), t.client.Close())
	})
	return err
}

const remoteSQLiteNull = "\x1e"

type remoteSQLiteConn struct {
	client *ssh.Client
	path   string
}

func connectSQLiteThroughGateway(ctx context.Context, info driver.ConnInfo) (driver.Conn, error) {
	path := driver.SQLitePath(info)
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("sqlite: no remote database file path provided")
	}
	client, err := dialSSH(ctx, info.Gateway)
	if err != nil {
		return nil, err
	}
	c := &remoteSQLiteConn{client: client, path: path}
	if err := c.Ping(ctx); err != nil {
		client.Close()
		return nil, err
	}
	return c, nil
}

func (c *remoteSQLiteConn) Ping(ctx context.Context) error {
	_, err := c.runSQLite(ctx, "SELECT 1;", false)
	return err
}

func (c *remoteSQLiteConn) Schemas(ctx context.Context) ([]driver.Schema, error) {
	return []driver.Schema{{Name: "main"}}, nil
}

func (c *remoteSQLiteConn) Objects(ctx context.Context, schema string) ([]driver.DBObject, error) {
	res, err := c.Query(ctx, `SELECT name, type FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		return nil, err
	}
	var objs []driver.DBObject
	for _, row := range res.Rows {
		if len(row) < 2 {
			continue
		}
		kind := driver.KindTable
		if row[1] == "view" {
			kind = driver.KindView
		}
		objs = append(objs, driver.DBObject{Schema: "main", Name: row[0], Kind: kind})
	}
	return objs, nil
}

func (c *remoteSQLiteConn) Columns(ctx context.Context, schema, table string) ([]driver.Column, error) {
	res, err := c.Query(ctx, "PRAGMA table_info("+sqliteIdent(table)+")")
	if err != nil {
		return nil, err
	}
	idx := map[string]int{}
	for i, col := range res.Columns {
		idx[col] = i
	}
	var cols []driver.Column
	for _, row := range res.Rows {
		primaryKey := field(row, idx["pk"])
		key := ""
		if primaryKey != "" && primaryKey != "0" {
			key = "PK"
		}
		cols = append(cols, driver.Column{
			Name:     field(row, idx["name"]),
			Type:     field(row, idx["type"]),
			Nullable: field(row, idx["notnull"]) != "1",
			Key:      key,
			Default:  field(row, idx["dflt_value"]),
		})
	}
	return cols, nil
}

func (c *remoteSQLiteConn) Definition(ctx context.Context, obj driver.DBObject) (string, error) {
	res, err := c.Query(ctx, "SELECT sql FROM sqlite_master WHERE name = "+sqliteLiteral(obj.Name))
	if err != nil {
		return "", err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 || res.Nulls[0][0] {
		return "", fmt.Errorf("no definition available for %s", obj.Name)
	}
	return res.Rows[0][0] + ";", nil
}

func (c *remoteSQLiteConn) Query(ctx context.Context, sql string) (driver.QueryResult, error) {
	start := time.Now()
	out, err := c.runSQLite(ctx, sql, true)
	if err != nil {
		return driver.QueryResult{}, err
	}
	reader := csv.NewReader(bytes.NewReader(out))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return driver.QueryResult{}, err
	}
	res := driver.QueryResult{Duration: time.Since(start)}
	if len(records) == 0 {
		return res, nil
	}
	res.Columns = records[0]
	for _, rec := range records[1:] {
		if len(res.Rows) >= 1000 {
			res.Truncated = true
			break
		}
		nulls := make([]bool, len(rec))
		for i := range rec {
			if rec[i] == remoteSQLiteNull {
				rec[i] = ""
				nulls[i] = true
			}
		}
		res.Rows = append(res.Rows, rec)
		res.Nulls = append(res.Nulls, nulls)
	}
	return res, nil
}

func (c *remoteSQLiteConn) Exec(ctx context.Context, sql string) (driver.ExecResult, error) {
	start := time.Now()
	_, err := c.runSQLite(ctx, sql, false)
	if err != nil {
		return driver.ExecResult{}, err
	}
	return driver.ExecResult{RowsAffected: -1, Duration: time.Since(start)}, nil
}

func (c *remoteSQLiteConn) TableMetadata(ctx context.Context, schema, table string) (driver.TableMetadata, error) {
	var meta driver.TableMetadata
	idxRows, err := c.Query(ctx, "PRAGMA index_list("+sqliteIdent(table)+")")
	if err == nil {
		idx := columnIndex(idxRows.Columns)
		for _, row := range idxRows.Rows {
			name := field(row, idx["name"])
			index := driver.Index{Name: name, Unique: field(row, idx["unique"]) == "1", Primary: field(row, idx["origin"]) == "pk"}
			colRows, err := c.Query(ctx, "PRAGMA index_info("+sqliteIdent(name)+")")
			if err == nil {
				colIdx := columnIndex(colRows.Columns)
				for _, colRow := range colRows.Rows {
					index.Columns = append(index.Columns, field(colRow, colIdx["name"]))
				}
			}
			meta.Indexes = append(meta.Indexes, index)
		}
	}
	fkRows, err := c.Query(ctx, "PRAGMA foreign_key_list("+sqliteIdent(table)+")")
	if err != nil {
		return meta, nil
	}
	idx := columnIndex(fkRows.Columns)
	byID := map[string]*driver.ForeignKey{}
	var order []string
	for _, row := range fkRows.Rows {
		id := field(row, idx["id"])
		fk := byID[id]
		if fk == nil {
			fk = &driver.ForeignKey{Name: "fk_" + table + "_" + id, RefSchema: schema, RefTable: field(row, idx["table"]), OnUpdate: field(row, idx["on_update"]), OnDelete: field(row, idx["on_delete"])}
			byID[id] = fk
			order = append(order, id)
		}
		fk.Columns = append(fk.Columns, field(row, idx["from"]))
		fk.RefColumns = append(fk.RefColumns, field(row, idx["to"]))
	}
	for _, id := range order {
		meta.ForeignKeys = append(meta.ForeignKeys, *byID[id])
	}
	return meta, nil
}

func (c *remoteSQLiteConn) Explain(ctx context.Context, query string) (string, error) {
	res, err := c.Query(ctx, "EXPLAIN QUERY PLAN "+query)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	for _, row := range res.Rows {
		b.WriteString(strings.Join(row, " | "))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func (c *remoteSQLiteConn) Close() error { return c.client.Close() }

func (c *remoteSQLiteConn) runSQLite(ctx context.Context, sql string, header bool) ([]byte, error) {
	args := []string{"sqlite3", "-batch"}
	if header {
		args = append(args, "-header", "-csv", "-nullvalue", remoteSQLiteNull)
	}
	args = append(args, c.path, sql)
	cmd := shellCommand(args...)
	return runSSHCommand(ctx, c.client, cmd)
}

func runSSHCommand(ctx context.Context, client *ssh.Client, command string) ([]byte, error) {
	sess, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	type result struct {
		out []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(command)
		ch <- result{out: out, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = sess.Close()
		return nil, ctx.Err()
	case res := <-ch:
		if res.err != nil {
			msg := strings.TrimSpace(string(res.out))
			if msg == "" {
				msg = res.err.Error()
			}
			return nil, fmt.Errorf("remote sqlite: %s", msg)
		}
		return res.out, nil
	}
}

func shellCommand(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func sqliteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func sqliteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func columnIndex(cols []string) map[string]int {
	out := map[string]int{}
	for i, col := range cols {
		out[col] = i
	}
	return out
}

func field(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}

type gatewayConn struct {
	driver.Conn
	tunnel *sshTunnel
}

func (c *gatewayConn) Close() error {
	return errors.Join(c.Conn.Close(), c.tunnel.Close())
}

func (c *gatewayConn) TableMetadata(ctx context.Context, schema, table string) (driver.TableMetadata, error) {
	p, ok := c.Conn.(driver.TableMetadataProvider)
	if !ok {
		return driver.TableMetadata{}, fmt.Errorf("connection does not support table metadata")
	}
	return p.TableMetadata(ctx, schema, table)
}

func (c *gatewayConn) Explain(ctx context.Context, query string) (string, error) {
	ex, ok := c.Conn.(driver.Explainer)
	if !ok {
		return "", fmt.Errorf("connection does not support explain")
	}
	return ex.Explain(ctx, query)
}

func (c *gatewayConn) CallProcedure(ctx context.Context, obj driver.DBObject, args []string) (driver.QueryResult, error) {
	runner, ok := c.Conn.(driver.ProcedureRunner)
	if !ok {
		return driver.QueryResult{}, fmt.Errorf("connection does not support procedures")
	}
	return runner.CallProcedure(ctx, obj, args)
}
