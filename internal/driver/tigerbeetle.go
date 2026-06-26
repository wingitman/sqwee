package driver

// TigerBeetle driver for sqwee.
//
// TigerBeetle is a purpose-built financial accounting database implementing
// double-entry bookkeeping. Its data model contains exactly two entity types:
//
//   Account  — a ledger account tracking debit/credit balances.
//   Transfer — a transaction moving an amount from one account to another.
//
// Schema browser mapping:
//   Schemas    → single entry "cluster"
//   Objects    → two fixed entries: "accounts" and "transfers" (KindTable)
//   Columns    → static field schema for each entity type
//   Definition → human-readable field reference card
//
// Connection:
//   URL:  tigerbeetle://host:port  or  tb://host:port
//   Host + Port are used when URL is absent (default localhost:3000).
//   Options:
//     cluster_id  — decimal cluster ID (default "0")
//     replicas    — comma-separated replica addresses for multi-replica clusters
//
// Query language (JSON envelope, consistent with the DynamoDB driver):
//
//   Read operations (Query):
//     {"operation":"query_accounts","ledger":1,"code":718,"limit":100}
//     {"operation":"lookup_accounts","ids":["1","2"]}
//     {"operation":"query_transfers","ledger":1,"code":1,"limit":50}
//     {"operation":"lookup_transfers","ids":["1","2"]}
//     {"operation":"get_account_transfers","account_id":"1","limit":100,"debits":true,"credits":true}
//     {"operation":"get_account_balances","account_id":"1","limit":50}
//
//   Write operations (Exec):
//     {"operation":"create_accounts","accounts":[{"id":"1","ledger":1,"code":718}]}
//     {"operation":"create_transfers","transfers":[{"id":"1","debit_account_id":"1","credit_account_id":"2","amount":"100","ledger":1,"code":1}]}
//
//   All ID/amount values are decimal strings (e.g. "42"). Uint128 values that
//   fit in a uint64 are accepted as plain numbers in JSON (e.g. 42); larger
//   values must be quoted strings.
//
// Provisioning:
//   Docker mode only. TigerBeetle requires formatting a data file before
//   starting, so this driver uses a custom two-step Docker sequence rather than
//   the shared runDockerContainer helper. Data files are stored in:
//     ~/.config/delbysoft/tigerbeetle/<container-name>/

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tb "github.com/tigerbeetle/tigerbeetle-go"
)

func init() { Register(&tbDriver{}) }

type tbDriver struct{}

func (d *tbDriver) Name() string      { return "tigerbeetle" }
func (d *tbDriver) Schemes() []string { return []string{"tigerbeetle", "tb"} }
func (d *tbDriver) DefaultPort() int  { return 3000 }

func (d *tbDriver) Connect(ctx context.Context, info ConnInfo) (Conn, error) {
	// Resolve addresses.
	var addrs []string
	if info.URL != "" {
		raw := info.URL
		raw = strings.TrimPrefix(raw, "tigerbeetle://")
		raw = strings.TrimPrefix(raw, "tb://")
		raw = strings.TrimSuffix(raw, "/")
		if raw != "" {
			addrs = append(addrs, raw)
		}
	}
	if len(addrs) == 0 && info.Options["replicas"] != "" {
		for _, r := range strings.Split(info.Options["replicas"], ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				addrs = append(addrs, r)
			}
		}
	}
	if len(addrs) == 0 {
		host := info.Host
		if host == "" {
			host = "localhost"
		}
		port := info.Port
		if port == 0 {
			port = 3000
		}
		addrs = []string{fmt.Sprintf("%s:%d", host, port)}
	}

	// Resolve cluster ID.
	var clusterID tb.Uint128
	cidStr := strings.TrimSpace(info.Options["cluster_id"])
	if cidStr == "" {
		clusterID = tb.ToUint128(0)
	} else {
		n, err := strconv.ParseUint(cidStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: invalid cluster_id %q: %w", cidStr, err)
		}
		clusterID = tb.ToUint128(n)
	}

	client, err := tb.NewClient(clusterID, addrs)
	if err != nil {
		return nil, fmt.Errorf("tigerbeetle: connect: %w", err)
	}

	// Smoke-test: query with a zero-result filter to confirm connectivity.
	_, err = client.QueryAccounts(tb.QueryFilter{Limit: 1})
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("tigerbeetle: ping: %w", err)
	}

	return &tbConn{client: client}, nil
}

