package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	"wangui/internal/api"
)

const (
	schoolSite          = "https://xhbcs.henau.edu.cn"
	schoolOAuthAppID    = "wx312828c5c278c93c"
	schoolCallbackPath  = "/农大白.png"
	schoolOAuthLifetime = 5 * time.Minute
)

type schoolOAuthPrepared struct {
	AttemptID        string `json:"attemptId"`
	AuthorizationURL string `json:"authorizationUrl"`
	ExpiresAt        int64  `json:"expiresAt"`
}

type schoolOAuthAttempt struct {
	id       string
	audience string
	state    string
	client   *api.Client
	expires  time.Time
	busy     bool
	consumed bool
}

type schoolOAuthManager struct {
	mu            sync.Mutex
	attempts      map[string]*schoolOAuthAttempt
	now           func() time.Time
	clientFactory func() *api.Client
}

func newSchoolOAuthManager() *schoolOAuthManager {
	return &schoolOAuthManager{
		attempts:      make(map[string]*schoolOAuthAttempt),
		now:           time.Now,
		clientFactory: api.NewOAuth,
	}
}

func (m *schoolOAuthManager) prepare(ctx context.Context, audience string) (*schoolOAuthPrepared, error) {
	audience = strings.TrimSpace(audience)
	if audience == "" {
		return nil, errors.New("OAuth audience is empty")
	}
	started := m.now()
	client := m.clientFactory()
	prepared, err := client.OAuth2Prepare(ctx)
	if err != nil {
		client.ClearCookies()
		return nil, fmt.Errorf("学校登录准备失败: %w", err)
	}
	expires := started.Add(schoolOAuthLifetime)
	if !m.now().Before(expires) {
		client.ClearCookies()
		return nil, errors.New("学校登录准备已过期，请重试")
	}
	id, err := randomAttemptID()
	if err != nil {
		client.ClearCookies()
		return nil, err
	}

	authorizationURL, err := buildSchoolAuthorizationURL(prepared.State)
	if err != nil {
		client.ClearCookies()
		return nil, err
	}
	attempt := &schoolOAuthAttempt{
		id:       id,
		audience: audience,
		state:    prepared.State,
		client:   client,
		expires:  expires,
	}

	m.mu.Lock()
	m.pruneLocked()
	m.attempts[id] = attempt
	m.mu.Unlock()
	time.AfterFunc(expires.Sub(m.now()), func() {
		m.expire(attempt)
	})
	return &schoolOAuthPrepared{
		AttemptID:        id,
		AuthorizationURL: authorizationURL,
		ExpiresAt:        expires.Unix(),
	}, nil
}

func (m *schoolOAuthManager) exchange(ctx context.Context, audience, attemptID, callbackText string) (string, *api.User, error) {
	m.mu.Lock()
	attempt := m.attempts[strings.TrimSpace(attemptID)]
	if attempt == nil || attempt.audience != strings.TrimSpace(audience) {
		m.pruneLocked()
		m.mu.Unlock()
		return "", nil, errors.New("没有匹配的本轮学校登录请求，请重新生成授权链接")
	}
	if !m.now().Before(attempt.expires) {
		attempt.client.ClearCookies()
		delete(m.attempts, attempt.id)
		m.mu.Unlock()
		return "", nil, errors.New("本轮学校登录已过期，请重新生成授权链接")
	}
	if attempt.busy {
		m.mu.Unlock()
		return "", nil, errors.New("本轮学校登录正在兑换，请勿重复提交")
	}
	if attempt.consumed {
		m.mu.Unlock()
		return "", nil, errors.New("本轮学校登录已提交过，请重新生成授权链接")
	}
	code, state, err := validateSchoolCallback(callbackText, attempt.state)
	if err != nil {
		m.mu.Unlock()
		return "", nil, err
	}
	// Mark consumed before the network request. A lost response is ambiguous:
	// the school may already have consumed the one-time code.
	attempt.busy = true
	attempt.consumed = true
	client := attempt.client
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		attempt.busy = false
		client.Token = ""
		client.ClearCookies()
		if !m.now().Before(attempt.expires) && m.attempts[attempt.id] == attempt {
			delete(m.attempts, attempt.id)
		}
		m.mu.Unlock()
	}()
	login, err := client.OAuth2Login(ctx, code, state)
	if err != nil {
		return "", nil, fmt.Errorf("学校 OAuth 兑换失败: %w", err)
	}
	token := normalizeToken(login.AccessToken)
	if token == "" {
		if login.IsNewUser != nil && *login.IsNewUser {
			return "", nil, errors.New("学校账号尚未完成绑定，请先在学校原页面完成绑定后重新授权")
		}
		return "", nil, errors.New("学校 OAuth 响应没有 accessToken，请重新授权")
	}
	client.Token = token
	user, err := client.GetUser(ctx)
	if err != nil {
		return "", nil, fmt.Errorf("学校用户接口未验证通过: %w", err)
	}
	return token, user, nil
}

