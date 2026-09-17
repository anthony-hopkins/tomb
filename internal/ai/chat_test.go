package ai

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

const groundedReply = `{"candidates":[{"content":{"role":"model","parts":[{"text":"Use **Unyielding Netherprism**, from Nexus-King Salhadaar."}]},"finishReason":"STOP","groundingMetadata":{"webSearchQueries":["best protection warrior trinkets"],"groundingChunks":[{"web":{"uri":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/abc","title":"wowhead.com"}},{"web":{"uri":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/abc","title":"wowhead.com"}},{"web":{"uri":"http://plain.example","title":"not https"}},{"web":{"uri":"https://vertexaisearch.cloud.google.com/grounding-api-redirect/def","title":""}}]}}],"usageMetadata":{"promptTokenCount":900,"candidatesTokenCount":120}}`

// TestAsk: the conversation goes as contents with roles, search grounding
// as a tool, and no response schema even when the client has one; the
// answer's sources come from the grounding metadata, deduplicated, https
// only, titled by the URL when untitled.
func TestAsk(t *testing.T) {
	v, _, body := vertexServer(t, func() (int, string) { return http.StatusOK, groundedReply })
	v.Schema = map[string]any{"type": "object"}
	ans, usage, err := v.Ask(context.Background(), "the game only", []Turn{
		{Role: "user", Text: "trinkets?"}, {Role: "model", Text: "which spec?"}, {Role: "user", Text: "protection"},
	}, AskOptions{Search: true, MaxOutputTokens: 800})
	if err != nil {
		t.Fatal(err)
	}
	if ans.Text != "Use **Unyielding Netherprism**, from Nexus-King Salhadaar." || usage.PromptTokens != 900 || usage.OutputTokens != 120 {
		t.Errorf("answer = %+v usage %+v", ans, usage)
	}
	if len(ans.Sources) != 2 || ans.Sources[0] != (Source{Title: "wowhead.com", URL: "https://vertexaisearch.cloud.google.com/grounding-api-redirect/abc"}) || ans.Sources[1].Title != ans.Sources[1].URL {
		t.Errorf("sources = %+v", ans.Sources)
	}
	req := *body
	contents, _ := req["contents"].([]any)
	if len(contents) != 3 {
		t.Fatalf("contents = %v", req["contents"])
	}
	second, _ := contents[1].(map[string]any)
	if second["role"] != "model" {
		t.Errorf("second turn role = %v", second["role"])
	}
	tools, _ := req["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", req["tools"])
	}
	if _, ok := tools[0].(map[string]any)["googleSearch"]; !ok {
		t.Errorf("tools = %v, want googleSearch", tools)
	}
	cfg, _ := req["generationConfig"].(map[string]any)
	if _, has := cfg["responseSchema"]; has {
		t.Error("a response schema was sent with grounding")
	}
	if cfg["maxOutputTokens"] != float64(800) {
		t.Errorf("maxOutputTokens = %v", cfg["maxOutputTokens"])
	}
	sys, _ := req["systemInstruction"].(map[string]any)
	if parts, _ := sys["parts"].([]any); len(parts) != 1 || parts[0].(map[string]any)["text"] != "the game only" {
		t.Errorf("systemInstruction = %v", sys)
	}
}

// TestAskWithoutSearch: no tools when grounding is off, and a busy service
// is ErrBusy as it is for a review.
func TestAskWithoutSearch(t *testing.T) {
	v, _, body := vertexServer(t, func() (int, string) { return http.StatusOK, goodReply })
	if _, _, err := v.Ask(context.Background(), "s", []Turn{{Role: "user", Text: "q"}}, AskOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, has := (*body)["tools"]; has {
		t.Error("tools sent without search")
	}
	v, _, _ = vertexServer(t, func() (int, string) { return http.StatusTooManyRequests, "" })
	if _, _, err := v.Ask(context.Background(), "s", []Turn{{Role: "user", Text: "q"}}, AskOptions{}); !errors.Is(err, ErrBusy) {
		t.Errorf("busy err = %v", err)
	}
}
