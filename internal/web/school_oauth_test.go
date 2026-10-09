package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wangui/internal/api"
)

const (
	testOAuthState = "fictional-state-for-tests"
	testOAuthToken = "fictional.jwt.token"
)

func TestSchoolOAuthPrepareAndExchange(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	server := newSchoolOAuthTestServer(t, schoolOAuthTestOptions{
		onLogin: func(r *http.Request) {
			loginCalls.Add(1)
			if cookie, err := r.Cookie("fictional_session"); err != nil || cookie.Value != "cookie-for-tests" {
				t.Errorf("login cookie = %v, %v", cookie, err)
			}
		},
	})
	defer server.Close()

	manager := newTestSchoolOAuthManager(server.URL)
	prepared, err := manager.prepare(context.Background(), "user:fictional-user")
	if err != nil {
		t.Fatalf("prepare() error = %v", err)
	}
	assertAuthorizationURL(t, prepared.AuthorizationURL)
	if prepared.AttemptID == "" || prepared.AttemptID == testOAuthState {
		t.Fatal("attempt ID must be non-empty and distinct from school state")
	}

	token, user, err := manager.exchange(
		context.Background(),
		"user:fictional-user",
		prepared.AttemptID,
		testCallbackURL(testOAuthState),
	)
	if err != nil {
		t.Fatalf("exchange() error = %v", err)
	}
	if token != testOAuthToken {
		t.Fatalf("token = %q, want fictional test token", token)
	}
	if user == nil || user.UserNumber != "2099000001" {
		t.Fatalf("user = %#v", user)
	}
	if loginCalls.Load() != 1 {
		t.Fatalf("login calls = %d, want 1", loginCalls.Load())
	}
	if _, _, err := manager.exchange(context.Background(), "user:fictional-user", prepared.AttemptID, testCallbackURL(testOAuthState)); err == nil || !strings.Contains(err.Error(), "已提交") {
		t.Fatalf("second exchange error = %v, want consumed error", err)
	}
}

func TestSchoolOAuthRejectsInvalidCallbacksBeforeLogin(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	server := newSchoolOAuthTestServer(t, schoolOAuthTestOptions{
		onLogin: func(*http.Request) { loginCalls.Add(1) },
	})
	defer server.Close()

	manager := newTestSchoolOAuthManager(server.URL)
	prepared, err := manager.prepare(context.Background(), "user:fictional-user")
	if err != nil {
		t.Fatalf("prepare() error = %v", err)
	}
	valid := testCallbackURL(testOAuthState)
	tests := []struct {
		name     string
		callback string
	}{
		{name: "wrong state", callback: testCallbackURL("different-fictional-state")},
		{name: "repeated code", callback: valid + "&code=second-fictional-code"},
		{name: "foreign origin", callback: "https://example.invalid/%E5%86%9C%E5%A4%A7%E7%99%BD.png?code=fake&state=" + testOAuthState},
		{name: "wrong path", callback: schoolSite + "/wrong.png?code=fake&state=" + testOAuthState},
		{name: "authorization entry", callback: prepared.AuthorizationURL},
		{name: "raw code", callback: "fictional-code-only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := manager.exchange(context.Background(), "user:fictional-user", prepared.AttemptID, tt.callback); err == nil {
				t.Fatal("exchange() expected validation error")
			}
		})
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("login calls = %d, want 0", loginCalls.Load())
	}

	if _, _, err := manager.exchange(context.Background(), "other-audience", prepared.AttemptID, valid); err == nil {
		t.Fatal("exchange() with another audience expected error")
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("login calls after audience mismatch = %d, want 0", loginCalls.Load())
	}

	if _, _, err := manager.exchange(context.Background(), "user:fictional-user", prepared.AttemptID, valid); err != nil {
		t.Fatalf("valid exchange after local validation errors = %v", err)
	}
}