// ─── Conn ────────────────────────────────────────────────────────────────────

type tbConn struct {
	client tb.Client
}

func (c *tbConn) Ping(ctx context.Context) error {
	_, err := c.client.QueryAccounts(tb.QueryFilter{Limit: 1})
	return err
}

func (c *tbConn) Close() error {
	c.client.Close()
	return nil
}

func (c *tbConn) Schemas(_ context.Context) ([]Schema, error) {
	return []Schema{{Name: "cluster"}}, nil
}

func (c *tbConn) Objects(_ context.Context, _ string) ([]DBObject, error) {
	return []DBObject{
		{Schema: "cluster", Name: "accounts", Kind: KindTable},
		{Schema: "cluster", Name: "transfers", Kind: KindTable},
	}, nil
}

func (c *tbConn) Columns(_ context.Context, _, table string) ([]Column, error) {
	switch table {
	case "accounts":
		return tbAccountColumns(), nil
	case "transfers":
		return tbTransferColumns(), nil
	default:
		return nil, fmt.Errorf("tigerbeetle: unknown table %q (must be \"accounts\" or \"transfers\")", table)
	}
}

func tbAccountColumns() []Column {
	return []Column{
		{Name: "id", Type: "Uint128", Key: "PK"},
		{Name: "ledger", Type: "uint32"},
		{Name: "code", Type: "uint16"},
		{Name: "flags", Type: "uint16"},
		{Name: "debits_pending", Type: "Uint128"},
		{Name: "debits_posted", Type: "Uint128"},
		{Name: "credits_pending", Type: "Uint128"},
		{Name: "credits_posted", Type: "Uint128"},
		{Name: "user_data_128", Type: "Uint128", Nullable: true},
		{Name: "user_data_64", Type: "uint64", Nullable: true},
		{Name: "user_data_32", Type: "uint32", Nullable: true},
		{Name: "timestamp", Type: "uint64"},
	}
}

func tbTransferColumns() []Column {
	return []Column{
		{Name: "id", Type: "Uint128", Key: "PK"},
		{Name: "debit_account_id", Type: "Uint128", Key: "FK"},
		{Name: "credit_account_id", Type: "Uint128", Key: "FK"},
		{Name: "amount", Type: "Uint128"},
		{Name: "ledger", Type: "uint32"},
		{Name: "code", Type: "uint16"},
		{Name: "flags", Type: "uint16"},
		{Name: "pending_id", Type: "Uint128", Nullable: true, Key: "FK"},
		{Name: "user_data_128", Type: "Uint128", Nullable: true},
		{Name: "user_data_64", Type: "uint64", Nullable: true},
		{Name: "user_data_32", Type: "uint32", Nullable: true},
		{Name: "timeout", Type: "uint32", Nullable: true},
		{Name: "timestamp", Type: "uint64"},
	}
}

