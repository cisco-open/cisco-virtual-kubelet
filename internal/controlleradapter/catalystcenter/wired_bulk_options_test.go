// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWiredBulkModuleOptionsAndSourceGrouping(t *testing.T) {
	for _, mode := range []string{"default", "root", "managed", "root-precedence", "false", "bad-bool", "root-without-bulk", "deep-root", "unknown-root", "null-sites", "string-sites", "bad-site", "duplicate-sites", "overlapping-sites", "wrong-section"} {
		t.Run(mode, func(t *testing.T) {
			_, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Annotations = map[string]string{wiredBulkAnnotation: "true"}
			source := &configSourceSnapshot{raw: []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"PROVISION"},{"name":"b","site":"Global/Lab/Building","state":"REPROVISION"}]}}}`)}
			before := bytes.Clone(source.raw)
			switch mode {
			case "root":
				config.Annotations[wiredBulkRootAnnotation] = "Global/Lab"
			case "managed":
				config.Annotations[wiredBulkManagedSitesAnnotation] = `["Global/Lab"]`
			case "root-precedence":
				config.Annotations[wiredBulkRootAnnotation] = "Global/Lab"
				config.Annotations[wiredBulkManagedSitesAnnotation] = `["Global/Lab","Global/Lab/Building"]`
			case "false":
				config.Annotations[wiredBulkAnnotation] = "false"
			case "bad-bool":
				config.Annotations[wiredBulkAnnotation] = "TRUE"
			case "root-without-bulk":
				config.Annotations[wiredBulkAnnotation] = "false"
				config.Annotations[wiredBulkRootAnnotation] = "Global/Lab"
			case "deep-root":
				config.Annotations[wiredBulkRootAnnotation] = "Global/A/B/C/D/E"
			case "unknown-root":
				config.Annotations[wiredBulkRootAnnotation] = "Global/Unknown"
			case "null-sites":
				config.Annotations[wiredBulkManagedSitesAnnotation] = `null`
			case "string-sites":
				config.Annotations[wiredBulkManagedSitesAnnotation] = `"Global/Lab"`
			case "bad-site":
				config.Annotations[wiredBulkManagedSitesAnnotation] = `["Global/Lab/"]`
			case "duplicate-sites":
				config.Annotations[wiredBulkManagedSitesAnnotation] = `["Global/Lab","Global/Lab"]`
			case "overlapping-sites":
				config.Annotations[wiredBulkManagedSitesAnnotation] = `["Global/Lab","Global/Lab/Building"]`
			case "wrong-section":
				config.Spec.ManagedSections = []string{"sites"}
			}
			batches, err := configuredWiredBulkBatches(config, source, []string{"Global", "Global/Lab", "Global/Lab/Building"})
			valid := mode == "default" || mode == "root" || mode == "managed" || mode == "root-precedence"
			if (err == nil) != valid || !bytes.Equal(before, source.raw) {
				t.Fatal("option selection mutated or accepted invalid model", err)
			}
			if valid {
				path := "Global/Lab"
				if mode == "default" {
					path = "Global/Lab/Building"
				}
				if len(batches) != 2 || batches[0].path != path || batches[1].path != path || batches[0].members[0].state != "PROVISION" || batches[1].members[0].state != "REPROVISION" {
					t.Fatal("grouping discarded or mixed requested actions")
				}
			}
		})
	}
}

func TestWiredBulkPublicOptionsRefuseSingleDeviceFallback(t *testing.T) {
	for _, mode := range []string{"selected", "invalid", "root-disabled", "resolved-selected", "specific-empty", "invalid-global", "selection-disabled"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"PROVISION"}]}}}`)
			config.Annotations = map[string]string{wiredBulkAnnotation: "true"}
			reason := "BulkExecutionUnavailable"
			if mode == "invalid" {
				config.Annotations[wiredBulkAnnotation] = "yes"
				reason = "InvalidBulkOptions"
			}
			if mode == "root-disabled" {
				config.Annotations[wiredBulkAnnotation] = "false"
				config.Annotations[wiredBulkRootAnnotation] = "Global/Lab"
				reason = "InvalidBulkOptions"
			}
			switch mode {
			case "resolved-selected":
				config.Spec.Source.Inline.Raw = wiredBulkSourceWithSites(t, &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw}, wiredBulkSitesFixture)
				config.Annotations[wiredBulkSpecificSitesAnnotation] = "true"
				config.Annotations[wiredBulkManagedSitesAnnotation] = `["Global/Lab/Building"]`
			case "specific-empty":
				config.Annotations[wiredBulkSpecificSitesAnnotation] = "true"
				reason = "InvalidBulkOptions"
			case "invalid-global":
				config.Annotations[wiredBulkGlobalSettingsAnnotation] = "yes"
				reason = "InvalidBulkOptions"
			case "selection-disabled":
				config.Annotations[wiredBulkAnnotation] = "false"
				config.Annotations[wiredBulkGlobalSettingsAnnotation] = "false"
				reason = "InvalidBulkOptions"
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			requests := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				t.Error("bulk refusal contacted native API")
				w.WriteHeader(500)
			})
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			var published configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &published); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, condition := range published.Status.Conditions {
				if condition.Reason == reason {
					found = true
				}
			}
			state := journalState(t, a)
			if !found || published.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || requests != 0 || state != nil && (state.Operation != nil || len(state.ActionApplications) != 0 || len(state.Owners) != 0) {
				t.Fatal("public bulk option fell through to execution or ownership")
			}
		})
	}
}
