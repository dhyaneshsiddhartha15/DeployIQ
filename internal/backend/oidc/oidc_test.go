package oidc

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func jwt(claims map[string]any) string {
	p, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(p) + ".sig"
}

func TestValidateClaims(t *testing.T) {
	c := New("https://idp.example.com/", "cid", "secret", nil, "")
	now := time.Unix(1_800_000_000, 0)
	c.Now = func() time.Time { return now }
	good := map[string]any{"iss": "https://idp.example.com", "aud": "cid", "exp": float64(now.Unix() + 60), "nonce": "n", "sub": "s"}
	if _, err := c.validate(jwt(good), "https://idp.example.com", "n"); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	clone := func(k string, v any) map[string]any {
		m := map[string]any{}
		for kk, vv := range good {
			m[kk] = vv
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}
	for name, claims := range map[string]map[string]any{
		"wrong issuer":   clone("iss", "https://evil.example.com"),
		"wrong audience": clone("aud", "other"),
		"expired":        clone("exp", float64(now.Unix()-120)),
		"no exp":         clone("exp", nil),
		"wrong nonce":    clone("nonce", "x"),
		"wrong azp":      clone("azp", "other"),
	} {
		if _, err := c.validate(jwt(claims), "https://idp.example.com", "n"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := c.validate(jwt(clone("aud", []any{"x", "cid"})), "https://idp.example.com", "n"); err != nil {
		t.Errorf("audience array rejected: %v", err)
	}
	for _, bad := range []string{"", "a.b", "a.!!!.c", "a." + base64.RawURLEncoding.EncodeToString([]byte("[")) + ".c"} {
		if _, err := c.validate(bad, "https://idp.example.com", "n"); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got := strs("a,b c"); len(got) != 3 {
		t.Error(got)
	}
	if c.Scopes[0] != "openid" || c.GroupsClaim != "groups" {
		t.Error("defaults")
	}
}