func (c *tbConn) Definition(_ context.Context, obj DBObject) (string, error) {
	switch obj.Name {
	case "accounts":
		return `-- TigerBeetle Account
--
-- id              Uint128   Unique account identifier (time-based recommended)
-- ledger          uint32    Groups accounts into isolated ledgers (e.g. currency)
-- code            uint16    Application-defined account category
-- flags           uint16    Bitfield: linked, debits_must_not_exceed_credits,
--                           credits_must_not_exceed_debits, history, imported, closed
-- debits_pending  Uint128   Sum of pending (two-phase) debit amounts
-- debits_posted   Uint128   Sum of posted debit amounts
-- credits_pending Uint128   Sum of pending (two-phase) credit amounts
-- credits_posted  Uint128   Sum of posted credit amounts
-- user_data_128   Uint128   Application metadata (e.g. external reference)
-- user_data_64    uint64    Application metadata
-- user_data_32    uint32    Application metadata
-- timestamp       uint64    Cluster-assigned creation timestamp (nanoseconds)
--
-- Balance = credits_posted - debits_posted  (for a credit-normal account)
`, nil
	case "transfers":
		return `-- TigerBeetle Transfer
--
-- id               Uint128  Unique transfer identifier (time-based recommended)
-- debit_account_id Uint128  Account debited (source of funds)
-- credit_account_id Uint128 Account credited (destination of funds)
-- amount           Uint128  Transfer amount (in ledger units)
-- ledger           uint32   Must match debit and credit account ledgers
-- code             uint16   Application-defined transfer category
-- flags            uint16   Bitfield: linked, pending, post_pending_transfer,
--                           void_pending_transfer, balancing_debit,
--                           balancing_credit, closing_debit, closing_credit, imported
-- pending_id       Uint128  For two-phase: ID of the pending transfer to post/void
-- user_data_128    Uint128  Application metadata
-- user_data_64     uint64   Application metadata
-- user_data_32     uint32   Application metadata
-- timeout          uint32   Two-phase timeout in seconds (0 = no timeout)
-- timestamp        uint64   Cluster-assigned creation timestamp (nanoseconds)
`, nil
	default:
		return "", fmt.Errorf("tigerbeetle: unknown object %q", obj.Name)
	}
}

// ─── Query ────────────────────────────────────────────────────────────────────

func (c *tbConn) Query(ctx context.Context, query string) (QueryResult, error) {
	start := time.Now()
	query = strings.TrimSpace(query)

	var op tbOp
	if err := json.Unmarshal([]byte(query), &op); err != nil {
		return QueryResult{}, fmt.Errorf(
			"tigerbeetle: query must be a JSON object with an \"operation\" field: %w", err)
	}

	switch strings.ToLower(op.Operation) {
	case "query_accounts":
		var req tbQueryAccountsReq
		if err := json.Unmarshal([]byte(query), &req); err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: parse query_accounts: %w", err)
		}
		filter, err := req.toFilter()
		if err != nil {
			return QueryResult{}, err
		}
		accounts, err := c.client.QueryAccounts(filter)
		if err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: query_accounts: %w", err)
		}
		return tbAccountsToResult(accounts, start), nil

	case "lookup_accounts":
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal([]byte(query), &req); err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: parse lookup_accounts: %w", err)
		}
		ids, err := tbParseIDs(req.IDs)
		if err != nil {
			return QueryResult{}, err
		}
		accounts, err := c.client.LookupAccounts(ids)
		if err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: lookup_accounts: %w", err)
		}
		return tbAccountsToResult(accounts, start), nil

	case "query_transfers":
		var req tbQueryTransfersReq
		if err := json.Unmarshal([]byte(query), &req); err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: parse query_transfers: %w", err)
		}
		filter, err := req.toFilter()
		if err != nil {
			return QueryResult{}, err
		}
		transfers, err := c.client.QueryTransfers(filter)
		if err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: query_transfers: %w", err)
		}
		return tbTransfersToResult(transfers, start), nil

	case "lookup_transfers":
		var req struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal([]byte(query), &req); err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: parse lookup_transfers: %w", err)
		}
		ids, err := tbParseIDs(req.IDs)
		if err != nil {
			return QueryResult{}, err
		}
		transfers, err := c.client.LookupTransfers(ids)
		if err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: lookup_transfers: %w", err)
		}
		return tbTransfersToResult(transfers, start), nil

	case "get_account_transfers":
		var req tbAccountFilterReq
		if err := json.Unmarshal([]byte(query), &req); err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: parse get_account_transfers: %w", err)
		}
		filter, err := req.toFilter()
		if err != nil {
			return QueryResult{}, err
		}
		transfers, err := c.client.GetAccountTransfers(filter)
		if err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: get_account_transfers: %w", err)
		}
		return tbTransfersToResult(transfers, start), nil

	case "get_account_balances":
		var req tbAccountFilterReq
		if err := json.Unmarshal([]byte(query), &req); err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: parse get_account_balances: %w", err)
		}
		filter, err := req.toFilter()
		if err != nil {
			return QueryResult{}, err
		}
		balances, err := c.client.GetAccountBalances(filter)
		if err != nil {
			return QueryResult{}, fmt.Errorf("tigerbeetle: get_account_balances: %w", err)
		}
		return tbBalancesToResult(balances, start), nil

	default:
		return QueryResult{}, fmt.Errorf(
			"tigerbeetle: unknown read operation %q — use query_accounts, lookup_accounts, "+
				"query_transfers, lookup_transfers, get_account_transfers, get_account_balances",
			op.Operation)
	}
}

