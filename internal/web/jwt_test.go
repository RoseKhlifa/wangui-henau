package web

import (
	"encoding/base64"
	"testing"
)

func TestParseJWTAccountClaimCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		wantID  string
	}{
		{name: "legacy issuer", payload: `{"iss":"fictional-user-a","exp":4102444800}`, wantID: "fictional-user-a"},
		{name: "camel user ID", payload: `{"userId":"fictional-user-b","exp":4102444800}`, wantID: "fictional-user-b"},
		{name: "snake user ID", payload: `{"user_id":"fictional-user-c","exp":4102444800}`, wantID: "fictional-user-c"},
		{name: "subject fallback", payload: `{"sub":"fictional-user-d","exp":4102444800}`, wantID: "fictional-user-d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			token := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
				base64.RawURLEncoding.EncodeToString([]byte(tt.payload)) + ".fictional-signature"
			claims, err := parseJWT(token)
			if err != nil {
				t.Fatalf("parseJWT() error = %v", err)
			}
			if claims.Iss != tt.wantID {
				t.Fatalf("parseJWT() account ID = %q, want %q", claims.Iss, tt.wantID)
			}
		})
	}
}
