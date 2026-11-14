/// Reads and writes Trave files: tables of named, typed columns, as the
/// format in the repository's README describes them.
///
/// [readTrave] reads a file's headers and keeps each column as a slice of the
/// file; a table's decoders ([TraveTable.ints], [TraveTable.strings],
/// [TraveTable.intLists], [TraveTable.stringLists]) decode one column on
/// demand. [packTrave] writes tables to the same bytes as the reference
/// `trave.py`.
///
/// Both take and give unpacked bytes: decompress a gzip or zstd file before
/// reading it.
///
/// Every format error and every refusal of the writer is a [FormatException].
///
/// Native platforms only: integers are 64-bit and int columns decode to
/// [Int64List], which the web does not support.
library;

import 'dart:convert';
import 'dart:typed_data';

/// An absent value in an `int` or `int_delta` column, -2^63.
///
/// No `int` or `int_delta` value can be -2^63, so [TraveTable.ints] gives
/// this for an absent value and [TraveInts] writes it as absent.
const int traveAbsent = -9223372036854775808;

/// The largest `int` or `int_delta` value and `int_delta` difference, 2^63 - 1.
const int _maxInt = 9223372036854775807;

const int _version = 1;

/// The column types, one byte each in a file.
///
/// A file may hold columns of other types (0, 7 to 255): [TraveTable.columns]
/// lists them with their number, and no decoder accepts them.
abstract final class TraveType {
  /// Type 1, the spec's `int`: a signed number.
  static const int integer = 1;

  /// Type 2, the spec's `int_delta`: a signed number, stored as its difference
  /// from the column's previous present value.
  static const int integerDelta = 2;

  /// Type 3, the spec's `str`: a UTF-8 string.
  static const int string = 3;

  /// Type 4, the spec's `dict`: a string from the column's dictionary.
  static const int dictionary = 4;

  /// Type 5, the spec's `int_list`: a list of signed numbers.
  static const int integerList = 5;

  /// Type 6, the spec's `str_list`: a list of UTF-8 strings.
  static const int stringList = 6;
}

// --- reading -----------------------------------------------------------------

/// A read file: its magic and its tables, in the file's order.
final class TraveFile {
  TraveFile._(this.magic, this.tables, this._byName);

  /// The file's first 4 bytes, one character per byte (Latin-1).
  final String magic;

  /// The file's tables, in the file's order.
  final List<TraveTable> tables;

  final Map<String, TraveTable> _byName;

  /// The table named [name], or null if the file has none.
  TraveTable? table(String name) => _byName[name];
}

/// A table of a read file: its name, its number of rows, and its columns.
///
/// The decoders read one column, by name, and throw a [FormatException] if
/// the table has no column of that name, if the column's type is not one the
/// decoder reads, or if its data is not valid.
final class TraveTable {
  TraveTable._(this._bytes, this.name, this.rows, this.columns, this._byName);

  final Uint8List _bytes;

  /// The table's name.
  final String name;

  /// The table's number of rows: every column has one value per row.
  final int rows;

  /// The table's columns, in the file's order, including those of a type
  /// this library does not know.
  final List<TraveColumn> columns;

  final Map<String, TraveColumn> _byName;

  /// The column named [name], or null if the table has none.
  TraveColumn? column(String name) => _byName[name];

  /// The values of the `int` or `int_delta` column [name], [traveAbsent] for
  /// an absent value.
  Int64List ints(String name) {
    final column = _find(name, TraveType.integer, TraveType.integerDelta);
    final r = _open(column);
    final out = Int64List(rows);
    if (column.type == TraveType.integer) {
      for (var i = 0; i < rows; i++) {
        final tag = r.varint();
        out[i] = tag == 0 ? traveAbsent : _unzigzag(tag - 1);
      }
    } else {
      var previous = 0;
      for (var i = 0; i < rows; i++) {
        final tag = r.varint();
        if (tag == 0) {
          out[i] = traveAbsent;
          continue;
        }
        final difference = _unzigzag(tag - 1);
        if (difference > 0
            ? previous > _maxInt - difference
            : previous < -_maxInt - difference) {
          throw r.error('an int_delta value outside ±(2^63 - 1)');
        }
        previous += difference;
        out[i] = previous;
      }
    }
    r.done();
    return out;
  }

