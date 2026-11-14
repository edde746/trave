package dev.edde746.trave

/** A table for [Trave.pack]: its [name], its number of [rows], and its [columns], each holding [rows] values. */
public class TableData(
    /** The table's name, unique in its file. */
    public val name: String,
    /** The table's number of rows. */
    public val rows: Int,
    /** The table's columns, in the order they are written; names unique in the table. */
    public val columns: List<ColumnData>,
)

/** A column for [Trave.pack]: its [name], unique in its table, and one value per row of its table. */
public sealed class ColumnData(
    /** The column's name, unique in its table. */
    public val name: String,
) {
    /**
     * An [Trave.INT] column, or an [Trave.INT_DELTA] one if [delta]. [Trave.ABSENT] in [values] is an absent value; with
     * [delta], each difference from the previous present value (0 before the first) must be within ±(2^63 - 1).
     */
    public class Ints(
        name: String,
        /** One value per row, [Trave.ABSENT] for an absent one. */
        public val values: LongArray,
        /** Whether to store each value as its difference from the previous present value. */
        public val delta: Boolean = false,
    ) : ColumnData(name)

    /** A [Trave.STR] column, or a [Trave.DICT] one if [dict]. Null in [values] is an absent value. */
    public class Strings(
        name: String,
        /** One value per row, null for an absent one. */
        public val values: List<String?>,
        /** Whether to store the strings once each in a dictionary, in order of first use. */
        public val dict: Boolean = false,
    ) : ColumnData(name)

    /** A [Trave.INT_LIST] column. Null in [values] is an absent list, distinct from an empty one. */
    public class IntLists(
        name: String,
        /** One list per row, null for an absent one. */
        public val values: List<LongArray?>,
    ) : ColumnData(name)

    /** A [Trave.STR_LIST] column. Null in [values] is an absent list, distinct from an empty one. */
    public class StringLists(
        name: String,
        /** One list per row, null for an absent one. */
        public val values: List<List<String>?>,
    ) : ColumnData(name)
}

internal fun packFile(
    magic: String,
    tables: List<TableData>,
): ByteArray {
    if (magic.length != 4 || magic.any { it.code > 0xFF }) {
        throw TraveException("magic \"$magic\": 4 bytes (ISO-8859-1 chars) expected")
    }
    val m = IntArray(4) { magic[it].code }
    if ((m[0] == 0x1f && m[1] == 0x8b) || (m[0] == 0x28 && m[1] == 0xb5 && m[2] == 0x2f && m[3] == 0xfd)) {
        throw TraveException("magic \"$magic\" starts like gzip or zstd")
    }
    val tableNames = HashSet<String>()
    for (table in tables) {
        if (!tableNames.add(table.name)) throw TraveException("two tables named ${table.name}")
    }
    val out = Out()
    for (b in m) out.byte(b)
    out.byte(Trave.VERSION)
    out.varint(tables.size.toLong())
    val body = Out()
    for (table in tables) {
        if (table.rows < 0) throw TraveException("${table.name}: ${table.rows} rows")
        val columnNames = HashSet<String>()
        for (column in table.columns) {
            if (!columnNames.add(column.name)) throw TraveException("${table.name}: two columns named ${column.name}")
        }
        out.string(table.name, table.name)
        out.varint(table.rows.toLong())
        out.varint(table.columns.size.toLong())
        for (column in table.columns) {
            val context = "${table.name}.${column.name}"
            val count = column.valueCount()
            if (count != table.rows) throw TraveException("$context: $count values for ${table.rows} rows")
            body.size = 0
            val type = body.column(column, context)
            out.string(column.name, context)
            out.byte(type)
            out.varint(body.size.toLong())
            out.bytes(body.buffer, body.size)
        }
    }
    return out.buffer.copyOf(out.size)
}

