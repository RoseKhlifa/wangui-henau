package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"wangui/internal/api"
)

type schoolAuthInput struct {
	Token          string `json:"token"`
	CallbackURL    string `json:"callbackUrl"`
	OAuthAttemptID string `json:"oauthAttemptId"`
}

type resolvedSchoolAuth struct {
	Token           string
	Claims          *jwtClaims
	User            *api.User
	ProfileHydrated bool
}

func (h *handlers) resolveSchoolAuth(ctx context.Context, in schoolAuthInput, audience string) (*resolvedSchoolAuth, int, error) {
	tok := normalizeToken(in.Token)
	var exchangedUser *api.User
	if tok == "" {
		if strings.TrimSpace(in.OAuthAttemptID) == "" || strings.TrimSpace(in.CallbackURL) == "" {
			return nil, http.StatusBadRequest, errors.New("请先生成本轮授权链接，再粘贴完整的学校回调 URL")
		}
		var err error
		tok, exchangedUser, err = h.schoolOAuth.exchange(ctx, audience, in.OAuthAttemptID, in.CallbackURL)
		if err != nil {
			return nil, schoolOAuthAPIErrorStatus(err), err
		}
	}

	claims, err := parseJWT(tok)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	if time.Until(claims.ExpiresAt()) < 5*time.Minute {
		return nil, http.StatusBadRequest, errors.New("学校 Token 已过期或即将过期，请重新授权")
	}

	if exchangedUser != nil {
		return &resolvedSchoolAuth{
			Token:           tok,
			Claims:          claims,
			User:            exchangedUser,
			ProfileHydrated: true,
		}, http.StatusOK, nil
	}

	// Manual JWT remains an advanced fallback. Validate it against the school
	// instead of trusting its unverified claims.
	c := api.New(tok)
	su, profileErr := c.GetUser(ctx)
	profileHydrated := profileErr == nil
	if profileErr != nil {
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

func (h *handlers) prepareUserTokenOAuth(w http.ResponseWriter, r *http.Request) {
	prepared, err := h.schoolOAuth.prepare(r.Context(), userOAuthAudience(userIDOf(r)))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prepared)
}

func (h *handlers) prepareAdminUserTokenOAuth(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.store.GetUser(r.Context(), id); err != nil {
		writeErr(w, http.StatusNotFound, "用户不存在")
		return
	}
	prepared, err := h.schoolOAuth.prepare(r.Context(), adminUserOAuthAudience(id))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prepared)
}

func (h *handlers) prepareAdminGuestOAuth(w http.ResponseWriter, r *http.Request) {
	prepared, err := h.schoolOAuth.prepare(r.Context(), adminGuestOAuthAudience)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, prepared)
}
