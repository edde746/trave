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

This repository has [the format](#the-format), the reference writer and reader ([`trave.py`](trave.py)), and
[test vectors](#test-vectors) for readers in other languages.

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

Every number is a signed 64-bit integer. An `int` value, and an `int_delta` difference, is between `-(2^63 - 1)` and
`2^63 - 1`, so that its tag (zigzag plus one) fits in an unsigned 64-bit integer; `-2^63` can only be a list item.

A reader skips a column of a type it doesn't know (0, 7 to 255) by its length. A column's data ends exactly at its
length: anything else is an error, as is a dictionary number past the dictionary's end or a string that isn't
UTF-8. A file of another version is an error: a new version number means a change an old reader can't skip. Within
a version, new types, columns and tables may appear, and their order may change.

### The magic

The first 4 bytes say which dataset a file is, so that an app never reads one dataset's file as another's. A
dataset picks its own (4 ASCII capitals is the convention) and its readers check it. A magic can't start with
`1f 8b` or `28 b5 2f fd`, which would read as compressed.

### Writing

`trave.py`'s writer always gives the same bytes for the same tables: each dictionary lists its strings in the order
of their first use, and `gzip_bytes` compresses with `gzip -9`, no file name and no time. A dataset's own tables say
what a file means: name and document its tables, columns and their types, and add columns rather than change one.

## The reference implementation

[`trave.py`](trave.py) is one file, Python 3.9 or newer, standard library only (reading zstd needs Python 3.14's
`compression.zstd`). Copy it into the builder that uses it.

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
value, without the columns of a type it doesn't know. `python3 trave.py FILE...` prints each file's magic, tables and
columns with their sizes.

## Test vectors

[`vectors.json`](vectors.json) has files, as hex (spaces between bytes are for reading), for every reader to check
itself against. All use the magic `TEST`.

- A vector with `tables` is a valid file: reading it gives those tables, in that order, with each column's `type`
  and `values` (`null` for absent). A column of an unknown type isn't listed: the reader skips it. When `canonical`
  is true, writing the tables gives the same bytes.
- A vector with `error` is an invalid file: reading it is an error. `error` says why.

The `int64_bounds` vector has integers up to `2^63 - 1`: parse the JSON with 64-bit integers, not doubles.

```sh
python3 -m unittest discover tests
```

## Licence

[MIT](LICENSE).
