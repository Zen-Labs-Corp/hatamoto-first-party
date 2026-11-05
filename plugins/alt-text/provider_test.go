package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const testKey = "sk-test-SECRET"

func decodeBody(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	return m
}

func TestBuildRequestOpenAI(t *testing.T) {
	s := settings{Provider: "openai", APIKey: testKey, Language: "de", MaxLength: 90}
	u, h, b, err := buildRequest(s, "https://x.test/a.jpg", "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://api.openai.com/v1/chat/completions" || h["Authorization"] != "Bearer "+testKey {
		t.Fatalf("url/auth: %s %v", u, h)
	}
	m := decodeBody(t, b)
	if m["model"] != "gpt-4o-mini" || m["max_tokens"] != float64(120) {
		t.Fatalf("model/tokens: %v", m)
	}
	msgs := m["messages"].([]any)
	sys := msgs[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "90 characters") || !strings.Contains(sys, "de") || !strings.Contains(sys, "DECORATIVE") {
		t.Fatalf("system prompt: %s", sys)
	}
	parts := msgs[1].(map[string]any)["content"].([]any)
	img := parts[1].(map[string]any)
	iu := img["image_url"].(map[string]any)
	if img["type"] != "image_url" || iu["url"] != "https://x.test/a.jpg" || iu["detail"] != "low" {
		t.Fatalf("image part: %v", img)
	}
}

func TestBuildRequestAnthropic(t *testing.T) {
	s := settings{Provider: "anthropic", APIKey: testKey, Model: "my-model"}
	u, h, b, err := buildRequest(s, "https://x.test/a.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://api.anthropic.com/v1/messages" || h["x-api-key"] != testKey || h["anthropic-version"] != "2023-06-01" {
		t.Fatalf("url/headers: %s %v", u, h)
	}
	m := decodeBody(t, b)
	if m["model"] != "my-model" || m["system"] == "" {
		t.Fatalf("body: %v", m)
	}
	parts := m["messages"].([]any)[0].(map[string]any)["content"].([]any)
	src := parts[0].(map[string]any)["source"].(map[string]any)
	if parts[0].(map[string]any)["type"] != "image" || src["type"] != "url" || src["url"] != "https://x.test/a.png" {
		t.Fatalf("image part: %v", parts[0])
	}
}

func TestBuildRequestGemini(t *testing.T) {
	s := settings{Provider: "gemini", APIKey: testKey, Model: "gem ini/x"}
	u, h, b, err := buildRequest(s, "https://x.test/a.webp", "image/webp")
	if err != nil {
		t.Fatal(err)
	}
	if u != "https://generativelanguage.googleapis.com/v1beta/models/gem%20ini%2Fx:generateContent" {
		t.Fatalf("url: %s", u)
	}
	if strings.Contains(u, testKey) || h["x-goog-api-key"] != testKey {
		t.Fatalf("key placement: %s %v", u, h)
	}
	m := decodeBody(t, b)
	if m["systemInstruction"] == nil || m["generationConfig"].(map[string]any)["maxOutputTokens"] != float64(120) {
		t.Fatalf("body: %v", m)
	}
	parts := m["contents"].([]any)[0].(map[string]any)["parts"].([]any)
	fd := parts[1].(map[string]any)["file_data"].(map[string]any)
	if fd["mime_type"] != "image/webp" || fd["file_uri"] != "https://x.test/a.webp" {
		t.Fatalf("file_data: %v", fd)
	}
}

func TestBuildRequestDefaultsAndUnknown(t *testing.T) {
	for p, want := range defaultModels {
		u, _, b, err := buildRequest(settings{Provider: p}, "https://x.test/a.jpg", "image/jpeg")
		if err != nil || !strings.Contains(u+string(b), want) {
			t.Fatalf("%s default model %q missing: %v", p, want, err)
		}
	}
	if _, _, _, err := buildRequest(settings{Provider: "nope", APIKey: testKey}, "u", "m"); err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("unknown provider error: %v", err)
	}
}

func TestParseAnswer(t *testing.T) {
	ok := map[string]string{
		"openai":    `{"choices":[{"message":{"content":"A red car"}}]}`,
		"anthropic": `{"content":[{"type":"thinking"},{"type":"text","text":"A red car"}]}`,
		"gemini":    `{"candidates":[{"content":{"parts":[{"text":"A red car"}]}}]}`,
	}
	for p, body := range ok {
		got, err := parseAnswer(p, 200, []byte(body))
		if err != nil || got != "A red car" {
			t.Errorf("%s: %q %v", p, got, err)
		}
	}
	bad := map[string]string{
		"openai":    `{"error":{"message":"bad key sk-test-SECRET"}}`,
		"anthropic": `{"content":[]}`,
		"gemini":    `not json sk-test-SECRET`,
	}
	for p, body := range bad {
		if _, err := parseAnswer(p, 200, []byte(body)); err == nil || strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: want clean error, got %v", p, err)
		}
		_, err := parseAnswer(p, 401, []byte(body))
		if err == nil || strings.Contains(err.Error(), testKey) || !strings.Contains(err.Error(), "401") {
			t.Errorf("%s status error: %v", p, err)
		}
	}
}

// hostStub answers http.fetch with a canned result and records the call.
type hostStub struct {
	res  fetched
	err  error
	args map[string]any
}

func (h *hostStub) call(fn string, args, out any) error {
	if fn != "http.fetch" {
		return errors.New("unexpected " + fn)
	}
	h.args = args.(map[string]any)
	if h.err != nil {
		return h.err
	}
	b, _ := json.Marshal(h.res)
	return json.Unmarshal(b, out)
}

func TestDescribe(t *testing.T) {
	s := settings{Provider: "openai", APIKey: testKey}
	h := &hostStub{res: fetched{Status: 200, Body: `{"choices":[{"message":{"content":"Two cats"}}]}`}}
	callHost = h.call
	t.Cleanup(func() { callHost = nil })
	got, err := describe(s, "https://x.test/a.jpg", "image/jpeg")
	if err != nil || got != "Two cats" {
		t.Fatalf("%q %v", got, err)
	}
	if h.args["method"] != "POST" || h.args["timeout_ms"] != 6500 || h.args["url"] != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("call args: %v", h.args)
	}

	for name, st := range map[string]*hostStub{
		"truncated": {res: fetched{Status: 200, Body: `{"choices":[`, Truncated: true}},
		"http 500":  {res: fetched{Status: 500, Body: "oops " + testKey}},
		"host err":  {err: errors.New("deadline " + testKey)},
	} {
		callHost = st.call
		_, err := describe(s, "https://x.test/a.jpg", "image/jpeg")
		if err == nil {
			t.Errorf("%s: want error", name)
		} else if strings.Contains(err.Error(), testKey) {
			t.Errorf("%s: error leaks the key: %v", name, err)
		}
	}
}
