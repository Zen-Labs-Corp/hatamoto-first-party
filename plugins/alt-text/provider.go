package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const (
	providerOpenAI    = "openai"
	providerAnthropic = "anthropic"
	providerGemini    = "gemini"

	// visionTimeoutMS leaves room in the 10 s step for the list, buffer and
	// checkpoint calls around the one provider call.
	visionTimeoutMS = 8000
	maxAnswerTokens = 120
)

// defaultModels are used when the model setting is empty.
var defaultModels = map[string]string{
	providerOpenAI:    "gpt-4o-mini",
	providerAnthropic: "claude-haiku-4-5",
	providerGemini:    "gemini-2.0-flash",
}

// settings are the plugin settings (settings.get).
type settings struct {
	Provider  string `json:"provider"`
	APIKey    string `json:"api_key"`
	Model     string `json:"model"`
	Language  string `json:"language"`
	SiteURL   string `json:"site_url"`
	MaxLength int    `json:"max_length"`
}

func (s settings) model() string {
	if m := strings.TrimSpace(s.Model); m != "" {
		return m
	}
	return defaultModels[s.Provider]
}

func (s settings) language() string {
	if l := strings.TrimSpace(s.Language); l != "" {
		return l
	}
	return "en"
}

func (s settings) prompt() string {
	return fmt.Sprintf("You write alt text for images on a website. Reply with one plain description of the image, "+
		"at most %d characters, in this language: %s. No quotes, no markdown, no line breaks, and do not start with "+
		"\"image of\" or \"picture of\". If the image is purely decorative (a divider, background pattern or ornament "+
		"that carries no information), reply exactly DECORATIVE.", clampMax(s.MaxLength), s.language())
}

const userText = "Write the alt text for this image."

// buildRequest returns the HTTPS request that asks the provider to describe
// the image at imageURL.
func buildRequest(s settings, imageURL, mime string) (string, map[string]string, []byte, error) {
	headers := map[string]string{"Content-Type": "application/json"}
	var (
		endpoint string
		payload  any
	)
	switch s.Provider {
	case providerOpenAI:
		endpoint = "https://api.openai.com/v1/chat/completions"
		headers["Authorization"] = "Bearer " + s.APIKey
		payload = map[string]any{
			"model":      s.model(),
			"max_tokens": maxAnswerTokens,
			"messages": []any{
				map[string]any{"role": "system", "content": s.prompt()},
				map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "text", "text": userText},
					map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL, "detail": "low"}},
				}},
			},
		}
	case providerAnthropic:
		endpoint = "https://api.anthropic.com/v1/messages"
		headers["x-api-key"] = s.APIKey
		headers["anthropic-version"] = "2023-06-01"
		payload = map[string]any{
			"model":      s.model(),
			"max_tokens": maxAnswerTokens,
			"system":     s.prompt(),
			"messages": []any{
				map[string]any{"role": "user", "content": []any{
					map[string]any{"type": "image", "source": map[string]any{"type": "url", "url": imageURL}},
					map[string]any{"type": "text", "text": userText},
				}},
			},
		}
	case providerGemini:
		endpoint = "https://generativelanguage.googleapis.com/v1beta/models/" + url.PathEscape(s.model()) + ":generateContent"
		headers["x-goog-api-key"] = s.APIKey
		payload = map[string]any{
			"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": s.prompt()}}},
			"contents": []any{
				map[string]any{"role": "user", "parts": []any{
					map[string]any{"text": userText},
					map[string]any{"file_data": map[string]any{"mime_type": mime, "file_uri": imageURL}},
				}},
			},
			"generationConfig": map[string]any{"maxOutputTokens": maxAnswerTokens},
		}
	default:
		return "", nil, nil, errors.New("unknown provider: choose openai, anthropic or gemini in the plugin settings")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", nil, nil, err
	}
	return endpoint, headers, body, nil
}

// parseAnswer extracts the model's text from a provider response. Its errors
// carry the status at most, never the body.
func parseAnswer(provider string, status int, body []byte) (string, error) {
	if status < 200 || status > 299 {
		return "", fmt.Errorf("%s answered HTTP %d", provider, status)
	}
	var text string
	switch provider {
	case providerOpenAI:
		var r struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return "", fmt.Errorf("%s answer is not valid JSON", provider)
		}
		if len(r.Choices) > 0 {
			text = r.Choices[0].Message.Content
		}
	case providerAnthropic:
		var r struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return "", fmt.Errorf("%s answer is not valid JSON", provider)
		}
		for _, c := range r.Content {
			if c.Type == "text" {
				text = c.Text
				break
			}
		}
	case providerGemini:
		var r struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return "", fmt.Errorf("%s answer is not valid JSON", provider)
		}
		if len(r.Candidates) > 0 && len(r.Candidates[0].Content.Parts) > 0 {
			text = r.Candidates[0].Content.Parts[0].Text
		}
	default:
		return "", errors.New("unknown provider")
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s gave no text", provider)
	}
	return text, nil
}

// fetched is the http.fetch result.
type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// describe makes the one provider call for an image and returns the raw
// answer. Errors never contain the request, the response body or the key.
func describe(s settings, imageURL, mime string) (string, error) {
	endpoint, headers, body, err := buildRequest(s, imageURL, mime)
	if err != nil {
		return "", err
	}
	var res fetched
	if err := callHost("http.fetch", map[string]any{
		"method": "POST", "url": endpoint, "headers": headers, "body": string(body), "timeout_ms": visionTimeoutMS,
	}, &res); err != nil {
		return "", fmt.Errorf("%s request failed", s.Provider)
	}
	if res.Truncated {
		return "", fmt.Errorf("%s answer was cut off", s.Provider)
	}
	return parseAnswer(s.Provider, res.Status, []byte(res.Body))
}
