package trave

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// TableData is a table to Pack.
type TableData struct {
	Name    string
	Rows    int
	Columns []ColumnData
}

// ColumnData is a column to Pack: its name, its type, and its values in the one field Type uses, with one entry
// per row:
//   - Int and IntDelta: Ints, Absent for an absent value.
//   - Str and Dict: Strings, and Present, false for an absent value. A nil Present means every value is present.
//   - IntList: IntLists, nil for an absent list.
//   - StrList: StringLists, nil for an absent list.
type ColumnData struct {
	Name        string
	Type        Type
	Ints        []int64
	Strings     []string
	Present     []bool
	IntLists    [][]int64
	StringLists [][]string
}

// Pack returns the Trave file of the tables, not compressed: the same bytes as the reference writer, trave.py, with
// each dictionary listing its strings in the order of their first use.
//
// Pack refuses a magic that isn't 4 bytes or that starts like gzip (1f 8b) or zstd (28 b5 2f fd), two tables or
// two columns of a table with one name, a column with another number of values than its table's rows, an
// int_delta difference past ±(2^63 - 1), a string that isn't UTF-8 and a type it doesn't know. Its errors don't
// wrap ErrFormat.
func Pack(magic string, tables []TableData) ([]byte, error) {
	if len(magic) != 4 || strings.HasPrefix(magic, "\x1f\x8b") || strings.HasPrefix(magic, "\x28\xb5\x2f\xfd") {
		return nil, fmt.Errorf("trave: magic %q: 4 bytes expected, not starting like gzip or zstd", magic)
	}
	out := append([]byte(magic), version)
	out = appendVarint(out, uint64(len(tables)))
	names := make(map[string]struct{})
	var body []byte
	for i := range tables {
		t := &tables[i]
		if _, dup := names[t.Name]; dup {
			return nil, fmt.Errorf("trave: two tables named %q", t.Name)
		}
		names[t.Name] = struct{}{}
		if !utf8.ValidString(t.Name) {
			return nil, fmt.Errorf("trave: table name %q is not UTF-8", t.Name)
		}
		if t.Rows < 0 {
			return nil, fmt.Errorf("trave: table %q: %d rows", t.Name, t.Rows)
		}
		out = appendText(out, t.Name)
		out = appendVarint(out, uint64(t.Rows))
		out = appendVarint(out, uint64(len(t.Columns)))
		cnames := make(map[string]struct{})
		for j := range t.Columns {
			col := &t.Columns[j]
			if _, dup := cnames[col.Name]; dup {
				return nil, fmt.Errorf("trave: table %q: two columns named %q", t.Name, col.Name)
			}
			cnames[col.Name] = struct{}{}
			if !utf8.ValidString(col.Name) {
				return nil, fmt.Errorf("trave: table %q: column name %q is not UTF-8", t.Name, col.Name)
			}
			var err error
			if body, err = appendColumn(body[:0], col, t.Rows); err != nil {
				return nil, fmt.Errorf("trave: table %q column %q: %w", t.Name, col.Name, err)
			}
			out = appendText(out, col.Name)
			out = append(out, byte(col.Type))
			out = appendVarint(out, uint64(len(body)))
			out = append(out, body...)
		}
	}
	return out, nil
}

// appendColumn appends the column's data.
func appendColumn(out []byte, col *ColumnData, rows int) ([]byte, error) {
	switch col.Type {
	case Int, IntDelta:
		if len(col.Ints) != rows {
			return nil, countErr(len(col.Ints), rows)
		}
		var previous int64
		for i, v := range col.Ints {
			switch {
			case v == Absent:
				out = append(out, 0)
			case col.Type == Int:
				out = appendVarint(out, zigzag(v)+1)
			default:
				if !differenceFits(previous, v) {
					return nil, fmt.Errorf("row %d: %d after %d, a difference past ±(2^63 - 1)", i, v, previous)
				}
				out = appendVarint(out, zigzag(v-previous)+1)
				previous = v
			}
		}
	case Str, Dict:
		if len(col.Strings) != rows {
			return nil, countErr(len(col.Strings), rows)
		}
		if col.Present != nil && len(col.Present) != rows {
			return nil, fmt.Errorf("%d presence flags for %d rows", len(col.Present), rows)
		}
		for i, s := range col.Strings {
			if (col.Present == nil || col.Present[i]) && !utf8.ValidString(s) {
				return nil, fmt.Errorf("row %d: %w", i, errNotUTF8)
			}
		}
		if col.Type == Str {
			for i, s := range col.Strings {
				if col.Present != nil && !col.Present[i] {
					out = append(out, 0)
					continue
				}
				out = appendVarint(out, uint64(len(s))+1)
				out = append(out, s...)
			}
			break
		}
		index := make(map[string]uint64)
		var words []string
		for i, s := range col.Strings {
			if col.Present != nil && !col.Present[i] {
				continue
			}
			if _, seen := index[s]; !seen {
				index[s] = uint64(len(words))
				words = append(words, s)
			}
		}
		out = appendVarint(out, uint64(len(words)))
		for _, w := range words {
			out = appendText(out, w)
		}
		for i, s := range col.Strings {
			if col.Present != nil && !col.Present[i] {
				out = append(out, 0)
				continue
			}
			out = appendVarint(out, index[s]+1)
		}
	case IntList:
		if len(col.IntLists) != rows {
			return nil, countErr(len(col.IntLists), rows)
		}
		for _, list := range col.IntLists {
			if list == nil {
				out = append(out, 0)
				continue
			}
			out = appendVarint(out, uint64(len(list))+1)
			for _, x := range list {
				out = appendVarint(out, zigzag(x))
			}
		}
	case StrList:
		if len(col.StringLists) != rows {
			return nil, countErr(len(col.StringLists), rows)
		}
		for i, list := range col.StringLists {
			if list == nil {
				out = append(out, 0)
				continue
			}
			out = appendVarint(out, uint64(len(list))+1)
			for j, s := range list {
				if !utf8.ValidString(s) {
					return nil, fmt.Errorf("row %d item %d: %w", i, j, errNotUTF8)
				}
				out = appendText(out, s)
			}
		}
	default:
		return nil, fmt.Errorf("unknown type %d", byte(col.Type))
	}
	return out, nil
}

func countErr(values, rows int) error {
	return fmt.Errorf("%d values for %d rows", values, rows)
}

// differenceFits reports whether v - previous, both within ±(2^63 - 1), is too, without overflowing.
func differenceFits(previous, v int64) bool {
	switch {
	case (v >= 0) == (previous >= 0):
		return true
	case v >= 0: // previous < 0
		return v <= math.MaxInt64+previous
	default: // v < 0 <= previous
		return v >= -math.MaxInt64+previous
	}
}

func appendVarint(out []byte, n uint64) []byte {
	for n > 0x7f {
		out = append(out, byte(n)|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

func appendText(out []byte, s string) []byte {
	out = appendVarint(out, uint64(len(s)))
	return append(out, s...)
}