// ─── Exec ─────────────────────────────────────────────────────────────────────

func (c *tbConn) Exec(ctx context.Context, query string) (ExecResult, error) {
	start := time.Now()
	query = strings.TrimSpace(query)

	var op tbOp
	if err := json.Unmarshal([]byte(query), &op); err != nil {
		return ExecResult{}, fmt.Errorf(
			"tigerbeetle: exec must be a JSON object with an \"operation\" field: %w", err)
	}

	switch strings.ToLower(op.Operation) {
	case "create_accounts":
		var req struct {
			Accounts []tbAccountInput `json:"accounts"`
		}
		if err := json.Unmarshal([]byte(query), &req); err != nil {
			return ExecResult{}, fmt.Errorf("tigerbeetle: parse create_accounts: %w", err)
		}
		accounts, err := tbParseAccounts(req.Accounts)
		if err != nil {
			return ExecResult{}, err
		}
		results, err := c.client.CreateAccounts(accounts)
		if err != nil {
			return ExecResult{}, fmt.Errorf("tigerbeetle: create_accounts: %w", err)
		}
		if len(results) > 0 {
			var msgs []string
			for i, r := range results {
				msgs = append(msgs, fmt.Sprintf("account[%d]: %s", i, r.Status))
			}
			return ExecResult{}, fmt.Errorf("tigerbeetle: create_accounts errors: %s",
				strings.Join(msgs, "; "))
		}
		n := int64(len(accounts))
		return ExecResult{
			RowsAffected: n,
			Duration:     time.Since(start),
			Message:      fmt.Sprintf("create_accounts: %d created", n),
		}, nil

	case "create_transfers":
		var req struct {
			Transfers []tbTransferInput `json:"transfers"`
		}
		if err := json.Unmarshal([]byte(query), &req); err != nil {
			return ExecResult{}, fmt.Errorf("tigerbeetle: parse create_transfers: %w", err)
		}
		transfers, err := tbParseTransfers(req.Transfers)
		if err != nil {
			return ExecResult{}, err
		}
		results, err := c.client.CreateTransfers(transfers)
		if err != nil {
			return ExecResult{}, fmt.Errorf("tigerbeetle: create_transfers: %w", err)
		}
		if len(results) > 0 {
			var msgs []string
			for i, r := range results {
				msgs = append(msgs, fmt.Sprintf("transfer[%d]: %s", i, r.Status))
			}
			return ExecResult{}, fmt.Errorf("tigerbeetle: create_transfers errors: %s",
				strings.Join(msgs, "; "))
		}
		n := int64(len(transfers))
		return ExecResult{
			RowsAffected: n,
			Duration:     time.Since(start),
			Message:      fmt.Sprintf("create_transfers: %d created", n),
		}, nil

	default:
		// Try as a read query and summarise.
		qr, err := c.Query(ctx, query)
		if err != nil {
			return ExecResult{}, err
		}
		return ExecResult{
			RowsAffected: int64(len(qr.Rows)),
			Duration:     qr.Duration,
			Message:      fmt.Sprintf("%d rows", len(qr.Rows)),
		}, nil
	}
}

// ─── JSON request structs ─────────────────────────────────────────────────────

type tbOp struct {
	Operation string `json:"operation"`
}

