package driver

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// maxRows caps how many rows a single Query returns to the UI, keeping the TUI
// responsive on huge result sets. Beyond this the result is marked Truncated.
const maxRows = 1000

// sqlConn is a reusable base for any driver built on database/sql. The
// per-database drivers embed it and supply the introspection methods
// (Schemas/Objects/Columns/Definition) that vary by dialect.
type sqlConn struct {
	db *sql.DB
}

func (c *sqlConn) Ping(ctx context.Context) error {
	return c.db.PingContext(ctx)
}

func (c *sqlConn) Close() error {
	if c.db == nil {
		return nil
	}
	return c.db.Close()
}

// Query runs a row-returning statement and renders every cell as a string.
func (c *sqlConn) Query(ctx context.Context, query string) (QueryResult, error) {
	start := time.Now()
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return QueryResult{}, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return QueryResult{}, err
	}

	res := QueryResult{Columns: cols}
	for rows.Next() {
		if len(res.Rows) >= maxRows {
			res.Truncated = true
			break
		}
		cells := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range cells {
			ptrs[i] = &cells[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return QueryResult{}, err
		}
		rowStr := make([]string, len(cols))
		rowNull := make([]bool, len(cols))
		for i, v := range cells {
			rowStr[i], rowNull[i] = renderCell(v)
		}
		res.Rows = append(res.Rows, rowStr)
		res.Nulls = append(res.Nulls, rowNull)
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, err
	}
	res.Duration = time.Since(start)
	return res, nil
}

// Exec runs a non-row statement (DDL/DML).
func (c *sqlConn) Exec(ctx context.Context, query string) (ExecResult, error) {
	start := time.Now()
	r, err := c.db.ExecContext(ctx, query)
	if err != nil {
		return ExecResult{}, err
	}
	affected, _ := r.RowsAffected() // not all drivers report this
	return ExecResult{
		RowsAffected: affected,
		Duration:     time.Since(start),
	}, nil
}

// renderCell converts a scanned value into a display string, flagging NULLs.
func renderCell(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case []byte:
		return string(t), false
	case time.Time:
		return t.Format(time.RFC3339), false
	case bool:
		if t {
			return "true", false
		}
		return "false", false
	default:
		return fmt.Sprintf("%v", t), false
	}
}
