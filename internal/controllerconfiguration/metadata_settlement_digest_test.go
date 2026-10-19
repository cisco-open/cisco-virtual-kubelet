// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMetadataTerminalSettlementDigest(t *testing.T) {
	n := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "catc", UID: "controller", Generation: 3}}
	n.Spec.Type = "catalyst-center"
	n.Spec.Endpoint = "https://controller.example"
	n.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{MetadataHandoff: &cvk.NetworkControllerConfigurationMetadataHandoff{Protocol: cvk.NetworkControllerMetadataHandoffProtocol, Operation: cvk.NetworkControllerConfigurationOperation{ID: "original", ControllerGeneration: 2}, BindingDigest: "original-binding"}}
	stable, err := metadataSettlementDigest(n)
	if err != nil {
		t.Fatal(err)
	}
	n.Status.Configuration.MetadataHandoff.SettlementDigest = stable
	for _, mode := range []string{"spec", "generation", "release-ack", "worker", "binding", "operation", "controller", "history"} {
		t.Run(mode, func(t *testing.T) {
			changed := n.DeepCopy()
			switch mode {
			case "spec":
				changed.Spec.Paused = true
			case "generation":
				changed.Generation++
			case "release-ack":
				at := metav1.NewTime(time.Now())
				changed.Status.Configuration.MetadataHandoff.ReleasedAt = &at
			case "worker":
				changed.Status.Configuration.MetadataHandoff.WorkerPod.UID = "replacement"
			case "binding":
				changed.Status.Configuration.MetadataHandoff.BindingDigest = "replacement"
			case "operation":
				changed.Status.Configuration.MetadataHandoff.Operation.ControllerGeneration++
			case "controller":
				changed.UID = "replacement"
			case "history":
				changed.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigUID: "foreign"}}
			}
			digest, err := metadataSettlementDigest(changed)
			compatible := mode == "spec" || mode == "generation" || mode == "release-ack"
			if compatible && (err != nil || digest != stable) {
				t.Fatal("desired edit stranded original terminal evidence", err)
			}
			if !compatible && err == nil {
				t.Fatal("changed retained evidence accepted")
			}
		})
	}
	legacy, err := metadataLegacySettlementDigest(n)
	if err != nil {
		t.Fatal(err)
	}
	n.Status.Configuration.MetadataHandoff.SettlementDigest = legacy
	if got, err := metadataSettlementDigest(n); err != nil || got != legacy {
		t.Fatal("corroborated legacy intent rejected", err)
	}
	n.Spec.Paused = true
	n.Generation++
	if _, err := metadataSettlementDigest(n); err == nil {
		t.Fatal("unverifiable legacy intent migrated or released")
	}
}