// tbQueryAccountsReq mirrors QueryFilter fields accepted for query_accounts.
type tbQueryAccountsReq struct {
	UserData128  string `json:"user_data_128"`
	UserData64   uint64 `json:"user_data_64"`
	UserData32   uint32 `json:"user_data_32"`
	Ledger       uint32 `json:"ledger"`
	Code         uint16 `json:"code"`
	TimestampMin uint64 `json:"timestamp_min"`
	TimestampMax uint64 `json:"timestamp_max"`
	Limit        uint32 `json:"limit"`
	Reversed     bool   `json:"reversed"`
}

func (r tbQueryAccountsReq) toFilter() (tb.QueryFilter, error) {
	limit := r.Limit
	if limit == 0 || limit > uint32(maxRows) {
		limit = uint32(maxRows)
	}
	ud128, err := tbParseUint128OrZero(r.UserData128)
	if err != nil {
		return tb.QueryFilter{}, fmt.Errorf("tigerbeetle: user_data_128: %w", err)
	}
	flags := tb.QueryFilterFlags{Reversed: r.Reversed}
	return tb.QueryFilter{
		UserData128:  ud128,
		UserData64:   r.UserData64,
		UserData32:   r.UserData32,
		Ledger:       r.Ledger,
		Code:         r.Code,
		TimestampMin: r.TimestampMin,
		TimestampMax: r.TimestampMax,
		Limit:        limit,
		Flags:        flags.ToUint32(),
	}, nil
}

// tbQueryTransfersReq is identical to accounts — QueryFilter is the same type.
type tbQueryTransfersReq = tbQueryAccountsReq

// tbAccountFilterReq is for get_account_transfers / get_account_balances.
type tbAccountFilterReq struct {
	AccountID    string `json:"account_id"`
	UserData128  string `json:"user_data_128"`
	UserData64   uint64 `json:"user_data_64"`
	UserData32   uint32 `json:"user_data_32"`
	Code         uint16 `json:"code"`
	TimestampMin uint64 `json:"timestamp_min"`
	TimestampMax uint64 `json:"timestamp_max"`
	Limit        uint32 `json:"limit"`
	Debits       bool   `json:"debits"`
	Credits      bool   `json:"credits"`
	Reversed     bool   `json:"reversed"`
}

func (r tbAccountFilterReq) toFilter() (tb.AccountFilter, error) {
	if r.AccountID == "" {
		return tb.AccountFilter{}, fmt.Errorf("tigerbeetle: account_id is required")
	}
	aid, err := tbParseUint128(r.AccountID)
	if err != nil {
		return tb.AccountFilter{}, fmt.Errorf("tigerbeetle: account_id: %w", err)
	}
	limit := r.Limit
	if limit == 0 || limit > uint32(maxRows) {
		limit = uint32(maxRows)
	}
	ud128, err := tbParseUint128OrZero(r.UserData128)
	if err != nil {
		return tb.AccountFilter{}, fmt.Errorf("tigerbeetle: user_data_128: %w", err)
	}
	// Default to both debits and credits when neither is explicitly set.
	debits, credits := r.Debits, r.Credits
	if !debits && !credits {
		debits, credits = true, true
	}
	flags := tb.AccountFilterFlags{Debits: debits, Credits: credits, Reversed: r.Reversed}
	return tb.AccountFilter{
		AccountID:    aid,
		UserData128:  ud128,
		UserData64:   r.UserData64,
		UserData32:   r.UserData32,
		Code:         r.Code,
		TimestampMin: r.TimestampMin,
		TimestampMax: r.TimestampMax,
		Limit:        limit,
		Flags:        flags.ToUint32(),
	}, nil
}

// tbAccountInput is the JSON shape accepted in create_accounts.
type tbAccountInput struct {
	ID          string `json:"id"`
	Ledger      uint32 `json:"ledger"`
	Code        uint16 `json:"code"`
	Flags       uint16 `json:"flags"`
	UserData128 string `json:"user_data_128"`
	UserData64  uint64 `json:"user_data_64"`
	UserData32  uint32 `json:"user_data_32"`
}

