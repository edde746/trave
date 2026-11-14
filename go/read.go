package trave

import (
	"fmt"
	"math"
)

// File is a Trave file's headers.
type File struct {
	Magic  string   // the file's 4-byte magic
	Tables []*Table // the tables, in the file's order
}

// Table is a table's header: its name, its row count and its columns. Its decoders, Ints, Strings, IntLists and
// StringLists, decode a column's values by the column's name.
type Table struct {
	Name    string
	Rows    int
	Columns []Column // the columns in the file's order, those of a type this package doesn't know included
}

// Column is a column's header and data.
type Column struct {
	Name string
	Type Type   // the type byte as found, which may be one this package doesn't know
	Data []byte // the column's data: a subslice of the bytes given to Read, not a copy
}

// Table returns the table of the given name, or nil if the file has none.
func (f *File) Table(name string) *Table {
	for _, t := range f.Tables {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Column returns the column of the given name, or nil if the table has none.
func (t *Table) Column(name string) *Column {
	for i := range t.Columns {
		if t.Columns[i].Name == name {
			return &t.Columns[i]
		}
	}
	return nil
}

// Read reads the headers of a Trave file. data is the file's bytes as they are, not compressed: decompress gzip or
// zstd input first. A non-empty magic must equal the file's; an empty one accepts any.
//
// Read checks every header: the version, every varint, every length against the file's end, that table and column
// names are UTF-8 and not repeated, and that nothing follows the last table. It decodes no column's data; the
// columns keep subslices of data, which must not change while they are in use.
func Read(data []byte, magic string) (*File, error) {
	if len(data) < 4 {
		return nil, formatErr("%d bytes, shorter than a magic", len(data))
	}
	found := string(data[:4])
	if magic != "" && found != magic {
		return nil, formatErr("magic %q, not %q", found, magic)
	}
	if len(data) < 5 {
		return nil, formatErr("cut short before the version")
	}
	if data[4] != version {
		return nil, formatErr("version %d, not %d", data[4], version)
	}
	c := cursor{b: data, pos: 5}
	count, err := c.varint()
	if err != nil {
		return nil, formatErr("the table count: %v", err)
	}
	f := &File{Magic: found, Tables: make([]*Table, 0, min(count, uint64(c.left())))}
	names := make(map[string]struct{})
	for i := uint64(0); i < count; i++ {
		t, err := readTable(&c, i)
		if err != nil {
			return nil, err
		}
		if _, dup := names[t.Name]; dup {
			return nil, formatErr("two tables named %q", t.Name)
		}
		names[t.Name] = struct{}{}
		f.Tables = append(f.Tables, t)
	}
	if n := c.left(); n != 0 {
		return nil, formatErr("%d bytes after the last table", n)
	}
	return f, nil
}

// name reads a table or column name.
func (c *cursor) name() (string, error) {
	start, end, err := c.text()
	if err != nil {
		return "", err
	}
	return string(c.b[start:end]), nil
}

// readTable reads the header of table number i.
func readTable(c *cursor, i uint64) (*Table, error) {
	name, err := c.name()
	if err != nil {
		return nil, formatErr("table %d: its name: %v", i, err)
	}
	rows, err := c.varint()
	if err != nil {
		return nil, formatErr("table %q: its rows: %v", name, err)
	}
	if rows > math.MaxInt {
		return nil, formatErr("table %q: %d rows, more than an int holds", name, rows)
	}
	count, err := c.varint()
	if err != nil {
		return nil, formatErr("table %q: its column count: %v", name, err)
	}
	t := &Table{Name: name, Rows: int(rows), Columns: make([]Column, 0, min(count, uint64(c.left())))}
	names := make(map[string]struct{})
	for j := uint64(0); j < count; j++ {
		cname, err := c.name()
		if err != nil {
			return nil, formatErr("table %q column %d: its name: %v", name, j, err)
		}
		if _, dup := names[cname]; dup {
			return nil, formatErr("table %q: two columns named %q", name, cname)
		}
		names[cname] = struct{}{}
		if c.left() == 0 {
			return nil, formatErr("table %q column %q: cut short before its type", name, cname)
		}
		kind := Type(c.b[c.pos])
		c.pos++
		length, err := c.varint()
		if err != nil {
			return nil, formatErr("table %q column %q: its length: %v", name, cname, err)
		}
		if length > uint64(c.left()) {
			return nil, formatErr("table %q column %q: %d bytes long, %d left in the file", name, cname, length, c.left())
		}
		end := c.pos + int(length)
		t.Columns = append(t.Columns, Column{Name: cname, Type: kind, Data: c.b[c.pos:end:end]})
		c.pos = end
	}
	return t, nil
}

// column finds a column of one of two types for a decoder, and checks the row count against its length, every value
// taking at least a byte.
func (t *Table) column(name string, a, b Type) (*Column, error) {
	col := t.Column(name)
	if col == nil {
		return nil, formatErr("table %q: no column %q", t.Name, name)
	}
	if col.Type != a && col.Type != b {
		want := a.String()
		if b != a {
			want += " or " + b.String()
		}
		return nil, formatErr("table %q column %q: type %s, not %s", t.Name, name, col.Type, want)
	}
	if t.Rows < 0 || t.Rows > len(col.Data) {
		return nil, formatErr("table %q column %q: %d rows in %d bytes", t.Name, name, t.Rows, len(col.Data))
	}
	return col, nil
}

func (t *Table) columnErr(name string, err error) error {
	return formatErr("table %q column %q: %v", t.Name, name, err)
}

// Ints decodes an int or int_delta column: one number per row, Absent for an absent value.
func (t *Table) Ints(name string) ([]int64, error) {
	col, err := t.column(name, Int, IntDelta)
	if err != nil {
		return nil, err
	}
	values, err := decodeInts(col, t.Rows)
	if err != nil {
		return nil, t.columnErr(name, err)
	}
	return values, nil
}

func decodeInts(col *Column, rows int) ([]int64, error) {
	c := cursor{b: col.Data}
	values := make([]int64, rows)
	var previous int64
	for i := range values {
		tag, err := c.varint()
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		if tag == 0 {
			values[i] = Absent
			continue
		}
		v := unzigzag(tag - 1) // within ±(2^63 - 1), as tag - 1 < 2^64 - 1
		if col.Type == IntDelta {
			if v > 0 && previous > math.MaxInt64-v || v < 0 && previous < -math.MaxInt64-v {
				return nil, fmt.Errorf("row %d: %d, then %d more, past ±(2^63 - 1)", i, previous, v)
			}
			previous += v
			v = previous
		}
		values[i] = v
	}
	return values, c.end()
}

// Strings decodes a str or dict column. Both slices have one entry per row; an absent value is "" with present
// false. A dict column's values share one string per dictionary entry.
func (t *Table) Strings(name string) (values []string, present []bool, err error) {
	col, err := t.column(name, Str, Dict)
	if err != nil {
		return nil, nil, err
	}
	values, present, err = decodeStrings(col, t.Rows)
	if err != nil {
		return nil, nil, t.columnErr(name, err)
	}
	return values, present, nil
}

func decodeStrings(col *Column, rows int) ([]string, []bool, error) {
	c := cursor{b: col.Data}
	var words []string
	var s string // a copy of the column's data, which a str column's values are substrings of
	if col.Type == Dict {
		var err error
		if words, err = readDictionary(&c); err != nil {
			return nil, nil, err
		}
	} else {
		s = string(col.Data)
	}
	values := make([]string, rows)
	present := make([]bool, rows)
	for i := range values {
		tag, err := c.varint()
		if err != nil {
			return nil, nil, fmt.Errorf("row %d: %w", i, err)
		}
		if tag == 0 {
			continue
		}
		if col.Type == Dict {
			if tag > uint64(len(words)) {
				return nil, nil, fmt.Errorf("row %d: dictionary string %d of %d", i, tag-1, len(words))
			}
			values[i] = words[tag-1]
		} else {
			start, end, err := c.utf8(tag - 1)
			if err != nil {
				return nil, nil, fmt.Errorf("row %d: %w", i, err)
			}
			values[i] = s[start:end]
		}
		present[i] = true
	}
	return values, present, c.end()
}

// readDictionary reads a dict column's dictionary: a count, then that many strings, all substrings of one copy of
// the dictionary's bytes.
func readDictionary(c *cursor) ([]string, error) {
	count, err := c.varint()
	if err != nil {
		return nil, fmt.Errorf("the dictionary's count: %w", err)
	}
	if count > uint64(c.left()) {
		return nil, fmt.Errorf("a dictionary of %d strings in %d bytes", count, c.left())
	}
	start := c.pos
	for i := range count {
		if _, _, err := c.text(); err != nil {
			return nil, fmt.Errorf("dictionary string %d: %w", i, err)
		}
	}
	s := string(c.b[start:c.pos])
	words := make([]string, count)
	d := cursor{b: c.b[:c.pos], pos: start}
	for i := range words {
		a, b, _ := d.text() // checked above
		words[i] = s[a-start : b-start]
	}
	return words, nil
}

// IntLists decodes an int_list column: one list per row, nil for an absent list and a non-nil empty slice for an
// empty one.
func (t *Table) IntLists(name string) ([][]int64, error) {
	col, err := t.column(name, IntList, IntList)
	if err != nil {
		return nil, err
	}
	values, err := decodeIntLists(col, t.Rows)
	if err != nil {
		return nil, t.columnErr(name, err)
	}
	return values, nil
}

func decodeIntLists(col *Column, rows int) ([][]int64, error) {
	c := cursor{b: col.Data}
	values := make([][]int64, rows)
	for i := range values {
		tag, err := c.varint()
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		if tag == 0 {
			continue
		}
		if tag-1 > uint64(c.left()) {
			return nil, fmt.Errorf("row %d: a list of %d items in %d bytes", i, tag-1, c.left())
		}
		list := make([]int64, tag-1)
		for j := range list {
			u, err := c.varint()
			if err != nil {
				return nil, fmt.Errorf("row %d item %d: %w", i, j, err)
			}
			list[j] = unzigzag(u)
		}
		values[i] = list
	}
	return values, c.end()
}

// StringLists decodes a str_list column: one list per row, nil for an absent list and a non-nil empty slice for
// an empty one.
func (t *Table) StringLists(name string) ([][]string, error) {
	col, err := t.column(name, StrList, StrList)
	if err != nil {
		return nil, err
	}
	values, err := decodeStringLists(col, t.Rows)
	if err != nil {
		return nil, t.columnErr(name, err)
	}
	return values, nil
}

func decodeStringLists(col *Column, rows int) ([][]string, error) {
	c := cursor{b: col.Data}
	s := string(col.Data) // the values are substrings of this one copy
	values := make([][]string, rows)
	for i := range values {
		tag, err := c.varint()
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i, err)
		}
		if tag == 0 {
			continue
		}
		if tag-1 > uint64(c.left()) {
			return nil, fmt.Errorf("row %d: a list of %d items in %d bytes", i, tag-1, c.left())
		}
		list := make([]string, tag-1)
		for j := range list {
			start, end, err := c.text()
			if err != nil {
				return nil, fmt.Errorf("row %d item %d: %w", i, j, err)
			}
			list[j] = s[start:end]
		}
		values[i] = list
	}
	return values, c.end()
}
