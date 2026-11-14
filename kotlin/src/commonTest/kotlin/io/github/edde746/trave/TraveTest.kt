package io.github.edde746.trave

import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlin.test.Test
import kotlin.test.assertContentEquals
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith
import kotlin.test.assertNull
import kotlin.test.assertSame

private val VECTORS: JsonObject = Json.parseToJsonElement(VECTORS_JSON).jsonObject
private val MAGIC: String = VECTORS.getValue("magic").jsonPrimitive.content
private val TYPES: Map<String, Int> =
    mapOf(
        "int" to Trave.INT,
        "int_delta" to Trave.INT_DELTA,
        "str" to Trave.STR,
        "dict" to Trave.DICT,
        "int_list" to Trave.INT_LIST,
        "str_list" to Trave.STR_LIST,
    )

private fun hex(s: String): ByteArray {
    val digits = s.filter { it != ' ' }
    return ByteArray(digits.length / 2) { digits.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
}

private fun JsonObject.string(key: String): String = getValue(key).jsonPrimitive.content

private fun JsonObject.array(key: String): List<JsonElement> = getValue(key).jsonArray

/** Exact: the JSON literal parsed as a Long, never through a double. */
private fun JsonElement.long(): Long = jsonPrimitive.content.toLong()

private fun JsonElement.longs(): LongArray = jsonArray.map { it.long() }.toLongArray()

private fun JsonElement.strings(): List<String> = jsonArray.map { it.jsonPrimitive.content }

private fun <T> JsonElement.orNull(value: (JsonElement) -> T): T? = if (this is JsonNull) null else value(this)

/** A column's values as plain lists: Long, String, List<Long> or List<String>, null for absent. */
private fun expectedValues(
    type: Int,
    values: List<JsonElement>,
): List<Any?> =
    values.map { v ->
        when (type) {
            Trave.INT, Trave.INT_DELTA -> v.orNull { it.long() }
            Trave.STR, Trave.DICT -> v.orNull { it.jsonPrimitive.content }
            Trave.INT_LIST -> v.orNull { it.longs().toList() }
            else -> v.orNull { it.strings() }
        }
    }

private fun decode(
    table: TraveTable,
    column: TraveColumn,
): List<Any?> =
    when (column.type) {
        Trave.INT, Trave.INT_DELTA -> table.ints(column.name).map { if (it == Trave.ABSENT) null else it }
        Trave.STR, Trave.DICT -> table.strings(column.name).toList()
        Trave.INT_LIST -> table.intLists(column.name).map { it?.toList() }
        Trave.STR_LIST -> table.stringLists(column.name).toList()
        else -> error("type ${column.type}")
    }

private fun isKnown(type: Int): Boolean = type in Trave.INT..Trave.STR_LIST

private fun toPack(vector: JsonObject): List<TableData> =
    vector.array("tables").map { t ->
        val table = t.jsonObject
        val columns =
            table.array("columns").map { c ->
                val column = c.jsonObject
                val name = column.string("name")
                val values = column.array("values")
                when (val type = TYPES.getValue(column.string("type"))) {
                    Trave.INT, Trave.INT_DELTA -> {
                        ColumnData.Ints(
                            name,
                            values.map { v -> v.orNull { it.long() } ?: Trave.ABSENT }.toLongArray(),
                            delta = type == Trave.INT_DELTA,
                        )
                    }

                    Trave.STR, Trave.DICT -> {
                        ColumnData.Strings(name, values.map { v -> v.orNull { it.jsonPrimitive.content } }, dict = type == Trave.DICT)
                    }

                    Trave.INT_LIST -> {
                        ColumnData.IntLists(name, values.map { v -> v.orNull { it.longs() } })
                    }

                    else -> {
                        ColumnData.StringLists(name, values.map { v -> v.orNull { it.strings() } })
                    }
                }
            }
        TableData(table.string("name"), table.getValue("rows").long().toInt(), columns)
    }

private fun vectors(): List<JsonObject> = VECTORS.array("vectors").map { it.jsonObject }

private fun vector(name: String): JsonObject = vectors().single { it.string("name") == name }

class TraveTest {
    @Test
    fun validFilesReadAsExpected() {
        for (v in vectors().filter { "error" !in it }) {
            val name = v.string("name")
            val file = Trave.read(hex(v.string("hex")), MAGIC)
            assertEquals(MAGIC, file.magic, name)
            val tables = v.array("tables").map { it.jsonObject }
            assertEquals(tables.map { it.string("name") }, file.tables.map { it.name }, name)
            for ((expected, table) in tables.zip(file.tables)) {
                assertEquals(expected.getValue("rows").long(), table.rows.toLong(), "$name: ${table.name}")
                val columns = expected.array("columns").map { it.jsonObject }
                val known = table.columns.filter { isKnown(it.type) }
                assertEquals(
                    columns.map { it.string("name") to TYPES.getValue(it.string("type")) },
                    known.map { it.name to it.type },
                    "$name: ${table.name}",
                )
                for ((c, column) in columns.zip(known)) {
                    assertEquals(
                        expectedValues(column.type, c.array("values")),
                        decode(table, column),
                        "$name: ${table.name}.${column.name}",
                    )
                }
            }
        }
    }

    @Test
    fun canonicalFilesPackToTheSameBytes() {
        for (v in vectors().filter { it["canonical"]?.jsonPrimitive?.content == "true" }) {
            assertContentEquals(hex(v.string("hex")), Trave.pack(MAGIC, toPack(v)), v.string("name"))
        }
    }

    @Test
    fun invalidFilesAreErrors() {
        for (v in vectors().filter { "error" in it }) {
            assertFailsWith<TraveException>(v.string("name")) {
                val file = Trave.read(hex(v.string("hex")), MAGIC)
                for (table in file.tables) {
                    for (column in table.columns.filter { isKnown(it.type) }) decode(table, column)
                }
            }
        }
    }

    @Test
    fun anotherMagicIsAnError() {
        assertFailsWith<TraveException> { Trave.read(Trave.pack("AAAA", emptyList()), "BBBB") }
    }

    @Test
    fun unknownTypesStayVisibleButNoDecoderTakesThem() {
        val table = Trave.read(hex(vector("unknown_types").string("hex")), MAGIC).table("t")!!
        assertEquals(listOf("w" to 0, "x" to 9, "y" to Trave.INT), table.columns.map { it.name to it.type })
        val x = table.column("x")!!
        assertEquals(3, x.length)
        for (decoder in listOf(table::ints, table::strings, table::intLists, table::stringLists)) {
            assertFailsWith<TraveException> { decoder("x") }
        }
    }

    @Test
    fun missingColumnsAndWrongTypes() {
        val file = Trave.read(hex(vector("all_types").string("hex")), MAGIC)
        assertNull(file.table("nope"))
        val table = file.table("t")!!
        assertNull(table.column("nope"))
        assertFailsWith<TraveException> { table.ints("nope") }
        assertFailsWith<TraveException> { table.ints("s") }
        assertFailsWith<TraveException> { table.strings("i") }
        assertFailsWith<TraveException> { table.intLists("m") }
        assertFailsWith<TraveException> { table.stringLists("l") }
    }

    @Test
    fun dictValuesShareOneStringPerEntry() {
        val values = Trave.read(hex(vector("all_types").string("hex")), MAGIC).table("t")!!.strings("k")
        assertSame(values[0], values[2])
    }

    @Test
    fun rowsOverIntMaxAreAnError() {
        assertFailsWith<TraveException> { Trave.read(hex("54455354 01 01 0174 8080808008 00"), MAGIC) }
    }

    @Test
    fun absentIntsPackAsAbsent() {
        // -2^63 is never an int or int_delta value: in Kotlin it is ABSENT, so it packs as absent rather than being refused.
        val data =
            Trave.pack(
                MAGIC,
                listOf(
                    TableData(
                        "t",
                        2,
                        listOf(
                            ColumnData.Ints("i", longArrayOf(Trave.ABSENT, 1)),
                            ColumnData.Ints("d", longArrayOf(-1, Trave.ABSENT), delta = true),
                        ),
                    ),
                ),
            )
        val table = Trave.read(data, MAGIC).table("t")!!
        assertContentEquals(longArrayOf(Trave.ABSENT, 1), table.ints("i"))
        assertContentEquals(longArrayOf(-1, Trave.ABSENT), table.ints("d"))
    }

    @Test
    fun writerRefusesWhatReadersCannotHold() {
        val cases =
            mapOf(
                "delta over 2^63 - 1" to listOf(TableData("t", 2, listOf(ColumnData.Ints("d", longArrayOf(-1, Long.MAX_VALUE), true)))),
                "delta of -2^63" to listOf(TableData("t", 2, listOf(ColumnData.Ints("d", longArrayOf(1, -Long.MAX_VALUE), true)))),
                "values for another row count" to listOf(TableData("t", 2, listOf(ColumnData.Ints("i", longArrayOf(1))))),
                "two columns of one name" to
                    listOf(
                        TableData(
                            "t",
                            0,
                            listOf(ColumnData.Ints("i", longArrayOf()), ColumnData.Strings("i", emptyList())),
                        ),
                    ),
                "two tables of one name" to listOf(TableData("t", 0, emptyList()), TableData("t", 0, emptyList())),
                "a string that isn't UTF-16" to listOf(TableData("t", 1, listOf(ColumnData.Strings("s", listOf("\uD800"))))),
            )
        for ((name, tables) in cases) {
            assertFailsWith<TraveException>(name) { Trave.pack(MAGIC, tables) }
        }
    }

    @Test
    fun magicThatReadsAsCompressedIsRefused() {
        for (magic in listOf("\u001f\u008bAB", "\u0028\u00b5\u002f\u00fd", "ABC", "ABCDE", "AB\u20acC")) {
            assertFailsWith<TraveException>(magic) { Trave.pack(magic, emptyList()) }
        }
    }
}
