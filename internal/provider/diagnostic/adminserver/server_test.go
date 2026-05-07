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

package adminserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// fakeTransport is a minimal transport.Interface that satisfies the
// adminserver's borrowing pattern. Mirrors the diagnostic
// reconciler's test fixture but kept separate to avoid cyclic
// imports between the test packages.
type fakeTransport struct {
	caps   transport.Capabilities
	exec   func(ctx context.Context, cmds []string) ([]transport.CommandResult, error)
	closed bool
}

func (f *fakeTransport) Capabilities() transport.Capabilities { return f.caps }
func (f *fakeTransport) Fetch(_ context.Context, _ string) ([]byte, error) {
	return nil, transport.ErrUnsupported
}
func (f *fakeTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}
func (f *fakeTransport) Mutate(context.Context, transport.TxHandle, []transport.Op) error {
	return transport.ErrUnsupported
}
func (f *fakeTransport) Commit(context.Context, transport.TxHandle) error  { return nil }
func (f *fakeTransport) Discard(context.Context, transport.TxHandle) error { return nil }
func (f *fakeTransport) SaveStartup(context.Context) error                 { return transport.ErrUnsupported }
func (f *fakeTransport) Close() error                                      { f.closed = true; return nil }
func (f *fakeTransport) DiagnosticExec(ctx context.Context, cmds []string) ([]transport.CommandResult, error) {
	return f.exec(ctx, cmds)
}

type stubProvider struct{ tr transport.Interface }

func (s *stubProvider) GetTransport() transport.Interface { return s.tr }

// TestExecHappyPath drives a single show command through the
// admin endpoint and asserts the JSON response shape + body.
func TestExecHappyPath(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindNETCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			out := make([]transport.CommandResult, 0, len(cmds))
			for _, c := range cmds {
				out = append(out, transport.CommandResult{
					Command: c,
					Output:  "Cisco IOS XE Software, Version 17.18.2",
				})
			}
			return out, nil
		},
	}
	s := &Server{DeviceName: "cat9k-smoke", TP: &stubProvider{tr: tr}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := strings.NewReader(`{"commands":["show version"]}`)
	resp, err := http.Post(srv.URL+"/v1/exec", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var got ExecResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Device != "cat9k-smoke" || got.Transport != "netconf" {
		t.Errorf("unexpected device/transport: %+v", got)
	}
	if len(got.Results) != 1 || !strings.Contains(got.Results[0].Output, "17.18.2") {
		t.Errorf("unexpected results: %+v", got.Results)
	}
}

// TestExecRedactsByDefault pins the secret-redaction default-on
// behaviour at the admin endpoint.
func TestExecRedactsByDefault(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindRESTCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			return []transport.CommandResult{{
				Command: cmds[0],
				Output:  "interface Loopback0\nenable secret 5 $1$abcd$xyz",
			}}, nil
		},
	}
	s := &Server{DeviceName: "cat9k-smoke", TP: &stubProvider{tr: tr}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := strings.NewReader(`{"commands":["show running-config"]}`)
	resp, err := http.Post(srv.URL+"/v1/exec", "application/json", body)
	if err != nil {
		t.Fatalf("POST /v1/exec: %v", err)
	}
	defer resp.Body.Close()
	var got ExecResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if !got.Results[0].Redacted {
		t.Errorf("expected Redacted=true")
	}
	if strings.Contains(got.Results[0].Output, "$1$abcd$xyz") {
		t.Errorf("secret leaked: %s", got.Results[0].Output)
	}
}

// TestExecAllowSecretsIgnored pins the adversarial-review fix
// (2026-05-01): the only authn/z gate on this admin endpoint is
// the caller's ability to port-forward to the VK pod. That scope
// is too broad to safely honour an unauthenticated
// `allowSecrets=true` body flag. The flag is preserved in the
// JSON shape (so older kubectl-ciscovk plugin builds continue to
// deserialise) but the server forces redaction on regardless.
func TestExecAllowSecretsIgnored(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindRESTCONF, SupportsDiagnosticExec: true},
		exec: func(_ context.Context, cmds []string) ([]transport.CommandResult, error) {
			return []transport.CommandResult{{Command: cmds[0],
				Output: "enable secret 5 $1$abcd$xyz"}}, nil
		},
	}
	s := &Server{DeviceName: "cat9k-smoke", TP: &stubProvider{tr: tr}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := strings.NewReader(`{"commands":["show running-config"], "allowSecrets": true}`)
	resp, err := http.Post(srv.URL+"/v1/exec", "application/json", body)
	if err != nil {
		t.Fatalf("POST /v1/exec: %v", err)
	}
	defer resp.Body.Close()
	var got ExecResponse
	_ = json.NewDecoder(resp.Body).Decode(&got)
	if !got.Results[0].Redacted {
		t.Errorf("expected Redacted=true: allowSecrets must be ignored on the port-forward endpoint")
	}
	if strings.Contains(got.Results[0].Output, "$1$abcd$xyz") {
		t.Errorf("expected redacted output even when caller set allowSecrets=true; got %q", got.Results[0].Output)
	}
}

// TestExecGNMITransportNotImplemented covers the fail-fast path
// when the live transport doesn't implement DiagnosticExec.
func TestExecGNMITransportNotImplemented(t *testing.T) {
	tr := &fakeTransport{
		caps: transport.Capabilities{Kind: transport.KindGNMI, SupportsDiagnosticExec: false},
		exec: func(_ context.Context, _ []string) ([]transport.CommandResult, error) {
			return nil, nil
		},
	}
	s := &Server{DeviceName: "cat9k-smoke", TP: &stubProvider{tr: tr}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	body := strings.NewReader(`{"commands":["show version"]}`)
	resp, err := http.Post(srv.URL+"/v1/exec", "application/json", body)
	if err != nil {
		t.Fatalf("POST /v1/exec: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("status=%d want 501", resp.StatusCode)
	}
}

// TestExecBadInput rejects empty commands list with 400.
func TestExecBadInput(t *testing.T) {
	tr := &fakeTransport{caps: transport.Capabilities{
		Kind: transport.KindNETCONF, SupportsDiagnosticExec: true}}
	s := &Server{DeviceName: "cat9k-smoke", TP: &stubProvider{tr: tr}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	for _, body := range []string{`{}`, `{"commands":[]}`, `not-json`} {
		resp, _ := http.Post(srv.URL+"/v1/exec", "application/json", strings.NewReader(body))
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body=%q got status %d, want 400", body, resp.StatusCode)
		}
	}
}

// TestExecMethodNotAllowed pins that GET on /v1/exec is rejected
// (operators sometimes hit the URL in a browser by accident).
func TestExecMethodNotAllowed(t *testing.T) {
	tr := &fakeTransport{caps: transport.Capabilities{
		Kind: transport.KindNETCONF, SupportsDiagnosticExec: true}}
	s := &Server{DeviceName: "cat9k-smoke", TP: &stubProvider{tr: tr}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/v1/exec")
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status=%d want 405", resp.StatusCode)
	}
}

// TestHealthz pings the readiness endpoint.
func TestHealthz(t *testing.T) {
	tr := &fakeTransport{caps: transport.Capabilities{
		Kind: transport.KindNETCONF, SupportsDiagnosticExec: true}}
	s := &Server{DeviceName: "cat9k-smoke", TP: &stubProvider{tr: tr}}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/healthz")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz status=%d", resp.StatusCode)
	}
}
