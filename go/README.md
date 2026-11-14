# trave for Go

A Go reader and writer for [Trave](../README.md) files, standard library only (Go 1.23 or newer). It agrees byte
for byte with the reference implementation, [`trave.py`](../python/trave.py), and is checked against
[`vectors.json`](../vectors.json).

## Getting it

```sh
go get github.com/edde746/trave/go
```

```go
import "github.com/edde746/trave/go" // package trave
```

The reader takes a file's bytes as they are: decompress a gzip file (`compress/gzip`) or a zstd one first.

## API

```go
type Type byte // Int, IntDelta, Str, Dict, IntList, StrList; String() gives "int", "int_delta", ...

const Absent int64 = math.MinInt64 // an absent int or int_delta value
var ErrFormat error                // wrapped by every reader error: errors.Is(err, trave.ErrFormat)

func Read(data []byte, magic string) (*File, error) // magic "" accepts any

type File struct { Magic string; Tables []*Table }
func (f *File) Table(name string) *Table // nil if missing

type Table struct { Name string; Rows int; Columns []Column }
func (t *Table) Column(name string) *Column // nil if missing
func (t *Table) Ints(name string) ([]int64, error)                       // int, int_delta; Absent if absent
func (t *Table) Strings(name string) (values []string, present []bool, err error) // str, dict
func (t *Table) IntLists(name string) ([][]int64, error)                 // int_list; nil if absent
func (t *Table) StringLists(name string) ([][]string, error)             // str_list; nil if absent

type Column struct { Name string; Type Type; Data []byte } // Data is a subslice of Read's input

func Pack(magic string, tables []TableData) ([]byte, error)

type TableData struct { Name string; Rows int; Columns []ColumnData }
type ColumnData struct {
	Name        string
	Type        Type
	Ints        []int64    // Int, IntDelta: Absent for absent
	Strings     []string   // Str, Dict
	Present     []bool     // Str, Dict: false for absent; nil means all present
	IntLists    [][]int64  // IntList: nil for absent
	StringLists [][]string // StrList: nil for absent
}
```

`Read` checks every header (magic, version, varints, lengths, UTF-8 names, duplicate names, nothing after the last
table) but decodes no column until a decoder asks for it by name. A decoder checks the column's data in full: it
ends exactly at the column's length, dictionary numbers are in range, strings are UTF-8, varints are valid, and an
int_delta running value stays within ±(2^63 - 1). A missing column, or one of another type, is an error. Columns of
a type this package doesn't know stay in `Table.Columns` with their type byte, and no decoder accepts them.

In a list column, a nil slice is an absent list and a non-nil empty slice an empty one.

`Pack` writes the same bytes as `trave.py`'s `pack`, each dictionary listing its strings in the order of first use.
It refuses a magic that isn't 4 bytes or that starts like gzip (`1f 8b`) or zstd (`28 b5 2f fd`), repeated table
or column names, a column with another number of values than its table's rows, an int_delta difference past
±(2^63 - 1), a string that isn't UTF-8, and an unknown type. Its errors don't wrap `ErrFormat`.

## Reading

```go
raw, err := os.ReadFile("demo.bin.gz")
if err != nil {
	return err
}
zr, err := gzip.NewReader(bytes.NewReader(raw))
if err != nil {
	return err
}
data, err := io.ReadAll(zr)
if err != nil {
	return err
}

f, err := trave.Read(data, "DEMO")
if err != nil {
	return err
}
films := f.Table("films")
ids, err := films.Ints("tmdb")
if err != nil {
	return err
}
titles, present, err := films.Strings("title")
if err != nil {
	return err
}
for i := range films.Rows {
	if ids[i] != trave.Absent && present[i] {
		fmt.Println(ids[i], titles[i])
	}
}
```

## Packing

```go
data, err := trave.Pack("DEMO", []trave.TableData{{
	Name: "films",
	Rows: 3,
	Columns: []trave.ColumnData{
		{Name: "tmdb", Type: trave.IntDelta, Ints: []int64{603, 604, 605}},
		{Name: "title", Type: trave.Str, Strings: []string{"The Matrix", "The Matrix Reloaded", ""},
			Present: []bool{true, true, false}},
		{Name: "kind", Type: trave.Dict, Strings: []string{"film", "film", "film"}},
	},
}})
```

`data` is uncompressed; gzip it with `compress/gzip` if you want, though only `trave.py`'s `gzip_bytes` promises the
same compressed bytes every time.

## Testing

```sh
cd go
gofmt -l . && go vet ./... && go test ./...
```
