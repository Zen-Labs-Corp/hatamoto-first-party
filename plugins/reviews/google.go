package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

type googleText struct {
	Text string `json:"text"`
}

type googlePlace struct {
	DisplayName     googleText `json:"displayName"`
	Rating          float64    `json:"rating"`
	UserRatingCount int        `json:"userRatingCount"`
	GoogleMapsURI   string     `json:"googleMapsUri"`
	Reviews         []struct {
		Name              string     `json:"name"`
		Rating            float64    `json:"rating"`
		Text              googleText `json:"text"`
		OriginalText      googleText `json:"originalText"`
		PublishTime       string     `json:"publishTime"`
		AuthorAttribution struct {
			DisplayName string `json:"displayName"`
			URI         string `json:"uri"`
			PhotoURI    string `json:"photoUri"`
		} `json:"authorAttribution"`
		GoogleMapsURI string `json:"googleMapsUri"`
	} `json:"reviews"`
}

// fetchGoogle reads one place from the Places API (New): the overall rating
// and up to five reviews. Missing fields never fail the refresh; unusable
// reviews are skipped.
func fetchGoogle(s settings, deadline time.Time) (sourceInfo, []review, error) {
	u := "https://places.googleapis.com/v1/places/" + url.PathEscape(s.GooglePlace)
	res, err := fetchJSON("GET", u, map[string]string{
		"X-Goog-Api-Key":   s.GoogleKey,
		"X-Goog-FieldMask": "rating,userRatingCount,reviews,googleMapsUri,displayName",
	}, deadline)
	if err != nil {
		return sourceInfo{}, nil, hostErr(err, s.GoogleKey, s.GooglePlace)
	}
	if res.Status < 200 || res.Status > 299 {
		return sourceInfo{}, nil, errors.New("Google " + redact(apiError(res.Status, res.Body).Error(), s.GoogleKey, s.GooglePlace))
	}
	var p googlePlace
	if err := json.Unmarshal([]byte(res.Body), &p); err != nil {
		return sourceInfo{}, nil, errors.New("Google sent an answer that is not JSON")
	}
	info := sourceInfo{Source: sourceGoogle, Name: p.DisplayName.Text, ProfileURL: httpsURL(p.GoogleMapsURI),
		Rating: p.Rating, Count: p.UserRatingCount}
	var out []review
	for _, r := range p.Reviews {
		author := strings.TrimSpace(r.AuthorAttribution.DisplayName)
		pub, perr := time.Parse(time.RFC3339Nano, r.PublishTime)
		rating := int(r.Rating)
		if author == "" || perr != nil || r.Rating != float64(rating) || rating < 1 || rating > 5 {
			continue
		}
		body := r.Text.Text
		if body == "" {
			body = r.OriginalText.Text
		}
		id := r.Name
		if id == "" {
			sum := sha256.Sum256([]byte(author + "\n" + r.PublishTime))
			id = hex.EncodeToString(sum[:8])
		}
		out = append(out, review{Source: sourceGoogle, ExtID: id, Author: author,
			AuthorURL: httpsURL(r.AuthorAttribution.URI), PhotoURL: httpsURL(r.AuthorAttribution.PhotoURI),
			Body: strings.TrimSpace(body), Link: httpsURL(r.GoogleMapsURI), Rating: rating, Published: pub})
	}
	return info, out, nil
}