  /// The values of the `str` or `dict` column [name], null for an absent
  /// value. The values of a `dict` column share one [String] per dictionary
  /// string.
  List<String?> strings(String name) {
    final column = _find(name, TraveType.string, TraveType.dictionary);
    final r = _open(column);
    final out = List<String?>.filled(rows, null);
    if (column.type == TraveType.string) {
      for (var i = 0; i < rows; i++) {
        final tag = r.varint();
        if (tag != 0) out[i] = r.text(tag - 1);
      }
    } else {
      final count = r.count();
      final words = List<String>.generate(count, (_) => r.string());
      for (var i = 0; i < rows; i++) {
        final tag = r.varint();
        if (tag == 0) continue;
        final index = tag - 1;
        if (index < 0 || index >= count) {
          throw r.error('dictionary string ${_u64(index)} of $count');
        }
        out[i] = words[index];
      }
    }
    r.done();
    return out;
  }

  /// The values of the `int_list` column [name], null for an absent list
  /// (an empty list is present).
  List<Int64List?> intLists(String name) {
    final r = _open(_find(name, TraveType.integerList, TraveType.integerList));
    final out = List<Int64List?>.filled(rows, null);
    for (var i = 0; i < rows; i++) {
      final tag = r.varint();
      if (tag == 0) continue;
      final items = Int64List(r.checkCount(tag - 1));
      for (var j = 0; j < items.length; j++) {
        items[j] = _unzigzag(r.varint());
      }
      out[i] = items;
    }
    r.done();
    return out;
  }

  /// The values of the `str_list` column [name], null for an absent list
  /// (an empty list is present).
  List<List<String>?> stringLists(String name) {
    final r = _open(_find(name, TraveType.stringList, TraveType.stringList));
    final out = List<List<String>?>.filled(rows, null);
    for (var i = 0; i < rows; i++) {
      final tag = r.varint();
      if (tag == 0) continue;
      out[i] = List<String>.generate(
        r.checkCount(tag - 1),
        (_) => r.string(),
        growable: false,
      );
    }
    r.done();
    return out;
  }

  TraveColumn _find(String column, int type, int otherType) {
    final found = _byName[column];
    if (found == null) {
      throw FormatException('Trave: table $name has no column $column');
    }
    if (found.type != type && found.type != otherType) {
      throw FormatException(
        'Trave: $name.$column is of type ${found.type}, not $type'
        '${otherType == type ? '' : ' or $otherType'}',
      );
    }
    return found;
  }

  _Cursor _open(TraveColumn column) {
    final r = _Cursor(
      _bytes,
      column.offset,
      column.offset + column.length,
      '$name.${column.name}: ',
    );
    // Every value takes at least one byte.
    if (rows > column.length) {
      throw r.error('$rows rows in ${column.length} bytes');
    }
    return r;
  }
}

/// A column of a read table: its name, its type, and where its data is in the
/// file's bytes, which it does not copy.
final class TraveColumn {
  TraveColumn._(this.name, this.type, this.offset, this.length);

  /// The column's name.
  final String name;

  /// The column's type: one of [TraveType]'s, or another number (0, 7 to
  /// 255) for a type this library does not know.
  final int type;

  /// Where the column's data starts in the bytes given to [readTrave].
  final int offset;

  /// The column's data length in bytes.
  final int length;
}

/// Reads the headers of the Trave file [bytes], which must not be compressed.
///
/// Checks the magic against [magic] when one is given, the version, every
/// varint, length and name, and that nothing follows the last table. Column
/// data is decoded later, by the table's decoders, from [bytes]: keep them
/// unchanged while the result is in use.
///
/// Throws a [FormatException] if the file is not valid.
TraveFile readTrave(Uint8List bytes, {String? magic}) {
  if (bytes.length < 4) throw const FormatException('Trave: cut short');
  final found = String.fromCharCodes(bytes, 0, 4);
  if (magic != null && found != magic) {
    throw FormatException('Trave: magic $found, not $magic');
  }
  final r = _Cursor(bytes, 4, bytes.length, '');
  final version = r.byte();
  if (version != _version) {
    throw FormatException('Trave: version $version, not $_version');
  }
  final tableCount = r.count();
  final tables = <TraveTable>[];
  final tablesByName = <String, TraveTable>{};
  for (var t = 0; t < tableCount; t++) {
    final name = r.string();
    final rows = r.varint();
    if (tablesByName.containsKey(name)) {
      throw FormatException('Trave: two tables named $name');
    }
    if (rows < 0) {
      throw FormatException('Trave: $name: ${_u64(rows)} rows, over 2^63 - 1');
    }
    final columnCount = r.count();
    final columns = <TraveColumn>[];
    final columnsByName = <String, TraveColumn>{};
    for (var c = 0; c < columnCount; c++) {
      final columnName = r.string();
      final type = r.byte();
      final length = r.count();
      if (columnsByName.containsKey(columnName)) {
        throw FormatException('Trave: $name: two columns named $columnName');
      }
      final column = TraveColumn._(columnName, type, r.pos, length);
      r.pos += length;
      columns.add(column);
      columnsByName[columnName] = column;
    }
    final table = TraveTable._(
      bytes,
      name,
      rows,
      List.unmodifiable(columns),
      columnsByName,
    );
    tables.add(table);
    tablesByName[name] = table;
  }
  if (r.pos != bytes.length) {
    throw FormatException(
      'Trave: ${bytes.length - r.pos} bytes after the last table',
    );
  }
  return TraveFile._(found, List.unmodifiable(tables), tablesByName);
}

