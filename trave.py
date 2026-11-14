"""Trave: tables of named, typed columns packed for an app to read with nothing but a gzip or zstd decoder.

The reference writer and reader of the README's format, Python 3.9 or newer, standard library only (zstd input needs
Python 3.14's compression.zstd). Copy this file into a builder; nothing else is needed.

  pack(magic, tables) -> bytes      tables: [(name, rows, [(column name, type, values)])], None for an absent value
  gzip_bytes(data) -> bytes         gzip -9 with no file name and no time: the same data gives the same bytes
  read(data, magic=None)            -> {table: (rows, {column: values})}, gzip, zstd or not compressed

Usage:
  python trave.py FILE...           print each file's magic, tables and columns with their sizes
"""
import argparse
import gzip
import io
from pathlib import Path

try:
    from compression import zstd
except ImportError:
    zstd = None

VERSION = 1

# Column types, one byte each in the file. A reader skips a column of any other type.
INT, INT_DELTA, STR, DICT, INT_LIST, STR_LIST = 1, 2, 3, 4, 5, 6
TYPE_NAMES = {INT: "int", INT_DELTA: "int_delta", STR: "str", DICT: "dict", INT_LIST: "int_list", STR_LIST: "str_list"}

GZIP_MAGIC, ZSTD_MAGIC = b"\x1f\x8b", b"\x28\xb5\x2f\xfd"
U64 = 1 << 64


# --- writing ---------------------------------------------------------------------------------------------------------

def varint(n, out):
    if not 0 <= n < U64:
        raise ValueError(f"varint of {n}, outside 0 .. 2**64 - 1")
    while n > 0x7F:
        out.append(n & 0x7F | 0x80)
        n >>= 7
    out.append(n)


def zigzag(n):
    return n * 2 if n >= 0 else -n * 2 - 1


def text(s, out):
    b = s.encode("utf-8")
    varint(len(b), out)
    out += b


def column(kind, values):
    """The column's bytes: one value per row, `None` for an absent value."""
    out = bytearray()
    if kind == DICT:
        words = list(dict.fromkeys(v for v in values if v is not None))  # in order of first use
        varint(len(words), out)
        for w in words:
            text(w, out)
        index = {w: i for i, w in enumerate(words)}
    previous = 0
    for v in values:
        if v is None:
            out.append(0)
        elif kind == INT:
            varint(zigzag(v) + 1, out)
        elif kind == INT_DELTA:
            varint(zigzag(v - previous) + 1, out)
            previous = v
        elif kind == STR:
            b = v.encode("utf-8")
            varint(len(b) + 1, out)
            out += b
        elif kind == DICT:
            varint(index[v] + 1, out)
        elif kind == INT_LIST:
            varint(len(v) + 1, out)
            for x in v:
                if not -(1 << 63) <= x < 1 << 63:
                    raise ValueError(f"list item {x} is not a signed 64-bit integer")
                varint(zigzag(x), out)
        elif kind == STR_LIST:
            varint(len(v) + 1, out)
            for x in v:
                text(x, out)
    return out


def pack(magic, tables):
    """tables: [(name, rows, [(column name, type, values)])] -> the file's bytes, not compressed."""
    if len(magic) != 4 or magic.startswith(GZIP_MAGIC) or magic.startswith(ZSTD_MAGIC):
        raise ValueError(f"magic {magic!r}: 4 bytes expected, not starting like gzip or zstd")
    out = bytearray(magic)
    out.append(VERSION)
    varint(len(tables), out)
    if len({name for name, _, _ in tables}) != len(tables):
        raise ValueError("two tables of the same name")
    for name, nrows, columns in tables:
        text(name, out)
        varint(nrows, out)
        varint(len(columns), out)
        if len({cname for cname, _, _ in columns}) != len(columns):
            raise ValueError(f"{name}: two columns of the same name")
        for cname, kind, values in columns:
            if kind not in TYPE_NAMES:
                raise ValueError(f"{name}.{cname}: unknown type {kind}")
            if len(values) != nrows:
                raise ValueError(f"{name}.{cname}: {len(values)} values for {nrows} rows")
            try:
                body = column(kind, values)
            except ValueError as e:
                raise ValueError(f"{name}.{cname}: {e}") from None
            text(cname, out)
            out.append(kind)
            varint(len(body), out)
            out += body
    return bytes(out)


def gzip_bytes(data):
    """gzip -9 with no file name and no time, so the same data gives the same bytes."""
    buffer = io.BytesIO()
    with gzip.GzipFile(filename="", mode="wb", compresslevel=9, fileobj=buffer, mtime=0) as f:
        f.write(data)
    return buffer.getvalue()


# --- reading ---------------------------------------------------------------------------------------------------------

