package dev.edde746.trave

/** A Trave file's headers, as parsed by [Trave.read]: its [magic] and its [tables] in the file's order. */
public class TraveFile internal constructor(
    /** The file's 4-byte magic, one char per byte (ISO-8859-1). */
    public val magic: String,
    /** The file's tables, in the file's order. */
    public val tables: List<TraveTable>,
) {
    private val byName: Map<String, TraveTable> = tables.associateBy { it.name }

    /** The table named [name], or null if the file has none. */
    public fun table(name: String): TraveTable? = byName[name]
}

/**
 * A table: its [name], its number of [rows] and its [columns] in the file's order, columns of unknown types included.
 *
 * Its decoders take a column's name, decode and check that column's data, and return one value per row. A missing
 * column, a column of another type, or invalid data is a [TraveException].
 */
public class TraveTable internal constructor(
    /** The table's name. */
    public val name: String,
    /** The table's number of rows: every column holds this many values. */
    public val rows: Int,
    /** The table's columns, in the file's order, columns of unknown types included. */
    public val columns: List<TraveColumn>,
    private val bytes: ByteArray,
) {
    private val byName: Map<String, TraveColumn> = columns.associateBy { it.name }

    /** The column named [name], or null if the table has none. */
    public fun column(name: String): TraveColumn? = byName[name]

    /** The values of the [Trave.INT] or [Trave.INT_DELTA] column [name], [Trave.ABSENT] for an absent value. */
    public fun ints(name: String): LongArray {
        val column = column(name, Trave.INT, Trave.INT_DELTA)
        val r = cursor(column)
        val delta = column.type == Trave.INT_DELTA
        val values = LongArray(rows)
        var previous = 0L
        for (i in 0 until rows) {
            val tag = r.varint()
            if (tag == 0L) {
                values[i] = Trave.ABSENT
                continue
            }
            val n = unzigzag(tag - 1)
            if (delta) {
                val sum = previous + n
                if ((previous xor sum) and (n xor sum) < 0 || sum == Long.MIN_VALUE) {
                    r.fail("an int_delta value outside ±(2^63 - 1)")
                }
                previous = sum
                values[i] = sum
            } else {
                values[i] = n
            }
        }
        r.finish()
        return values
    }

    /** The values of the [Trave.STR] or [Trave.DICT] column [name], null for an absent value. */
    public fun strings(name: String): Array<String?> {
        val column = column(name, Trave.STR, Trave.DICT)
        val r = cursor(column)
        val words = if (column.type == Trave.DICT) Array(r.count(r.varint())) { r.string() } else null
        val values = arrayOfNulls<String>(rows)
        for (i in 0 until rows) {
            val tag = r.varint()
            if (tag == 0L) continue
            if (words == null) {
                values[i] = r.string(r.count(tag - 1))
            } else {
                val index = tag - 1
                if (index < 0 || index >= words.size) {
                    r.fail("dictionary string ${index.toULong()} of ${words.size}")
                }
                values[i] = words[index.toInt()]
            }
        }
        r.finish()
        return values
    }

    /** The values of the [Trave.INT_LIST] column [name], null for an absent list. */
    public fun intLists(name: String): Array<LongArray?> {
        val r = cursor(column(name, Trave.INT_LIST))
        val values = arrayOfNulls<LongArray>(rows)
        for (i in 0 until rows) {
            val tag = r.varint()
            if (tag == 0L) continue
            values[i] = LongArray(r.count(tag - 1)) { unzigzag(r.varint()) }
        }
        r.finish()
        return values
    }

    /** The values of the [Trave.STR_LIST] column [name], null for an absent list. */
    public fun stringLists(name: String): Array<List<String>?> {
        val r = cursor(column(name, Trave.STR_LIST))
        val values = arrayOfNulls<List<String>>(rows)
        for (i in 0 until rows) {
            val tag = r.varint()
            if (tag == 0L) continue
            values[i] = List(r.count(tag - 1)) { r.string() }
        }
        r.finish()
        return values
    }

    private fun column(
        name: String,
        type: Int,
        other: Int = type,
    ): TraveColumn {
        val column = byName[name] ?: throw TraveException("${this.name}: no column named $name")
        if (column.type != type && column.type != other) {
            val expected = if (other == type) typeName(type) else "${typeName(type)} or ${typeName(other)}"
            throw TraveException("${this.name}.$name: ${typeName(column.type)}, not $expected")
        }
        return column
    }

    /** A cursor over [column]'s data, once its byte length can hold a value for every row. */
    private fun cursor(column: TraveColumn): Cursor {
        val r = Cursor(bytes, column.offset, column.offset + column.length, "$name.${column.name}: ")
        if (rows > column.length) r.fail("$rows rows in ${column.length} bytes")
        return r
    }
}

