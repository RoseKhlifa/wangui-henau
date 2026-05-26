package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	BaseURL   = "https://xhbcs.henau.edu.cn/api"
	DefaultUA = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.40(0x18002834) NetType/WIFI Language/zh_CN"
)

// Client wraps the henau wangui REST API.
type Client struct {
	BaseURL string
	Token   string
	UA      string
	HTTP    *http.Client
}

// New creates a new client with sane defaults.
func New(token string) *Client {
	return &Client{
		BaseURL: BaseURL,
		Token:   token,
		UA:      DefaultUA,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// APIError is returned when the backend reports code != 200.
type APIError struct {
	Code       int
	Message    string
	Path       string
	HTTPStatus int             // raw HTTP status from the school server
	Data       json.RawMessage // raw `data` field (school sometimes attaches diagnostics here)
	RawBody    []byte          // full response body — kept for verbose diagnostic logging
}

func (e *APIError) Error() string {
	return fmt.Sprintf("api %s: code=%d msg=%q", e.Path, e.Code, e.Message)
}

// IsAuthExpired reports whether the error looks like an expired/invalid token.
func IsAuthExpired(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code == 401 || ae.Code == 403
	}
	return false
}

// do executes a request and unmarshals the `data` field of the envelope into out.
// out may be nil.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Origin", "https://xhbcs.henau.edu.cn")
	req.Header.Set("Referer", "https://xhbcs.henau.edu.cn/")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("http %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	// Some 4xx/5xx may also return JSON envelope; try envelope first.
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode envelope (status=%d body=%q): %w", resp.StatusCode, truncate(string(raw), 200), err)
	}
	if env.Code != 200 {
		// Prefer HTTP status for auth detection.
		code := env.Code
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			code = resp.StatusCode
		}
		return &APIError{
			Code:       code,
			Message:    env.Message,
			Path:       path,
			HTTPStatus: resp.StatusCode,
			Data:       env.Data,
			RawBody:    raw,
		}
	}
	if out != nil && len(env.Data) > 0 && !bytes.Equal(env.Data, []byte("null")) {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("decode data for %s: %w", path, err)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
