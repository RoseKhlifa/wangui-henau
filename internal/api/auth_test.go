package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOAuth2PrepareAndLoginPreserveSession(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/oauth2/prepare":
			if r.Method != http.MethodPost {
				t.Errorf("prepare method = %s", r.Method)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode prepare body: %v", err)
			}
			if len(body) != 0 {
				t.Errorf("prepare body = %#v, want empty object", body)
			}
			http.SetCookie(w, &http.Cookie{Name: "fictional_session", Value: "cookie-for-tests", Path: "/"})
			writeAuthTestEnvelope(t, w, map[string]any{"state": "fictional-state"})
		case "/auth/oauth2/login":
			cookie, err := r.Cookie("fictional_session")
			if err != nil || cookie.Value != "cookie-for-tests" {
				t.Errorf("login cookie = %v, %v", cookie, err)
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode login body: %v", err)
			}
			if body["code"] != "fictional-code" || body["state"] != "fictional-state" || len(body) != 2 {
				t.Errorf("login body = %#v", body)
			}
			writeAuthTestEnvelope(t, w, map[string]any{
				"accessToken": "fictional.jwt.token",
				"isNewUser":   nil,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewOAuth()
	client.BaseURL = server.URL
	prepared, err := client.OAuth2Prepare(context.Background())
	if err != nil {
		t.Fatalf("OAuth2Prepare() error = %v", err)
	}
	if prepared.State != "fictional-state" {
		t.Fatalf("state = %q", prepared.State)
	}
	login, err := client.OAuth2Login(context.Background(), "fictional-code", prepared.State)
	if err != nil {
		t.Fatalf("OAuth2Login() error = %v", err)
	}
	if login.AccessToken != "fictional.jwt.token" || login.IsNewUser != nil {
		t.Fatalf("login = %#v", login)
	}
}

func TestOAuth2PrepareRequiresState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAuthTestEnvelope(t, w, map[string]any{"state": "  "})
	}))
	defer server.Close()
	client := NewOAuth()
	client.BaseURL = server.URL
	if _, err := client.OAuth2Prepare(context.Background()); err == nil {
		t.Fatal("OAuth2Prepare() expected empty-state error")
	}
}

func writeAuthTestEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code":    http.StatusOK,
		"message": "fictional test response",
		"data":    data,
	}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
