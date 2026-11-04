package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

type tpUnit struct {
	DisplayName string `json:"displayName"`
	Name        struct {
		Identifying string `json:"identifying"`
	} `json:"name"`
	NumberOfReviews struct {
		Total int `json:"total"`
	} `json:"numberOfReviews"`
	Score struct {
		Stars      float64 `json:"stars"`
		TrustScore float64 `json:"trustScore"`
	} `json:"score"`
}

type tpReviews struct {
	Reviews []struct {
		ID        string  `json:"id"`
		Stars     float64 `json:"stars"`
		Title     string  `json:"title"`
		Text      string  `json:"text"`
		CreatedAt string  `json:"createdAt"`
		Consumer  struct {
			DisplayName string `json:"displayName"`
		} `json:"consumer"`
	} `json:"reviews"`
}

const tpBase = "https://api.trustpilot.com/v1/business-units/"

// tpGet runs one Trustpilot call. The key travels in the apikey request
// header, never in the URL (the host logs URLs); error texts are redacted too.
func tpGet(s settings, path string, q url.Values, out any, deadline time.Time) error {
	u := tpBase + url.PathEscape(s.TPUnit) + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	res, err := fetchJSON("GET", u, map[string]string{"apikey": s.TPKey}, deadline)
	if err != nil {
		return hostErr(err, s.TPKey, s.TPUnit)
	}
	if res.Status < 200 || res.Status > 299 {
		return errors.New("Trustpilot " + redact(apiError(res.Status, res.Body).Error(), s.TPKey, s.TPUnit))
	}
	if err := json.Unmarshal([]byte(res.Body), out); err != nil {
		return errors.New("Trustpilot sent an answer that is not JSON")
	}
	return nil
}

// fetchTrustpilot reads the business unit summary and its newest reviews.
func fetchTrustpilot(s settings, deadline time.Time) (sourceInfo, []review, error) {
	var u tpUnit
	if err := tpGet(s, "", url.Values{}, &u, deadline); err != nil {
		return sourceInfo{}, nil, err
	}
	rating := u.Score.TrustScore
	if rating == 0 {
		rating = u.Score.Stars
	}
	info := sourceInfo{Source: sourceTrustpilot, Name: u.DisplayName, Rating: rating, Count: u.NumberOfReviews.Total}
	if id := strings.TrimSpace(u.Name.Identifying); id != "" {
		info.ProfileURL = httpsURL("https://www.trustpilot.com/review/" + url.PathEscape(id))
	}
	var list tpReviews
	if err := tpGet(s, "/reviews", url.Values{"perPage": {"20"}, "orderBy": {"createdat.desc"}}, &list, deadline); err != nil {
		return sourceInfo{}, nil, err
	}
	var out []review
	for _, r := range list.Reviews {
		author := strings.TrimSpace(r.Consumer.DisplayName)
		pub, perr := time.Parse(time.RFC3339Nano, r.CreatedAt)
		stars := int(r.Stars)
		if author == "" || r.ID == "" || perr != nil || r.Stars != float64(stars) || stars < 1 || stars > 5 {
			continue
		}
		out = append(out, review{Source: sourceTrustpilot, ExtID: r.ID, Author: author,
			Body: tpBody(r.Title, r.Text), Link: "https://www.trustpilot.com/reviews/" + url.PathEscape(r.ID),
			Rating: stars, Published: pub})
	}
	return info, out, nil
}

// tpBody joins the title and the text: "Title. Text", or the one that exists.
func tpBody(title, text string) string {
	title, text = strings.TrimSpace(title), strings.TrimSpace(text)
	switch {
	case title == "":
		return text
	case text == "":
		return title
	case strings.ContainsAny(title[len(title)-1:], ".!?"):
		return title + " " + text
	}
	return title + ". " + text
}
