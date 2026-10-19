// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestTagProjectedOwnerExample(t *testing.T) {
	data, err := os.ReadFile("../../../examples/configs/catalyst-center/tags-projected-owner-execution.yaml")
	if err != nil {
		t.Fatal(err)
	}
	docs := bytes.Split(data, []byte("\n---\n"))
	if len(docs) != 2 {
		t.Fatal("expected ConfigMap and config")
	}
	var cm corev1.ConfigMap
	var cfg configapi.NetworkControllerConfig
	if err := yaml.UnmarshalStrict(docs[0], &cm); err != nil {
		t.Fatal(err)
	}
	if err := yaml.UnmarshalStrict(docs[1], &cfg); err != nil {
		t.Fatal(err)
	}
	ref := cfg.Spec.Source.ConfigMapRef
	if ref == nil || ref.Name != cm.Name || cfg.Namespace != cm.Namespace || cfg.Spec.Source.Inline != nil || ref.Key != "model.yaml" {
		t.Fatal("projected example source mismatch")
	}
	if supportedConfigurationApply(&cfg) {
		t.Fatal("Delete policy bypassed manager enrollment")
	}
	cfg.Finalizers = append(cfg.Finalizers, tagCleanupFinalizer(&cfg))
	if !supportedConfigurationApply(&cfg) || cfg.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyRetain || cfg.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete {
		t.Fatal("projected example contract invalid")
	}
	raw := []byte(cm.Data[ref.Key])
	if !tagPruneSource(raw, cfg.Spec.ManagedSections) || validatePinnedModelPreconditions(raw) != nil {
		t.Fatal("example is not pinned metadata-only tag intent")
	}
	plan, err := (&client{}).compileConfigurationApply(raw, cfg.Spec.ManagedSections, nil)
	if err != nil || plan.executor.count() != 1 {
		t.Fatal("projected model does not compile", err)
	}
}
