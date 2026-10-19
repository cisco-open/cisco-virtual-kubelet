// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func floorImageAssetFixture() *corev1.ConfigMap {
	return &corev1.ConfigMap{Data: map[string]string{"model.yaml": plannedAPFixture, floorImageAssetsKey: `{"maps/floor.pdf":"floor-pdf"}`}, BinaryData: map[string][]byte{"floor-pdf": []byte("%PDF-1.7\nfirst")}}
}
func TestFloorImageAssetIndex(t *testing.T) {
	for _, mode := range []string{"valid", "missing-index", "duplicate", "missing-binary", "text-collision", "model-key", "null", "non-string", "array", "trailing", "binary-index", "wrong-type", "invalid-key"} {
		t.Run(mode, func(t *testing.T) {
			cm := floorImageAssetFixture()
			switch mode {
			case "missing-index":
				delete(cm.Data, floorImageAssetsKey)
			case "duplicate":
				cm.Data[floorImageAssetsKey] = `{"maps/floor.pdf":"floor-pdf","maps/floor.pdf":"floor-pdf"}`
			case "missing-binary":
				delete(cm.BinaryData, "floor-pdf")
			case "text-collision":
				cm.Data["floor-pdf"] = "text"
			case "model-key":
				cm.Data[floorImageAssetsKey] = `{"floor.pdf":"model.yaml"}`
			case "null":
				cm.Data[floorImageAssetsKey] = `null`
			case "non-string":
				cm.Data[floorImageAssetsKey] = `{"floor.pdf":null}`
			case "array":
				cm.Data[floorImageAssetsKey] = `[]`
			case "trailing":
				cm.Data[floorImageAssetsKey] += ` {}`
			case "binary-index":
				cm.BinaryData[floorImageAssetsKey] = []byte("index")
			case "wrong-type":
				cm.BinaryData["floor-pdf"] = []byte("not a PDF")
			case "invalid-key":
				cm.Data[floorImageAssetsKey] = `{"floor.pdf":"../floor-pdf"}`
			}
			assets, err := loadFloorImageAssets(cm, "model.yaml")
			valid := mode == "valid" || mode == "missing-index"
			if valid != (err == nil) {
				t.Fatalf("mode=%s error=%v", mode, err)
			}
			if !valid && assets != nil {
				t.Fatal("invalid index returned partial assets")
			}
			if mode == "valid" {
				cm.BinaryData["floor-pdf"][0] = 'x'
				if string(assets["maps/floor.pdf"]) != "%PDF-1.7\nfirst" {
					t.Fatal("snapshot aliases ConfigMap bytes")
				}
			}
		})
	}
}
func TestConfigurationJournalRejectsChangedFloorImageAsset(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"sites"}
	cm := floorImageAssetFixture()
	cm.ObjectMeta = metav1.ObjectMeta{Namespace: config.Namespace, Name: "floor-model", UID: "floor-source-uid"}
	if err := a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil || string(source.floorImages["maps/floor.pdf"]) != "%PDF-1.7\nfirst" {
		t.Fatal("assets not loaded", err)
	}
	op, err := a.prepareConfiguration(ctx, config, source, "sites", "Global/Lab", "Create")
	if err != nil {
		t.Fatal(err)
	}
	cm.BinaryData["floor-pdf"] = []byte("%PDF-1.7\nsecond")
	if err := a.statusWriter.Update(ctx, cm); err != nil {
		t.Fatal(err)
	}
	if string(source.floorImages["maps/floor.pdf"]) != "%PDF-1.7\nfirst" {
		t.Fatal("old snapshot changed")
	}
	if err := a.startConfiguration(ctx, config, source, op.ID); !errors.Is(err, errConfigurationStale) {
		t.Fatal("asset change did not fence submission", err)
	}
	if journalState(t, a).Operation.Phase != "Prepared" {
		t.Fatal("stale source advanced")
	}
	fresh, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil || fresh.version == source.version || !strings.HasSuffix(string(fresh.floorImages["maps/floor.pdf"]), "second") {
		t.Fatal("new revision not observed", err)
	}
}
