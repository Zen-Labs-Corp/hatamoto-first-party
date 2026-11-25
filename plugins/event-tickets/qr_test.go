package main

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestQRPNG(t *testing.T) {
	b, err := qrPNG("https://example.com/p/event-tickets/ticket/abc", qrSize)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Size(); got.X != 240 || got.Y != 240 {
		t.Errorf("size %v, want 240x240", got)
	}
}

func TestQRDataURI(t *testing.T) {
	if u := qrDataURI("x"); !strings.HasPrefix(u, "data:image/png;base64,") {
		t.Errorf("uri %q", u)
	}
	if u := qrDataURI(strings.Repeat("x", 10000)); u != "" {
		t.Error("content too long for a QR code must give no image")
	}
}

func TestTicketLinks(t *testing.T) {
	if got := ticketPath("t"); got != "/p/event-tickets/ticket/t" {
		t.Errorf("path %q", got)
	}
	if got := ticketURL("https://example.com", "t"); got != "https://example.com/p/event-tickets/ticket/t" {
		t.Errorf("url %q", got)
	}
	if got := ticketURL("", "t"); got != "/p/event-tickets/ticket/t" {
		t.Errorf("url without origin %q", got)
	}
}
