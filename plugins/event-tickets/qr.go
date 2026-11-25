package main

import (
	"bytes"
	"encoding/base64"
	"image/png"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
)

const qrSize = 240

// qrPNG draws content as a QR code of size x size pixels.
func qrPNG(content string, size int) ([]byte, error) {
	bc, err := qr.Encode(content, qr.M, qr.Auto)
	if err != nil {
		return nil, err
	}
	bc, err = barcode.Scale(bc, size, size)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, bc); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// qrDataURI is the code as a data: URI for the ticket page, "" when it cannot
// be drawn.
func qrDataURI(content string) string {
	b, err := qrPNG(content, qrSize)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b)
}

// ticketPath is the site-relative address of a ticket page.
func ticketPath(tok string) string { return "/p/event-tickets/ticket/" + tok }

// ticketURL is the absolute address of a ticket page, or its path when no
// origin is known.
func ticketURL(origin, tok string) string { return origin + ticketPath(tok) }
