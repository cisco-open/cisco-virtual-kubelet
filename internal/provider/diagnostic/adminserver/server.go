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

// Package adminserver hosts the per-pod-kubelet HTTP endpoint that
// the kubectl-ciscovk plugin POSTs into to invoke ad-hoc show
// commands. The endpoint is bound to localhost (or a fixed port the
// operator port-forwards) — there is no listener exposed off-pod, so
// `pods/portforward` RBAC is the authorization gate. Operators
// without portforward privilege cannot reach the endpoint; operators
// with portforward privilege can.
//
// This is the Phase-C-MVP shape per docs/rfcs/diagnostics-rfc.md
// §3.3 Option A. Phase E (APIService aggregation) replaces it with
// SubjectAccessReview-gated RBAC if and when usage justifies it.
package adminserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic"
)

// TransportProvider mirrors diagnostic.TransportProvider so the
// adminserver can borrow the per-device-pod's existing transport
// without circular-importing the parent package.
type TransportProvider interface {
	GetTransport() transport.Interface
}

// ExecRequest is the POST body accepted by /v1/exec.
type ExecRequest struct {
	// Commands is the list of show / read-only commands to run.
	// Per-command failures populate the per-result Err field but
	// do not abort the batch.
	Commands []string `json:"commands"`

	// AllowSecrets disables the default secret-redaction filter.
	// Operators with elevated audit rights can opt in via the
	// plugin's --allow-secrets flag.
	AllowSecrets bool `json:"allowSecrets,omitempty"`

	// TruncateBytes caps each command's output. 0 disables.
	// Default 64 KiB applied server-side when 0.
	TruncateBytes int `json:"truncateBytes,omitempty"`
}

// ExecResponse is the JSON shape returned by /v1/exec.
type ExecResponse struct {
	Device         string       `json:"device"`
	Transport      string       `json:"transport"`
	CapturedAt     time.Time    `json:"capturedAt"`
	Results        []ExecResult `json:"results"`
	TransportError string       `json:"transportError,omitempty"`
}

// ExecResult is one command's outcome. Mirrors
// configv1alpha1.CommandOutput with an exec-specific subset (the
// ad-hoc plugin doesn't surface Truncated / Redacted as separate
// fields — the body is text and operators see the markers inline).
type ExecResult struct {
	Command   string `json:"command"`
	Output    string `json:"output,omitempty"`
	Err       string `json:"err,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`
}

// Server hosts the HTTP endpoint.
type Server struct {
	DeviceName string
	TP         TransportProvider

	// TelemetrySource, if set, backs the GET /telemetry/health
	// endpoint. cmd/cisco-vk plumbs the IOSXETelemetryReconciler's snapshot
	// accessor through here.
	TelemetrySource func() TelemetryHealth

	// BindAddr defaults to "127.0.0.1:8082". The plugin's
	// kubectl-port-forward tunnel terminates here; the device-side
	// listener is intentionally NOT bound to 0.0.0.0 so off-pod
	// dial attempts fail at the kernel.
	BindAddr string
}

// TelemetryHealth is the JSON payload returned by GET /telemetry/health.
type TelemetryHealth struct {
	Device        string                        `json:"device"`
	Subscriptions []TelemetrySubscriptionHealth `json:"subscriptions"`
}

// TelemetrySubscriptionHealth is the per-subscription health projection.
type TelemetrySubscriptionHealth struct {
	Name                string `json:"name"`
	Phase               string `json:"phase,omitempty"`
	MessagesReceived    int64  `json:"messagesReceived"`
	LogRecordsEmitted   int64  `json:"logRecordsEmitted"`
	MetricPointsEmitted int64  `json:"metricPointsEmitted"`
	Reconnects          int64  `json:"reconnects"`
	StreamID            string `json:"streamID,omitempty"`
	LastError           string `json:"lastError,omitempty"`
	CurrentBackoff      string `json:"currentBackoff,omitempty"`
}

// Default values for unspecified fields.
const (
	DefaultBindAddr      = "127.0.0.1:8082"
	DefaultTruncateBytes = 64 * 1024
)

