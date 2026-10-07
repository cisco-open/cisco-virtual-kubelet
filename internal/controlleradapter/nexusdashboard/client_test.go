// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nexusdashboard

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeND struct {
	srv        *httptest.Server
	logins     atomic.Int32
	probes     atomic.Int32
	password   string
	token      atomic.Value // string; token currently accepted
	failProbe  atomic.Int32 // number of probe calls to fail with 503
	probeCode  atomic.Int32 // fixed status for probe if non-zero
	lastDomain atomic.Value
}

func newFakeND(t *testing.T, tls bool) *fakeND {
	t.Helper()
	f := &fakeND{password: "s3cret"}
	f.token.Store("tok-1")
	mux := http.NewServeMux()
	mux.HandleFunc(loginPath, func(w http.ResponseWriter, r *http.Request) {
		f.logins.Add(1)
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.lastDomain.Store(in["domain"])
		if in["userPasswd"] != f.password {
			http.Error(w, `{"error":"bad userPasswd s3cret-echo"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": f.token.Load(), "jwttoken": f.token.Load(), "statusCode": 200})
	})
	mux.HandleFunc(probePath, func(w http.ResponseWriter, r *http.Request) {
		f.probes.Add(1)
		if code := int(f.probeCode.Load()); code != 0 {
			w.WriteHeader(code)
			return
		}
		if f.failProbe.Add(-1) >= 0 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.failProbe.Store(0)
		ck, err := r.Cookie(authCookie)
		if err != nil || ck.Value != f.token.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"fabrics":[]}`))
	})
	if tls {
		f.srv = httptest.NewTLSServer(mux)
	} else {
		f.srv = httptest.NewServer(mux)
	}
	t.Cleanup(f.srv.Close)
	return f
}

