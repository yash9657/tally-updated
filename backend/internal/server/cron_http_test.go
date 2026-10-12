package server

import (
	"net/http/httptest"
	"testing"
)

func TestCronAuthorized(t *testing.T) {
	cases := []struct {
		name, secret, header string
		want                 bool
	}{
		{"exact bearer", "s3cret", "Bearer s3cret", true},
		{"wrong secret", "s3cret", "Bearer nope", false},
		{"prefix of secret", "s3cret", "Bearer s3cre", false},
		{"no bearer scheme", "s3cret", "s3cret", false},
		{"no header", "s3cret", "", false},
		{"empty secret never authorizes", "", "Bearer ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/internal/cron/tick", nil)
			if c.header != "" {
				r.Header.Set("Authorization", c.header)
			}
			if got := cronAuthorized(r, c.secret); got != c.want {
				t.Fatalf("cronAuthorized(%q, %q) = %v, want %v", c.header, c.secret, got, c.want)
			}
		})
	}
}
