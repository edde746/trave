package trave_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/edde746/trave/go"
)

type vectorFile struct {
	Magic   string   `json:"magic"`
	Vectors []vector `json:"vectors"`
}

type vector struct {
	Name      string        `json:"name"`
	Hex       string        `json:"hex"`
	Canonical bool          `json:"canonical"`
	Error     *string       `json:"error"`
	Tables    []vectorTable `json:"tables"`
}

type vectorTable struct {
	Name    string         `json:"name"`
	Rows    int            `json:"rows"`
	Columns []vectorColumn `json:"columns"`
}

type vectorColumn struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Values []any  `json:"values"`
}

var typesByName = map[string]trave.Type{}

func init() {
	for t := trave.Int; t <= trave.StrList; t++ {
		typesByName[t.String()] = t
	}
}

func loadVectors(t *testing.T) vectorFile {
	t.Helper()
	f, err := os.Open("../vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.UseNumber()
	var v vectorFile
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (v vector) bytes(t *testing.T) []byte {
	t.Helper()
	b, err := hex.DecodeString(strings.ReplaceAll(v.Hex, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func number(t *testing.T, v any) int64 {
	t.Helper()
	n, err := strconv.ParseInt(string(v.(json.Number)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// column returns the column's expected values as Pack takes them, which are also what the decoders return.
func (c vectorColumn) data(t *testing.T) trave.ColumnData {
	t.Helper()
	kind, ok := typesByName[c.Type]
	if !ok {
		t.Fatalf("column %s: type %q", c.Name, c.Type)
	}
	col := trave.ColumnData{Name: c.Name, Type: kind}
	n := len(c.Values)
	switch kind {
	case trave.Int, trave.IntDelta:
		col.Ints = make([]int64, n)
		for i, v := range c.Values {
			col.Ints[i] = trave.Absent
			if v != nil {
				col.Ints[i] = number(t, v)
			}
		}
	case trave.Str, trave.Dict:
		col.Strings, col.Present = make([]string, n), make([]bool, n)
		for i, v := range c.Values {
			if v != nil {
				col.Strings[i], col.Present[i] = v.(string), true
			}
		}
	case trave.IntList:
		col.IntLists = make([][]int64, n)
		for i, v := range c.Values {
			if v != nil {
				list := []int64{}
				for _, x := range v.([]any) {
					list = append(list, number(t, x))
				}
				col.IntLists[i] = list
			}
		}
	case trave.StrList:
		col.StringLists = make([][]string, n)
		for i, v := range c.Values {
			if v != nil {
				list := []string{}
				for _, x := range v.([]any) {
					list = append(list, x.(string))
				}
				col.StringLists[i] = list
			}
		}
	}
	return col
}

// decode decodes a column with the decoder of its type, into the fields of a ColumnData.
func decode(table *trave.Table, col trave.Column) (trave.ColumnData, error) {
	got := trave.ColumnData{Name: col.Name, Type: col.Type}
	var err error
	switch col.Type {
	case trave.Int, trave.IntDelta:
		got.Ints, err = table.Ints(col.Name)
	case trave.Str, trave.Dict:
		got.Strings, got.Present, err = table.Strings(col.Name)
	case trave.IntList:
		got.IntLists, err = table.IntLists(col.Name)
	case trave.StrList:
		got.StringLists, err = table.StringLists(col.Name)
	}
	return got, err
}

func known(t trave.Type) bool { return t >= trave.Int && t <= trave.StrList }

func TestValidVectors(t *testing.T) {
	vf := loadVectors(t)
	for _, v := range vf.Vectors {
		if v.Error != nil {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			data := v.bytes(t)
			f, err := trave.Read(data, vf.Magic)
			if err != nil {
				t.Fatal(err)
			}
			if f.Magic != vf.Magic {
				t.Errorf("magic %q", f.Magic)
			}
			if len(f.Tables) != len(v.Tables) {
				t.Fatalf("%d tables, want %d", len(f.Tables), len(v.Tables))
			}
			for i, want := range v.Tables {
				table := f.Tables[i]
				if table.Name != want.Name || table.Rows != want.Rows {
					t.Fatalf("table %d: %q of %d rows, want %q of %d", i, table.Name, table.Rows, want.Name, want.Rows)
				}
				if f.Table(want.Name) != table {
					t.Errorf("Table(%q) is not table %d", want.Name, i)
				}
				var cols []trave.Column
				for _, c := range table.Columns {
					if known(c.Type) {
						cols = append(cols, c)
					}
				}
				if len(cols) != len(want.Columns) {
					t.Fatalf("table %s: %d known columns, want %d", want.Name, len(cols), len(want.Columns))
				}
				for j, wc := range want.Columns {
					c := cols[j]
					expected := wc.data(t)
					if c.Name != wc.Name || c.Type != expected.Type {
						t.Fatalf("table %s column %d: %s %s, want %s %s", want.Name, j, c.Name, c.Type, wc.Name, wc.Type)
					}
					got, err := decode(table, c)
					if err != nil {
						t.Fatal(err)
					}
					if !reflect.DeepEqual(got, expected) {
						t.Errorf("table %s column %s:\n got %#v\nwant %#v", want.Name, c.Name, got, expected)
					}
				}
			}
		})
	}
}

func TestCanonicalVectorsPack(t *testing.T) {
	vf := loadVectors(t)
	for _, v := range vf.Vectors {
		if !v.Canonical {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			tables := []trave.TableData{}
			for _, vt := range v.Tables {
				td := trave.TableData{Name: vt.Name, Rows: vt.Rows}
				for _, vc := range vt.Columns {
					td.Columns = append(td.Columns, vc.data(t))
				}
				tables = append(tables, td)
			}
			got, err := trave.Pack(vf.Magic, tables)
			if err != nil {
				t.Fatal(err)
			}
			if want := v.bytes(t); !bytes.Equal(got, want) {
				t.Errorf("got  % x\nwant % x", got, want)
			}
		})
	}
}

func TestErrorVectors(t *testing.T) {
	vf := loadVectors(t)
	for _, v := range vf.Vectors {
		if v.Error == nil {
			continue
		}
		t.Run(v.Name, func(t *testing.T) {
			err := readAndDecode(v.bytes(t), vf.Magic)
			if err == nil {
				t.Fatalf("no error for %s", *v.Error)
			}
			if !errors.Is(err, trave.ErrFormat) {
				t.Errorf("%v does not wrap ErrFormat", err)
			}
			t.Logf("%s: %v", *v.Error, err)
		})
	}
}

// readAndDecode reads the file and decodes every column of a known type, returning the first error.
func readAndDecode(data []byte, magic string) error {
	f, err := trave.Read(data, magic)
	if err != nil {
		return err
	}
	for _, table := range f.Tables {
		for _, c := range table.Columns {
			if known(c.Type) {
				if _, err := decode(table, c); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func TestReaderErrors(t *testing.T) {
	data, err := trave.Pack("AAAA", []trave.TableData{{Name: "t", Rows: 1, Columns: []trave.ColumnData{
		{Name: "i", Type: trave.Int, Ints: []int64{1}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := trave.Read(data, "BBBB"); !errors.Is(err, trave.ErrFormat) {
		t.Errorf("another magic: %v", err)
	}
	f, err := trave.Read(data, "")
	if err != nil {
		t.Fatal(err)
	}
	table := f.Table("t")
	if f.Table("u") != nil || table.Column("j") != nil {
		t.Error("a missing table or column is not nil")
	}
	if c := table.Column("i"); &c.Data[0] != &data[len(data)-1] {
		t.Error("a column's data is not a subslice of the file")
	}
	if _, err := table.Ints("j"); !errors.Is(err, trave.ErrFormat) {
		t.Errorf("a missing column: %v", err)
	}
	if _, _, err := table.Strings("i"); !errors.Is(err, trave.ErrFormat) {
		t.Errorf("a column of another type: %v", err)
	}
	if _, err := trave.Read(append(data[:len(data):len(data)], 0), ""); !errors.Is(err, trave.ErrFormat) {
		t.Errorf("a byte after the last table: %v", err)
	}
}

func TestPackAbsentAndPresent(t *testing.T) {
	// -2^63 is Absent: an int column's value -2^63 is written as absent.
	data, err := trave.Pack("TEST", []trave.TableData{{Name: "t", Rows: 2, Columns: []trave.ColumnData{
		{Name: "i", Type: trave.Int, Ints: []int64{trave.Absent, 5}},
		{Name: "d", Type: trave.IntDelta, Ints: []int64{-1, trave.Absent}},
		{Name: "s", Type: trave.Str, Strings: []string{"a", "b"}},
		{Name: "k", Type: trave.Dict, Strings: []string{"\xff", "b"}, Present: []bool{false, true}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	f, err := trave.Read(data, "TEST")
	if err != nil {
		t.Fatal(err)
	}
	table := f.Tables[0]
	if got, _ := table.Ints("i"); !reflect.DeepEqual(got, []int64{trave.Absent, 5}) {
		t.Errorf("i: %v", got)
	}
	if got, _ := table.Ints("d"); !reflect.DeepEqual(got, []int64{-1, trave.Absent}) {
		t.Errorf("d: %v", got)
	}
	if got, present, _ := table.Strings("s"); !reflect.DeepEqual(got, []string{"a", "b"}) || !reflect.DeepEqual(present, []bool{true, true}) {
		t.Errorf("s: %q %v", got, present)
	}
	if got, present, _ := table.Strings("k"); !reflect.DeepEqual(got, []string{"", "b"}) || !reflect.DeepEqual(present, []bool{false, true}) {
		t.Errorf("k: %q %v", got, present)
	}
}

func TestWriterRefusesWhatReadersCannotHold(t *testing.T) {
	// Python's "int -2^63" and "int_delta value -2^63" cases are Absent here, and its "list item 2^63" doesn't fit
	// an int64; a difference past -(2^63 - 1) stands in for them.
	cases := map[string][]trave.TableData{
		"delta over 2^63 - 1": {{Name: "t", Rows: 2, Columns: []trave.ColumnData{
			{Name: "d", Type: trave.IntDelta, Ints: []int64{-1, math.MaxInt64}}}}},
		"delta under -(2^63 - 1)": {{Name: "t", Rows: 2, Columns: []trave.ColumnData{
			{Name: "d", Type: trave.IntDelta, Ints: []int64{1, -math.MaxInt64}}}}},
		"values for another row count": {{Name: "t", Rows: 2, Columns: []trave.ColumnData{
			{Name: "i", Type: trave.Int, Ints: []int64{1}}}}},
		"presence for another row count": {{Name: "t", Rows: 1, Columns: []trave.ColumnData{
			{Name: "s", Type: trave.Str, Strings: []string{"a"}, Present: []bool{true, true}}}}},
		"two columns of one name": {{Name: "t", Columns: []trave.ColumnData{
			{Name: "i", Type: trave.Int}, {Name: "i", Type: trave.Str}}}},
		"two tables of one name": {{Name: "t"}, {Name: "t"}},
		"unknown type":           {{Name: "t", Columns: []trave.ColumnData{{Name: "i", Type: 9}}}},
		"negative rows":          {{Name: "t", Rows: -1}},
		"a string that is not UTF-8": {{Name: "t", Rows: 1, Columns: []trave.ColumnData{
			{Name: "m", Type: trave.StrList, StringLists: [][]string{{"\xff"}}}}}},
	}
	for name, tables := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := trave.Pack("TEST", tables)
			if err == nil {
				t.Fatal("no error")
			}
			if errors.Is(err, trave.ErrFormat) {
				t.Errorf("%v wraps ErrFormat", err)
			}
		})
	}
}

func TestMagicThatReadsAsCompressedIsRefused(t *testing.T) {
	for _, magic := range []string{"\x1f\x8bAB", "\x28\xb5\x2f\xfd", "ABC", "ABCDE"} {
		if _, err := trave.Pack(magic, nil); err == nil {
			t.Errorf("magic %q: no error", magic)
		}
	}
}
