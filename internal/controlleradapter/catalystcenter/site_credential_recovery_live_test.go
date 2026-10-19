//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Native service is real; Kubernetes persistence and scheduling are driven by
// the existing fake API fixture. No host-side phase edits are used.
func qualifyLiveSiteCredentialRecovery(t *testing.T, c *client, desired []siteCredentialIntent, selectedSource []byte) *cvk.NetworkControllerConfigurationOperation {
	t.Helper()
	if len(desired) != 1 || desired[0].siteKind != "area" || desired[0].name == "" {
		t.Fatal("one generated-area assignment required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a, config := noAuthCredentialConfigurationFixture(t)
	a.client = c
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = c.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	var areaName string
	for i := len(desired[0].site) - 1; i >= 0; i-- {
		if desired[0].site[i] == '/' {
			areaName = desired[0].site[i+1:]
			break
		}
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{}, "sites": map[string]any{"areas": []any{map[string]any{"name": areaName, "parent_name": "Global", desired[0].kind: desired[0].name}}}}})
	if selectedSource != nil {
		raw = selectedSource
		config.Spec.ModelSource.Exporter = CredentialSiteSelectionExporter
	}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
	config.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-site-credential-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private assignment recovery checkpoint", dir)
	save := func(name string, value any) {
		t.Helper()
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		tmp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(tmp, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	var original *cvk.NetworkControllerConfigurationOperation
	for {
		noAuthCredentialTick(t, a, config)
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		save("controller.json", current)
		save("config.json", config)
		if current.Status.Configuration != nil && current.Status.Configuration.Operation != nil {
			op := current.Status.Configuration.Operation
			if original != nil && (op.ID != original.ID || op.TaskID != original.TaskID) {
				t.Fatal("original assignment task replaced")
			}
			if op.Phase == "Unknown" {
				original = op.DeepCopy()
				save("original-unknown.json", original)
			}
		}
		if config.Status.ObservedGeneration == config.Generation && config.Status.Phase == "InSync" {
			if original == nil || current.Status.Configuration.Operation != nil {
				t.Fatal("timeout recovery not proven")
			}
			for range 2 {
				noAuthCredentialTick(t, a, config)
			}
			if journalState(t, a).Operation != nil {
				t.Fatal("restarted assignment replayed")
			}
			save("result.json", map[string]any{"originalTask": original.TaskID, "operationID": original.ID, "target": original.TargetID, "credential": original.ParentID, "recovered": true, "kubernetesPersistence": "simulated", "timeoutTransition": "production", "phaseEdits": false})
			return original
		}
		select {
		case <-ctx.Done():
			t.Fatal("assignment recovery pending; retain checkpoint", config.Status.Conditions)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
