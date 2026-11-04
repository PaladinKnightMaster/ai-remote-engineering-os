package server

import (
	"net/http"
	"testing"

	"aireos.local/packages/go/auth"
)

// The pin holds under REAL tokens and the connector's REAL verifier (audience
// connector-service): api's IssueService("api",...) token reaches the handler,
func TestGenerationPinWithRealPasetoTokens(t *testing.T) {
	key, _ := auth.GenerateKey()
	signer := auth.NewSigner(key, "c")
	cfg := Config{Verifier: auth.AsMiddlewareVerifier(auth.NewVerifier(key, "c", "connector-service")), SecretKey: "k"}
	api, _ := signer.IssueService("api", "connector-service")
	broker, _ := signer.IssueService("session-broker", "connector-service")
	const path = "/v1/credentials/not-a-uuid/generate" // a pass reaches validation: 400, no DB needed
	if got := status(t, cfg, api, "POST", path, `{}`); got != http.StatusBadRequest {
		t.Errorf("api token = %d, want 400 (admitted)", got)
	}
	if got := status(t, cfg, broker, "POST", path, `{}`); got != http.StatusForbidden {
		t.Errorf("session-broker token = %d, want 403", got)
	}
}