func TestSchoolOAuthExpiredAttemptDoesNotLogin(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	server := newSchoolOAuthTestServer(t, schoolOAuthTestOptions{
		onLogin: func(*http.Request) { loginCalls.Add(1) },
	})
	defer server.Close()

	now := time.Date(2035, time.January, 2, 3, 4, 5, 0, time.UTC)
	manager := newTestSchoolOAuthManager(server.URL)
	manager.now = func() time.Time { return now }
	prepared, err := manager.prepare(context.Background(), "user:fictional-user")
	if err != nil {
		t.Fatalf("prepare() error = %v", err)
	}
	now = now.Add(schoolOAuthLifetime)
	if _, _, err := manager.exchange(context.Background(), "user:fictional-user", prepared.AttemptID, testCallbackURL(testOAuthState)); err == nil || !strings.Contains(err.Error(), "已过期") {
		t.Fatalf("exchange() error = %v, want expired error", err)
	}
	if loginCalls.Load() != 0 {
		t.Fatalf("login calls = %d, want 0", loginCalls.Load())
	}
}

func TestSchoolOAuthFailedExchangeCannotBeRetried(t *testing.T) {
	t.Parallel()

	var loginCalls atomic.Int32
	server := newSchoolOAuthTestServer(t, schoolOAuthTestOptions{
		loginStatus: http.StatusBadGateway,
		onLogin:     func(*http.Request) { loginCalls.Add(1) },
	})
	defer server.Close()

	manager := newTestSchoolOAuthManager(server.URL)
	prepared, err := manager.prepare(context.Background(), "user:fictional-user")
	if err != nil {
		t.Fatalf("prepare() error = %v", err)
	}
	if _, _, err := manager.exchange(context.Background(), "user:fictional-user", prepared.AttemptID, testCallbackURL(testOAuthState)); err == nil {
		t.Fatal("exchange() expected school error")
	}
	if _, _, err := manager.exchange(context.Background(), "user:fictional-user", prepared.AttemptID, testCallbackURL(testOAuthState)); err == nil || !strings.Contains(err.Error(), "已提交") {
		t.Fatalf("retry error = %v, want consumed error", err)
	}
	if loginCalls.Load() != 1 {
		t.Fatalf("login calls = %d, want 1", loginCalls.Load())
	}
}