/// Reads [bytes] from [pos] to [end], naming [where] in its errors.
final class _Cursor {
  _Cursor(this.bytes, this.pos, this.end, this.where);

  final Uint8List bytes;
  int pos;
  final int end;
  final String where;

  FormatException error(String message) =>
      FormatException('Trave: $where$message');

  int byte() {
    if (pos >= end) throw error('cut short');
    return bytes[pos++];
  }

  /// An unsigned varint, as the bits of a 64-bit integer: one of 2^63 or more
  /// is negative.
  int varint() {
    var n = 0;
    for (var shift = 0;; shift += 7) {
      final b = byte();
      if (shift == 63) {
        if (b >= 0x80) throw error('a varint longer than 10 bytes');
        if (b > 1) throw error('a varint over 2^64 - 1');
        return n | b << 63;
      }
      n |= (b & 0x7F) << shift;
      if (b < 0x80) return n;
    }
  }

  /// [n], the unsigned number of things that follow, each taking at least
  /// one byte.
  int checkCount(int n) {
    if (n < 0 || n > end - pos) {
      throw error('${_u64(n)} items or bytes in ${end - pos} bytes');
    }
    return n;
  }

  /// A varint counting things that follow, each taking at least one byte.
  int count() => checkCount(varint());

  /// A string: a varint byte length and that many bytes of UTF-8.
  String string() => text(count());

  /// [length] bytes of UTF-8, [length] unsigned.
  String text(int length) {
    final start = pos;
    pos += checkCount(length);
    final String s;
    try {
      s = _utf8.convert(bytes, start, pos);
    } on FormatException {
      throw error('a string that is not UTF-8');
    }
    // The decoder drops a leading byte order mark, which is part of the string.
    if (length >= 3 &&
        bytes[start] == 0xEF &&
        bytes[start + 1] == 0xBB &&
        bytes[start + 2] == 0xBF) {
      return '\uFEFF$s';
    }
    return s;
  }

  /// The column's data ends exactly here.
  void done() {
    if (pos != end) throw error('${end - pos} bytes left in a column');
  }
}

const _utf8 = Utf8Decoder(allowMalformed: false);

int _unzigzag(int n) => (n >>> 1) ^ -(n & 1);

int _zigzag(int n) => (n << 1) ^ (n >> 63);

/// [n]'s bits as an unsigned 64-bit number, for messages.
String _u64(int n) => n >= 0 ? '$n' : BigInt.from(n).toUnsigned(64).toString();

// --- writing -----------------------------------------------------------------

/// A table to write: its name, its number of rows, and its columns, each with
/// one value per row.
final class TraveTableData {
  /// A table named [name] of [rows] rows.
  const TraveTableData(this.name, this.rows, this.columns);

  /// The table's name, unique in the file.
  final String name;

  /// The table's number of rows.
  final int rows;

  /// The table's columns, their names unique in the table.
  final List<TraveColumnData> columns;
}

/// A column to write: its name and one value per row.
sealed class TraveColumnData {
  const TraveColumnData(this.name);

  /// The column's name, unique in its table.
  final String name;
}