// Handler returns a *http.ServeMux configured with the admin routes.
// Exposed for testing — tests call ServeHTTP directly.
func (s *Server) Handler() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/exec", s.handleExec)
	mux.HandleFunc("/telemetry/health", s.handleTelemetryHealth)
	return mux
}

func (s *Server) handleTelemetryHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if s.TelemetrySource == nil {
		_ = json.NewEncoder(w).Encode(TelemetryHealth{Device: s.DeviceName})
		return
	}
	payload := s.TelemetrySource()
	if payload.Device == "" {
		payload.Device = s.DeviceName
	}
	_ = json.NewEncoder(w).Encode(payload)
}

// ListenAndServe binds + serves until ctx errors or the server
// fails. Blocking; intended to run in its own goroutine.
func (s *Server) ListenAndServe(stop <-chan struct{}) error {
	addr := s.BindAddr
	if addr == "" {
		addr = DefaultBindAddr
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-stop:
		_ = srv.Close()
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	tr := s.TP.GetTransport()
	if tr == nil {
		http.Error(w, "transport not yet ready", http.StatusServiceUnavailable)
		return
	}
	if !tr.Capabilities().SupportsDiagnosticExec {
		http.Error(w, "transport does not support cli-exec", http.StatusNotImplemented)
		return
	}
	_, _ = fmt.Fprintln(w, "ok")
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var req ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Commands) == 0 {
		http.Error(w, "commands list is empty", http.StatusBadRequest)
		return
	}
	// Wave 10 release-readiness P0 fix (2026-04-28): server-side
	// allowlist enforcement. The kubectl-ciscovk plugin has a
	// denylist of its own, but a direct HTTP caller against the
	// admin port (port-forward, in-cluster operator) would bypass
	// it. ValidateCommands rejects every non-read-only command
	// before it reaches the device.
	if err := diagnostic.ValidateCommands(req.Commands); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	tr := s.TP.GetTransport()
	if tr == nil {
		http.Error(w, "transport not yet ready", http.StatusServiceUnavailable)
		return
	}
	d, ok := tr.(transport.DiagnosticExecer)
	if !ok || !tr.Capabilities().SupportsDiagnosticExec {
		http.Error(w, fmt.Sprintf("transport %q does not implement DiagnosticExec",
			tr.Capabilities().Kind), http.StatusNotImplemented)
		return
	}

	truncate := req.TruncateBytes
	if truncate == 0 {
		truncate = DefaultTruncateBytes
	}

	resp := ExecResponse{
		Device:     s.DeviceName,
		Transport:  string(tr.Capabilities().Kind),
		CapturedAt: time.Now().UTC(),
	}
	results, err := d.DiagnosticExec(r.Context(), req.Commands)
	if err != nil {
		resp.TransportError = err.Error()
	}
	// Adversarial-review fix (2026-05-01): the only authn/z gate
	// on this admin endpoint is the caller's ability to
	// port-forward to the VK pod. That scope is too broad to
	// safely honour an unauthenticated `allowSecrets=true` body
	// flag — a caller who has port-forward but should NOT have
	// secret-tier diagnostic rights would otherwise receive
	// unredacted `show running-config` output. Force redaction on
	// regardless of the request body. The field is preserved in
	// the JSON shape so existing kubectl-ciscovk plugin builds
	// continue to deserialise; a future commit may re-introduce
	// unredacted output behind a SubjectAccessReview-gated path.
	for _, res := range results {
		out := ExecResult{Command: res.Command, Err: res.Err, Output: res.Output}
		if out.Output != "" {
			redacted, didRedact := diagnostic.Redact(out.Output)
			out.Output = redacted
			out.Redacted = didRedact
		}
		if out.Output != "" {
			clipped, t := diagnostic.Truncate(out.Output, truncate)
			out.Output = clipped
			out.Truncated = t
		}
		resp.Results = append(resp.Results, out)
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Body partially written; best-effort log via http.Error
		// is no-op at this point. Drop quietly.
		_ = err
	}
}
