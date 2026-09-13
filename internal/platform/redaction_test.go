package platform

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// This file is the standing audit for Principle III: the Blizzard access token,
// the session token and the client secret must never reach a log line, a
// template, a URL, or a response header.

const sentinelToken = "SENTINEL-ACCESS-TOKEN-do-not-log"

// TestTokensNeverReachTheResponse drives a real dashboard request with a
// recognisable token and asserts it appears nowhere in the output.
func TestTokensNeverReachTheResponse(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	fake := &fakeClient{
		refs: refsFor("Maintank"),
		profileFor: func(ref blizzard.CharacterRef) (blizzard.Character, error) {
			return memberOf(ref.Name, "TOMB"), nil
		},
	}

	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates() error = %v", err)
	}

	core := &Core{
		Deps:      Deps{Logger: logger, Blizzard: fake},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		Profiles:  newFetcher(fake),
		CSRF:      &CSRF{},
		Templates: templates,
	}
	core.Deps.RenderInLayout = core.RenderInLayout

	stub := newStub("probe", "Probe", "probe-body", true)
	stub.render = core.RenderInLayout

	handler, err := Mount(core, &auth.Handlers{Logger: logger}, []App{stub})
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/app/probe", nil)
	req = req.WithContext(ContextWithSession(req.Context(), auth.Session{
		User:        auth.User{ID: 1, BnetSub: "sub", BattleTag: "Tester#1234"},
		AccessToken: sentinelToken,
		ExpiresAt:   time.Now().Add(time.Hour),
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	if strings.Contains(rec.Body.String(), sentinelToken) {
		t.Error("the access token appeared in the rendered page")
	}
	for name, values := range rec.Header() {
		for _, v := range values {
			if strings.Contains(v, sentinelToken) {
				t.Errorf("the access token appeared in response header %s", name)
			}
		}
	}
	if strings.Contains(logs.String(), sentinelToken) {
		t.Errorf("the access token appeared in the logs: %s", logs.String())
	}
}

// TestRequestLoggerOmitsQueryStrings: query strings can carry an OAuth code,
// which is credential material.
func TestRequestLoggerOmitsQueryStrings(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	templates, err := LoadTemplates()
	if err != nil {
		t.Fatalf("LoadTemplates() error = %v", err)
	}

	core := &Core{
		Deps:      Deps{Logger: logger},
		Sessions:  &auth.SessionManager{Store: &auth.Store{}},
		CSRF:      &CSRF{},
		Templates: templates,
	}

	handler, err := Mount(core, &auth.Handlers{Logger: logger}, nil)
	if err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"/?code=SECRET-OAUTH-CODE&state=abc", nil))

	if strings.Contains(logs.String(), "SECRET-OAUTH-CODE") {
		t.Errorf("the request log included a query string carrying an OAuth code: %s", logs.String())
	}
	// The log line should still be useful.
	if !strings.Contains(logs.String(), `"path":"/"`) {
		t.Errorf("the request log is missing the path: %s", logs.String())
	}
}

// TestNoSourceLogsTheAccessToken is a static guard against a future edit
// logging the token by name. It reads the production sources and looks for a
// slog call mentioning an access token.
func TestNoSourceLogsTheAccessToken(t *testing.T) {
	roots := []string{".", filepath.Join("..", "auth"), filepath.Join("..", "blizzard")}

	// Substrings that would indicate a token being handed to a logger.
	banned := []string{
		`"access_token"`,
		`"token", `,
		`"bnet_access_token"`,
		"AccessToken)",
	}

	for _, root := range roots {
		files, err := filepath.Glob(filepath.Join(root, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", root, err)
		}

		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read %s: %v", file, err)
			}

			for _, line := range strings.Split(string(src), "\n") {
				if !strings.Contains(line, "Logger.") && !strings.Contains(line, "logger.") &&
					!strings.Contains(line, "slog.") {
					continue
				}
				for _, bad := range banned {
					if strings.Contains(line, bad) {
						t.Errorf("%s appears to log token material: %s",
							file, strings.TrimSpace(line))
					}
				}
			}
		}
	}
}
