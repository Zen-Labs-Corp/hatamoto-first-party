package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
)

const psiEndpoint = "https://www.googleapis.com/pagespeedonline/v5/runPagespeed"

// Lab statuses.
const (
	labOK      = "ok"
	labTimeout = "timeout"
	labError   = "error"
	labPending = "pending"
)

// labResult is the Lighthouse performance score (0-100) of one run.
type labResult struct {
	Score  int
	Status string
}

// runPSI runs PageSpeed Insights for one page. Only the performance score is
// requested, so the answer stays far below the 1 MiB a plugin may receive. A
// run that does not finish in time is a timeout, not an error.
func runPSI(key, target, strategy string) (labResult, error) {
	q := url.Values{}
	q.Set("url", target)
	q.Set("strategy", strategy)
	q.Set("category", "performance")
	q.Set("fields", "lighthouseResult/categories/performance/score")
	res, err := fetch(key, "GET", psiEndpoint+"?"+q.Encode(), "")
	if err != nil {
		if isTimeout(err) {
			return labResult{Status: labTimeout}, nil
		}
		return labResult{Status: labError}, err
	}
	if res.Status != http.StatusOK {
		return labResult{Status: labError}, fmt.Errorf("PageSpeed Insights answered HTTP %d", res.Status)
	}
	var doc struct {
		Lighthouse struct {
			Categories struct {
				Performance struct {
					Score *float64 `json:"score"`
				} `json:"performance"`
			} `json:"categories"`
		} `json:"lighthouseResult"`
	}
	if err := json.Unmarshal([]byte(res.Body), &doc); err != nil || doc.Lighthouse.Categories.Performance.Score == nil {
		return labResult{Status: labError}, fmt.Errorf("PageSpeed Insights answered without a score")
	}
	return labResult{Score: int(math.Round(*doc.Lighthouse.Categories.Performance.Score * 100)), Status: labOK}, nil
}
