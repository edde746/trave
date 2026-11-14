# Trave

A file format for shipping tables of data to apps: tables of named, typed columns, packed so that an app reads them
with nothing but a gzip (or zstd) decoder and a hundred lines of code. Gzipped, it is about as small as tuned Apache
Parquet.

A *trave* (Swedish) is a stack of split logs, laid in neat rows.

- Columnar: a column's values are stored together, so similar values sit next to each other for the compressor.
- Typed: integers as varints, sorted IDs as deltas, repeated strings through a dictionary, lists of either.
- Every value can be absent, at one byte.
- A reader finds tables and columns by name and skips, by its length, any column it doesn't need or doesn't know.
  New columns and tables can be added without breaking old readers.
- The same tables always give the same bytes.

This repository has [the format](#the-format), [libraries](#libraries) for Python, Go, Kotlin Multiplatform and
Dart, and [test vectors](#test-vectors) that every library, and any other reader, checks itself against.

## Files using it

Each dataset has its own 4-byte [magic](#the-magic).

| Magic | Dataset |
|---|---|
| `AMAP` | [anime-mappings](https://github.com/edde746/anime-mappings) |
| `WDTT` | [wikidata-titles](https://github.com/edde746/wikidata-titles) |

## The format

A Trave file is the bytes below, either stored as they are or compressed as one gzip member or one zstd frame. The
first bytes tell which: `1f 8b` is gzip, `28 b5 2f fd` is zstd, anything else is uncompressed.

| Bytes | Contents |
|---|---|
| 4 | the [magic](#the-magic) |
| 1 | the format version, `1` |
| varint | the number of tables, then each table: |
| string | its name |
| varint | its number of rows |
| varint | its number of columns, then each column: |
| string | its name |
| 1 | its type (below) |
| varint | the length of its data in bytes |
| that many bytes | its data: one value per row, in row order |

Nothing follows the last table.

- A **varint** is an unsigned LEB128 number: 7 bits per byte, low bits first, the high bit set on every byte but the
  last. It fits in an unsigned 64-bit integer, in at most 10 bytes.
- A **signed** number is zigzag-coded into a varint: 0, -1, 1, -2, 2 ... become 0, 1, 2, 3, 4 ... (`n * 2` for
  `n >= 0`, `-n * 2 - 1` for `n < 0`).
- A **string** is a varint byte length, then that many bytes of UTF-8.
- Table names are unique in a file, and column names in a table.
- Every value can be **absent**. Each value starts with a varint tag: `0` is absent; any other tag `t` says what
  follows, by the column's type:

| Type | Name | A present value |
|---|---|---|
| 1 | `int` | `t - 1` is the number, signed |
| 2 | `int_delta` | `t - 1` is the number's difference from the column's previous present value (0 before the first), signed |
| 3 | `str` | `t - 1` bytes of UTF-8 follow |
| 4 | `dict` | the column's data starts with a varint count and that many strings, the dictionary, before the first row; the value is the dictionary's string number `t - 1` (from 0) |
| 5 | `int_list` | `t - 1` items follow, each a signed varint |
| 6 | `str_list` | `t - 1` items follow, each a string |

Every number is a signed 64-bit integer. An `int` or `int_delta` value, and an `int_delta` difference, is between
`-(2^63 - 1)` and `2^63 - 1`, so that its tag (zigzag plus one) fits in an unsigned 64-bit integer; `-2^63` can only
be a list item. Libraries use `-2^63` to mark an absent `int` or `int_delta` value.

A reader skips a column of a type it doesn't know (0, 7 to 255) by its length. A column's data ends exactly at its
length: anything else is an error, as is a dictionary number past the dictionary's end or a string that isn't
UTF-8. A file of another version is an error: a new version number means a change an old reader can't skip. Within
a version, new types, columns and tables may appear, and their order may change.

### The magic

The first 4 bytes say which dataset a file is, so that an app never reads one dataset's file as another's. A
dataset picks its own (4 ASCII capitals is the convention) and its readers check it. A magic can't start with
`1f 8b` or `28 b5 2f fd`, which would read as compressed.

### Writing

Every library's writer gives the same bytes for the same tables: each dictionary lists its strings in the order of
their first use. Python's `gzip_bytes` compresses with `gzip -9`, no file name and no time, so a gzipped file is
reproducible too. A dataset's own tables say what a file means: name and document its tables, columns and their
types, and add columns rather than change one.

## Libraries

Each reads and writes the format and passes every [test vector](#test-vectors). They are not on a package registry
yet; depend on them from this repository.

| Language | Directory | Depend on it |
|---|---|---|
| Python 3.9+ | [`python/`](python) | `pip install "trave @ git+https://github.com/edde746/trave#subdirectory=python"`, or copy `trave.py`: one module, standard library only |
| Go 1.23+ | [`go/`](go) | `go get github.com/edde746/trave/go` |
| Kotlin Multiplatform (JVM and Android, iOS, macOS, tvOS) | [`kotlin/`](kotlin) | a Gradle included build of `kotlin/`; see its README |
| Dart 3.5+ (native, not web) | [`dart/`](dart) | a git dependency with `path: dart`; see its README |

The Python library decodes a whole file at once and handles gzip and zstd itself. The others are for apps: they
take the unpacked bytes (the app decompresses), check the file's layout, and decode a column only when asked, into
primitive arrays:

| | Python | Go | Kotlin | Dart |
|---|---|---|---|---|
| read | `trave.read(data, magic)` | `trave.Read(data, magic)` | `Trave.read(bytes, magic)` | `readTrave(bytes, magic: …)` |
| `int`, `int_delta` | `int`, `None` | `[]int64`, `Absent` | `LongArray`, `Trave.ABSENT` | `Int64List`, `traveAbsent` |
| `str`, `dict` | `str`, `None` | `[]string` and `present []bool` | `Array<String?>` | `List<String?>` |
| `int_list` | `list[int]`, `None` | `[][]int64`, nil | `Array<LongArray?>` | `List<Int64List?>` |
| `str_list` | `list[str]`, `None` | `[][]string`, nil | `Array<List<String>?>` | `List<List<String>?>` |
| write | `trave.pack(magic, tables)` | `trave.Pack(magic, tables)` | `Trave.pack(magic, tables)` | `packTrave(magic, tables)` |
| errors | `ValueError` | wrap `trave.ErrFormat` | `TraveException` | `FormatException` |

### Python

```python
import trave

data = trave.pack(b"DEMO", [
    ("films", 3, [
        ("tmdb", trave.INT_DELTA, [603, 604, 605]),
        ("title", trave.STR, ["The Matrix", "The Matrix Reloaded", None]),
        ("kind", trave.DICT, ["film", "film", "film"]),
    ]),
])
open("demo.bin.gz", "wb").write(trave.gzip_bytes(data))

tables = trave.read(open("demo.bin.gz", "rb").read(), b"DEMO")
rows, columns = tables["films"]   # 3, {"tmdb": [603, 604, 605], "title": [...], "kind": [...]}
```

`read` returns `{table: (rows, {column: values})}`, tables and columns in the file's order, `None` for an absent
value, without the columns of a type it doesn't know. `python3 -m trave FILE...` prints each file's magic, tables
and columns with their sizes.

The other libraries' READMEs have their examples.

## Test vectors

[`vectors.json`](vectors.json) has files, as hex (spaces between bytes are for reading), for every reader to check
itself against. All use the magic `TEST`.

- A vector with `tables` is a valid file: reading it gives those tables, in that order, with each column's `type`
  and `values` (`null` for absent). A column of an unknown type isn't listed: the reader skips it. When `canonical`
  is true, writing the tables gives the same bytes.
- A vector with `error` is an invalid file: reading it, and decoding its columns, is an error. `error` says why.

The `int64_bounds` vector has integers up to `2^63 - 1`: parse the JSON with 64-bit integers, not doubles.

```sh
(cd python && python3 -m unittest discover tests)
(cd go && go test ./...)
(cd kotlin && ./gradlew allTests)
(cd dart && dart test)
```

## Licence

[MIT](LICENSE).
