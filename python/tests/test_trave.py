import json
import sys
import unittest
from pathlib import Path

PYTHON = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(PYTHON))
import trave  # noqa: E402

VECTORS = json.loads((PYTHON.parent / "vectors.json").read_text(encoding="utf-8"))
MAGIC = VECTORS["magic"].encode("ascii")
TYPES = {name: kind for kind, name in trave.TYPE_NAMES.items()}


def expected(vector):
    """The vector's tables as read() returns them."""
    return {t["name"]: (t["rows"], {c["name"]: c["values"] for c in t["columns"]}) for t in vector["tables"]}


def to_pack(vector):
    return [(t["name"], t["rows"], [(c["name"], TYPES[c["type"]], c["values"]) for c in t["columns"]])
            for t in vector["tables"]]


class Vectors(unittest.TestCase):
    def test_valid_files_read_as_expected(self):
        for v in VECTORS["vectors"]:
            if "error" in v:
                continue
            with self.subTest(v["name"]):
                got = trave.read(bytes.fromhex(v["hex"]), MAGIC)
                self.assertEqual(got, expected(v))
                self.assertEqual(list(got), [t["name"] for t in v["tables"]])  # in the file's order

    def test_canonical_files_pack_to_the_same_bytes(self):
        for v in VECTORS["vectors"]:
            if v.get("canonical"):
                with self.subTest(v["name"]):
                    self.assertEqual(trave.pack(MAGIC, to_pack(v)), bytes.fromhex(v["hex"]))

    def test_invalid_files_are_errors(self):
        for v in VECTORS["vectors"]:
            if "error" in v:
                with self.subTest(v["name"]):
                    with self.assertRaises(ValueError):
                        trave.read(bytes.fromhex(v["hex"]), MAGIC)


class Compression(unittest.TestCase):
    data = trave.pack(MAGIC, [("t", 2, [("s", trave.STR, ["a", None])])])

    def test_gzip_is_read_and_deterministic(self):
        packed = trave.gzip_bytes(self.data)
        self.assertEqual(packed, trave.gzip_bytes(self.data))
        self.assertEqual(trave.read(packed), trave.read(self.data))

    @unittest.skipIf(trave.zstd is None, "zstd needs Python 3.14")
    def test_zstd_is_read(self):
        self.assertEqual(trave.read(trave.zstd.compress(self.data)), trave.read(self.data))


class Errors(unittest.TestCase):
    def test_another_magic_is_an_error(self):
        with self.assertRaises(ValueError):
            trave.read(trave.pack(b"AAAA", []), b"BBBB")

    def test_writer_refuses_what_readers_cannot_hold(self):
        cases = {
            "int -2^63": [("t", 1, [("i", trave.INT, [-(1 << 63)])])],
            "delta over 2^63 - 1": [("t", 2, [("d", trave.INT_DELTA, [-1, (1 << 63) - 1])])],
            "int_delta value -2^63": [("t", 2, [("d", trave.INT_DELTA, [-1, -(1 << 63)])])],
            "list item 2^63": [("t", 1, [("l", trave.INT_LIST, [[1 << 63]])])],
            "values for another row count": [("t", 2, [("i", trave.INT, [1])])],
            "two columns of one name": [("t", 0, [("i", trave.INT, []), ("i", trave.STR, [])])],
            "unknown type": [("t", 0, [("i", 9, [])])],
        }
        for name, tables in cases.items():
            with self.subTest(name):
                with self.assertRaises(ValueError):
                    trave.pack(MAGIC, tables)

    def test_magic_that_reads_as_compressed_is_refused(self):
        for magic in (b"\x1f\x8bAB", b"\x28\xb5\x2f\xfd", b"ABC"):
            with self.subTest(magic):
                with self.assertRaises(ValueError):
                    trave.pack(magic, [])


if __name__ == "__main__":
    unittest.main()