/**
 * A column's header: its [name], its [type] (one of [Trave]'s type constants, or any other byte for a type this library
 * doesn't know) and where its data lies in the file's bytes, from [offset] for [length] bytes. Nothing is copied.
 */
public class TraveColumn internal constructor(
    /** The column's name. */
    public val name: String,
    /** The column's type byte, 0 to 255. */
    public val type: Int,
    /** The index of the column's first data byte in the file's bytes. */
    public val offset: Int,
    /** The length of the column's data in bytes. */
    public val length: Int,
)

internal fun readFile(
    bytes: ByteArray,
    magic: String?,
): TraveFile {
    val r = Cursor(bytes, 0, bytes.size, "")
    if (bytes.size < 4) r.fail("cut short: no magic")
    val found = CharArray(4) { (bytes[it].toInt() and 0xFF).toChar() }.concatToString()
    r.pos = 4
    if (magic != null && found != magic) r.fail("magic \"$found\", not \"$magic\"")
    val version = r.byte()
    if (version != Trave.VERSION) r.fail("version $version, not ${Trave.VERSION}")
    val tables = ArrayList<TraveTable>()
    val tableNames = HashSet<String>()
    repeat(r.count(r.varint())) {
        val name = r.string()
        if (!tableNames.add(name)) r.fail("two tables named $name")
        val rows = r.varint()
        if (rows < 0 || rows > Int.MAX_VALUE) r.fail("$name: ${rows.toULong()} rows, over ${Int.MAX_VALUE}")
        val columns = ArrayList<TraveColumn>()
        val columnNames = HashSet<String>()
        repeat(r.count(r.varint())) {
            val columnName = r.string()
            if (!columnNames.add(columnName)) r.fail("$name: two columns named $columnName")
            val type = r.byte()
            val length = r.count(r.varint())
            columns += TraveColumn(columnName, type, r.pos, length)
            r.pos += length
        }
        tables += TraveTable(name, rows.toInt(), columns, bytes)
    }
    if (r.pos != bytes.size) r.fail("${bytes.size - r.pos} bytes after the last table")
    return TraveFile(found, tables)
}

/** Reads [bytes] from [pos] up to [end]; every error message starts with [context]. */
private class Cursor(
    private val bytes: ByteArray,
    var pos: Int,
    private val end: Int,
    private val context: String,
) {
    fun fail(message: String): Nothing = throw TraveException(context + message)

    fun byte(): Int {
        if (pos >= end) fail("cut short")
        return bytes[pos++].toInt() and 0xFF
    }

    /** An unsigned LEB128 varint of at most 10 bytes, its 64 bits in a Long. */
    fun varint(): Long {
        var n = 0L
        var shift = 0
        while (true) {
            val b = byte()
            if (shift == 63 && b > 1) fail(if (b >= 0x80) "a varint longer than 10 bytes" else "a varint over 2^64 - 1")
            n = n or ((b and 0x7F).toLong() shl shift)
            if (b < 0x80) return n
            shift += 7
        }
    }

    /** [n], unsigned, as a count of things of at least one byte each that the remaining bytes can hold. */
    fun count(n: Long): Int {
        if (n < 0 || n > end - pos) fail("cut short: ${n.toULong()} past the end")
        return n.toInt()
    }

    /** A varint byte length, then that many bytes of UTF-8. */
    fun string(): String = string(count(varint()))

    /** [length] bytes of UTF-8. */
    fun string(length: Int): String {
        val start = pos
        pos += length
        try {
            return bytes.decodeToString(start, pos, throwOnInvalidSequence = true)
        } catch (_: CharacterCodingException) {
            fail("a string that is not UTF-8")
        }
    }

    fun finish() {
        if (pos != end) fail("${end - pos} bytes left in the column")
    }
}

private fun unzigzag(n: Long): Long = (n ushr 1) xor -(n and 1)

private fun typeName(type: Int): String =
    when (type) {
        Trave.INT -> "int"
        Trave.INT_DELTA -> "int_delta"
        Trave.STR -> "str"
        Trave.DICT -> "dict"
        Trave.INT_LIST -> "int_list"
        Trave.STR_LIST -> "str_list"
        else -> "type $type"
    }
