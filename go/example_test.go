package trave_test

import (
	"fmt"
	"log"

	"github.com/edde746/trave/go"
)

func ExamplePack() {
	data, err := trave.Pack("DEMO", []trave.TableData{{
		Name: "films",
		Rows: 3,
		Columns: []trave.ColumnData{
			{Name: "tmdb", Type: trave.IntDelta, Ints: []int64{603, 604, 605}},
			{Name: "title", Type: trave.Str, Strings: []string{"The Matrix", "The Matrix Reloaded", ""},
				Present: []bool{true, true, false}},
			{Name: "kind", Type: trave.Dict, Strings: []string{"film", "film", "film"}},
		},
	}})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("% x\n", data)
	// Output: 44 45 4d 4f 01 01 05 66 69 6c 6d 73 03 03 04 74 6d 64 62 02 04 b7 09 03 03 05 74 69 74 6c 65 03 20 0b 54 68 65 20 4d 61 74 72 69 78 14 54 68 65 20 4d 61 74 72 69 78 20 52 65 6c 6f 61 64 65 64 00 04 6b 69 6e 64 04 09 01 04 66 69 6c 6d 01 01 01
}

func ExampleRead() {
	data, _ := trave.Pack("DEMO", []trave.TableData{{
		Name: "films",
		Rows: 3,
		Columns: []trave.ColumnData{
			{Name: "tmdb", Type: trave.IntDelta, Ints: []int64{603, 604, trave.Absent}},
			{Name: "title", Type: trave.Str, Strings: []string{"The Matrix", "The Matrix Reloaded", ""},
				Present: []bool{true, true, false}},
		},
	}})

	f, err := trave.Read(data, "DEMO")
	if err != nil {
		log.Fatal(err)
	}
	films := f.Table("films")
	ids, err := films.Ints("tmdb")
	if err != nil {
		log.Fatal(err)
	}
	titles, present, err := films.Strings("title")
	if err != nil {
		log.Fatal(err)
	}
	for i := range films.Rows {
		if ids[i] == trave.Absent || !present[i] {
			fmt.Println("absent")
			continue
		}
		fmt.Println(ids[i], titles[i])
	}
	// Output:
	// 603 The Matrix
	// 604 The Matrix Reloaded
	// absent
}
