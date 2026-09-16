package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Vertex calls Gemini through Vertex AI as the VM (contracts/external-apis.md,
// research D10): a bearer token from the metadata server, no key. The
// project comes from the metadata server too, so there is nothing to
// configure but the model and the region.
type Vertex struct {
	Model  string
	Region string

	// MetadataURL and EndpointBase default to Google's; a test points them
	// at a local server. EndpointBase, when set, replaces the whole
	// "https://{region}-aiplatform.googleapis.com" prefix.
	MetadataURL  string
	EndpointBase string
	HTTP         *http.Client

	// Temperature and MaxOutputTokens shape the answer; zero means the
	// defaults below.
	Temperature     float64
	MaxOutputTokens int

	mu      sync.Mutex
	token   string
	expiry  time.Time
	project string
	now     func() time.Time
}

var _ Writer = (*Vertex)(nil)

// NewVertex builds a client for the model in the region.
func NewVertex(model, region string) *Vertex {
	return &Vertex{
		Model: model, Region: region,
		MetadataURL: "http://metadata.google.internal/computeMetadata/v1",
		HTTP:        &http.Client{Timeout: 90 * time.Second},
	}
}

func (v *Vertex) http() *http.Client {
	if v.HTTP != nil {
		return v.HTTP
	}
	return http.DefaultClient
}

func (v *Vertex) clock() time.Time {
	if v.now != nil {
		return v.now()
	}
	return time.Now()
}

// Ready proves the VM can mint a token and knows its project, for the
// startup log line. It makes no model call.
func (v *Vertex) Ready(ctx context.Context) error {
	if _, err := v.accessToken(ctx); err != nil {
		return err
	}
	_, err := v.projectID(ctx)
	return err
}

// metadata reads one value from the metadata server.
func (v *Vertex) metadata(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.MetadataURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	client := &http.Client{Timeout: 5 * time.Second}
	if v.HTTP != nil {
		client = v.HTTP
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metadata server: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metadata server answered %d for %s", resp.StatusCode, path)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<16))
}

func (v *Vertex) accessToken(ctx context.Context) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.token != "" && v.clock().Before(v.expiry) {
		return v.token, nil
	}
	body, err := v.metadata(ctx, "/instance/service-accounts/default/token")
	if err != nil {
		return "", err
	}
	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.AccessToken == "" {
		return "", fmt.Errorf("metadata server: token: %w", err)
	}
	v.token = payload.AccessToken
	v.expiry = v.clock().Add(time.Duration(payload.ExpiresIn)*time.Second - time.Minute)
	return v.token, nil
}

func (v *Vertex) projectID(ctx context.Context) (string, error) {
	v.mu.Lock()
	if v.project != "" {
		p := v.project
		v.mu.Unlock()
		return p, nil
	}
	v.mu.Unlock()
	body, err := v.metadata(ctx, "/project/project-id")
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(body))
	if p == "" {
		return "", errors.New("metadata server: empty project id")
	}
	v.mu.Lock()
	v.project = p
	v.mu.Unlock()
	return p, nil
}

func (v *Vertex) endpoint(project string) string {
	base := v.EndpointBase
	if base == "" {
		base = "https://" + v.Region + "-aiplatform.googleapis.com"
	}
	return fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent", base, project, v.Region, v.Model)
}

// Write asks the model.
func (v *Vertex) Write(ctx context.Context, system, prompt string) (string, Usage, error) {
	token, err := v.accessToken(ctx)
	if err != nil {
		return "", Usage{}, err
	}
	project, err := v.projectID(ctx)
	if err != nil {
		return "", Usage{}, err
	}
	temp, max := v.Temperature, v.MaxOutputTokens
	if temp == 0 {
		temp = 0.4
	}
	if max == 0 {
		max = 2048
	}
	body, _ := json.Marshal(map[string]any{
		"systemInstruction": map[string]any{"parts": []map[string]string{{"text": system}}},
		"contents":          []map[string]any{{"role": "user", "parts": []map[string]string{{"text": prompt}}}},
		"generationConfig":  map[string]any{"temperature": temp, "maxOutputTokens": max},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint(project), bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := v.http().Do(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("vertex ai: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests, http.StatusServiceUnavailable:
		return "", Usage{}, ErrBusy
	default:
		return "", Usage{}, fmt.Errorf("vertex ai answered %d", resp.StatusCode)
	}

	var payload struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		Usage struct {
			Prompt int `json:"promptTokenCount"`
			Output int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", Usage{}, fmt.Errorf("vertex ai: decode: %w", err)
	}
	usage := Usage{PromptTokens: payload.Usage.Prompt, OutputTokens: payload.Usage.Output}
	if len(payload.Candidates) == 0 || payload.Candidates[0].FinishReason == "SAFETY" {
		return "", usage, ErrDeclined
	}
	var text strings.Builder
	for _, p := range payload.Candidates[0].Content.Parts {
		text.WriteString(p.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", usage, ErrDeclined
	}
	return text.String(), usage, nil
}
