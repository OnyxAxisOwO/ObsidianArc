package arc

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Rows is the result of a query, read whole: the server hands it over in one
// response, so there is no cursor to close.
type Rows struct {
	Columns []string
	data    [][]any
	pos     int
}

// Row is the first row of a query, for the cases that want one.
type Row struct {
	rows *Rows
	err  error
}

// ErrNoRows is what Row.Scan returns when the query found nothing.
var ErrNoRows = errors.New("arc: no rows")

// Query runs a SELECT. Placeholders are "?", whatever database the server
// is on.
func (c *Ctx) Query(sql string, args ...any) (*Rows, error) {
	raw, err := hostCall("db.query", map[string]any{"sql": sql, "args": encodeArgs(args), "tx": c.inTx})
	if err != nil {
		return nil, err
	}
	var out struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
	}
	if err := decodeNumbers(raw, &out); err != nil {
		return nil, err
	}
	return &Rows{Columns: out.Columns, data: out.Rows, pos: -1}, nil
}

// QueryRow runs a query and keeps its first row.
func (c *Ctx) QueryRow(sql string, args ...any) *Row {
	rows, err := c.Query(sql, args...)
	return &Row{rows: rows, err: err}
}

// Scan reads the row's columns into dest.
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if !r.rows.Next() {
		return ErrNoRows
	}
	return r.rows.Scan(dest...)
}

// Exec runs a statement and returns the rows it changed.
func (c *Ctx) Exec(sql string, args ...any) (int64, error) {
	raw, err := hostCall("db.exec", map[string]any{"sql": sql, "args": encodeArgs(args), "tx": c.inTx})
	if err != nil {
		return 0, err
	}
	var out struct {
		RowsAffected int64 `json:"rows_affected"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, err
	}
	return out.RowsAffected, nil
}

// Tx runs fn in a transaction: committed if it returns nil, rolled back if
// it returns an error or panics. Calls on the same Ctx made inside fn join
// it. There is one transaction per call, it is short by construction — the
// server refuses an outgoing HTTP request while one is open — and a plugin
// that keeps one open past its call has it rolled back.
func (c *Ctx) Tx(fn func() error) (err error) {
	if c.inTx {
		return errors.New("arc: transactions do not nest")
	}
	if _, err := hostCall("db.begin", nil); err != nil {
		return err
	}
	c.inTx = true
	done := false
	defer func() {
		c.inTx = false
		if !done {
			_, _ = hostCall("db.rollback", nil)
		}
	}()
	if err := fn(); err != nil {
		return err
	}
	done = true
	if _, err := hostCall("db.commit", nil); err != nil {
		return err
	}
	return nil
}

// Next advances to the next row.
func (r *Rows) Next() bool {
	r.pos++
	return r.pos < len(r.data)
}

// Len is the number of rows.
func (r *Rows) Len() int { return len(r.data) }

// Scan reads the current row's columns, in order, into dest. A destination
// may be *string, *int, *int64, *bool, *float64, *[]byte or *any; a NULL
// leaves the zero value.
func (r *Rows) Scan(dest ...any) error {
	if r.pos < 0 || r.pos >= len(r.data) {
		return errors.New("arc: Scan without a current row")
	}
	row := r.data[r.pos]
	if len(dest) != len(row) {
		return fmt.Errorf("arc: %d destinations for %d columns", len(dest), len(row))
	}
	for i, d := range dest {
		if err := assign(d, row[i]); err != nil {
			return fmt.Errorf("arc: column %s: %w", r.Columns[i], err)
		}
	}
	return nil
}

func assign(dest, value any) error {
	if value == nil {
		switch d := dest.(type) {
		case *string:
			*d = ""
		case *int:
			*d = 0
		case *int64:
			*d = 0
		case *bool:
			*d = false
		case *float64:
			*d = 0
		case *[]byte:
			*d = nil
		case *any:
			*d = nil
		default:
			return fmt.Errorf("cannot scan NULL into %T", dest)
		}
		return nil
	}
	switch d := dest.(type) {
	case *any:
		*d = value
	case *string:
		switch v := value.(type) {
		case string:
			*d = v
		case json.Number:
			*d = v.String()
		default:
			*d = fmt.Sprint(v)
		}
	case *int64:
		n, err := integer(value)
		if err != nil {
			return err
		}
		*d = n
	case *int:
		n, err := integer(value)
		if err != nil {
			return err
		}
		*d = int(n)
	case *bool:
		switch v := value.(type) {
		case bool:
			*d = v
		default:
			n, err := integer(value)
			if err != nil {
				return err
			}
			*d = n != 0
		}
	case *float64:
		n, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("cannot scan %T into *float64", value)
		}
		f, err := n.Float64()
		if err != nil {
			return err
		}
		*d = f
	case *[]byte:
		switch v := value.(type) {
		case map[string]any:
			enc, _ := v["$b64"].(string)
			b, err := base64.StdEncoding.DecodeString(enc)
			if err != nil {
				return err
			}
			*d = b
		case string:
			*d = []byte(v)
		default:
			return fmt.Errorf("cannot scan %T into *[]byte", value)
		}
	default:
		return fmt.Errorf("cannot scan into %T", dest)
	}
	return nil
}

func integer(value any) (int64, error) {
	switch v := value.(type) {
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return n, nil
		}
		f, err := v.Float64()
		return int64(f), err
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		var n int64
		_, err := fmt.Sscan(strings.TrimSpace(v), &n)
		return n, err
	}
	return 0, fmt.Errorf("cannot scan %T into an integer", value)
}

// encodeArgs turns Go values into what the server binds: bytes as a tagged
// string, everything else as itself.
func encodeArgs(args []any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case []byte:
			out[i] = map[string]string{"$b64": base64.StdEncoding.EncodeToString(v)}
		default:
			out[i] = a
		}
	}
	return out
}

// decodeNumbers decodes JSON keeping numbers exact: a 64-bit integer through
// float64 is a bug that shows up only on the row that matters.
func decodeNumbers(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	return dec.Decode(v)
}
