package server

// Operator routes for connector-generated target credentials (ADR-0079): PUBLIC
// material only - internal/connector rejects any response carrying the private half.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"aireos.local/services/api/internal/connector"
	"aireos.local/services/api/internal/resource"
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

// demoLabel names the install-time pending credential and the demo resource alike
// (the same name the cockpit's OnboardingChecklist finds the demo target by).
const demoLabel = "loopback-demo"

const demoNotPrepared = "the demo target was not prepared at install time; run `sudo ./setup.sh` on the server and choose the demo target, or use infra/offline/setup-loopback-demo.sh, then try again"

// CreateDemoTarget (POST /v1/demo-target) creates the loopback demo resource from the
// pending credential's NON-secret metadata, through the SAME governed store.Create as
// POST /v1/resources (so resource.created is chained, ONB-4), then asks the connector to
// bind the pending key to it. No service writes authorized_keys (ADR-0079 D3/D4). If the
// bind fails the resource is deleted again (compensation) so the result is never a
// half-made target that can't connect; a failed compensation is reported by id.
func (a *apiHandlers) CreateDemoTarget(w http.ResponseWriter, r *http.Request) {
	operator, ok := operatorSubject(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized", "no capability")
		return
	}
	if a.connector == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "demo target creation is not configured: CONNECTOR_URL is not set")
		return
	}
	pend, err := a.connector.Pending(r.Context(), demoLabel)
	if errors.Is(err, connector.ErrNotFound) {
		writeError(w, http.StatusConflict, "conflict", demoNotPrepared)
		return
	}
	if err == nil && (pend.Account == "" || pend.HostKey == "") {
		err = connector.ErrBadResponse
	}
	if err != nil {
		a.connectorFailure(w, "pending read", err)
		return
	}
	res, err := a.store.Create(r.Context(), operator, resource.Input{Type: "ssh", Name: demoLabel, Labels: map[string]string{
		"ssh_host": "127.0.0.1", "ssh_port": "22", "ssh_user": pend.Account, "ssh_host_key": pend.HostKey,
		// requires_approval deliberately omitted: same as the cockpit's manual demo flow.
	}})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", "create failed")
		return
	}
	if _, err := a.connector.Bind(r.Context(), demoLabel, res.ID, operator); err != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
		defer cancel()
		if derr := a.store.Delete(ctx, operator, res.ID); derr != nil {
			log.Printf("demo target: bind failed (%v) and compensating delete of %s failed: %v", err, res.ID, derr)
			writeError(w, http.StatusInternalServerError, "internal", "the demo target was created but could not be bound, and could not be removed again; remove the resource "+res.ID+" in the cockpit, then try again")
			return
		}
		if errors.Is(err, connector.ErrNotFound) {
			writeError(w, http.StatusConflict, "conflict", demoNotPrepared)
			return
		}
		a.connectorFailure(w, "bind", err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}
