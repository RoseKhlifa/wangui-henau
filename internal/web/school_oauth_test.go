package web

import "testing"

func TestExtractOAuthCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   schoolAuthInput
		want string
	}{
		{
			name: "callback url",
			in: schoolAuthInput{
				CallbackURL: "https://example.invalid/?code=fictional-code-123&state=EXAMPLE#/checkin",
			},
			want: "fictional-code-123",
		},
		{
			name: "raw code",
			in: schoolAuthInput{
				OAuthCode: "fictional-code-123",
			},
			want: "fictional-code-123",
		},
		{
			name: "query only",
			in: schoolAuthInput{
				CallbackURL: "?code=fictional-code-123&state=EXAMPLE",
			},
			want: "fictional-code-123",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := extractOAuthCode(tt.in)
			if err != nil {
				t.Fatalf("extractOAuthCode() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("extractOAuthCode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractOAuthCodeMissingCode(t *testing.T) {
	t.Parallel()

	if _, err := extractOAuthCode(schoolAuthInput{
		CallbackURL: "https://example.invalid/#/checkin",
	}); err == nil {
		t.Fatal("extractOAuthCode() expected error, got nil")
	}
}
