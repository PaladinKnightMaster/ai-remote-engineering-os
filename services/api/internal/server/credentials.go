package server

// Operator routes for connector-generated target credentials (ADR-0079): PUBLIC
// material only - internal/connector rejects any response carrying the private half.

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"aireos.local/services/api/internal/connector"
	"aireos.local/services/api/internal/store"
)

// authorizedKeysOptions is the restricted option set for a target's authorized_keys
// entry. It MUST equal RESTRICT_OPTS in infra/offline/setup-loopback-demo.sh
// (credentials_test.go fails if they drift).
const authorizedKeysOptions = "restrict,no-port-forwarding,no-agent-forwarding,no-X11-forwarding,no-pty"

type resourceCredential struct {
	PublicKey          string `json:"publicKey"`
	Fingerprint        string `json:"fingerprint"`
	AuthorizedKeysLine string `json:"authorizedKeysLine"`
}

func toCredential(p connector.Public) resourceCredential {
	return resourceCredential{p.PublicKey, p.Fingerprint, authorizedKeysOptions + " " + p.PublicKey}
}

// connectorFailure maps a client error to a fixed, key-free response.
func (a *apiHandlers) connectorFailure(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, connector.ErrExists):
		writeError(w, http.StatusConflict, "conflict", "a credential already exists for this resource; request rotation (rotate: true) to replace it - access to the target breaks until the new public key is installed on it")
	case errors.Is(err, connector.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no public key is recorded for this resource")
	default:
		log.Printf("connector %s: %v", op, err) // errors never carry connector bodies
		writeError(w, http.StatusBadGateway, "bad_gateway", "connector-service is unavailable or answered unusably")
	}
}

func (a *apiHandlers) credentialPrecheck(w http.ResponseWriter, r *http.Request, id string) bool {
	if a.connector == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "credential generation is not configured: CONNECTOR_URL is not set")
		return false
	}
	if _, err := a.store.Get(r.Context(), id); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "resource not found")
		return false
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "get failed")
		return false
	}
	return true
}

// CreateResourceCredential (POST /v1/resources/{id}/credential).
func (a *apiHandlers) CreateResourceCredential(w http.ResponseWriter, r *http.Request, id string) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if !a.credentialPrecheck(w, r, id) {
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	dec.DisallowUnknownFields()
	var in CredentialGenerateRequest
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "body must be {\"rotate\": true|false}")
		return
	}
	pub, err := a.connector.Generate(r.Context(), id, operator, in.Rotate)
	if err != nil {
		a.connectorFailure(w, "generate", err)
		return
	}
	writeJSON(w, http.StatusOK, toCredential(pub))
}

// GetResourceCredential (GET /v1/resources/{id}/credential).
func (a *apiHandlers) GetResourceCredential(w http.ResponseWriter, r *http.Request, id string) {
	if !a.credentialPrecheck(w, r, id) {
		return
	}
	pub, err := a.connector.PublicKey(r.Context(), id)
	if err != nil {
		a.connectorFailure(w, "public read", err)
		return
	}
	writeJSON(w, http.StatusOK, toCredential(pub))
}