func (m *schoolOAuthManager) pruneLocked() {
	now := m.now()
	for id, attempt := range m.attempts {
		if !attempt.busy && !now.Before(attempt.expires) {
			attempt.client.ClearCookies()
			delete(m.attempts, id)
		}
	}
}

func (m *schoolOAuthManager) expire(attempt *schoolOAuthAttempt) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.attempts[attempt.id] != attempt || attempt.busy || m.now().Before(attempt.expires) {
		return
	}
	attempt.client.Token = ""
	attempt.client.ClearCookies()
	delete(m.attempts, attempt.id)
}

func randomAttemptID() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate OAuth attempt ID: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func buildSchoolAuthorizationURL(state string) (string, error) {
	callback, err := url.Parse(schoolSite)
	if err != nil {
		return "", err
	}
	callback.Path = schoolCallbackPath
	authorize, err := url.Parse("https://open.weixin.qq.com/connect/oauth2/authorize")
	if err != nil {
		return "", err
	}
	q := authorize.Query()
	q.Set("appid", schoolOAuthAppID)
	q.Set("redirect_uri", callback.String())
	q.Set("response_type", "code")
	q.Set("scope", "snsapi_userinfo")
	q.Set("state", state)
	authorize.RawQuery = q.Encode()
	authorize.Fragment = "wechat_redirect"
	return authorize.String(), nil
}

func validateSchoolCallback(raw, expectedState string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 16*1024 {
		return "", "", errors.New("学校回调 URL 过长")
	}
	callback, err := url.Parse(raw)
	if err != nil || !callback.IsAbs() {
		return "", "", errors.New("请粘贴完整的学校回调 URL")
	}
	if callback.Scheme == "https" && callback.Host == "open.weixin.qq.com" {
		return "", "", errors.New("这是微信授权入口，不是授权后的学校回调")
	}
	if callback.Scheme != "https" || callback.Host != "xhbcs.henau.edu.cn" || callback.User != nil || callback.Path != schoolCallbackPath {
		return "", "", errors.New("学校回调的主机或路径不匹配")
	}
	values := callback.Query()
	codes, states := values["code"], values["state"]
	if len(codes) != 1 || len(states) != 1 {
		return "", "", errors.New("学校回调必须各包含一个 code 和 state")
	}
	code, state := strings.TrimSpace(codes[0]), states[0]
	if code == "" || len(code) > 4096 || strings.IndexFunc(code, unicode.IsSpace) >= 0 || strings.IndexFunc(code, unicode.IsControl) >= 0 {
		return "", "", errors.New("学校回调中的 code 格式无效")
	}
	if !constantTimeEqual(state, expectedState) {
		return "", "", errors.New("学校回调的 state 不属于本轮授权")
	}
	return code, state, nil
}

func constantTimeEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func schoolOAuthAPIErrorStatus(err error) int {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && (apiErr.Code == http.StatusUnauthorized || apiErr.Code == http.StatusForbidden) {
		return http.StatusUnauthorized
	}
	return http.StatusBadRequest
}

func activationOAuthAudience(inviteCode string) string {
	return "activate:" + strings.ToUpper(strings.TrimSpace(inviteCode))
}

func userOAuthAudience(userID string) string { return "user:" + strings.TrimSpace(userID) }

func adminUserOAuthAudience(userID string) string {
	return "admin-user:" + strings.TrimSpace(userID)
}

const adminGuestOAuthAudience = "admin-guest"
