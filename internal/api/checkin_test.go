package api

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUnmaskPrepareValue(t *testing.T) {
	t.Parallel()
	got, err := unmaskPrepareValue("aXbcYdeZfgh")
	if err != nil {
		t.Fatalf("unmaskPrepareValue() error = %v", err)
	}
	if got != "abcdefgh" {
		t.Fatalf("unmaskPrepareValue() = %q, want %q", got, "abcdefgh")
	}
}

func TestSignUsesPrepareAndEncryptedEnvelope(t *testing.T) {
	t.Parallel()

	const signKey = "fictional-sign-key"
	aesKey := []byte("0123456789abcdef")
	nonce := "fictional-nonce"
	prepareCalls := 0
	postCalls := 0
	checkedIn := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/checkin/available-rules":
			writeTestEnvelope(t, w, []Rule{{RuleID: 42, RuleName: "虚构规则"}})
		case r.Method == http.MethodGet && r.URL.Path == "/checkin/status":
			if got := r.URL.Query().Get("ruleId"); got != "42" {
				t.Fatalf("status ruleId = %q, want 42", got)
			}
			writeTestEnvelope(t, w, map[string]any{
				"canCheckin":   !checkedIn,
				"hasCheckedIn": checkedIn,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/checkin/prepare":
			prepareCalls++
			writeTestEnvelope(t, w, map[string]string{
				"a":     maskForTest(signKey),
				"b":     maskForTest(base64.StdEncoding.EncodeToString(aesKey)),
				"nonce": nonce,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/checkin":
			postCalls++
			if got := r.Header.Get("X-Requested-With"); got != "XMLHttpRequest" {
				t.Errorf("X-Requested-With = %q", got)
			}
			var envelope encryptedSignEnvelope
			if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
				t.Fatalf("decode encrypted envelope: %v", err)
			}
			if envelope.Nonce != nonce {
				t.Fatalf("nonce = %q, want %q", envelope.Nonce, nonce)
			}
			plain := decryptEnvelopeForTest(t, envelope.Param, aesKey)
			var payload signedCheckinPayload
			if err := json.Unmarshal(plain, &payload); err != nil {
				t.Fatalf("decode signed payload: %v", err)
			}
			if payload.RuleID != "42" || payload.Latitude != "12.345678" || payload.Longitude != "98.765432" {
				t.Fatalf("unexpected signed payload: %+v", payload)
			}
			if payload.LocationAddress != "示例市测试路" {
				t.Fatalf("locationAddress = %q", payload.LocationAddress)
			}
			source := canonicalSignSource(payload, signKey)
			sum := md5.Sum([]byte(source))
			wantSign := strings.ToUpper(hex.EncodeToString(sum[:]))
			if payload.Sign != wantSign {
				t.Fatalf("sign = %q, want %q", payload.Sign, wantSign)
			}
			checkedIn = true
			writeTestEnvelope(t, w, map[string]bool{"accepted": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := New("fictional.jwt.token")
	c.BaseURL = server.URL
	c.HTTP = server.Client()
	rule, err := c.CurrentRule(context.Background())
	if err != nil {
		t.Fatalf("CurrentRule() error = %v", err)
	}
	if rule.RuleID != 42 {
		t.Fatalf("CurrentRule() ID = %d, want 42", rule.RuleID)
	}
	before, err := c.CheckinStatus(context.Background(), rule.RuleID)
	if err != nil || !before.CanCheckin {
		t.Fatalf("pre-sign CheckinStatus() = %+v, %v", before, err)
	}
	data, err := c.Sign(context.Background(), SignRequest{
		RuleID:          rule.RuleID,
		Latitude:        12.345678,
		Longitude:       98.765432,
		DeviceModel:     "Example Phone",
		DeviceSystem:    "Example OS",
		LocationAddress: "示例市测试路",
	})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if prepareCalls != 1 || postCalls != 1 {
		t.Fatalf("calls prepare=%d post=%d, want 1 each", prepareCalls, postCalls)
	}
	if !strings.Contains(string(data), "accepted") {
		t.Fatalf("Sign() data = %s", data)
	}
	after, err := c.CheckinStatus(context.Background(), rule.RuleID)
	if err != nil || after.HasCheckedIn == nil || !*after.HasCheckedIn {
		t.Fatalf("post-sign CheckinStatus() = %+v, %v", after, err)
	}
}

func TestSignRequiresLocationAddressBeforePrepare(t *testing.T) {
	t.Parallel()
	c := New("fictional.jwt.token")
	if _, err := c.Sign(context.Background(), SignRequest{RuleID: 1}); err == nil {
		t.Fatal("Sign() expected location address error")
	}
}

func maskForTest(value string) string {
	var b strings.Builder
	next := 0
	for i := 0; next < len(value) || i <= 7; i++ {
		if i == 1 || i == 4 || i == 7 {
			b.WriteByte('X')
			continue
		}
		if next < len(value) {
			b.WriteByte(value[next])
			next++
		}
	}
	return b.String()
}

func decryptEnvelopeForTest(t *testing.T, encoded string, key []byte) []byte {
	t.Helper()
	ciphertext, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	plain := make([]byte, len(ciphertext))
	for offset := 0; offset < len(ciphertext); offset += block.BlockSize() {
		block.Decrypt(plain[offset:offset+block.BlockSize()], ciphertext[offset:offset+block.BlockSize()])
	}
	padding := int(plain[len(plain)-1])
	return plain[:len(plain)-padding]
}

func writeTestEnvelope(t *testing.T, w io.Writer, data any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code":    200,
		"message": "ok",
		"data":    data,
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}
}
