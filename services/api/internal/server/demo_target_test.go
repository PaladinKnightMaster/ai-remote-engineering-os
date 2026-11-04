package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aireos.local/packages/go/auth"
)

const pendingBody = `{"public_key":"` + stubPub + `","fingerprint":"` + stubFP + `","account":"aireos-demo","host_key":"ssh-ed25519 AAAAhostkey root@host"}`

// demoStub is a connector with a pending demo credential. onBind runs when the bind arrives.
func demoStub(t *testing.T, pending string, bindStatus int, onBind func()) (*httptest.Server, *[]string) {
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/credentials/pending/loopback-demo":
			if pending == "" {
				w.WriteHeader(404)
				return
			}
			_, _ = w.Write([]byte(pending))
		case r.Method == "POST" && r.URL.Path == "/v1/credentials/pending/loopback-demo/bind":
			if onBind != nil {
				onBind()
			}
			w.WriteHeader(bindStatus)
			_, _ = w.Write([]byte(okBody))
		default:
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func demoHandler(f *fakeStore, url string) http.Handler {
	key, _ := auth.GenerateKey()
	return New(f, nil, auth.NewSigner(key, ""), allowAll{}, "", "", WithConnector(url))
}

func TestDemoTargetCreatesThroughGovernedPathThenBinds(t *testing.T) {
	f := newFake()
	var eventsAtBind []string
	srv, calls := demoStub(t, pendingBody, 200, func() {
		for _, e := range f.events {
			eventsAtBind = append(eventsAtBind, e.Outcome)
		}
	})
	rec := req(t, demoHandler(f, srv.URL), http.MethodPost, "/v1/demo-target", "", true)
	var res struct {
		ID, Name string
		Labels   map[string]string
	}
	if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &res) != nil {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	if res.Name != "loopback-demo" || res.Labels["ssh_host"] != "127.0.0.1" || res.Labels["ssh_port"] != "22" ||
		res.Labels["ssh_user"] != "aireos-demo" || res.Labels["ssh_host_key"] != "ssh-ed25519 AAAAhostkey root@host" || res.Labels["requires_approval"] != "" {
		t.Fatalf("resource = %+v", res)
	}
	// resource.created was chained BEFORE the bind was asked for, and the order of calls is read -> bind.
	if strings.Join(eventsAtBind, ",") != "resource.created" || strings.Join(*calls, ";") != "GET /v1/credentials/pending/loopback-demo;POST /v1/credentials/pending/loopback-demo/bind" {
		t.Fatalf("events at bind = %v, calls = %v", eventsAtBind, *calls)
	}
	if len(f.events) != 1 || f.events[0].Actor != "operator" {
		t.Fatalf("events = %+v", f.events)
	}
}

func TestDemoTargetNotPreparedIs409AndCreatesNothing(t *testing.T) {
	f := newFake()
	srv, _ := demoStub(t, "", 200, nil)
	rec := req(t, demoHandler(f, srv.URL), http.MethodPost, "/v1/demo-target", "", true)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "not prepared at install time") || len(f.byID) != 0 || len(f.events) != 0 {
		t.Fatalf("%d %s resources=%d events=%d", rec.Code, rec.Body, len(f.byID), len(f.events))
	}
}

// A failed bind must not leave a half-made target: the resource is deleted again (and
// the delete is chained), and the operator is told to retry.
func TestDemoTargetBindFailureCompensates(t *testing.T) {
	f := newFake()
	srv, _ := demoStub(t, pendingBody, 500, nil)
	rec := req(t, demoHandler(f, srv.URL), http.MethodPost, "/v1/demo-target", "", true)
	if rec.Code != 502 || len(f.byID) != 0 {
		t.Fatalf("%d %s resources left=%d", rec.Code, rec.Body, len(f.byID))
	}
	if len(f.events) != 2 || f.events[0].Outcome != "resource.created" || f.events[1].Outcome != "resource.deleted" {
		t.Fatalf("events = %+v", f.events)
	}
	// ... and a retry once the connector recovers succeeds with no duplicate.
	srv2, _ := demoStub(t, pendingBody, 200, nil)
	if rec := req(t, demoHandler(f, srv2.URL), http.MethodPost, "/v1/demo-target", "", true); rec.Code != 201 || len(f.byID) != 1 {
		t.Fatalf("retry: %d %s resources=%d", rec.Code, rec.Body, len(f.byID))
	}
}

// If the compensating delete fails too, the response names the leftover resource.
func TestDemoTargetBindAndCompensationFailureNamesTheResource(t *testing.T) {
	f := newFake()
	srv, _ := demoStub(t, pendingBody, 500, func() { f.inUse["res-00001"] = true })
	rec := req(t, demoHandler(f, srv.URL), http.MethodPost, "/v1/demo-target", "", true)
	if rec.Code != 500 || !strings.Contains(rec.Body.String(), "res-00001") || len(f.byID) != 1 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestDemoTargetRefusesHostileOrUnconfiguredConnector(t *testing.T) {
	for name, body := range map[string]string{
		"private field":   strings.TrimSuffix(pendingBody, "}") + `,"private_key":"x"}`,
		"pem in host key": strings.Replace(pendingBody, "ssh-ed25519 AAAAhostkey root@host", `-----BEGIN OPENSSH PRIVATE KEY-----`, 1),
		"no metadata":     okBody,
	} {
		f := newFake()
		srv, _ := demoStub(t, body, 200, nil)
		if rec := req(t, demoHandler(f, srv.URL), http.MethodPost, "/v1/demo-target", "", true); rec.Code != 502 || len(f.byID) != 0 {
			t.Errorf("%s: %d, resources=%d; want 502 and nothing created", name, rec.Code, len(f.byID))
		}
	}
	if rec := req(t, demoHandler(newFake(), ""), http.MethodPost, "/v1/demo-target", "", true); rec.Code != 503 {
		t.Errorf("unconfigured: %d, want 503", rec.Code)
	}
}

func TestDemoTargetNeedsAuthAndCSRF(t *testing.T) {
	if rec := req(t, New(newFake(), nil, nil, allowAll{}, "", ""), http.MethodPost, "/v1/demo-target", "", false); rec.Code != 401 {
		t.Errorf("no token: %d, want 401", rec.Code)
	}
	h, _, _ := loginHandler(t)
	login := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	r := httptest.NewRequest(http.MethodPost, "/v1/demo-target", nil)
	r.AddCookie(findCookie(t, login, sessionCookieName))
	r.AddCookie(findCookie(t, login, csrfCookieName))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("cookie without X-CSRF-Token: %d, want 403", rec.Code)
	}
}
