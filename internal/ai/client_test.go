package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// vertexServer fakes the metadata server and the generateContent endpoint.
func vertexServer(t *testing.T, answer func() (int, string)) (*Vertex, *atomic.Int32, *map[string]any) {
	t.Helper()
	var tokens atomic.Int32
	var lastBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/meta/instance/service-accounts/default/token":
			if r.Header.Get("Metadata-Flavor") != "Google" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			tokens.Add(1)
			_, _ = w.Write([]byte(`{"access_token":"VMTOKEN","expires_in":3600,"token_type":"Bearer"}`))
		case r.URL.Path == "/meta/project/project-id":
			_, _ = w.Write([]byte("tomb-project\n"))
		case strings.HasSuffix(r.URL.Path, ":generateContent"):
			if r.Header.Get("Authorization") != "Bearer VMTOKEN" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.URL.Path != "/v1/projects/tomb-project/locations/us-central1/publishers/google/models/gemini-3.1-pro:generateContent" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &lastBody)
			status, out := answer()
			w.WriteHeader(status)
			_, _ = w.Write([]byte(out))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	v := NewVertex("gemini-3.1-pro", "us-central1")
	v.MetadataURL, v.EndpointBase, v.HTTP = srv.URL+"/meta", srv.URL, srv.Client()
	return v, &tokens, &lastBody
}

const goodReply = `{"candidates":[{"content":{"role":"model","parts":[{"text":"You pressed Death Strike "},{"text":"too rarely."}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1200,"candidatesTokenCount":300,"totalTokenCount":1500}}`

// TestWrite: the request carries the system instruction, the prompt and the
// generation config; the reply's parts are joined and its usage read; the
// token is reused.
func TestWrite(t *testing.T) {
	v, tokens, body := vertexServer(t, func() (int, string) { return 200, goodReply })
	if err := v.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	text, usage, err := v.Write(context.Background(), "be a raid leader", "here is the fight")
	if err != nil {
		t.Fatal(err)
	}
	if text != "You pressed Death Strike too rarely." || usage.PromptTokens != 1200 || usage.OutputTokens != 300 {
		t.Errorf("Write = %q, %+v", text, usage)
	}
	b := *body
	sys := b["systemInstruction"].(map[string]any)["parts"].([]any)[0].(map[string]any)["text"]
	contents := b["contents"].([]any)[0].(map[string]any)
	cfg := b["generationConfig"].(map[string]any)
	if sys != "be a raid leader" || contents["role"] != "user" || contents["parts"].([]any)[0].(map[string]any)["text"] != "here is the fight" {
		t.Errorf("request = %v", b)
	}
	if cfg["temperature"].(float64) != 0.4 || cfg["maxOutputTokens"].(float64) != 16384 {
		t.Errorf("generationConfig = %v", cfg)
	}
	if _, _, err := v.Write(context.Background(), "s", "p"); err != nil {
		t.Fatal(err)
	}
	if tokens.Load() != 1 {
		t.Errorf("token fetched %d times, want 1", tokens.Load())
	}
}

// TestWriteOutcomes: empty candidates and a safety stop decline; 429 is
// busy; anything else is an error naming the status.
func TestWriteOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		status int
		reply  string
		want   error
	}{
		{"no candidates", 200, `{"candidates":[],"usageMetadata":{"promptTokenCount":5}}`, ErrDeclined},
		{"safety", 200, `{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}`, ErrDeclined},
		{"empty text", 200, `{"candidates":[{"content":{"parts":[{"text":"  "}]},"finishReason":"STOP"}]}`, ErrDeclined},
		{"busy", 429, `{}`, ErrBusy},
		{"unavailable", 503, `{}`, ErrBusy},
		{"no such model", 404, `{"error":{"code":404,"message":"Publisher model was not found"}}`, ErrNoModel},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, _, _ := vertexServer(t, func() (int, string) { return tc.status, tc.reply })
			if _, _, err := v.Write(context.Background(), "s", "p"); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
	v, _, _ := vertexServer(t, func() (int, string) { return 500, `{}` })
	if _, _, err := v.Write(context.Background(), "s", "p"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("500: err = %v", err)
	}
}

// TestEndpointByLocation: a region has its own host; "global" is served
// from the bare domain (checked live, 2026-09-17).
func TestEndpointByLocation(t *testing.T) {
	for _, tc := range []struct{ region, want string }{
		{"us-central1", "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/publishers/google/models/gemini-2.5-pro:generateContent"},
		{"global", "https://aiplatform.googleapis.com/v1/projects/p/locations/global/publishers/google/models/gemini-2.5-pro:generateContent"},
	} {
		v := NewVertex("gemini-2.5-pro", tc.region)
		if got := v.endpoint("p"); got != tc.want {
			t.Errorf("%s: endpoint = %s", tc.region, got)
		}
	}
}

// TestTokenRefresh: the metadata token is fetched again near expiry.
func TestTokenRefresh(t *testing.T) {
	v, tokens, _ := vertexServer(t, func() (int, string) { return 200, goodReply })
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	v.now = func() time.Time { return now }
	if _, _, err := v.Write(context.Background(), "s", "p"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3600*time.Second - 30*time.Second)
	if _, _, err := v.Write(context.Background(), "s", "p"); err != nil {
		t.Fatal(err)
	}
	if tokens.Load() != 2 {
		t.Errorf("token fetched %d times, want 2", tokens.Load())
	}
}

// TestReadyWithoutMetadata: no metadata server is a plain error, so the
// startup line can say so.
func TestReadyWithoutMetadata(t *testing.T) {
	v := NewVertex("m", "r")
	v.MetadataURL = "http://127.0.0.1:1/meta"
	v.HTTP = &http.Client{Timeout: 200 * time.Millisecond}
	if err := v.Ready(context.Background()); err == nil {
		t.Error("Ready succeeded with no metadata server")
	}
}