/// An `int` column, or an `int_delta` one if [delta]: [traveAbsent] in
/// [values] is an absent value.
///
/// An `int_delta` column suits sorted numbers: each value is stored as its
/// difference from the previous present one, which must be within
/// ±(2^63 - 1).
final class TraveInts extends TraveColumnData {
  /// An `int` column, or an `int_delta` one if [delta].
  const TraveInts(super.name, this.values, {this.delta = false});

  /// The values, [traveAbsent] for an absent value.
  final Int64List values;

  /// Whether the column is `int_delta` rather than `int`.
  final bool delta;
}

/// A `str` column, or a `dict` one if [dict]: null in [values] is an absent
/// value.
///
/// A `dict` column suits repeated strings: it stores each string once, in
/// the order of first use, and each value as its number.
final class TraveStrings extends TraveColumnData {
  /// A `str` column, or a `dict` one if [dict].
  const TraveStrings(super.name, this.values, {this.dict = false});

  /// The values, null for an absent value.
  final List<String?> values;

  /// Whether the column is `dict` rather than `str`.
  final bool dict;
}

/// An `int_list` column: null in [values] is an absent list, distinct from an
/// empty one. Items cover the whole 64-bit range.
final class TraveIntLists extends TraveColumnData {
  /// An `int_list` column.
  const TraveIntLists(super.name, this.values);

  /// The values, null for an absent list.
  final List<Int64List?> values;
}

/// A `str_list` column: null in [values] is an absent list, distinct from an
/// empty one.
final class TraveStringLists extends TraveColumnData {
  /// A `str_list` column.
  const TraveStringLists(super.name, this.values);

  /// The values, null for an absent list.
  final List<List<String>?> values;
}

/// The bytes of a Trave file of [tables] with the 4-byte [magic], not
/// compressed: the same as the reference `trave.py` writes.
///
/// [magic] is 4 characters, one per byte (Latin-1), and can't start like gzip
/// (`1f 8b`) or zstd (`28 b5 2f fd`).
///
/// Throws a [FormatException] for another magic, two tables or two columns of
/// a table of the same name, a column of another number of values than its
/// table's rows, a string with an unpaired surrogate, or an `int_delta`
/// difference outside ±(2^63 - 1).
Uint8List packTrave(String magic, List<TraveTableData> tables) {
  final m = magic.codeUnits;
  if (m.length != 4 ||
      m.any((c) => c > 0xFF) ||
      (m[0] == 0x1F && m[1] == 0x8B) ||
      (m[0] == 0x28 && m[1] == 0xB5 && m[2] == 0x2F && m[3] == 0xFD)) {
    throw FormatException(
      'Trave: magic $magic: 4 bytes expected, not starting like gzip or zstd',
    );
  }
  final out = _Out();
  for (final c in m) {
    out.byte(c);
  }
  out.byte(_version);
  out.varint(tables.length);
  final tableNames = <String>{};
  for (final table in tables) {
    if (!tableNames.add(table.name)) {
      throw FormatException('Trave: two tables named ${table.name}');
    }
  }
  final body = _Out();
  for (final table in tables) {
    final rows = table.rows;
    if (rows < 0) throw FormatException('Trave: ${table.name}: $rows rows');
    out.text(table.name, 0, '${table.name}: ');
    out.varint(rows);
    out.varint(table.columns.length);
    final columnNames = <String>{};
    for (final column in table.columns) {
      if (!columnNames.add(column.name)) {
        throw FormatException(
          'Trave: ${table.name}: two columns named ${column.name}',
        );
      }
    }
    for (final column in table.columns) {
      final where = '${table.name}.${column.name}: ';
      body.length = 0;
      final type = _writeColumn(column, rows, body, where);
      out.text(column.name, 0, where);
      out.byte(type);
      out.varint(body.length);
      out.append(body);
    }
  }
  return out.toBytes();
}

