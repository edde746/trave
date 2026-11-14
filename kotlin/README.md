# Trave for Kotlin

A Kotlin Multiplatform reader and writer for the [Trave format](../README.md#the-format), byte for byte the same as
the reference [`trave.py`](../python/trave.py). Targets: `jvm` (which Android code uses), `iosArm64`,
`iosSimulatorArm64`, `macosArm64`, `tvosArm64` and `tvosSimulatorArm64`. No runtime dependencies.

It works on unpacked bytes: decompress a gzip or zstd file before reading it, and compress after packing.

## Depending on it

It isn't published to any registry. Take it from git, for example as a submodule, and include it as a composite
build:

```sh
git submodule add https://github.com/edde746/trave.git trave
```

```kotlin
// settings.gradle.kts
includeBuild("trave/kotlin")

// build.gradle.kts
kotlin {
    sourceSets {
        commonMain.dependencies {
            implementation("dev.edde746:trave:1.0.0")
        }
    }
}
```

Gradle builds the library from the included sources in place of the coordinates. Alternatively, run
`./gradlew publishToMavenLocal` in this directory and add `mavenLocal()` to your repositories.

## API

Package `dev.edde746.trave`. Every format error and every writer refusal is a `TraveException`, an
`IllegalArgumentException`.

```kotlin
object Trave {
    const val ABSENT: Long = Long.MIN_VALUE              // an absent int or int_delta value
    const val INT = 1; const val INT_DELTA = 2; const val STR = 3
    const val DICT = 4; const val INT_LIST = 5; const val STR_LIST = 6

    fun read(bytes: ByteArray, magic: String? = null): TraveFile
    fun pack(magic: String, tables: List<TableData>): ByteArray
}

class TraveFile { val magic: String; val tables: List<TraveTable>; fun table(name: String): TraveTable? }

class TraveTable {
    val name: String; val rows: Int; val columns: List<TraveColumn>
    fun column(name: String): TraveColumn?
    fun ints(name: String): LongArray                    // int or int_delta, ABSENT for absent
    fun strings(name: String): Array<String?>            // str or dict, null for absent
    fun intLists(name: String): Array<LongArray?>        // int_list, null for absent
    fun stringLists(name: String): Array<List<String>?>  // str_list, null for absent
}

class TraveColumn { val name: String; val type: Int; val offset: Int; val length: Int }

class TableData(name: String, rows: Int, columns: List<ColumnData>)
sealed class ColumnData(name: String) {
    class Ints(name: String, values: LongArray, delta: Boolean = false)       // ABSENT for absent
    class Strings(name: String, values: List<String?>, dict: Boolean = false)
    class IntLists(name: String, values: List<LongArray?>)
    class StringLists(name: String, values: List<List<String>?>)
}
```

- `read` checks the magic (when given), the version and every header, without decoding or copying any column data.
  Each column is a slice of `bytes` (`offset`, `length`); keep the array unchanged while the file is in use.
  Columns of unknown types are listed with their type byte, but no decoder takes them.
- The decoders check a column's data as they decode it: its length, varints, dictionary numbers and strict UTF-8.
  A missing column or one of another type is an error. A dict column's values share one `String` per dictionary
  entry.
- A magic is 4 chars, each one byte (ISO-8859-1), and `pack` refuses one that starts like gzip or zstd.
- `pack` gives the same bytes as `trave.py` for the same tables: dictionaries list their strings in order of first
  use. It refuses two tables or two columns of one name, a column whose value count isn't its table's row count,
  an `int_delta` difference outside ±(2^63 - 1), and a string that isn't valid UTF-16.

## Reading

```kotlin
import dev.edde746.trave.Trave

val file = Trave.read(unpackedBytes, "DEMO")
val films = file.table("films") ?: error("no films")
val tmdb = films.ints("tmdb")       // LongArray, Trave.ABSENT for absent
val titles = films.strings("title") // Array<String?>
for (row in 0 until films.rows) {
    if (tmdb[row] != Trave.ABSENT) println("${tmdb[row]}: ${titles[row]}")
}
```

## Packing

```kotlin
import dev.edde746.trave.ColumnData
import dev.edde746.trave.TableData
import dev.edde746.trave.Trave

val bytes = Trave.pack("DEMO", listOf(
    TableData("films", 3, listOf(
        ColumnData.Ints("tmdb", longArrayOf(603, 604, 605), delta = true),
        ColumnData.Strings("title", listOf("The Matrix", "The Matrix Reloaded", null)),
        ColumnData.Strings("kind", listOf("film", "film", "film"), dict = true),
    )),
))
```

## Development

```sh
./gradlew jvmTest macosArm64Test iosSimulatorArm64Test   # or ./gradlew build
```

The tests read [`../vectors.json`](../vectors.json), which the `embedVectors` task writes into a generated Kotlin
source file, as common code can't read files on native targets.
