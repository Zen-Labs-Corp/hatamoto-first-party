package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// errUnsupportedCharset marks a feed whose declared encoding is not handled.
var errUnsupportedCharset = errors.New("unsupported charset")

// cp1252High maps bytes 0x80-0x9F of windows-1252 to code points. The five
// undefined bytes map to themselves, as browsers do.
var cp1252High = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

// charsetReader is the xml.Decoder CharsetReader. UTF-8 and us-ascii pass
// through; ISO-8859-1 and windows-1252 are decoded as windows-1252 (what
// browsers do for both labels); any other label is an error. It uses only the
// standard library, so there is no charset table beyond these.
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "utf-8", "utf8", "us-ascii", "ascii":
		return in, nil
	case "iso-8859-1", "iso8859-1", "iso_8859-1", "latin1", "l1", "windows-1252", "cp1252":
		raw, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(raw)+len(raw)/8)
		for _, b := range raw {
			switch {
			case b < 0x80:
				out = append(out, b)
			case b < 0xA0:
				out = utf8.AppendRune(out, cp1252High[b-0x80])
			default:
				out = utf8.AppendRune(out, rune(b))
			}
		}
		return bytes.NewReader(out), nil
	}
	return nil, errUnsupportedCharset
}
