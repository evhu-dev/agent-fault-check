package runner

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	raw := "failed connecting to postgres://admin:supersecret@db.internal:5432/prod " +
		"and redis://:redispass@127.0.0.1:6379/0 " +
		"with Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9 " +
		"key=AFC_PRO_-00000000-0000-0000-0000-000000000000 " +
		"key2=AFCPRO-11111111-1111-1111-1111-111111111111 " +
		"token=ghp_1234567890abcdefghij123456 " +
		"OPENAI_API_KEY=sk-proj-1234567890abcdefghijklmnop"

	got := RedactSecrets(raw)
	for _, secret := range []string{
		"supersecret",
		"redispass",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		"00000000-0000-0000-0000-000000000000",
		"11111111-1111-1111-1111-111111111111",
		"ghp_1234567890abcdefghij123456",
		"sk-proj-1234567890abcdefghijklmnop",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("expected secret %q to be redacted, got: %s", secret, got)
		}
	}
	if !strings.Contains(got, "postgres://admin:[REDACTED]@db.internal:5432/prod") {
		t.Fatalf("expected credentialed postgres URL to preserve host while redacting password: %s", got)
	}
}
