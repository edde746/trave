import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:test/test.dart';
import 'package:trave/trave.dart';

const _types = {
  'int': TraveType.integer,
  'int_delta': TraveType.integerDelta,
  'str': TraveType.string,
  'dict': TraveType.dictionary,
  'int_list': TraveType.integerList,
  'str_list': TraveType.stringList,
};

final Map<String, dynamic> _vectors =
    jsonDecode(File('../vectors.json').readAsStringSync())
        as Map<String, dynamic>;
final String _magic = _vectors['magic'] as String;
final List<Map<String, dynamic>> _all =
    (_vectors['vectors'] as List).cast<Map<String, dynamic>>();

Uint8List _hex(String hex) {
  final digits = hex.replaceAll(' ', '');
  return Uint8List.fromList([
    for (var i = 0; i < digits.length; i += 2)
      int.parse(digits.substring(i, i + 2), radix: 16),
  ]);
}

List<Map<String, dynamic>> _list(Object? json) =>
    (json as List).cast<Map<String, dynamic>>();

/// A JSON integer, which must have parsed exactly as an int.
int _int(Object? json) {
  expect(json, isA<int>());
  return json as int;
}

/// A column's decoded values, as the vectors write them: null for absent.
List<Object?> _decode(TraveTable table, TraveColumn column) =>
    switch (column.type) {
      TraveType.integer || TraveType.integerDelta => [
          for (final v in table.ints(column.name)) v == traveAbsent ? null : v,
        ],
      TraveType.string || TraveType.dictionary => table.strings(column.name),
      TraveType.integerList => table.intLists(column.name),
      TraveType.stringList => table.stringLists(column.name),
      _ => throw StateError('type ${column.type}'),
    };

/// The JSON values, ints parsed exactly and lists of ints as Int64List.
List<Object?> _expected(String type, List<Object?> values) => [
      for (final v in values)
        v == null
            ? null
            : switch (type) {
                'int' || 'int_delta' => _int(v),
                'int_list' =>
                  Int64List.fromList([for (final x in v as List) _int(x)]),
                'str_list' => (v as List).cast<String>(),
                _ => v as String,
              },
    ];

TraveColumnData _toPack(Map<String, dynamic> column) {
  final name = column['name'] as String;
  final type = column['type'] as String;
  final values = column['values'] as List;
  return switch (type) {
    'int' || 'int_delta' => TraveInts(
        name,
        Int64List.fromList(
          [for (final v in values) v == null ? traveAbsent : _int(v)],
        ),
        delta: type == 'int_delta',
      ),
    'str' ||
    'dict' =>
      TraveStrings(name, values.cast<String?>(), dict: type == 'dict'),
    'int_list' => TraveIntLists(name, [
        for (final v in values)
          v == null
              ? null
              : Int64List.fromList([for (final x in v as List) _int(x)]),
      ]),
    'str_list' => TraveStringLists(name, [
        for (final v in values) v == null ? null : (v as List).cast<String>(),
      ]),
    _ => throw StateError('type $type'),
  };
}

TraveTableData _tableData(Map<String, dynamic> table) => TraveTableData(
      table['name'] as String,
      table['rows'] as int,
      [for (final c in _list(table['columns'])) _toPack(c)],
    );

void _decodeAll(TraveFile file) {
  for (final table in file.tables) {
    for (final column in table.columns) {
      if (_types.containsValue(column.type)) _decode(table, column);
    }
  }
}

