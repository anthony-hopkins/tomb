package ai

import (
	"context"
	"strings"
)

// The assistant's side of the model (spec 006): a conversation rather than
// one prompt, answered with web search grounding so what is "current" is
// read rather than recalled, and the pages read handed back as sources.

// Turn is one message in a conversation: the member's or the model's.
type Turn struct {
	// Role is "user" or "model", the two Vertex AI knows.
	Role string
	Text string
}

// Source is one page the model read for its answer, from the grounding
// metadata -- never from the answer's text, which the site never trusts to
// carry a link (FR-060).
type Source struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Answer is what the model said and what it read.
type Answer struct {
	Text    string
	Sources []Source
}

// AskOptions shapes one answer.
type AskOptions struct {
	// Search turns on Google Search grounding.
	Search bool
	// MaxOutputTokens bounds the answer; zero means the client's default.
	MaxOutputTokens int
}

// Chatter is what the assistant asks of the model.
type Chatter interface {
	// Ask answers the last turn in the light of the ones before it, under
	// the system instruction.
	Ask(ctx context.Context, system string, turns []Turn, opts AskOptions) (Answer, Usage, error)
}

var _ Chatter = (*Vertex)(nil)

// Ask asks the model with the conversation as its contents.
//
// The response schema, when the client has one, is left out here: Vertex AI
// does not combine grounding with a schema, and a conversation's answer is
// Markdown, not JSON.
func (v *Vertex) Ask(ctx context.Context, system string, turns []Turn, opts AskOptions) (Answer, Usage, error) {
	contents := make([]map[string]any, 0, len(turns))
	for _, t := range turns {
		role := t.Role
		if role != "model" {
			role = "user"
		}
		contents = append(contents, map[string]any{"role": role, "parts": []map[string]string{{"text": t.Text}}})
	}
	temp, max := v.Temperature, opts.MaxOutputTokens
	if temp == 0 {
		temp = 0.4
	}
	if max == 0 {
		max = v.MaxOutputTokens
	}
	if max == 0 {
		max = 4096
	}
	req := map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
		"contents":          contents,
		"generationConfig":  map[string]any{"temperature": temp, "maxOutputTokens": max},
	}
	if opts.Search {
		req["tools"] = []map[string]any{{"googleSearch": map[string]any{}}}
	}
	gen, usage, err := v.generate(ctx, req)
	if err != nil {
		return Answer{}, usage, err
	}
	ans := Answer{Text: gen.text}
	seen := map[string]bool{}
	for _, ch := range gen.grounding.Chunks {
		u := strings.TrimSpace(ch.Web.URI)
		if u == "" || seen[u] || !strings.HasPrefix(u, "https://") {
			continue
		}
		seen[u] = true
		title := strings.TrimSpace(ch.Web.Title)
		if title == "" {
			title = u
		}
		ans.Sources = append(ans.Sources, Source{Title: title, URL: u})
	}
	return ans, usage, nil
}