private fun ColumnData.valueCount(): Int =
    when (this) {
        is ColumnData.Ints -> values.size
        is ColumnData.Strings -> values.size
        is ColumnData.IntLists -> values.size
        is ColumnData.StringLists -> values.size
    }

/** A growable byte buffer: [buffer]'s first [size] bytes. */
private class Out {
    var buffer = ByteArray(256)
    var size = 0

    private fun reserve(extra: Int) {
        val needed = size + extra
        if (needed < 0) throw TraveException("over 2^31 - 1 bytes")
        if (needed > buffer.size) {
            buffer = buffer.copyOf(if (buffer.size > Int.MAX_VALUE / 2) needed else maxOf(buffer.size * 2, needed))
        }
    }

    fun byte(b: Int) {
        reserve(1)
        buffer[size++] = b.toByte()
    }

    fun bytes(
        b: ByteArray,
        length: Int = b.size,
    ) {
        reserve(length)
        b.copyInto(buffer, size, 0, length)
        size += length
    }

    /** [n] as an unsigned LEB128 varint. */
    fun varint(n: Long) {
        reserve(10)
        var v = n
        while (v ushr 7 != 0L) {
            buffer[size++] = (v and 0x7F or 0x80).toByte()
            v = v ushr 7
        }
        buffer[size++] = v.toByte()
    }

    /** [s]'s UTF-8 byte length as a varint, then its bytes. */
    fun string(
        s: String,
        context: String,
    ) {
        val b = utf8(s, context)
        varint(b.size.toLong())
        bytes(b)
    }

    /** Writes [column]'s values and returns its type. */
    fun column(
        column: ColumnData,
        context: String,
    ): Int =
        when (column) {
            is ColumnData.Ints -> {
                var previous = 0L
                for (v in column.values) {
                    if (v == Trave.ABSENT) {
                        byte(0)
                    } else if (column.delta) {
                        val difference = v - previous
                        // v and previous are within ±(2^63 - 1): the subtraction overflowed if the signs differ and the
                        // result's sign isn't v's.
                        if ((v xor previous) and (v xor difference) < 0 || difference == Long.MIN_VALUE) {
                            throw TraveException("$context: difference from $previous to $v outside ±(2^63 - 1)")
                        }
                        varint(zigzag(difference) + 1)
                        previous = v
                    } else {
                        varint(zigzag(v) + 1)
                    }
                }
                if (column.delta) Trave.INT_DELTA else Trave.INT
            }

            is ColumnData.Strings -> {
                if (column.dict) {
                    val index = LinkedHashMap<String, Int>()
                    for (v in column.values) if (v != null && v !in index) index[v] = index.size
                    varint(index.size.toLong())
                    for (word in index.keys) string(word, context)
                    for (v in column.values) varint(if (v == null) 0L else index.getValue(v) + 1L)
                    Trave.DICT
                } else {
                    for (v in column.values) {
                        if (v == null) {
                            byte(0)
                        } else {
                            val b = utf8(v, context)
                            varint(b.size + 1L)
                            bytes(b)
                        }
                    }
                    Trave.STR
                }
            }

            is ColumnData.IntLists -> {
                for (list in column.values) {
                    if (list == null) {
                        byte(0)
                    } else {
                        varint(list.size + 1L)
                        for (x in list) varint(zigzag(x))
                    }
                }
                Trave.INT_LIST
            }

            is ColumnData.StringLists -> {
                for (list in column.values) {
                    if (list == null) {
                        byte(0)
                    } else {
                        varint(list.size + 1L)
                        for (x in list) string(x, context)
                    }
                }
                Trave.STR_LIST
            }
        }
}

private fun zigzag(n: Long): Long = (n shl 1) xor (n shr 63)

private fun utf8(
    s: String,
    context: String,
): ByteArray =
    try {
        s.encodeToByteArray(0, s.length, throwOnInvalidSequence = true)
    } catch (_: CharacterCodingException) {
        throw TraveException("$context: a string that is not valid UTF-16")
    }
