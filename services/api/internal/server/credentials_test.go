package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"aireos.local/packages/go/auth"
)

const (
	stubPub = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGYBaIGagBf91js21bSjKiW+5mjBUu2zHVbEQODGURx7"
	stubFP  = "SHA256:lqGvsiY1iL3oSg6YrH2VPI3Wse3s2CoHpGTd2zPw2No"
	credURL = "/v1/resources/res-00001/credential"
)

type stubConnector struct {
	status       int
	body         string
	method, path string
	sent         map[string]any
	bearer       string
	srv          *httptest.Server
}

func newStub(t *testing.T, status int, body string) *stubConnector {
	s := &stubConnector{status: status, body: body}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.method, s.path, s.bearer = r.Method, r.URL.Path, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		s.sent = nil
		_ = json.NewDecoder(r.Body).Decode(&s.sent)
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func credHandler(t *testing.T, s *stubConnector, key auth.Key) (http.Handler, *fakeStore) {
	f := newFake()
	req(t, New(f, nil, nil, allowAll{}, "", ""), http.MethodPost, "/v1/resources", validBody, true) // res-00001
	url := ""
	if s != nil {
		url = s.srv.URL
	}
	return New(f, nil, auth.NewSigner(key, ""), allowAll{}, "", "", WithConnector(url)), f
}

var okBody = `{"public_key":"` + stubPub + `","fingerprint":"` + stubFP + `"}`

func TestCredentialGenerateRotateReadHappyPath(t *testing.T) {
	key, _ := auth.GenerateKey()
	s := newStub(t, 200, okBody)
	h, _ := credHandler(t, s, key)

	rec := req(t, h, http.MethodPost, credURL, `{"rotate":false}`, true)
	var got resourceCredential
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body)
	}
	if want := authorizedKeysOptions + " " + stubPub; got.AuthorizedKeysLine != want || got.PublicKey != stubPub || got.Fingerprint != stubFP {
		t.Fatalf("response = %+v", got)
	}
	if s.method != "POST" || s.path != "/v1/credentials/res-00001/generate" || s.sent["rotate"] != false || s.sent["operator_subject"] != "operator" {
		t.Fatalf("connector saw %s %s %v", s.method, s.path, s.sent)
	}
	c, err := auth.AsMiddlewareVerifier(auth.NewVerifier(key, "", "connector-service")).Verify(context.Background(), s.bearer)
	if err != nil || c.Subject != "api" || c.Edge != "service-service" {
		t.Fatalf("connector bearer = %+v, %v; want subject api / service-service", c, err)
	}
	if rec := req(t, h, http.MethodPost, credURL, `{"rotate":true}`, true); rec.Code != 200 || s.sent["rotate"] != true {
		t.Fatalf("rotate: %d sent=%v", rec.Code, s.sent)
	}
	if rec := req(t, h, http.MethodGet, credURL, "", true); rec.Code != 200 || s.method != "GET" || s.path != "/v1/credentials/res-00001/public" {
		t.Fatalf("read: %d %s %s", rec.Code, s.method, s.path)
	}
}

func TestCredentialRefusalsAndMappings(t *testing.T) {
	key, _ := auth.GenerateKey()
	s := newStub(t, 409, "x")
	h, _ := credHandler(t, s, key)
	if rec := req(t, h, http.MethodPost, credURL, `{"rotate":false}`, true); rec.Code != 409 || !strings.Contains(rec.Body.String(), "rotate") {
		t.Errorf("exists: %d %s", rec.Code, rec.Body)
	}
	s.status = 500
	if rec := req(t, h, http.MethodGet, credURL, "", true); rec.Code != 502 {
		t.Errorf("connector 500: %d, want 502", rec.Code)
	}
	if rec := req(t, h, http.MethodPost, "/v1/resources/nope/credential", `{"rotate":false}`, true); rec.Code != 404 {
		t.Errorf("unknown resource: %d", rec.Code)
	}
	h0, _ := credHandler(t, nil, key)
	if rec := req(t, h0, http.MethodPost, credURL, `{"rotate":false}`, true); rec.Code != 503 || !strings.Contains(rec.Body.String(), "CONNECTOR_URL") {
		t.Errorf("unconfigured: %d %s", rec.Code, rec.Body)
	}
}

