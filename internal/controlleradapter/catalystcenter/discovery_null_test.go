// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// These optional expressions use try(job.field, defaults.field, null) in the
// pinned cc_discovery.tf. A present null does not select the defaults fallback.
var discoveryNullableFields = []string{"cdp_level", "lldp_level", "retry", "time_out", "netconf_port", "preferred_mgmt_ip_method", "ip_filter_list"}

func discoveryNullFixture(t *testing.T, raw []byte, omit bool) []byte {
	t.Helper()
	var model map[string]any
	if err := json.Unmarshal(raw, &model); err != nil {
		t.Fatal(err)
	}
	rows := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["discovery"].([]any)
	for _, row := range rows {
		for _, field := range discoveryNullableFields {
			if omit {
				delete(row.(map[string]any), field)
			} else {
				row.(map[string]any)[field] = nil
			}
		}
	}
	out, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestDiscoveryOptionalNullProjection(t *testing.T) {
	base, err := parseInventoryIntent(discoveryModel())
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range append([]string{"all"}, discoveryNullableFields...) {
		t.Run(field, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(discoveryModel(), &doc); err != nil {
				t.Fatal(err)
			}
			row := doc["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["discovery"].([]any)[0].(map[string]any)
			if field == "all" {
				for _, k := range discoveryNullableFields {
					row[k] = nil
				}
			} else {
				row[field] = nil
			}
			raw, _ := json.Marshal(doc)
			got, err := parseInventoryIntent(raw)
			if err != nil || !reflect.DeepEqual(got, base) {
				t.Fatal("optional null differs from omission", err)
			}
		})
	}
}

func TestDiscoveryNullManifestLifecycle(t *testing.T) {
	for _, combined := range []bool{false, true} {
		t.Run(fmt.Sprint(combined), func(t *testing.T) {
			discoveryManifestScenario(t, discoveryCycleScenario{nullable: true, combined: combined})
		})
	}
}

func TestDiscoveryNullDefaultsPreserveSelection(t *testing.T) {
	for _, field := range discoveryNullableFields {
		t.Run(field, func(t *testing.T) {
			defaults, _ := json.Marshal(map[string]any{"defaults": map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": map[string]any{field: nil}}}}})
			out, meta, err := ExportSelectedDiscoveryWithDefaults(discoveryModel(), defaults, nacmodel.SiteSelectionOptions{})
			if err != nil || meta.AppliedDefaultFields != 1 {
				t.Fatal("optional null override rejected", err)
			}
			got, err := parseInventoryIntent(out)
			base, baseErr := parseInventoryIntent(discoveryModel())
			if err != nil || baseErr != nil || !reflect.DeepEqual(got, base) {
				t.Fatal("null override invented a native value", err)
			}
		})
	}
	// Explicit null suppresses a non-null optional fallback, just as try(null,x).
	defaults := []byte(`{"defaults":{"catalyst_center":{"inventory":{"discovery":{"retry":3}}}}}`)
	out, meta, err := ExportSelectedDiscoveryWithDefaults(discoveryNullFixture(t, discoveryModel(), false), defaults, nacmodel.SiteSelectionOptions{})
	if err != nil || meta.AppliedDefaultFields != 0 {
		t.Fatal("explicit null used fallback", err)
	}
	got, err := parseInventoryIntent(out)
	base, _ := parseInventoryIntent(discoveryModel())
	if err != nil || !reflect.DeepEqual(got, base) {
		t.Fatal("null became selected retry", err)
	}
}

func TestDiscoveryUnsupportedNullStillRefusesBeforeIO(t *testing.T) {
	for _, field := range []string{"name", "type", "ip_address_list", "protocol_order", "global_credential_list", "create_per_site", "password_list", "user_name_list", "enable_password_list", "http_read_credential", "http_write_credential", "unknown_optional"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			a, c := journalFixture(t)
			c.Spec.ManagedSections = []string{"inventory"}
			var doc map[string]any
			if err := json.Unmarshal(discoveryModel(), &doc); err != nil {
				t.Fatal(err)
			}
			doc["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["discovery"].([]any)[0].(map[string]any)[field] = nil
			raw, _ := json.Marshal(doc)
			c.Spec.Source.Inline.Raw = raw
			if err := a.statusWriter.Update(ctx, c); err != nil {
				t.Fatal(err)
			}
			calls := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
			before, _ := a.currentController(ctx)
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			after, _ := a.currentController(ctx)
			if calls != 0 || c.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || !reflect.DeepEqual(before.Status.Configuration, after.Status.Configuration) {
				t.Fatal("unsupported null bypassed whole-source validation", calls, c.Status)
			}
		})
	}
}
