# trave (Dart)

A reader and writer of [Trave](https://github.com/edde746/trave) files for Dart 3.5 or newer, with no dependencies.
The format is in the repository's README; this package reads and writes the same bytes as the reference `trave.py`.

Native platforms only (Android, iOS, Linux, macOS, Windows, the Dart VM): int columns decode to `Int64List`, which
the web does not support.

The package takes and gives **unpacked** bytes: decompress a gzip or zstd file first, for example with `dart:io`'s
`gzip.decode` or a zstd package of your choice.

## Depending on it

The package is not published to pub.dev; depend on it from git:

```yaml
dependencies:
  trave:
    git:
      url: https://github.com/edde746/trave.git
      path: dart
```

Add `ref:` with a commit or tag to pin a version.

## API

```dart
const int traveAbsent = -9223372036854775808; // an absent int or int_delta value

abstract final class TraveType {
  static const int integer = 1;       // int
  static const int integerDelta = 2;  // int_delta
  static const int string = 3;        // str
  static const int dictionary = 4;    // dict
  static const int integerList = 5;   // int_list
  static const int stringList = 6;    // str_list
}

TraveFile readTrave(Uint8List bytes, {String? magic});

class TraveFile {
  String magic;               // the first 4 bytes, one character per byte
  List<TraveTable> tables;    // in the file's order
  TraveTable? table(String name);
}

class TraveTable {
  String name;
  int rows;
  List<TraveColumn> columns;  // in the file's order, unknown types included
  TraveColumn? column(String name);
  Int64List ints(String name);                     // int, int_delta; traveAbsent for absent
  List<String?> strings(String name);              // str, dict; null for absent
  List<Int64List?> intLists(String name);          // int_list; null for absent
  List<List<String>?> stringLists(String name);    // str_list; null for absent
}

class TraveColumn {
  String name;
  int type;    // a TraveType, or 0, 7 to 255 for a type this library doesn't know
  int offset;  // where its data starts in the bytes given to readTrave
  int length;  // its data length
}

Uint8List packTrave(String magic, List<TraveTableData> tables);

class TraveTableData(String name, int rows, List<TraveColumnData> columns);
sealed class TraveColumnData(String name);
class TraveInts(String name, Int64List values, {bool delta = false});        // int, or int_delta
class TraveStrings(String name, List<String?> values, {bool dict = false});  // str, or dict
class TraveIntLists(String name, List<Int64List?> values);                   // int_list
class TraveStringLists(String name, List<List<String>?> values);             // str_list
```

`readTrave` checks every header (the magic when one is given, the version, varints, lengths, names) and that
nothing follows the last table, but decodes no column: each column is a slice of the given bytes, which must stay
unchanged while the file is in use. A table's decoders read a column by name, and check its data. A missing column,
a column of another type, an invalid file and every refusal of `packTrave` throw a `FormatException`.

`packTrave` writes each `dict` column's strings in the order of their first use, so the same tables always give the
same bytes. It refuses a magic that isn't 4 characters of one byte each or starts like gzip (`1f 8b`) or zstd
(`28 b5 2f fd`), two tables or two columns of a table of the same name, a column of another number of values than
its table's rows, an `int_delta` difference outside ±(2^63 - 1), and a string with an unpaired surrogate.

## Reading

```dart
import 'dart:io';
import 'dart:typed_data';

import 'package:trave/trave.dart';

void main() {
  final file = readTrave(gzip.decode(File('demo.bin.gz').readAsBytesSync()) as Uint8List, magic: 'DEMO');
  final films = file.table('films')!;
  final tmdb = films.ints('tmdb');       // [603, 604, 605]
  final titles = films.strings('title'); // ['The Matrix', 'The Matrix Reloaded', null]
  for (var i = 0; i < films.rows; i++) {
    print('${tmdb[i]} ${titles[i] ?? '?'}');
  }
}
```

## Writing

```dart
import 'dart:io';
import 'dart:typed_data';

import 'package:trave/trave.dart';

void main() {
  final data = packTrave('DEMO', [
    TraveTableData('films', 3, [
      TraveInts('tmdb', Int64List.fromList([603, 604, 605]), delta: true),
      TraveStrings('title', ['The Matrix', 'The Matrix Reloaded', null]),
      TraveStrings('kind', ['film', 'film', 'film'], dict: true),
    ]),
  ]);
  File('demo.bin').writeAsBytesSync(data);
}
```

## Tests

```sh
dart pub get
dart test
```

The tests read the repository's `../vectors.json`.
