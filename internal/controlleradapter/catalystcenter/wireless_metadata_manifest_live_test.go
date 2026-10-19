//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// The calling native harness owns native isolation and exact-UUID cleanup.
// This optional route exercises the public journal instead of invoking a plan's
// submission closure. Kubernetes storage remains simulated, never installed proof.
type wirelessMetadataManifest struct {
	a                     *adapter
	config                *configapi.NetworkControllerConfig
	guard                 *wirelessMetadataTransport
	dir                   string
	operationIDs, taskIDs map[string]bool
	cleanupCount          int
}

type wirelessMetadataTransport struct {
	base                     http.RoundTripper
	resource, action, target string
	writes                   int
	cleanup                  func(string) bool
	deleted                  map[string]bool
}

func (g *wirelessMetadataTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet || req.URL.Path == authPath {
		return g.base.RoundTrip(req)
	}
	deny := errors.New("Wireless metadata qualification refused an unexpected native mutation")
	if req.Method == http.MethodDelete {
		if !g.cleanup(req.URL.Path) || g.deleted[req.URL.Path] {
			return nil, deny
		}
		g.deleted[req.URL.Path] = true
		return g.base.RoundTrip(req)
	}
	if g.writes != 0 || g.resource == "" {
		return nil, deny
	}
	path, key, name := "", "", ""
	if strings.HasPrefix(g.resource, "power-profile:") {
		path, key, name = wirelessPowerPath, "profileName", strings.TrimPrefix(g.resource, "power-profile:")
	}
	if strings.HasPrefix(g.resource, "ap-profile:") {
		path, key, name = wirelessAPPath, "apProfileName", strings.TrimPrefix(g.resource, "ap-profile:")
	}
	if strings.HasPrefix(g.resource, "ssid:") {
		parts := strings.SplitN(g.resource, ":", 3)
		if len(parts) != 3 || !validAPIID(parts[1]) {
			return nil, deny
		}
		path, key, name = wirelessSSIDPath(parts[1]), "ssid", parts[2]
	}
	if path == "" {
		return nil, deny
	}
	method := http.MethodPost
	if g.action == "Update" {
		method = http.MethodPut
		path += "/" + g.target
	} else if g.action != "Create" || g.target != "" {
		return nil, deny
	}
	if req.Method != method || req.URL.Path != path || req.Body == nil {
		return nil, deny
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields[key] != name {
		return nil, deny
	}
	if key == "ssid" {
		if fields["authType"] != "OPEN" || fields["isEnabled"] != false {
			return nil, deny
		}
		if _, exists := fields["passphrase"]; exists {
			return nil, deny
		}
	}
	g.writes++
	return g.base.RoundTrip(req)
}

func newWirelessMetadataManifest(t *testing.T, ctx context.Context, c *client, cleanup func(string) bool, cleanupCount int) *wirelessMetadataManifest {
	t.Helper()
	a, config := journalFixture(t)
	a.client = c
	n, err := a.configurationRecoveryController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	n.Spec.Endpoint = c.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, n); err != nil {
		t.Fatal(err)
	}
	config.Spec.ManagedSections = []string{"wireless"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	g := &wirelessMetadataTransport{base: base, cleanup: cleanup, deleted: map[string]bool{}}
	rest.HTTPClient.Transport = g
	dir, err := os.MkdirTemp("/private/tmp", "cvk-wireless-metadata-manifest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private manifest recovery directory", dir)
	return &wirelessMetadataManifest{a: a, config: config, guard: g, dir: dir, operationIDs: map[string]bool{}, taskIDs: map[string]bool{}, cleanupCount: cleanupCount}
}

func (h *wirelessMetadataManifest) source(t *testing.T, ctx context.Context, raw []byte) {
	t.Helper()
	if bytes.Equal(raw, h.config.Spec.Source.Inline.Raw) {
		return
	}
	if journalState(t, h.a) != nil && journalState(t, h.a).Operation != nil {
		t.Fatal("source changed while prior metadata operation remains unsettled")
	}
	h.config.Generation++
	h.config.Spec.Source.Inline.Raw = append([]byte(nil), raw...)
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, "source.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
}

func (h *wirelessMetadataManifest) tick(t *testing.T, ctx context.Context) {
	t.Helper()
	restarted := *h.a
	if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(h.config)}); err != nil {
		t.Fatal(err)
	}
	if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), h.config); err != nil {
		t.Fatal(err)
	}
	state := journalState(t, h.a)
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, "journal.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
	reasons := []string{}
	for _, condition := range h.config.Status.Conditions {
		reasons = append(reasons, condition.Reason)
	}
	diagnostic, _ := json.Marshal(map[string]any{"configPhase": h.config.Status.Phase, "conditionReasons": reasons, "expectedResource": h.guard.resource, "nativeWrites": h.guard.writes})
	if err := os.WriteFile(filepath.Join(h.dir, "status.json"), diagnostic, 0600); err != nil {
		t.Fatal(err)
	}
}