func tbParseAccounts(inputs []tbAccountInput) ([]tb.Account, error) {
	out := make([]tb.Account, len(inputs))
	for i, inp := range inputs {
		id, err := tbParseUint128(inp.ID)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: account[%d].id: %w", i, err)
		}
		ud128, err := tbParseUint128OrZero(inp.UserData128)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: account[%d].user_data_128: %w", i, err)
		}
		out[i] = tb.Account{
			ID:          id,
			Ledger:      inp.Ledger,
			Code:        inp.Code,
			Flags:       inp.Flags,
			UserData128: ud128,
			UserData64:  inp.UserData64,
			UserData32:  inp.UserData32,
		}
	}
	return out, nil
}

// tbTransferInput is the JSON shape accepted in create_transfers.
type tbTransferInput struct {
	ID              string `json:"id"`
	DebitAccountID  string `json:"debit_account_id"`
	CreditAccountID string `json:"credit_account_id"`
	Amount          string `json:"amount"`
	PendingID       string `json:"pending_id"`
	Ledger          uint32 `json:"ledger"`
	Code            uint16 `json:"code"`
	Flags           uint16 `json:"flags"`
	Timeout         uint32 `json:"timeout"`
	UserData128     string `json:"user_data_128"`
	UserData64      uint64 `json:"user_data_64"`
	UserData32      uint32 `json:"user_data_32"`
}

func tbParseTransfers(inputs []tbTransferInput) ([]tb.Transfer, error) {
	out := make([]tb.Transfer, len(inputs))
	for i, inp := range inputs {
		id, err := tbParseUint128(inp.ID)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: transfer[%d].id: %w", i, err)
		}
		daid, err := tbParseUint128(inp.DebitAccountID)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: transfer[%d].debit_account_id: %w", i, err)
		}
		caid, err := tbParseUint128(inp.CreditAccountID)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: transfer[%d].credit_account_id: %w", i, err)
		}
		amount, err := tbParseUint128(inp.Amount)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: transfer[%d].amount: %w", i, err)
		}
		pendingID, err := tbParseUint128OrZero(inp.PendingID)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: transfer[%d].pending_id: %w", i, err)
		}
		ud128, err := tbParseUint128OrZero(inp.UserData128)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: transfer[%d].user_data_128: %w", i, err)
		}
		out[i] = tb.Transfer{
			ID:              id,
			DebitAccountID:  daid,
			CreditAccountID: caid,
			Amount:          amount,
			PendingID:       pendingID,
			Ledger:          inp.Ledger,
			Code:            inp.Code,
			Flags:           inp.Flags,
			Timeout:         inp.Timeout,
			UserData128:     ud128,
			UserData64:      inp.UserData64,
			UserData32:      inp.UserData32,
		}
	}
	return out, nil
}

// ─── Result builders ──────────────────────────────────────────────────────────

var tbAccountCols = func() []string {
	cols := make([]string, len(tbAccountColumns()))
	for i, c := range tbAccountColumns() {
		cols[i] = c.Name
	}
	return cols
}()

var tbTransferCols = func() []string {
	cols := make([]string, len(tbTransferColumns()))
	for i, c := range tbTransferColumns() {
		cols[i] = c.Name
	}
	return cols
}()

func tbAccountsToResult(accounts []tb.Account, start time.Time) QueryResult {
	if len(accounts) == 0 {
		return QueryResult{Columns: []string{"(no results)"}, Duration: time.Since(start)}
	}
	rows := make([][]string, len(accounts))
	nulls := make([][]bool, len(accounts))
	for i, a := range accounts {
		rows[i] = []string{
			tbU128(a.ID),
			strconv.FormatUint(uint64(a.Ledger), 10),
			strconv.FormatUint(uint64(a.Code), 10),
			strconv.FormatUint(uint64(a.Flags), 10),
			tbU128(a.DebitsPending),
			tbU128(a.DebitsPosted),
			tbU128(a.CreditsPending),
			tbU128(a.CreditsPosted),
			tbU128(a.UserData128),
			strconv.FormatUint(a.UserData64, 10),
			strconv.FormatUint(uint64(a.UserData32), 10),
			strconv.FormatUint(a.Timestamp, 10),
		}
		nulls[i] = make([]bool, len(tbAccountCols))
	}
	return QueryResult{Columns: tbAccountCols, Rows: rows, Nulls: nulls, Duration: time.Since(start)}
}

