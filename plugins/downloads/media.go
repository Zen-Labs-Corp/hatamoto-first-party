package main

import "regexp"

// media is the file record the host returns for media.get and media.list. The
// SDK has no media types, so the fields used here are declared locally.
type media struct {
	ID        string  `json:"id"`
	Filename  string  `json:"filename"`
	MimeType  string  `json:"mime_type"`
	FileSize  int64   `json:"file_size"`
	PublicURL *string `json:"public_url"`
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(s) }

func getMedia(id string) (media, error) {
	var m media
	err := callHost("media.get", map[string]any{"id": id}, &m)
	return m, err
}
