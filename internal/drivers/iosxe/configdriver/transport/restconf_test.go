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

package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func newTestRESTCONF(t *testing.T, h http.HandlerFunc) (Interface, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	cli, err := NewRESTCONF(RESTCONFConfig{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Username:   "u",
		Password:   "p",
	})
	if err != nil {
		t.Fatalf("NewRESTCONF: %v", err)
	}
	return cli, srv
}

func TestFetchBasicAuthAndHeaders(t *testing.T) {
	var (
		gotAuth   string
		gotAccept string
	)
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	body, err := cli.Fetch(context.Background(), "/data/Cisco-IOS-XE-native:native")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body=%s", string(body))
	}
	if !strings.HasPrefix(gotAuth, "Basic ") {
		t.Errorf("auth missing: %q", gotAuth)
	}
	if gotAccept != "application/yang-data+json" {
		t.Errorf("accept=%q", gotAccept)
	}
}

func TestMutateVerbsDispatch(t *testing.T) {
	var gotMethods []string
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		w.WriteHeader(http.StatusNoContent)
	})
	ops := []Op{
		{Verb: VerbReplace, Path: "/a", Body: []byte(`{}`)},
		{Verb: VerbMerge, Path: "/b", Body: []byte(`{}`)},
		{Verb: VerbDelete, Path: "/c"},
	}
	if err := cli.Mutate(context.Background(), "", ops); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	wantMethods := []string{"PUT", "PATCH", "DELETE"}
	if !stringSliceEqual(gotMethods, wantMethods) {
		t.Fatalf("methods=%v want %v", gotMethods, wantMethods)
	}
}

