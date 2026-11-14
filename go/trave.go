// Package trave reads and writes Trave files: tables of named, typed columns, packed for an app to read with a
// gzip or zstd decoder and little else. The format is described in the repository's README.
//
// Read takes the file's bytes as they are, not compressed: a caller decompresses gzip or zstd input first. It
// checks every header but decodes no column until asked, through Table.Ints, Table.Strings, Table.IntLists and
// Table.StringLists. Pack writes the same bytes as the reference writer, trave.py.
package trave

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"
)

// Type is a column's type, one byte in the file.
type Type byte

// The column types. A reader skips a column of any other type.
const (
	Int      Type = 1 // a signed integer
	IntDelta Type = 2 // a signed integer, stored as its difference from the column's previous present value
	Str      Type = 3 // a UTF-8 string
	Dict     Type = 4 // a UTF-8 string from the column's dictionary
	IntList  Type = 5 // a list of signed integers
	StrList  Type = 6 // a list of UTF-8 strings
)

// String returns the type's name in the format, such as "int_delta", or "type N" for a type this package doesn't
// know.
func (t Type) String() string {
	switch t {
	case Int:
		return "int"
	case IntDelta:
		return "int_delta"
	case Str:
		return "str"
	case Dict:
		return "dict"
	case IntList:
		return "int_list"
	case StrList:
		return "str_list"
	}
	return "type " + strconv.Itoa(int(t))
}

// Absent marks an absent value in the integers of an Int or IntDelta column, both those Table.Ints returns and
// those Pack writes. No such value can be -2^63, so it never stands for a number.
const Absent int64 = math.MinInt64

// version is the format version this package reads and writes.
const version = 1

// ErrFormat is wrapped by every error Read and the Table decoders return, so errors.Is(err, ErrFormat) holds for
// them. The error's message names the table and column involved.
var ErrFormat = errors.New("trave: invalid file")

func formatErr(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrFormat, fmt.Sprintf(format, args...))
}

var (
	errCutShort   = errors.New("cut short")
	errVarintLong = errors.New("a varint longer than 10 bytes")
	errVarintBig  = errors.New("a varint over 2^64 - 1")
	errNotUTF8    = errors.New("a string that is not UTF-8")
)

// cursor reads b from pos on.
type cursor struct {
	b   []byte
	pos int
}

func (c *cursor) left() int { return len(c.b) - c.pos }

// varint reads an unsigned LEB128 number of at most 10 bytes, at most 2^64 - 1.
func (c *cursor) varint() (uint64, error) {
	var n uint64
	for shift := uint(0); ; shift += 7 {
		if c.pos >= len(c.b) {
			return 0, errCutShort
		}
		b := c.b[c.pos]
		c.pos++
		if shift == 63 {
			if b >= 0x80 {
				return 0, errVarintLong
			}
			if b > 1 {
				return 0, errVarintBig
			}
		}
		n |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return n, nil
		}
	}
}

// utf8 skips n bytes of UTF-8 and returns their offsets.
func (c *cursor) utf8(n uint64) (start, end int, err error) {
	if n > uint64(c.left()) {
		return 0, 0, errCutShort
	}
	start, end = c.pos, c.pos+int(n)
	if !utf8.Valid(c.b[start:end]) {
		return 0, 0, errNotUTF8
	}
	c.pos = end
	return start, end, nil
}

// text skips a string, its varint length then its UTF-8 bytes, and returns the bytes' offsets.
func (c *cursor) text() (start, end int, err error) {
	n, err := c.varint()
	if err != nil {
		return 0, 0, err
	}
	return c.utf8(n)
}

// end checks that nothing is left.
func (c *cursor) end() error {
	if n := c.left(); n != 0 {
		return fmt.Errorf("%d bytes left after the last row", n)
	}
	return nil
}

func unzigzag(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

func zigzag(n int64) uint64 { return uint64(n<<1) ^ uint64(n>>63) }