/// Writes [column]'s data to [out] and gives its type.
int _writeColumn(TraveColumnData column, int rows, _Out out, String where) {
  void checkRows(int length) {
    if (length != rows) {
      throw FormatException('Trave: $where$length values for $rows rows');
    }
  }

  switch (column) {
    case TraveInts(:final values, :final delta):
      checkRows(values.length);
      var previous = 0;
      for (final v in values) {
        if (v == traveAbsent) {
          out.byte(0);
        } else if (!delta) {
          out.varint(_zigzag(v) + 1);
        } else {
          // v - previous within ±(2^63 - 1), without overflowing.
          if (previous >= 0 ? v < previous - _maxInt : v > _maxInt + previous) {
            throw FormatException(
              'Trave: ${where}difference from $previous to $v outside '
              '±(2^63 - 1)',
            );
          }
          out.varint(_zigzag(v - previous) + 1);
          previous = v;
        }
      }
      return delta ? TraveType.integerDelta : TraveType.integer;
    case TraveStrings(:final values, dict: false):
      checkRows(values.length);
      for (final v in values) {
        if (v == null) {
          out.byte(0);
        } else {
          out.text(v, 1, where);
        }
      }
      return TraveType.string;
    case TraveStrings(:final values, dict: true):
      checkRows(values.length);
      final index = <String, int>{};
      for (final v in values) {
        if (v != null) index.putIfAbsent(v, () => index.length);
      }
      out.varint(index.length);
      for (final word in index.keys) {
        out.text(word, 0, where);
      }
      for (final v in values) {
        out.varint(v == null ? 0 : index[v]! + 1);
      }
      return TraveType.dictionary;
    case TraveIntLists(:final values):
      checkRows(values.length);
      for (final list in values) {
        if (list == null) {
          out.byte(0);
          continue;
        }
        out.varint(list.length + 1);
        for (final item in list) {
          out.varint(_zigzag(item));
        }
      }
      return TraveType.integerList;
    case TraveStringLists(:final values):
      checkRows(values.length);
      for (final list in values) {
        if (list == null) {
          out.byte(0);
          continue;
        }
        out.varint(list.length + 1);
        for (final item in list) {
          out.text(item, 0, where);
        }
      }
      return TraveType.stringList;
  }
}

/// A growing byte buffer.
final class _Out {
  Uint8List _bytes = Uint8List(256);

  /// The number of bytes written; set to 0 to start again.
  int length = 0;

  void _reserve(int n) {
    if (length + n <= _bytes.length) return;
    var capacity = _bytes.length * 2;
    while (capacity < length + n) {
      capacity *= 2;
    }
    _bytes = Uint8List(capacity)..setRange(0, length, _bytes);
  }

  void byte(int b) {
    _reserve(1);
    _bytes[length++] = b;
  }

  /// [n]'s bits as an unsigned varint.
  void varint(int n) {
    _reserve(10);
    while (n & ~0x7F != 0) {
      _bytes[length++] = n & 0x7F | 0x80;
      n >>>= 7;
    }
    _bytes[length++] = n;
  }

  void append(_Out other) {
    _reserve(other.length);
    _bytes.setRange(length, length + other.length, other._bytes);
    length += other.length;
  }

  /// [s] as UTF-8 after a varint of its byte length plus [extra].
  void text(String s, int extra, String where) {
    var size = 0;
    for (var i = 0; i < s.length; i++) {
      final c = s.codeUnitAt(i);
      if (c < 0x80) {
        size += 1;
      } else if (c < 0x800) {
        size += 2;
      } else if (c & 0xF800 != 0xD800) {
        size += 3;
      } else if (c < 0xDC00 &&
          i + 1 < s.length &&
          s.codeUnitAt(i + 1) & 0xFC00 == 0xDC00) {
        size += 4;
        i++;
      } else {
        throw FormatException(
          'Trave: ${where}a string with an unpaired surrogate',
        );
      }
    }
    varint(size + extra);
    _reserve(size);
    for (var i = 0; i < s.length; i++) {
      var c = s.codeUnitAt(i);
      if (c < 0x80) {
        _bytes[length++] = c;
      } else if (c < 0x800) {
        _bytes[length++] = 0xC0 | c >> 6;
        _bytes[length++] = 0x80 | c & 0x3F;
      } else if (c & 0xF800 != 0xD800) {
        _bytes[length++] = 0xE0 | c >> 12;
        _bytes[length++] = 0x80 | c >> 6 & 0x3F;
        _bytes[length++] = 0x80 | c & 0x3F;
      } else {
        c = 0x10000 + ((c & 0x3FF) << 10 | s.codeUnitAt(++i) & 0x3FF);
        _bytes[length++] = 0xF0 | c >> 18;
        _bytes[length++] = 0x80 | c >> 12 & 0x3F;
        _bytes[length++] = 0x80 | c >> 6 & 0x3F;
        _bytes[length++] = 0x80 | c & 0x3F;
      }
    }
  }

  Uint8List toBytes() => _bytes.sublist(0, length);
}