func tbTransfersToResult(transfers []tb.Transfer, start time.Time) QueryResult {
	if len(transfers) == 0 {
		return QueryResult{Columns: []string{"(no results)"}, Duration: time.Since(start)}
	}
	rows := make([][]string, len(transfers))
	nulls := make([][]bool, len(transfers))
	for i, t := range transfers {
		rows[i] = []string{
			tbU128(t.ID),
			tbU128(t.DebitAccountID),
			tbU128(t.CreditAccountID),
			tbU128(t.Amount),
			strconv.FormatUint(uint64(t.Ledger), 10),
			strconv.FormatUint(uint64(t.Code), 10),
			strconv.FormatUint(uint64(t.Flags), 10),
			tbU128(t.PendingID),
			tbU128(t.UserData128),
			strconv.FormatUint(t.UserData64, 10),
			strconv.FormatUint(uint64(t.UserData32), 10),
			strconv.FormatUint(uint64(t.Timeout), 10),
			strconv.FormatUint(t.Timestamp, 10),
		}
		nulls[i] = make([]bool, len(tbTransferCols))
	}
	return QueryResult{Columns: tbTransferCols, Rows: rows, Nulls: nulls, Duration: time.Since(start)}
}

func tbBalancesToResult(balances []tb.AccountBalance, start time.Time) QueryResult {
	if len(balances) == 0 {
		return QueryResult{Columns: []string{"(no results)"}, Duration: time.Since(start)}
	}
	cols := []string{
		"debits_pending", "debits_posted",
		"credits_pending", "credits_posted",
		"timestamp",
	}
	rows := make([][]string, len(balances))
	nulls := make([][]bool, len(balances))
	for i, b := range balances {
		rows[i] = []string{
			tbU128(b.DebitsPending),
			tbU128(b.DebitsPosted),
			tbU128(b.CreditsPending),
			tbU128(b.CreditsPosted),
			strconv.FormatUint(b.Timestamp, 10),
		}
		nulls[i] = make([]bool, len(cols))
	}
	return QueryResult{Columns: cols, Rows: rows, Nulls: nulls, Duration: time.Since(start)}
}

// ─── Uint128 helpers ──────────────────────────────────────────────────────────

// tbU128 renders a Uint128 as a decimal string. Values that fit in uint64 are
// rendered without any big-integer overhead; larger values use math/big.
func tbU128(v tb.Uint128) string {
	// The Uint128 type is [2]uint64 in little-endian word order: [low, high].
	hi := v.BigInt()
	if hi == nil {
		return "0"
	}
	return hi.String()
}

// tbParseUint128 converts a decimal string to Uint128.
func tbParseUint128(s string) (tb.Uint128, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return tb.Uint128{}, fmt.Errorf("value is required")
	}
	// Fast path: fits in uint64.
	if n, err := strconv.ParseUint(s, 10, 64); err == nil {
		return tb.ToUint128(n), nil
	}
	// Slow path: 128-bit value.
	bi := new(big.Int)
	if _, ok := bi.SetString(s, 10); !ok {
		return tb.Uint128{}, fmt.Errorf("invalid decimal integer %q", s)
	}
	return tb.BigIntToUint128(bi), nil
}

// tbParseUint128OrZero returns the zero Uint128 for an empty string.
func tbParseUint128OrZero(s string) (tb.Uint128, error) {
	if strings.TrimSpace(s) == "" {
		return tb.ToUint128(0), nil
	}
	return tbParseUint128(s)
}

// tbParseIDs converts a slice of decimal ID strings to []Uint128.
func tbParseIDs(ss []string) ([]tb.Uint128, error) {
	out := make([]tb.Uint128, len(ss))
	for i, s := range ss {
		id, err := tbParseUint128(s)
		if err != nil {
			return nil, fmt.Errorf("tigerbeetle: ids[%d]: %w", i, err)
		}
		out[i] = id
	}
	return out, nil
}

