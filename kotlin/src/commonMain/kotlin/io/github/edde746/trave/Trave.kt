package io.github.edde746.trave

/**
 * Reads and writes Trave files: tables of named, typed columns.
 *
 * Both sides work on unpacked bytes. A file stored as gzip (`1f 8b`) or zstd (`28 b5 2f fd`) must be decompressed by
 * the caller before [read], and compressed by the caller after [pack].
 */
public object Trave {
    /** An absent value in an [INT] or [INT_DELTA] column, as returned by [TraveTable.ints] and taken by [ColumnData.Ints]. */
    public const val ABSENT: Long = Long.MIN_VALUE

    /** Column type: a signed 64-bit integer within ±(2^63 - 1). */
    public const val INT: Int = 1

    /** Column type: a signed 64-bit integer within ±(2^63 - 1), stored as the difference from the previous present value. */
    public const val INT_DELTA: Int = 2

    /** Column type: a UTF-8 string. */
    public const val STR: Int = 3

    /** Column type: a UTF-8 string, stored as an index into the column's dictionary. */
    public const val DICT: Int = 4

    /** Column type: a list of signed 64-bit integers, any value included. */
    public const val INT_LIST: Int = 5

    /** Column type: a list of UTF-8 strings. */
    public const val STR_LIST: Int = 6

    /** The format version this library reads and writes. */
    internal const val VERSION: Int = 1

    /**
     * Parses the headers of the unpacked file [bytes], without decoding any column's data.
     *
     * The file must have the 4-byte [magic], when one is given, read as ISO-8859-1 (one char per byte). The returned
     * [TraveFile] keeps [bytes] without copying them: don't change them while it is in use. Column data is decoded,
     * and checked, by [TraveTable]'s decoders.
     *
     * @throws TraveException if the file is not a valid Trave file of version 1 or has another magic.
     */
    public fun read(
        bytes: ByteArray,
        magic: String? = null,
    ): TraveFile = readFile(bytes, magic)

    /**
     * Writes [tables] as an unpacked Trave file with the 4-byte [magic], given as ISO-8859-1 chars (one per byte).
     *
     * The same tables always give the same bytes, those of the reference `trave.py`: each dictionary lists its strings
     * in order of first use.
     *
     * @throws TraveException for a magic that isn't 4 bytes or starts like gzip or zstd, two tables of one name, two
     *   columns of one name in a table, a column whose value count isn't its table's row count, an [INT_DELTA]
     *   difference outside ±(2^63 - 1), or a string that isn't valid UTF-16.
     */
    public fun pack(
        magic: String,
        tables: List<TableData>,
    ): ByteArray = packFile(magic, tables)
}

/** Any error in a Trave file, found by [Trave.read] or a [TraveTable] decoder, and any refusal of [Trave.pack]. */
public class TraveException(
    message: String,
) : IllegalArgumentException(message)