// SEC-66 at api: private material from a hostile connector is dropped, not relayed or logged.
func TestCredentialNeverRelaysPrivateMaterial(t *testing.T) {
	const marker = "PLANTED-PRIVATE-KEY-BYTES-0123456789"
	pem := "-----BEGIN OPENSSH PRIVATE KEY-----\n" + marker + "\n-----END OPENSSH PRIVATE KEY-----\n"
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	log.Print(marker) // negative control: the log search must see a planted instance
	if !strings.Contains(logBuf.String(), marker) {
		t.Fatal("negative control: log capture/search is dead")
	}
	logBuf.Reset()
	key, _ := auth.GenerateKey()
	for name, body := range map[string]string{
		"extra field":   `{"public_key":"` + stubPub + `","fingerprint":"` + stubFP + `","private_key":"` + strings.ReplaceAll(pem, "\n", `\n`) + `"}`,
		"pem as public": `{"public_key":"` + strings.ReplaceAll(pem, "\n", `\n`) + `","fingerprint":"` + stubFP + `"}`,
	} {
		s := newStub(t, 200, body)
		h, _ := credHandler(t, s, key)
		for _, rec := range []*httptest.ResponseRecorder{
			req(t, h, http.MethodPost, credURL, `{"rotate":false}`, true), req(t, h, http.MethodGet, credURL, "", true),
		} {
			if rec.Code != 502 {
				t.Errorf("%s: status %d, want 502", name, rec.Code)
			}
			if strings.Contains(rec.Body.String()+logBuf.String(), marker) {
				t.Errorf("%s: private bytes reached a response or the log", name)
			}
		}
	}
	if logBuf.Len() == 0 {
		t.Fatal("negative control: nothing was logged, so the empty log search proves nothing")
	}
}

func TestCredentialRoutesNeedAuthAndCSRF(t *testing.T) {
	if rec := req(t, New(newFake(), nil, nil, allowAll{}, "", ""), http.MethodPost, credURL, `{"rotate":false}`, false); rec.Code != 401 {
		t.Errorf("no token: %d, want 401", rec.Code)
	}
	h, _, _ := loginHandler(t)
	login := req(t, h, http.MethodPost, "/v1/auth/login", `{"username":"admin","password":"hunter2"}`, false)
	r := httptest.NewRequest(http.MethodPost, credURL, strings.NewReader(`{"rotate":true}`))
	r.AddCookie(findCookie(t, login, sessionCookieName))
	r.AddCookie(findCookie(t, login, csrfCookieName))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 403 {
		t.Errorf("cookie without X-CSRF-Token: %d, want 403 (a rotation must not be CSRF-able)", rec.Code)
	}
}

func TestAuthorizedKeysOptionsMatchTheInstallScript(t *testing.T) {
	b, err := os.ReadFile("../../../../infra/offline/setup-loopback-demo.sh")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^RESTRICT_OPTS="([^"]+)"`).FindSubmatch(b)
	if m == nil || string(m[1]) != authorizedKeysOptions {
		t.Fatalf("script RESTRICT_OPTS = %q, Go = %q", m, authorizedKeysOptions)
	}
}

// T-176c1: a connector 404 on GET .../public (an imported key, or none recorded) is
// api's own 404 with its fixed sentence, not a 502 and not the connector's body.
func TestCredentialReadMapsConnector404(t *testing.T) {
	key, _ := auth.GenerateKey()
	s := newStub(t, 404, "connector says: not found")
	h, _ := credHandler(t, s, key)
	rec := req(t, h, http.MethodGet, credURL, "", true)
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "no public key is recorded") || strings.Contains(rec.Body.String(), "connector says") {
		t.Errorf("connector 404 on GET public: %d %s, want api's own 404", rec.Code, rec.Body)
	}
}
