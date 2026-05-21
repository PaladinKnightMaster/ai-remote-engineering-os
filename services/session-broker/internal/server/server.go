// Package server is the session-broker HTTP surface (ADR-0043). At P0 this is a
// skeleton: an unauthenticated readiness endpoint (served over HTTP/2 via h2c by
// main) and a capability-gated WebSocket channel that echoes — the live-session
// transport other tickets build on. Session-lifecycle REST + oapi-codegen land
// with the session feature ticket (WebSocket does not model cleanly in OpenAPI).
package server

import (
	"net/http"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	mw "aireos.local/packages/go/middleware"
)

type broker struct{}

// New returns the session-broker router. The WebSocket channel is gated by
// RequireCapability over the agent-remote-session edge (the cockpit's remote
// session capability, docs/security/agent-permission-model.md); /healthz is
// public. The same shared middleware chain used by every other service applies
// (ADR-0043 §Verification item 5).
func New(v mw.Verifier) http.Handler {
	b := &broker{}
	r := chi.NewRouter()
	r.Get("/healthz", b.healthz)
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireCapability(v, "agent-remote-session", "human-agent"))
		r.Get("/v1/sessions/{id}/channel", b.channel)
	})
	return r
}

func (b *broker) healthz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

// channel upgrades to a WebSocket and echoes messages back (skeleton transport).
func (b *broker) channel(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the error response
	}
	defer c.CloseNow()

	ctx := r.Context()
	for {
		typ, msg, err := c.Read(ctx)
		if err != nil {
			return // client closed or context done
		}
		if err := c.Write(ctx, typ, msg); err != nil {
			return
		}
	}
}