// ─── Provisioner ─────────────────────────────────────────────────────────────

func (d *tbDriver) ProvisionModes() []ProvisionMode {
	return []ProvisionMode{
		{
			ID:    "docker",
			Label: "New Docker container (ghcr.io/tigerbeetle/tigerbeetle)",
			Fields: []ProvisionField{
				{Key: "container", Label: "Container name", Default: ""},
				{Key: "cluster_id", Label: "Cluster ID", Default: "0"},
				{Key: "port", Label: "Host port", Default: "3000"},
			},
		},
	}
}

func (d *tbDriver) Provision(ctx context.Context, mode string, values map[string]string) (ProvisionResult, error) {
	if mode != "docker" {
		return ProvisionResult{}, fmt.Errorf("tigerbeetle: unknown provision mode %q", mode)
	}

	if !dockerAvailable() {
		return ProvisionResult{}, fmt.Errorf("docker is not installed or the daemon is not running")
	}

	name := strings.TrimSpace(values["container"])
	if name == "" {
		name = "sqwee-tigerbeetle-" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	clusterID := strings.TrimSpace(values["cluster_id"])
	if clusterID == "" {
		clusterID = "0"
	}
	hostPort := freeHostPort(values, 3000)

	// TigerBeetle requires a data file to be formatted before the server can
	// start. We store it under ~/.config/delbysoft/tigerbeetle/<name>/.
	home, err := os.UserHomeDir()
	if err != nil {
		return ProvisionResult{}, fmt.Errorf("tigerbeetle: cannot determine home directory: %w", err)
	}
	dataDir := filepath.Join(home, ".config", "delbysoft", "tigerbeetle", name)
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return ProvisionResult{}, fmt.Errorf("tigerbeetle: create data directory: %w", err)
	}
	dataFile := "/data/0_0.tigerbeetle"
	image := "ghcr.io/tigerbeetle/tigerbeetle"

	var steps []string

	// Step 1: Format the data file.
	formatArgs := []string{
		"run", "--rm",
		"-v", dataDir + ":/data",
		image,
		"format",
		"--cluster=" + clusterID,
		"--replica=0",
		"--replica-count=1",
		dataFile,
	}
	var stderr bytes.Buffer
	fmtCmd := exec.CommandContext(ctx, "docker", formatArgs...)
	fmtCmd.Stderr = &stderr
	if err := fmtCmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return ProvisionResult{}, fmt.Errorf("tigerbeetle: format data file: %s", msg)
	}
	steps = append(steps, fmt.Sprintf("Formatted data file in %s", dataDir))

	// Step 2: Start the server.
	startArgs := []string{
		"run", "-d",
		"--name", name,
		"-v", dataDir + ":/data",
		"-p", fmt.Sprintf("%d:3000", hostPort),
		image,
		"start",
		"--addresses=0.0.0.0:3000",
		dataFile,
	}
	stderr.Reset()
	startCmd := exec.CommandContext(ctx, "docker", startArgs...)
	startCmd.Stderr = &stderr
	if err := startCmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return ProvisionResult{}, fmt.Errorf("tigerbeetle: start container: %s", msg)
	}
	steps = append(steps, fmt.Sprintf("Started Docker container %s (%s)", name, image))

	// Wait for the server to be ready.
	info := ConnInfo{
		Driver: "tigerbeetle",
		Host:   "localhost",
		Port:   hostPort,
		Options: map[string]string{
			"cluster_id": clusterID,
		},
	}
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	if err := waitForServer(waitCtx, d, info); err != nil {
		cancel()
		removeDockerContainer(name)
		return ProvisionResult{}, fmt.Errorf("container started but TigerBeetle never became ready (removed): %w", err)
	}
	cancel()
	steps = append(steps, "TigerBeetle is accepting connections")

	return ProvisionResult{
		Info:      info,
		Steps:     steps,
		Container: name,
	}, nil
}