func writeCreds(t *testing.T, password, domain string) string {
	t.Helper()
	dir := t.TempDir()
	for name, v := range map[string]string{"username": "admin", "password": password, "domain": domain} {
		if v == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func testClient(f *fakeND, credDir, caPath string, insecure bool) *client {
	return newClient(clientConfig{
		Endpoint: f.srv.URL, CredentialPath: credDir, CAPath: caPath, InsecureSkipVerify: insecure,
		RequestTimeout: 5 * time.Second, MaxSessionLifetime: time.Minute, MaxConcurrent: 2,
	})
}

func TestLoginAndProbeCachesToken(t *testing.T) {
	f := newFakeND(t, false)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	for i := 0; i < 3; i++ {
		if err := c.Probe(context.Background()); err != nil {
			t.Fatalf("probe %d: %v", i, err)
		}
	}
	if got := f.logins.Load(); got != 1 {
		t.Fatalf("logins = %d, want 1 (token cached)", got)
	}
	if d, _ := f.lastDomain.Load().(string); d != "local" {
		t.Fatalf("default domain = %q, want local", d)
	}
}

func TestCustomDomain(t *testing.T) {
	f := newFakeND(t, false)
	c := testClient(f, writeCreds(t, "s3cret", "radius"), "", false)
	if err := c.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d, _ := f.lastDomain.Load().(string); d != "radius" {
		t.Fatalf("domain = %q", d)
	}
}

func TestBadPasswordIsAuthenticationErrorAndRedacted(t *testing.T) {
	f := newFakeND(t, false)
	c := testClient(f, writeCreds(t, "wrong", ""), "", false)
	err := c.Probe(context.Background())
	if !errors.Is(err, errAuthentication) {
		t.Fatalf("err = %v, want errAuthentication", err)
	}
	res := classify(err)
	if res.authenticated || res.phase != "Error" || res.reason != "AuthenticationFailed" {
		t.Fatalf("unexpected result %+v", res)
	}
	for _, s := range []string{res.message, err.Error()} {
		if strings.Contains(s, "wrong") || strings.Contains(s, "s3cret") {
			t.Fatalf("secret leaked: %q", s)
		}
	}
}

func TestMissingCredentialFiles(t *testing.T) {
	f := newFakeND(t, false)
	c := testClient(f, t.TempDir(), "", false)
	err := c.Probe(context.Background())
	if !errors.Is(err, errCredentials) {
		t.Fatalf("err = %v", err)
	}
	if f.logins.Load() != 0 {
		t.Fatal("must not dial without credentials")
	}
	if classify(err).reason != "InvalidCredentials" {
		t.Fatal("wrong reason")
	}
}

func TestExpiredTokenTriggersSingleRelogin(t *testing.T) {
	f := newFakeND(t, false)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	if err := c.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.token.Store("tok-2") // server invalidates the old token
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("probe after expiry: %v", err)
	}
	if got := f.logins.Load(); got != 2 {
		t.Fatalf("logins = %d, want 2", got)
	}
}

func TestPersistent401DoesNotLoop(t *testing.T) {
	f := newFakeND(t, false)
	f.probeCode.Store(http.StatusUnauthorized)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	if err := c.Probe(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if got := f.logins.Load(); got != 2 {
		t.Fatalf("logins = %d, want exactly one re-login", got)
	}
	if got := f.probes.Load(); got != 2 {
		t.Fatalf("probes = %d, want 2", got)
	}
}

func TestInvalidateRereadsCredentials(t *testing.T) {
	f := newFakeND(t, false)
	dir := writeCreds(t, "s3cret", "")
	c := testClient(f, dir, "", false)
	if err := c.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "password"), []byte("rotated"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.password = "rotated"
	c.Invalidate()
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("after rotation: %v", err)
	}
	if f.logins.Load() != 2 {
		t.Fatal("expected a fresh login after Invalidate")
	}
}

func TestSessionLifetimeBackstop(t *testing.T) {
	f := newFakeND(t, false)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	c.cfg.MaxSessionLifetime = time.Nanosecond
	for i := 0; i < 2; i++ {
		if err := c.Probe(context.Background()); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	if f.logins.Load() != 2 {
		t.Fatalf("logins = %d, want 2", f.logins.Load())
	}
}

func TestTLS(t *testing.T) {
	f := newFakeND(t, true)
	creds := writeCreds(t, "s3cret", "")

	if err := testClient(f, creds, "", false).Probe(context.Background()); err == nil {
		t.Fatal("untrusted server cert must fail by default")
	}

	caPath := filepath.Join(t.TempDir(), "ca.crt")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := testClient(f, creds, caPath, false).Probe(context.Background()); err != nil {
		t.Fatalf("custom CA: %v", err)
	}

	badCA := filepath.Join(t.TempDir(), "bad.crt")
	if err := os.WriteFile(badCA, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := testClient(f, creds, badCA, false).Probe(context.Background()); err == nil {
		t.Fatal("garbage CA bundle must fail")
	}

	if err := testClient(f, creds, "", true).Probe(context.Background()); err != nil {
		t.Fatalf("explicit insecureSkipVerify: %v", err)
	}
}

func TestTransient5xxIsRetried(t *testing.T) {
	f := newFakeND(t, false)
	f.failProbe.Store(2)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("probe should recover after 503s: %v", err)
	}
	if f.probes.Load() != 3 {
		t.Fatalf("probes = %d, want 3", f.probes.Load())
	}
}

func TestRateLimitedStatusIsRetryableThenReported(t *testing.T) {
	f := newFakeND(t, false)
	f.probeCode.Store(http.StatusTooManyRequests)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	err := c.Probe(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if f.probes.Load() != 3 {
		t.Fatalf("429 should use the default 3 attempts, got %d", f.probes.Load())
	}
	if classify(err).reason != "Unreachable" {
		t.Fatalf("reason = %s", classify(err).reason)
	}
}

func TestCancellation(t *testing.T) {
	f := newFakeND(t, false)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Probe(ctx); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestClassifyAPIUnavailable(t *testing.T) {
	f := newFakeND(t, false)
	f.probeCode.Store(http.StatusNotFound)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	res := classify(c.Probe(context.Background()))
	if !res.authenticated || res.compatible || res.phase != "Degraded" || res.reason != "ManageAPIUnavailable" {
		t.Fatalf("unexpected %+v", res)
	}
}
