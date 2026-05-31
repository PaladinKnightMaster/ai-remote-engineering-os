package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// echoAuth is a terminal handler that writes back whatever Authorization header
// it received, so the test can assert what CookieToAuthHeader handed downstream.
func echoAuth() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	})
}

func TestCookieToAuthHeader(t *testing.T) {
	h := CookieToAuthHeader(SessionCookieName)(echoAuth())

	t.Run("cookie becomes bearer when Authorization is absent", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/v1/sessions/x/channel", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "tok123"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if got := rec.Body.String(); got != "Bearer tok123" {
			t.Fatalf("want %q, got %q", "Bearer tok123", got)
		}
	})

	t.Run("existing Authorization wins over the cookie", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", "Bearer header-wins")
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "cookie-loses"})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if got := rec.Body.String(); got != "Bearer header-wins" {
			t.Fatalf("want existing header preserved, got %q", got)
		}
	})

	t.Run("no cookie and no header leaves Authorization empty", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if got := rec.Body.String(); got != "" {
			t.Fatalf("want empty Authorization, got %q", got)
		}
	})

	t.Run("empty cookie value is ignored", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookieName, Value: ""})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if got := rec.Body.String(); got != "" {
			t.Fatalf("want empty Authorization for empty cookie, got %q", got)
		}
	})
}
