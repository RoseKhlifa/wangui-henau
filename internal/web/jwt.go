package web

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// jwtClaims is the subset of the JWT payload we actually consume.
type jwtClaims struct {
	Iss       string `json:"iss"`
	Subject   string `json:"sub"`
	UserID    string `json:"userId"`
	UserIDAlt string `json:"user_id"`
	UID       string `json:"uid"`
	Exp       int64  `json:"exp"`
}

// parseJWT decodes the payload of an unsigned/unverified JWT.
// The signature is not validated here — this only extracts account ID + exp.
func parseJWT(token string) (*jwtClaims, error) {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "Bearer ")
	token = strings.TrimPrefix(token, "bearer ")
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("不是有效的 JWT 格式（需要三段以 . 分隔）")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("JWT payload 不是合法 base64url")
	}
	var c jwtClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, errors.New("JWT payload 不是合法 JSON")
	}
	if c.Iss == "" {
		// Older tokens used iss as the account identifier. Keep that as the
		// first choice, but accept common successor claim names. The token is
		// still validated against a read-only school endpoint before use.
		for _, candidate := range []string{c.UserID, c.UserIDAlt, c.UID, c.Subject} {
			if candidate = strings.TrimSpace(candidate); candidate != "" {
				c.Iss = candidate
				break
			}
		}
	}
	if c.Iss == "" {
		return nil, errors.New("JWT 缺少账号标识字段")
	}
	if c.Exp == 0 {
		return nil, errors.New("JWT 缺少 exp 字段")
	}
	return &c, nil
}

func (c *jwtClaims) ExpiresAt() time.Time { return time.Unix(c.Exp, 0) }

// normalizeToken strips "Bearer " prefix and whitespace.
func normalizeToken(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "Bearer ")
	s = strings.TrimPrefix(s, "bearer ")
	return strings.TrimSpace(s)
}