void main() {
  group('valid vectors', () {
    for (final v in _all.where((v) => !v.containsKey('error'))) {
      test(v['name'], () {
        final file = readTrave(_hex(v['hex'] as String), magic: _magic);
        final tables = _list(v['tables']);
        expect(file.magic, _magic);
        expect([for (final t in file.tables) t.name],
            [for (final t in tables) t['name']]);
        for (final (i, expected) in tables.indexed) {
          final table = file.tables[i];
          expect(file.table(table.name), same(table));
          expect(table.rows, expected['rows']);
          final known = [
            for (final c in table.columns)
              if (_types.containsValue(c.type)) c,
          ];
          final columns = _list(expected['columns']);
          expect([
            for (final c in known) (c.name, c.type)
          ], [
            for (final c in columns) (c['name'], _types[c['type']]),
          ]);
          for (final (j, column) in known.indexed) {
            final type = columns[j]['type'] as String;
            expect(table.column(column.name), same(column));
            expect(
              _decode(table, column),
              _expected(type, columns[j]['values'] as List),
              reason: '${table.name}.${column.name}',
            );
          }
        }
      });
    }
  });

  group('canonical vectors pack to the same bytes', () {
    for (final v in _all.where((v) => v['canonical'] == true)) {
      test(v['name'], () {
        final tables = [for (final t in _list(v['tables'])) _tableData(t)];
        expect(packTrave(_magic, tables), _hex(v['hex'] as String));
      });
    }
  });

  group('error vectors', () {
    for (final v in _all.where((v) => v.containsKey('error'))) {
      test('${v['name']}: ${v['error']}', () {
        expect(
          () => _decodeAll(readTrave(_hex(v['hex'] as String), magic: _magic)),
          throwsFormatException,
        );
      });
    }
  });

  group('reader', () {
    final bytes = packTrave('TEST', [
      TraveTableData('t', 2, [
        TraveStrings('k', ['a', 'a'], dict: true),
        TraveInts('i', Int64List.fromList([1, traveAbsent])),
      ]),
    ]);

    test('another magic is an error', () {
      expect(() => readTrave(bytes, magic: 'ABCD'), throwsFormatException);
      expect(readTrave(bytes).magic, 'TEST');
    });

    test('a missing table or column is null', () {
      final file = readTrave(bytes);
      expect(file.table('u'), isNull);
      expect(file.table('t')!.column('x'), isNull);
    });

    test('a missing column or another type is an error', () {
      final table = readTrave(bytes).table('t')!;
      expect(() => table.ints('x'), throwsFormatException);
      expect(() => table.ints('k'), throwsFormatException);
      expect(() => table.strings('i'), throwsFormatException);
      expect(() => table.intLists('i'), throwsFormatException);
      expect(() => table.stringLists('k'), throwsFormatException);
    });

    test('dict values share one string per dictionary string', () {
      final values = readTrave(bytes).table('t')!.strings('k');
      expect(values[0], same(values[1]));
    });

    test('a column is a slice of the file', () {
      final column = readTrave(bytes).table('t')!.column('i')!;
      expect(
          bytes.sublist(column.offset, column.offset + column.length), [3, 0]);
    });

    test('a leading byte order mark is kept', () {
      const s = '\uFEFF\uFEFFa';
      final file = readTrave(packTrave('TEST', [
        TraveTableData('t', 1, [
          TraveStrings('s', [s]),
        ]),
      ]));
      expect(file.table('t')!.strings('s'), [s]);
    });
  });

  group('writer refuses', () {
    final cases = <String, List<TraveTableData>>{
      'a delta over 2^63 - 1': [
        TraveTableData('t', 2, [
          TraveInts('d', Int64List.fromList([-1, 9223372036854775807]),
              delta: true),
        ]),
      ],
      'a delta under -(2^63 - 1)': [
        TraveTableData('t', 2, [
          TraveInts('d', Int64List.fromList([1, -9223372036854775807]),
              delta: true),
        ]),
      ],
      'values for another row count': [
        TraveTableData('t', 2, [
          TraveInts('i', Int64List.fromList([1])),
        ]),
      ],
      'two columns of one name': [
        TraveTableData('t', 0, [
          TraveInts('i', Int64List(0)),
          TraveStrings('i', []),
        ]),
      ],
      'two tables of one name': [
        const TraveTableData('t', 0, []),
        const TraveTableData('t', 0, []),
      ],
      'a string with an unpaired surrogate': [
        TraveTableData('t', 1, [
          TraveStrings('s', ['\uD800']),
        ]),
      ],
    };
    for (final MapEntry(:key, :value) in cases.entries) {
      test(key, () {
        expect(() => packTrave('TEST', value), throwsFormatException);
      });
    }

    for (final magic in [
      '\x1f\x8bAB',
      '\x28\xb5\x2f\xfd',
      'ABC',
      'ABCDE',
      'ĀBCD'
    ]) {
      test('magic ${magic.codeUnits}', () {
        expect(() => packTrave(magic, []), throwsFormatException);
      });
    }
  });

  test('ABSENT in an int column is absent, not -2^63', () {
    final file = readTrave(packTrave('TEST', [
      TraveTableData('t', 2, [
        TraveInts('i', Int64List.fromList([traveAbsent, 5])),
        TraveInts('d', Int64List.fromList([traveAbsent, 5]), delta: true),
      ]),
    ]));
    expect(file.table('t')!.ints('i'), [traveAbsent, 5]);
    expect(file.table('t')!.ints('d'), [traveAbsent, 5]);
  });
}
