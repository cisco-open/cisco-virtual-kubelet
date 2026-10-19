//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Keep interrupted generated design state recoverable without borrowing an
// installed credential/key. Native cleanup remains with the existing exact
// identity checks; this wrapper does not invent or replay a cleanup operation.
func checkpointLiveSiteDefaultDesign(t *testing.T, ctx context.Context, h *liveSiteManifest, path string, baseline []Site, keyPath string, key []byte) func() {
	t.Helper()
	dir, err := os.MkdirTemp("/private/tmp", "cvk-site-default-design-")
	if err != nil {
		t.Fatal(err)
	}
	if keyPath == "" || len(key) == 0 {
		t.Fatal("existing generated comparison key required")
	}
	relative, err := filepath.Rel(h.a.intentSecretPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	h.a.intentSecretPath = filepath.Join(dir, "projection")
	target := filepath.Join(h.a.intentSecretPath, relative)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, key, 0600); err != nil {
		t.Fatal(err)
	}
	h.config.Spec.ModelSource.Exporter = SiteDefaultsExporter
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
	base := h.a.statusWriter
	write := func(name string, value any) error {
		b, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		temp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(temp, b, 0600); err != nil {
			return err
		}
		return os.Rename(temp, filepath.Join(dir, name))
	}
	checkpoint := func() error {
		var controller cvk.NetworkController
		var config configapi.NetworkControllerConfig
		if err := base.Get(ctx, h.a.key, &controller); err != nil {
			return err
		}
		if err := base.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), &config); err != nil {
			return err
		}
		var cm *corev1.ConfigMap
		if ref := config.Spec.Source.ConfigMapRef; ref != nil {
			cm = &corev1.ConfigMap{}
			if err := base.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: ref.Name}, cm); err != nil {
				return err
			}
		}
		return write("checkpoint.json", struct {
			Controller           cvk.NetworkController
			Config               configapi.NetworkControllerConfig
			Source               *corev1.ConfigMap
			Path, ProjectionRoot string
		}{controller, config, cm, path, h.a.intentSecretPath})
	}
	h.a.statusWriter = &siteLifecycleCheckpointClient{Client: base, checkpoint: checkpoint}
	if err := checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := write("baseline-sites.json", baseline); err != nil {
		t.Fatal(err)
	}
	t.Log("private default-design checkpoint", dir)
	verify := func() {
		clean, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		current, err := h.a.client.ListSites(clean)
		order := func(rows []Site) { sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID }) }
		order(current)
		order(baseline)
		restored := err == nil && reflect.DeepEqual(current, baseline)
		state := journalState(t, h.a)
		active, rows := false, 0
		if state != nil {
			active = state.Operation != nil
			rows = len(state.ActionApplications)
		}
		result := map[string]any{"generatedPath": path, "baselineRestored": restored, "originalTaskRecoveries": len(h.recovered), "installed": false, "sourceDefaultsExport": true, "modelAndAssetSnapshot": true, "yamlSourceResolved": os.Getenv("CATC_SITE_YAML_SOURCE") == "isolated-design", "mergedSource": os.Getenv("CATC_SITE_MERGED_SOURCE") == "isolated-design", "managedSiteSelection": os.Getenv("CATC_SITE_MANAGED_SELECTION") == "isolated-design", "activeOperation": active, "retainedActionRows": rows}
		if err := write("result.json", result); err != nil {
			t.Error("cannot write design result", err)
		}
		if !restored {
			t.Error("default design hierarchy/native baseline not restored")
		}
	}
	return verify
}