func (h *wirelessMetadataManifest) submit(t *testing.T, ctx context.Context, raw []byte, w *configurationWrite) Task {
	t.Helper()
	h.source(t, ctx, raw)
	h.guard.resource, h.guard.action, h.guard.target, h.guard.writes = w.resource, w.action, w.identity.target, 0
	defer func() { h.guard.resource = "" }()
	var accepted *cvk.NetworkControllerConfigurationOperation
	for range 40 {
		h.tick(t, ctx)
		state := journalState(t, h.a)
		if state != nil && state.Operation != nil {
			op := state.Operation
			if op.Resource != w.resource || op.Action != w.action || w.action == "Update" && op.TargetID != w.identity.target {
				t.Fatal("public journal selected an unexpected definition")
			}
			if op.Phase == "Accepted" && accepted == nil {
				accepted = op.DeepCopy()
				if h.operationIDs[op.ID] || h.taskIDs[op.TaskID] {
					t.Fatal("public wireless metadata operation or native task ID was reused")
				}
				h.operationIDs[op.ID] = true
				h.taskIDs[op.TaskID] = true
				n, err := h.a.configurationRecoveryController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				encoded, err := json.Marshal(n.Status.Configuration)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.dir, "journal.json"), encoded, 0600); err != nil {
					t.Fatal(err)
				}
				copy := n.DeepCopy()
				if err := json.Unmarshal(encoded, &copy.Status.Configuration); err != nil {
					t.Fatal(err)
				}
				if err := h.a.statusWriter.Status().Update(ctx, copy); err != nil {
					t.Fatal(err)
				}
				t.Log("public accepted-task JSON restart", op.Resource, op.TaskID)
			}
		} else if accepted != nil {
			if h.guard.writes != 1 {
				t.Fatal("public journal replayed or omitted native mutation")
			}
			return Task{ID: accepted.TaskID}
		}
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("public wireless metadata journal did not finish; retain private recovery evidence")
	return Task{}
}

func (h *wirelessMetadataManifest) noOp(t *testing.T, ctx context.Context, raw []byte) {
	t.Helper()
	h.source(t, ctx, raw)
	h.guard.resource = ""
	for range 4 {
		h.tick(t, ctx)
		state := journalState(t, h.a)
		if state != nil && state.Operation != nil {
			t.Fatal("converged manifest attempted another mutation")
		}
		if h.config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && h.config.Status.ObservedGeneration == h.config.Generation {
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(h.dir, "journal.json"), encoded, 0600); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("public wireless metadata manifest did not report current no-op convergence", h.config.Status.Conditions)
}

func (h *wirelessMetadataManifest) completed(t *testing.T) {
	t.Helper()
	if len(h.guard.deleted) != h.cleanupCount {
		t.Fatal("expected exact generated-definition cleanup")
	}
	raw, err := json.Marshal(map[string]any{"baselineRestored": true, "definitionCreateUpdateMutations": len(h.taskIDs), "acceptedTaskRestarts": len(h.operationIDs), "taskIDs": h.taskIDs, "cleanupPaths": h.guard.deleted, "kubernetesAuthority": "simulated; not installed worker qualification"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, "completed.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("public manifest/native tasks/JSON restarts/no replay/no-op convergence/exact cleanup qualified", len(h.taskIDs), h.dir)
}
