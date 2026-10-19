// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"testing"
)

func TestTagDeletionRetainedFamilySelection(t *testing.T) {
	for _, mode := range []string{"tags", "ambiguous-empty", "discovery-history", "discovery-operation", "credential-history", "mixed-history", "tag-history-source-gone", "foreign-history"} {
		t.Run(mode, func(t *testing.T) {
			a, c := journalFixture(t)
			c.Spec.ManagedSections = []string{"inventory"}
			c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			c.Finalizers = []string{discoveryConfigFinalizer}
			c.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(`{"catalyst_center":{"inventory":{"tags":[]}}}`)}
			nc, err := a.currentController(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
			creator := tagTestReceipt(true)
			creator.Operation.ConfigUID = string(c.UID)
			creator.Operation.ConfigName = c.Name
			foreign := creator
			foreign.Operation.Resource = "discovery-owned:retained"
			expected := false
			switch mode {
			case "tags":
				expected = true
			case "ambiguous-empty":
				c.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{}}}`)
			case "discovery-history":
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{foreign}
			case "discovery-operation":
				nc.Status.Configuration.Operation = foreign.Operation.DeepCopy()
			case "credential-history":
				nc.Status.Configuration.CredentialApplications = []cvk.NetworkControllerConfigurationCredentialApplication{{ConfigUID: string(c.UID)}}
			case "mixed-history":
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{creator, foreign}
			case "tag-history-source-gone":
				expected = true
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{creator}
				c.Spec.Source = configapi.NetworkControllerConfigurationSource{ConfigMapRef: &configapi.ConfigMapKeyRef{Name: "missing", Key: "model"}}
			case "foreign-history":
				expected = true
				foreign.Operation.ConfigUID = "other-owner"
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{foreign}
			}
			if (&configReconciler{a: a}).tagDeletionSelected(context.Background(), nc, c) != expected {
				t.Fatal("source bytes transferred retained lifecycle custody", mode)
			}
		})
	}
}