func TestSchoolOAuthRequiresTokenAndVerifiedProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		missingToken bool
		loginToken   string
		profileCode  int
		want         string
	}{
		{name: "missing token", missingToken: true, want: "accessToken"},
		{name: "profile rejected", loginToken: testOAuthToken, profileCode: http.StatusUnauthorized, want: "未验证通过"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newSchoolOAuthTestServer(t, schoolOAuthTestOptions{
				missingToken: tt.missingToken,
				loginToken:   tt.loginToken,
				profileCode:  tt.profileCode,
			})
			defer server.Close()
			manager := newTestSchoolOAuthManager(server.URL)
			prepared, err := manager.prepare(context.Background(), "user:fictional-user")
			if err != nil {
				t.Fatalf("prepare() error = %v", err)
			}
			if _, _, err := manager.exchange(context.Background(), "user:fictional-user", prepared.AttemptID, testCallbackURL(testOAuthState)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("exchange() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestSchoolOAuthConcurrentExchangeIsRejected(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})
	server := newSchoolOAuthTestServer(t, schoolOAuthTestOptions{
		onLogin: func(*http.Request) {
			close(started)
			<-release
		},
	})
	defer server.Close()
	manager := newTestSchoolOAuthManager(server.URL)
	prepared, err := manager.prepare(context.Background(), "user:fictional-user")
	if err != nil {
		t.Fatalf("prepare() error = %v", err)
	}

	firstDone := make(chan error, 1)
	go func() {
		_, _, exchangeErr := manager.exchange(context.Background(), "user:fictional-user", prepared.AttemptID, testCallbackURL(testOAuthState))
		firstDone <- exchangeErr
	}()
	<-started
	if _, _, err := manager.exchange(context.Background(), "user:fictional-user", prepared.AttemptID, testCallbackURL(testOAuthState)); err == nil || !strings.Contains(err.Error(), "正在兑换") {
		t.Fatalf("concurrent exchange error = %v, want busy error", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first exchange error = %v", err)
	}
}

type schoolOAuthTestOptions struct {
	loginStatus  int
	missingToken bool
	loginToken   string
	profileCode  int
	onLogin      func(*http.Request)
}

func newSchoolOAuthTestServer(t *testing.T, options schoolOAuthTestOptions) *httptest.Server {
	t.Helper()
	if options.loginToken == "" && !options.missingToken && options.loginStatus == 0 && options.profileCode == 0 {
		options.loginToken = testOAuthToken
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/oauth2/prepare":
			if r.Method != http.MethodPost {
				t.Errorf("prepare method = %s", r.Method)
			}
			http.SetCookie(w, &http.Cookie{Name: "fictional_session", Value: "cookie-for-tests", Path: "/"})
			writeTestEnvelope(t, w, http.StatusOK, http.StatusOK, map[string]any{"state": testOAuthState})
		case "/auth/oauth2/login":
			if options.onLogin != nil {
				options.onLogin(r)
			}
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode login body: %v", err)
			}
			if body["code"] != "fictional-fresh-code" || body["state"] != testOAuthState || len(body) != 2 {
				t.Errorf("login body = %#v", body)
			}
			if options.loginStatus != 0 {
				writeTestEnvelope(t, w, options.loginStatus, options.loginStatus, nil)
				return
			}
			writeTestEnvelope(t, w, http.StatusOK, http.StatusOK, map[string]any{
				"accessToken": options.loginToken,
				"isNewUser":   nil,
			})
		case "/auth/user":
			if r.Header.Get("Authorization") != "Bearer "+testOAuthToken {
				t.Errorf("profile authorization header is missing the fictional token")
			}
			if options.profileCode != 0 {
				writeTestEnvelope(t, w, options.profileCode, options.profileCode, nil)
				return
			}
			writeTestEnvelope(t, w, http.StatusOK, http.StatusOK, map[string]any{
				"userName":   "测试用户",
				"userNumber": "2099000001",
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func newTestSchoolOAuthManager(baseURL string) *schoolOAuthManager {
	manager := newSchoolOAuthManager()
	manager.clientFactory = func() *api.Client {
		client := api.NewOAuth()
		client.BaseURL = baseURL
		return client
	}
	return manager
}

func testCallbackURL(state string) string {
	values := url.Values{"code": {"fictional-fresh-code"}, "state": {state}}
	return schoolSite + "/%E5%86%9C%E5%A4%A7%E7%99%BD.png?" + values.Encode()
}

func assertAuthorizationURL(t *testing.T, raw string) {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	query := parsed.Query()
	if parsed.Scheme != "https" || parsed.Host != "open.weixin.qq.com" || parsed.Path != "/connect/oauth2/authorize" {
		t.Fatalf("authorization endpoint = %s", parsed.String())
	}
	callback, err := url.Parse(query.Get("redirect_uri"))
	if err != nil {
		t.Fatalf("parse redirect URI: %v", err)
	}
	if callback.Scheme != "https" || callback.Host != "xhbcs.henau.edu.cn" || callback.Path != schoolCallbackPath || query.Get("response_type") != "code" || query.Get("scope") != "snsapi_userinfo" || query.Get("state") != testOAuthState {
		t.Fatal("authorization query has unexpected fields")
	}
	if parsed.Fragment != "wechat_redirect" {
		t.Fatalf("authorization fragment = %q", parsed.Fragment)
	}
}

func writeTestEnvelope(t *testing.T, w http.ResponseWriter, httpStatus, code int, data any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code":    code,
		"message": "fictional test response",
		"data":    data,
	}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}
