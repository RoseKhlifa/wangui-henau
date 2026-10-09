package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"wangui/internal/api"
)

type schoolAuthInput struct {
	Token       string `json:"token"`
	CallbackURL string `json:"callbackUrl"`
	OAuthCode   string `json:"oauthCode"`
}

type resolvedSchoolAuth struct {
	Token           string
	Claims          *jwtClaims
	User            *api.User
	ProfileHydrated bool
}

func (h *handlers) resolveSchoolAuth(ctx context.Context, in schoolAuthInput) (*resolvedSchoolAuth, int, error) {
	tok := normalizeToken(in.Token)
	if tok == "" {
		code, err := extractOAuthCode(in)
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		exchanged, status, err := exchangeOAuthCode(ctx, code)
		if err != nil {
			return nil, status, err
		}
		tok = exchanged
	}

	claims, err := parseJWT(tok)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if time.Until(claims.ExpiresAt()) < 5*time.Minute {
		return nil, http.StatusBadRequest, errors.New("学校 Token 已过期或即将过期，请重新扫码")
	}

	c := api.New(tok)
	su, profileErr := c.GetUser(ctx)
	profileHydrated := profileErr == nil
	if profileErr != nil {
		// /auth/user has changed independently of the check-in endpoints in
		// the past. A manually captured JWT is still accepted only after a
		// school-side, read-only endpoint confirms it is usable.
		if _, err := c.AvailableRules(ctx); err != nil {
			return nil, http.StatusUnauthorized, fmt.Errorf("Token 校验失败: %w", err)
		}
		su = &api.User{
			UserName:   "学校用户",
			UserNumber: claims.Iss,
		}
	}
	return &resolvedSchoolAuth{
		Token:           tok,
		Claims:          claims,
		User:            su,
		ProfileHydrated: profileHydrated,
	}, http.StatusOK, nil
}

func extractOAuthCode(in schoolAuthInput) (string, error) {
	if code := normalizeOAuthCode(in.OAuthCode); code != "" {
		return code, nil
	}

	raw := strings.TrimSpace(in.CallbackURL)
	if raw == "" {
		return "", errors.New("请粘贴扫码后的回调链接或 code")
	}
	if code := codeFromMaybeURL(raw); code != "" {
		return code, nil
	}
	return "", errors.New("回调链接中没有找到 code 参数")
}

func normalizeOAuthCode(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.ContainsAny(s, "?#=&/ \t\r\n") {
		return ""
	}
	return s
}

func codeFromMaybeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if code := codeFromParsedURL(raw); code != "" {
		return code
	}

	if strings.HasPrefix(raw, "?") {
		if q, err := url.ParseQuery(strings.TrimPrefix(raw, "?")); err == nil {
			return strings.TrimSpace(q.Get("code"))
		}
	}

	if strings.Contains(raw, "code=") {
		if q, err := url.ParseQuery(raw); err == nil {
			return strings.TrimSpace(q.Get("code"))
		}
	}

	return ""
}

func codeFromParsedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if code := strings.TrimSpace(u.Query().Get("code")); code != "" {
		return code
	}
	if frag := strings.TrimSpace(u.Fragment); frag != "" {
		if i := strings.IndexByte(frag, '?'); i >= 0 {
			if q, err := url.ParseQuery(frag[i+1:]); err == nil {
				return strings.TrimSpace(q.Get("code"))
			}
		}
	}
	return ""
}

func exchangeOAuthCode(ctx context.Context, code string) (string, int, error) {
	resp, err := api.New("").OAuth2Login(ctx, code)
	if err != nil {
		var ae *api.APIError
		if errors.As(err, &ae) {
			if ae.Code == http.StatusUnauthorized || ae.Code == http.StatusForbidden {
				return "", http.StatusUnauthorized, errors.New("扫码回调已失效，请重新扫码")
			}
			if ae.Message != "" {
				return "", http.StatusBadGateway, fmt.Errorf("学校 OAuth 登录失败: %s", ae.Message)
			}
		}
		return "", http.StatusBadGateway, fmt.Errorf("学校 OAuth 登录失败: %w", err)
	}
	if resp.IsNewUser && strings.TrimSpace(resp.AccessToken) == "" {
		return "", http.StatusBadRequest, errors.New("学校系统返回首次绑定状态，请先在手机里打开晚归页面完成学校侧初始化后再重试")
	}

	tok := normalizeToken(resp.AccessToken)
	if tok == "" {
		return "", http.StatusBadGateway, errors.New("学校系统未返回 accessToken，请重新扫码后再试")
	}
	return tok, http.StatusOK, nil
}
