package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
)

type Role struct {
	RoleID   int    `json:"roleId"`
	RoleCode string `json:"roleCode"`
	RoleName string `json:"roleName"`
}

type User struct {
	UserName      string `json:"userName"`
	UserNumber    string `json:"userNumber"`
	UserSection   string `json:"userSection"`
	UserClass     string `json:"userClass"`
	Gender        int    `json:"gender"`
	UserAvatarURL string `json:"userAvatarUrl"`
	AccountStatus int    `json:"accountStatus"`
	UserStatus    int    `json:"userStatus"`
	Roles         []Role `json:"roles"`
}

type OAuth2LoginData struct {
	AccessToken string `json:"accessToken"`
	IsNewUser   *bool  `json:"isNewUser"`
}

type OAuth2PrepareData struct {
	State string `json:"state"`
}

// NewOAuth creates an unauthenticated client with an isolated cookie jar.
// One instance must be retained for the entire prepare -> login -> verify
// attempt so the school's short-lived session cookies are preserved.
func NewOAuth() *Client {
	c := New("")
	jar, _ := cookiejar.New(nil)
	c.HTTP.Jar = jar
	return c
}

// ClearCookies destroys the transient school session held by this client.
func (c *Client) ClearCookies() {
	if c.HTTP == nil {
		return
	}
	jar, _ := cookiejar.New(nil)
	c.HTTP.Jar = jar
}

// OAuth2Prepare starts one school-side OAuth attempt and stores the returned
// cookies in this client's jar.
func (c *Client) OAuth2Prepare(ctx context.Context) (*OAuth2PrepareData, error) {
	var out OAuth2PrepareData
	if err := c.do(ctx, http.MethodPost, "/auth/oauth2/prepare", nil, map[string]any{}, &out); err != nil {
		return nil, err
	}
	out.State = strings.TrimSpace(out.State)
	if out.State == "" {
		return nil, errors.New("oauth2 prepare returned an empty state")
	}
	return &out, nil
}

// OAuth2Login exchanges a fresh callback code and its school-issued state for
// the school's JWT. It must use the same Client instance as OAuth2Prepare.
func (c *Client) OAuth2Login(ctx context.Context, code, state string) (*OAuth2LoginData, error) {
	code = strings.TrimSpace(code)
	state = strings.TrimSpace(state)
	if code == "" {
		return nil, errors.New("oauth2 code is empty")
	}
	if state == "" {
		return nil, errors.New("oauth2 state is empty")
	}
	var out OAuth2LoginData
	if err := c.do(ctx, http.MethodPost, "/auth/oauth2/login", nil, map[string]string{
		"code":  code,
		"state": state,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUser fetches the current authenticated user. Returns an APIError(401/403)
// when the token is invalid or expired.
//
// If the avatar URL is an http(s) URL, we eagerly fetch it with the same token
// and re-encode it as a data: URI. Reason: the SPA can't pull the avatar
// directly from the school's CDN — those endpoints require the school's auth
// cookie / referer and 403 from a third-party origin. Inlining is the simplest
// way to make `<img :src="userAvatarUrl">` just work in our front-end.
//
// Failure is non-fatal: we keep the original URL so Avatar.vue's onError
// fallback still kicks in.
func (c *Client) GetUser(ctx context.Context) (*User, error) {
	var u User
	if err := c.do(ctx, "GET", "/auth/user", nil, nil, &u); err != nil {
		return nil, err
	}
	if strings.HasPrefix(u.UserAvatarURL, "http") {
		if dataURI, err := c.fetchAsDataURI(ctx, u.UserAvatarURL); err == nil {
			u.UserAvatarURL = dataURI
		}
	}
	return &u, nil
}

// fetchAsDataURI downloads an asset with the client's token and Referer, then
// returns a data: URI. Capped at 2 MiB so a broken endpoint can't blow up our
// memory. Used to inline avatars; see GetUser for why.
func (c *Client) fetchAsDataURI(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Referer", "https://xhbcs.henau.edu.cn/")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("avatar fetch: status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return "", err
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = http.DetectContentType(raw)
	}
	if i := strings.Index(mime, ";"); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}

// Permissions returns the permission codes for the current user.
func (c *Client) Permissions(ctx context.Context) ([]string, error) {
	var p []string
	if err := c.do(ctx, "GET", "/auth/permissions", nil, nil, &p); err != nil {
		return nil, err
	}
	return p, nil
}