class Reader:
    def __init__(self, data, pos=0, end=None):
        self.data, self.pos, self.end = data, pos, len(data) if end is None else end

    def byte(self):
        if self.pos >= self.end:
            raise ValueError("cut short")
        self.pos += 1
        return self.data[self.pos - 1]

    def varint(self):
        n = shift = 0
        while True:
            b = self.byte()
            n |= (b & 0x7F) << shift
            if b < 0x80:
                break
            shift += 7
            if shift > 63:
                raise ValueError("a varint longer than 10 bytes")
        if n >= U64:
            raise ValueError("a varint over 2**64 - 1")
        return n

    def bytes(self, n):
        if self.pos + n > self.end:
            raise ValueError("cut short")
        self.pos += n
        return self.data[self.pos - n:self.pos]

    def text(self):
        return self.bytes(self.varint()).decode("utf-8")

    def signed(self):
        return unzigzag(self.varint())


def unzigzag(n):
    return n >> 1 if n % 2 == 0 else -(n >> 1) - 1


def read_column(kind, nrows, r):
    """A column's values, `None` for absent; `r` reads exactly the column's bytes."""
    words = [r.text() for _ in range(r.varint())] if kind == DICT else None
    values, previous = [], 0
    for _ in range(nrows):
        tag = r.varint()
        if tag == 0:
            values.append(None)
            continue
        if kind == INT:
            values.append(unzigzag(tag - 1))
        elif kind == INT_DELTA:
            previous += unzigzag(tag - 1)
            values.append(previous)
        elif kind == STR:
            values.append(r.bytes(tag - 1).decode("utf-8"))
        elif kind == DICT:
            if tag > len(words):
                raise ValueError(f"dictionary string {tag - 1} of {len(words)}")
            values.append(words[tag - 1])
        elif kind == INT_LIST:
            values.append([r.signed() for _ in range(tag - 1)])
        elif kind == STR_LIST:
            values.append([r.text() for _ in range(tag - 1)])
    if r.pos != r.end:
        raise ValueError(f"{r.end - r.pos} bytes left in a column")
    return values


def unpack(data):
    """The file's bytes, decompressed if they are gzip or zstd."""
    if data[:2] == GZIP_MAGIC:
        return gzip.decompress(data)
    if data[:4] == ZSTD_MAGIC:
        if zstd is None:
            raise ValueError("zstd input needs Python 3.14 or newer")
        return zstd.decompress(data)
    return data


def read(data, magic=None):
    """The file (gzip, zstd or not compressed) as {table: (rows, {column: values})}, the tables and columns in the
    file's order; columns of an unknown type are skipped. With `magic`, a file with another magic is an error."""
    data = unpack(data)
    r = Reader(data)
    found = r.bytes(4)
    if magic is not None and found != magic:
        raise ValueError(f"magic {found!r}, not {magic!r}")
    version = r.byte()
    if version != VERSION:
        raise ValueError(f"version {version}, not {VERSION}")
    out = {}
    for _ in range(r.varint()):
        name, nrows = r.text(), r.varint()
        if name in out:
            raise ValueError(f"two tables named {name!r}")
        columns, seen = {}, set()
        for _ in range(r.varint()):
            cname, kind, length = r.text(), r.byte(), r.varint()
            if cname in seen:
                raise ValueError(f"{name}: two columns named {cname!r}")
            seen.add(cname)
            body = Reader(data, r.pos, r.pos + length)
            r.bytes(length)
            if kind in TYPE_NAMES:
                try:
                    columns[cname] = read_column(kind, nrows, body)
                except ValueError as e:
                    raise ValueError(f"{name}.{cname}: {e}") from None
        out[name] = (nrows, columns)
    if r.pos != len(data):
        raise ValueError(f"{len(data) - r.pos} bytes after the last table")
    return out


# --- command line ----------------------------------------------------------------------------------------------------

def dump(path):
    data = Path(path).read_bytes()
    raw = unpack(data)
    read(raw)  # the whole file is valid before anything is printed
    r = Reader(raw)
    magic, version = r.bytes(4), r.byte()
    print(f"{path}: magic {magic.decode('ascii', 'backslashreplace')}, version {version}, "
          f"{len(data):,} bytes, {len(raw):,} unpacked")
    for _ in range(r.varint()):
        name, nrows = r.text(), r.varint()
        print(f"{name}: {nrows:,} rows")
        for _ in range(r.varint()):
            cname, kind, length = r.text(), r.byte(), r.varint()
            r.bytes(length)
            print(f"  {cname:20} {TYPE_NAMES.get(kind, f'type {kind}'):10} {length:>11,} bytes")


def main():
    p = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    p.add_argument("files", nargs="+", type=Path)
    for path in p.parse_args().files:
        try:
            dump(path)
        except ValueError as e:
            raise SystemExit(f"{path}: {e}")


if __name__ == "__main__":
    main()
