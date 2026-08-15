package exportproxy

import "testing"

func TestEnsureHostPort(t *testing.T) {
	cases := map[string]string{
		"example.com":        "example.com:443",
		"example.com:8443":   "example.com:8443",
		"203.0.113.8":        "203.0.113.8:443",
		"203.0.113.8:80":     "203.0.113.8:80",
		"2001:db8::1":        "[2001:db8::1]:443",
		"[2001:db8::1]:8443": "[2001:db8::1]:8443",
	}
	for input, want := range cases {
		if got := ensureHostPort(input, "443"); got != want {
			t.Errorf("ensureHostPort(%q) = %q, want %q", input, got, want)
		}
	}
}