// TestMutateMergePatchFallsBackToPutOn404 is a regression test for
// the live-device finding against a Cat9300-24P (IOS-XE 17.18.2):
// VerbMerge against a target the device has not yet provisioned
// returns 404 from PATCH; the engine's MERGE has create-if-absent
// semantics, so the transport must retry as PUT (idempotent create).
func TestMutateMergePatchFallsBackToPutOn404(t *testing.T) {
	var gotMethods []string
	var patchSeen bool
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		if r.Method == http.MethodPatch && !patchSeen {
			patchSeen = true
			http.Error(w, `{"ietf-restconf:errors":"missing"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ops := []Op{{Verb: VerbMerge, Path: "/Cisco-IOS-XE-native:native/interface/Loopback=9999", Body: []byte(`{"Loopback":[{"name":9999}]}`)}}
	if err := cli.Mutate(context.Background(), "", ops); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	want := []string{"PATCH", "PUT"}
	if !stringSliceEqual(gotMethods, want) {
		t.Fatalf("methods=%v want %v", gotMethods, want)
	}
}

// TestMutateMergePatchSurfacesNon404Errors keeps the regression
// scoped — only 404 from PATCH triggers the PUT fallback. A 4xx that
// names a real semantic problem (validation error, locked datastore,
// etc.) must still surface as the engine error so callers see it.
func TestMutateMergePatchSurfacesNon404Errors(t *testing.T) {
	var gotMethods []string
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethods = append(gotMethods, r.Method)
		http.Error(w, `{"errors":"validation"}`, http.StatusBadRequest)
	})
	ops := []Op{{Verb: VerbMerge, Path: "/x", Body: []byte(`{}`)}}
	err := cli.Mutate(context.Background(), "", ops)
	if err == nil {
		t.Fatalf("expected 400 to surface, got nil")
	}
	if len(gotMethods) != 1 || gotMethods[0] != "PATCH" {
		t.Fatalf("methods=%v want exactly [PATCH]", gotMethods)
	}
}

func TestMutateShortCircuitsOnError(t *testing.T) {
	var requests int32
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		if n == 2 {
			http.Error(w, `{"errors":"oops"}`, http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	ops := []Op{
		{Verb: VerbMerge, Path: "/a", Body: []byte(`{}`)},
		{Verb: VerbMerge, Path: "/b", Body: []byte(`{}`)},
		{Verb: VerbMerge, Path: "/c", Body: []byte(`{}`)},
	}
	err := cli.Mutate(context.Background(), "", ops)
	if err == nil || !strings.Contains(err.Error(), "op[1]") {
		t.Fatalf("want error tagged op[1], got %v", err)
	}
	if atomic.LoadInt32(&requests) != 2 {
		t.Fatalf("requests=%d, want 2 (short-circuit)", requests)
	}
}

func TestStartTransactionYieldsZeroHandle(t *testing.T) {
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {})
	tx, err := cli.StartTransaction(context.Background())
	if err != nil {
		t.Fatalf("StartTransaction: %v", err)
	}
	if tx != "" {
		t.Fatalf("handle=%q, want empty", tx)
	}
	// Non-empty handles are rejected at Mutate.
	err = cli.Mutate(context.Background(), TxHandle("fake"), nil)
	if err == nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestDiscardUnsupported(t *testing.T) {
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {})
	if err := cli.Discard(context.Background(), ""); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v, want ErrUnsupported", err)
	}
}

func TestSaveStartupCallsRPCEndpoint(t *testing.T) {
	var gotPath string
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	if err := cli.SaveStartup(context.Background()); err != nil {
		t.Fatalf("SaveStartup: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/operations/cisco-ia:save-config") {
		t.Errorf("SaveStartup hit %q", gotPath)
	}
}

// TestSaveStartupStripsDataSegmentFromBaseURL is a regression test
// for the live-device finding against IOS-XE 17.18.2: pre-fix the
// helper composed `BaseURL + /operations/...`, but BaseURL ends in
// `/restconf/data` for data-resource paths so the result became
// `/restconf/data/operations/cisco-ia:save-config`, which the
// device rejects with `404 / uri keypath not found`. RFC 8040 §3.3.2
// puts data and operations on parallel roots; SaveStartup must hit
// `/restconf/operations/...` not `/restconf/data/operations/...`.
func TestSaveStartupStripsDataSegmentFromBaseURL(t *testing.T) {
	var gotPath string
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	})
	if err := cli.SaveStartup(context.Background()); err != nil {
		t.Fatalf("SaveStartup: %v", err)
	}
	// The httptest server's URL.Path strips the host but not the
	// rest of the URL. Reject any composed path that retains
	// `/data/operations/` — that is the historical bug shape.
	if strings.Contains(gotPath, "/data/operations/") {
		t.Errorf("SaveStartup hit %q which still has the /data/operations/ shape; expected /operations/...", gotPath)
	}
}

func TestSessionLockSerialisesRequests(t *testing.T) {
	var (
		inFlight int32
		peak     int32
	)
	h := func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&inFlight, 1)
		if n > atomic.LoadInt32(&peak) {
			atomic.StoreInt32(&peak, n)
		}
		// Simulate device latency.
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{}")
		atomic.AddInt32(&inFlight, -1)
	}
	srv := httptest.NewServer(http.HandlerFunc(h))
	defer srv.Close()

	lock := &sync.Mutex{}
	cli, err := NewRESTCONF(RESTCONFConfig{
		BaseURL:     srv.URL,
		HTTPClient:  srv.Client(),
		SessionLock: lock,
	})
	if err != nil {
		t.Fatalf("NewRESTCONF: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cli.Fetch(context.Background(), "/x"); err != nil {
				t.Errorf("Fetch: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&peak); got != 1 {
		t.Errorf("peak in-flight = %d, want 1 (session lock serialises)", got)
	}
}

func TestNewRESTCONFRejectsEmptyConfig(t *testing.T) {
	if _, err := NewRESTCONF(RESTCONFConfig{}); err == nil {
		t.Error("expected error for empty config")
	}
	if _, err := NewRESTCONF(RESTCONFConfig{BaseURL: "https://x"}); err == nil {
		t.Error("expected error when HTTPClient nil")
	}
}

func TestCapabilitiesReport(t *testing.T) {
	cli, _ := newTestRESTCONF(t, func(w http.ResponseWriter, r *http.Request) {})
	c := cli.Capabilities()
	if c.Kind != KindRESTCONF {
		t.Errorf("Kind=%v", c.Kind)
	}
	if c.SupportsTransactions {
		t.Error("RESTCONF should not advertise transactions")
	}
	if !c.SupportsSaveStartup {
		t.Error("RESTCONF should advertise save-startup via cisco-ia RPC")
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRESTCONFCLIVerbHitsCiscoIAEndpoint(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   []byte
	)
	h := func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}
	srv := httptest.NewServer(http.HandlerFunc(h))
	defer srv.Close()

	cli, err := NewRESTCONF(RESTCONFConfig{
		BaseURL:    srv.URL + "/restconf/data",
		HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewRESTCONF: %v", err)
	}

	// Multi-line CLI body; splitCLILines should trim empty lines.
	body := []byte("interface Loopback100\n\n ip address 1.1.1.1 255.255.255.255\n no shutdown\n")
	err = cli.Mutate(context.Background(), "",
		[]Op{{Verb: VerbCLI, Body: body}})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}

	if gotMethod != "POST" {
		t.Errorf("method=%q, want POST", gotMethod)
	}
	if !strings.HasSuffix(gotPath, "/operations/cisco-ia:cli-config-data") {
		t.Errorf("path=%q, want .../operations/cisco-ia:cli-config-data", gotPath)
	}
	// Body should be JSON wrapping the CLI lines under cisco-ia:input.
	if !strings.Contains(string(gotBody), "cisco-ia:input") {
		t.Errorf("body missing cisco-ia:input envelope:\n%s", gotBody)
	}
	if !strings.Contains(string(gotBody), "interface Loopback100") {
		t.Errorf("body missing CLI command:\n%s", gotBody)
	}
	// Empty line must not leak into the cmd array.
	if strings.Contains(string(gotBody), `""`) {
		t.Errorf("empty line leaked into cmd array:\n%s", gotBody)
	}
}
