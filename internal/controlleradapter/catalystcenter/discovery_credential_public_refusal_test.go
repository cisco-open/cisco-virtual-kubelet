// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDiscoveryCredentialPublicRefusesUnsupportedBeforeIO(t *testing.T) {
	for _, mode := range []string{"extra-root", "tags", "second-unsupported-job", "second-global-metadata-job", "plaintext", "missing-counter", "projection-lag", "incomplete-snmp", "wrong-pointer", "comparison-use", "revoked-alias", "delete", "prune-unresolved"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
			a, nc, c, _, _ := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
			var model map[string]any
			if json.Unmarshal(raw, &model) != nil {
				t.Fatal("invalid fixture")
			}
			root := model["catalyst_center"].(map[string]any)
			inventory := root["inventory"].(map[string]any)
			jobs := inventory["discovery"].([]any)
			job := jobs[0].(map[string]any)
			switch mode {
			case "extra-root":
				root["sites"] = map[string]any{}
			case "tags":
				inventory["tags"] = []any{}
			case "second-unsupported-job", "second-global-metadata-job":
				row := map[string]any{"name": "later", "type": "SINGLE", "ip_address_list": "192.0.2.2", "protocol_order": "SSH", "global_credential_list": []any{"global"}}
				if mode == "second-unsupported-job" {
					row["unknown"] = true
				} else {
					row["user_name_list"] = []any{"operator"}
				}
				inventory["discovery"] = append(jobs, row)
			case "plaintext":
				job["password_list"] = []any{discoveryCredentialMarker}
			case "missing-counter":
				delete(job, "password_list_version")
			case "projection-lag":
				job["password_list_version"] = 2
			case "incomplete-snmp":
				job["snmp_version"] = "v3"
				job["snmp_mode"] = "AuthPriv"
			case "wrong-pointer":
				c.Spec.SecretRefs[0].Path = "/discovery/0/unknown"
			case "comparison-use":
				c.Spec.SecretRefs[0].Use = "PrivateComparisonKey"
			case "revoked-alias":
				nc.Spec.IntentSecretSources = nil
				if err := a.statusWriter.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "delete":
				c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			case "prune-unresolved":
				c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
				c.Spec.ModelSource.Resolved = false
			}
			encoded, err := json.Marshal(model)
			if err != nil {
				t.Fatal(err)
			}
			c.Spec.Source.Inline.Raw = encoded
			if err := a.statusWriter.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			calls := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			nc, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 0 || c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || nc.Status.Configuration != nil {
				t.Fatal("unsupported/unauthorized source reached native I/O or journal", calls, c.Status.Phase)
			}
		})
	}
}
