package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"hatamoto.dev/sdk/guest"
)

// maxRecordBytes keeps one buffer value under the host's 1 MiB value limit
// once it is base64 encoded.
const maxRecordBytes = 700 << 10

var errRecordTooLarge = errors.New("record too large for the run buffer")

// translated holds the translated text fields of one entity. An empty field
// was not translated (it was empty in the source).
type translated struct {
	Name            string `json:"name"`
	Slug            string `json:"slug"`
	Content         string `json:"content,omitempty"`
	Excerpt         string `json:"excerpt,omitempty"`
	Description     string `json:"description,omitempty"`
	MetaTitle       string `json:"meta_title,omitempty"`
	MetaDescription string `json:"meta_description,omitempty"`
}

// record is what the fetch phase stores per entity for the transform phase.
// Attributes holds every attribute except the translated ones, byte for byte.
type record struct {
	ID           string `json:"id"`
	SchemaSlug   string `json:"schema_slug"`
	SourceLocale string `json:"source_locale"`
	SourceSlug   string `json:"source_slug,omitempty"`
	TargetLocale string `json:"target_locale"`
	Group        string `json:"group,omitempty"`
	// ExistingSlug is the slug of the copy already published in the target
	// locale; the copy keeps it.
	ExistingSlug string                     `json:"existing_slug,omitempty"`
	ParentID     string                     `json:"parent_id,omitempty"`
	Attributes   map[string]json.RawMessage `json:"attributes"`
	T            translated                 `json:"t"`
}

// slot says what one text sent to the provider is.
type slot struct {
	field string // name, slug, content, excerpt, description, meta_title, meta_description
	plain bool   // plain text (sent escaped, tags stripped and unescaped on return)
	n     int    // glossary terms wrapped in it
}

// attrFields are the translated attributes: content is HTML, the others are
// plain text that core renders escaped.
var attrFields = []struct {
	key   string
	plain bool
}{
	{"content", false}, {"excerpt", true}, {"description", true},
	{"meta_title", true}, {"meta_description", true},
}

// jsonString decodes a raw attribute that is a JSON string. Absent values,
// null and other types are not strings.
func jsonString(raw json.RawMessage) (string, bool) {
	var s string
	if t := strings.TrimSpace(string(raw)); !strings.HasPrefix(t, `"`) || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

// unitFor builds the provider texts of one entity: title, slug words and the
// translated attributes, empty ones left out. Glossary terms are wrapped.
func unitFor(e entity, g []glossaryEntry) (texts []string, slots []slot) {
	add := func(field, text string, plain bool) {
		if strings.TrimSpace(text) == "" {
			return
		}
		if plain {
			text = escapeText(text)
		}
		p, n := protect(text, g)
		texts = append(texts, p)
		slots = append(slots, slot{field, plain, n})
	}
	add("name", e.Name, true)
	add("slug", strings.ReplaceAll(e.Slug, "-", " "), true)
	for _, f := range attrFields {
		if s, ok := jsonString(e.Attributes[f.key]); ok {
			add(f.key, s, f.plain)
		}
	}
	return texts, slots
}

// fill turns the provider output into a record: glossary wrappers are
// removed, plain fields lose tags and entities. TargetLocale is left for the
// caller. lost counts glossary terms the provider dropped or broke.
func fill(e entity, out []string, slots []slot, g []glossaryEntry) (record, int) {
	r := record{
		ID: e.ID, SchemaSlug: e.SchemaSlug, SourceLocale: e.Locale, SourceSlug: e.Slug,
		Group: e.Group, ParentID: e.ParentID, ExistingSlug: e.existingSlug,
		Attributes: map[string]json.RawMessage{},
	}
	translatedKeys := map[string]bool{}
	for _, f := range attrFields {
		if _, ok := jsonString(e.Attributes[f.key]); ok {
			translatedKeys[f.key] = true
		}
	}
	for k, v := range e.Attributes {
		if !translatedKeys[k] {
			r.Attributes[k] = v
		}
	}
	lost := 0
	for i, sl := range slots {
		if i >= len(out) {
			lost += sl.n
			continue
		}
		text, found, broken := unwrap(out[i], g)
		lost += max(sl.n-found, 0) + broken
		if sl.plain {
			text = plainText(text)
		}
		switch sl.field {
		case "name":
			r.T.Name = strings.TrimSpace(text)
		case "slug":
			r.T.Slug = strings.TrimSpace(text)
		case "content":
			r.T.Content = text
		case "excerpt":
			r.T.Excerpt = text
		case "description":
			r.T.Description = text
		case "meta_title":
			r.T.MetaTitle = strings.TrimSpace(text)
		case "meta_description":
			r.T.MetaDescription = strings.TrimSpace(text)
		}
	}
	if r.T.Name == "" {
		r.T.Name = e.Name
	}
	return r, lost
}

// bufferKey is the run buffer key of a record. The parent id is part of the
// key so the transform phase can order parents first without reading values.
func bufferKey(seq int, id, parentID string) string {
	if parentID == "" {
		parentID = "-"
	}
	return fmt.Sprintf("src/%08d/%s/%s", seq, id, parentID)
}

// putRecord stores a record in the run buffer (fetch phase only).
func putRecord(seq int, r record) error {
	key := bufferKey(seq, r.ID, r.ParentID)
	if len(key) > 256 {
		return errors.New("buffer key too long")
	}
	b, err := json.Marshal(r)
	if err != nil {
		return errors.New("a record could not be encoded")
	}
	if len(b) > maxRecordBytes {
		return errRecordTooLarge
	}
	if err := guest.BufferPut(callHost, key, b); err != nil {
		return fmt.Errorf("the run buffer refused a record: %w", hostReason(err))
	}
	return nil
}
