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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// RESTCONFConfig configures a RESTCONF Interface. HTTPClient is required;
// the caller owns its lifecycle (typically it is the apphosting driver's
// HTTP client, shared so both drivers present a single TLS session and
// credential to the device).
type RESTCONFConfig struct {
	// BaseURL is the RESTCONF root — e.g. "https://192.0.2.10/restconf/data".
	// The RESTCONF adapter prepends this to every request path.
	BaseURL string

	// HTTPClient is used for every request. The caller supplies it so
	// TLS, timeouts, and proxy behaviour match the apphosting driver.
	HTTPClient *http.Client

	// Username / Password are set via BasicAuth on every request.
	// RESTCONF also supports token auth; add a field when that is needed.
	Username string
	Password string

	// SessionLock, when non-nil, is acquired around every request. Use
	// this to serialise with an apphosting driver that shares the same
	// HTTP client — a half-second lock on the caller is cheaper than a
	// mid-transaction interleave on the device.
	SessionLock *sync.Mutex
}

type restconfTransport struct {
	cfg RESTCONFConfig
}

// NewRESTCONF returns an Interface implementation that talks RESTCONF
// JSON. It validates the config eagerly so a misconfigured device CR
// fails at reconciler startup rather than on first device write.
func NewRESTCONF(cfg RESTCONFConfig) (Interface, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("transport: RESTCONF BaseURL required")
	}
	if cfg.HTTPClient == nil {
		return nil, fmt.Errorf("transport: RESTCONF HTTPClient required")
	}
	// Trim a trailing slash so path composition is predictable. Leave
	// the user-supplied value alone otherwise.
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	return &restconfTransport{cfg: cfg}, nil
}

func (r *restconfTransport) Capabilities() Capabilities {
	return Capabilities{
		Kind:                 KindRESTCONF,
		SupportsTransactions: false,
		SupportsSubscribe:    false,
		// RESTCONF does not standardise save-to-startup; IOS-XE exposes
		// an RPC at /operations/cisco-ia:save-config. The adapter
		// implements it below, so capability reports true.
		SupportsSaveStartup: true,
		// RESTCONF writes go directly to running by definition (RFC
		// 8040 — there is no candidate datastore in RESTCONF). Report
		// true unconditionally; the engine's non-transactional path
		// uses this signal to choose between direct write and
		// implicit lock+commit cycle.
		SupportsWritableRunning: true,
	}
}

func (r *restconfTransport) Fetch(ctx context.Context, path string) ([]byte, error) {
	return r.do(ctx, http.MethodGet, path, nil)
}

func (r *restconfTransport) StartTransaction(context.Context) (TxHandle, error) {
	// RESTCONF has no candidate datastore. Returning the zero handle
	// signals the engine to apply ops without a commit phase; writers
	// that require atomicity should pre-check Capabilities() and refuse
	// rather than call StartTransaction.
	return TxHandle(""), nil
}

func (r *restconfTransport) Mutate(ctx context.Context, tx TxHandle, ops []Op) error {
	if tx != "" {
		return fmt.Errorf("RESTCONF: non-empty TxHandle %q: %w", tx, ErrUnsupported)
	}
	for i, op := range ops {
		if err := r.mutate(ctx, op); err != nil {
			return fmt.Errorf("op[%d] %s %s: %w", i, op.Verb, op.Path, err)
		}
	}
	return nil
}

func (r *restconfTransport) mutate(ctx context.Context, op Op) error {
	switch op.Verb {
	case VerbReplace:
		_, err := r.do(ctx, http.MethodPut, op.Path, op.Body)
		return err
	case VerbMerge:
		// RFC 8040 §4.6.1: PATCH "modify existing resource" returns
		// 404 when the target does not yet exist. The engine's MERGE
		// verb has create-if-absent semantics — a writer asking to
		// MERGE a Loopback the device hasn't seen yet is a CREATE,
		// not a partial-update. PATCH-then-PUT-on-404 covers both:
		// • existing resource: PATCH applies a partial-merge (preserves
		//   leaves the writer doesn't manage; what the family writers
		//   already rely on);
		// • absent resource: PUT creates idempotently with the body
		//   the writer produced.
		// Caught against a live Cat9300-24P (IOS-XE 17.18.2) running
		// test 07's Loopback-9997 create.
		_, err := r.do(ctx, http.MethodPatch, op.Path, op.Body)
		if err != nil && isRESTCONFNotFound(err) {
			_, err = r.do(ctx, http.MethodPut, op.Path, op.Body)
		}
		return err
	case VerbDelete:
		_, err := r.do(ctx, http.MethodDelete, op.Path, nil)
		return err
	case VerbCLI:
		return r.pushCLI(ctx, op.Body)
	default:
		return fmt.Errorf("unknown verb %q", op.Verb)
	}
}

// isRESTCONFNotFound reports whether err is the 404 surfaced by
// `do` for a PATCH against a path with no existing resource. The
// status string from net/http is "404 Not Found"; matching against
// the wrapped error's text keeps us out of the response-body schema.
func isRESTCONFNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "404 Not Found")
}

// pushCLI invokes the Cisco-IA cli-config-data RPC over RESTCONF.
// The endpoint lives at /operations/cisco-ia:cli-config-data and
// accepts a body of the shape:
//
//	{"cisco-ia:input": {"cli-config-data": {"cmd": ["...", "..."]}}}
//
// Body is a newline-delimited CLI-text payload, optionally JSON-
// wrapped. splitCLILines normalises either shape into []string.
func (r *restconfTransport) pushCLI(ctx context.Context, body []byte) error {
	if len(body) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(body, &text); err != nil {
		text = string(body)
	}
	lines := splitCLILines(text)
	if len(lines) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string]any{
		"cisco-ia:input": map[string]any{
			"cli-config-data": map[string]any{"cmd": lines},
		},
	})
	if err != nil {
		return fmt.Errorf("RESTCONF pushCLI: marshal: %w", err)
	}
	// The operations endpoint lives outside the /data tree used
	// by the rest of the transport. We compute the parent from
	// the configured BaseURL by swapping /data for /operations.
	opsPath := "/operations/cisco-ia:cli-config-data"
	raw, err := r.doOps(ctx, http.MethodPost, opsPath, payload)
	if err != nil {
		return fmt.Errorf("RESTCONF pushCLI: %w", err)
	}
	_ = raw // response body is empty on success; keep for debugging
	return nil
}

// doOps POSTs to an /operations-rooted RPC endpoint. The normal
// do() method composes against BaseURL which points at /data; an
// RPC invocation needs the sibling /operations root.
func (r *restconfTransport) doOps(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	if r.cfg.SessionLock != nil {
		r.cfg.SessionLock.Lock()
		defer r.cfg.SessionLock.Unlock()
	}
	// BaseURL is .../restconf/data; strip the trailing /data and
	// append the ops path.
	root := r.cfg.BaseURL
	if strings.HasSuffix(root, "/data") {
		root = root[:len(root)-len("/data")]
	}
	url := root + path

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/yang-data+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/yang-data+json")
	}
	if r.cfg.Username != "" || r.cfg.Password != "" {
		req.SetBasicAuth(r.cfg.Username, r.cfg.Password)
	}
	resp, err := r.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return respBody, fmt.Errorf("RESTCONF %s %s: %s: %s",
			method, path, resp.Status, snippet(respBody, 512))
	}
	return respBody, nil
}

func (r *restconfTransport) Commit(context.Context, TxHandle) error {
	// No-op on a zero handle (the only legitimate handle a RESTCONF
	// caller gets). Not a wrong answer — it mirrors what the device
	// effectively did: nothing to commit, the writes are already live.
	return nil
}

func (r *restconfTransport) Discard(context.Context, TxHandle) error {
	return ErrUnsupported
}

// SaveStartup calls the Cisco-specific save-config RPC. The endpoint
// returns 204 on success and an error status with a yang-data+json
// body otherwise.
//
// RESTCONF roots are split: data resources live under
// /restconf/data/... and RPC operations under /restconf/operations/...
// — see RFC 8040 §3.3.2. Pre-fix, the request was issued against
// `BaseURL + /operations/cisco-ia:save-config`, where BaseURL is
// configured to `…/restconf/data` for the data-resource path. That
// composes to `…/restconf/data/operations/cisco-ia:save-config`,
// which the device rejects with `404 / uri keypath not found` even
// though it advertises the operation in `/restconf/operations`.
//
// Caught against a live Cat9300-24P / IOS-XE 17.18.2: every test 07
// reconcile recorded `save_startup_total{outcome="failed"}++` and
// emitted a `SaveStartupFailed` Warning event despite the
// running-config apply landing cleanly.
//
// Strip the trailing `/data` from the configured root and append
// `/operations/...`. Operators who supply a non-/data BaseURL keep
// the historical behavior — only the conventional `…/restconf/data`
// shape is special-cased.
func (r *restconfTransport) SaveStartup(ctx context.Context) error {
	const savePath = "/operations/cisco-ia:save-config"
	// HACK with a clear lifecycle: the do() helper composes
	// `r.cfg.BaseURL + path`. We need `r.cfg.BaseURL` minus the
	// trailing `/data` segment, plus `/operations/...`. Building a
	// dedicated request avoids the path-vs-base coupling.
	url := r.cfg.BaseURL
	if strings.HasSuffix(url, "/data") {
		url = strings.TrimSuffix(url, "/data") + savePath
	} else {
		url = url + savePath
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return fmt.Errorf("build save-config request: %w", err)
	}
	req.Header.Set("Accept", "application/yang-data+json")
	req.Header.Set("Content-Type", "application/yang-data+json")
	if r.cfg.Username != "" || r.cfg.Password != "" {
		req.SetBasicAuth(r.cfg.Username, r.cfg.Password)
	}
	if r.cfg.SessionLock != nil {
		r.cfg.SessionLock.Lock()
		defer r.cfg.SessionLock.Unlock()
	}
	resp, err := r.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("RESTCONF POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("RESTCONF POST %s: %s: %s", url, resp.Status, snippet(body, 512))
	}
	return nil
}

func (r *restconfTransport) Close() error {
	// HTTPClient is caller-owned by contract; nothing to close here.
	return nil
}

// do issues one HTTP request against the RESTCONF endpoint. It reads
// the response body fully before returning so the caller receives a
// complete buffer without having to drain it on error paths.
func (r *restconfTransport) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	if r.cfg.SessionLock != nil {
		r.cfg.SessionLock.Lock()
		defer r.cfg.SessionLock.Unlock()
	}

	url := r.cfg.BaseURL + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/yang-data+json")
	if body != nil {
		req.Header.Set("Content-Type", "application/yang-data+json")
	}
	if r.cfg.Username != "" || r.cfg.Password != "" {
		req.SetBasicAuth(r.cfg.Username, r.cfg.Password)
	}

	resp, err := r.cfg.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("transport: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 300 {
		// Return the body as part of the error so callers don't need to
		// re-request to see what the device complained about. Cap the
		// snippet so a multi-kilobyte HTML 500 page doesn't flood logs.
		return respBody, fmt.Errorf("RESTCONF %s %s: %s: %s",
			method, path, resp.Status, snippet(respBody, 512))
	}
	return respBody, nil
}

func snippet(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "...(truncated)"
}
