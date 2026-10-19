// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricEWLCPublicReport(t *testing.T) {
	for _, mode := range []string{"existing", "drift", "unprovisioned", "late-settings", "disable", "disabled-unverified", "duplicate-physical"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, members, _ := ewlcJournalFixture(t, "existing")
			nativeMode := "valid"
			if mode == "unprovisioned" || mode == "late-settings" {
				nativeMode = mode
			}
			c, _ := ewlcComposedFixture(t, nativeMode, func(w http.ResponseWriter, r *http.Request) bool {
				if mode != "disabled-unverified" || r.URL.Path != "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting" {
					return false
				}
				if r.Method != "GET" {
					t.Error("report attempted mutation")
					w.WriteHeader(http.StatusMethodNotAllowed)
					return true
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []fabricEWLCSetting{{ID: "edge-id", Enabled: false, Rolling: fabricEWLCRolling{Percentage: 25}}}})
				return true
			})
			a.client = c
			if mode == "drift" {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"name":"edge"`, `"name":"edge","enable_rolling_ap_upgrade":true`, 1))
			}
			if mode == "disable" || mode == "disabled-unverified" {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"name":"edge"`, `"name":"edge","enable_wireless":false`, 1))
			}
			if mode == "duplicate-physical" {
				var root map[string]any
				if err := json.Unmarshal(config.Spec.Source.Inline.Raw, &root); err != nil {
					t.Fatal(err)
				}
				inventory := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)
				rows := inventory["devices"].([]any)
				second := map[string]any{}
				for k, v := range rows[0].(map[string]any) {
					second[k] = v
				}
				second["name"] = "zzz-alias"
				inventory["devices"] = append(rows, second)
				raw, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline.Raw = raw
			}
			// A report needs neither managed workers nor physical reservations.
			for _, member := range members {
				for _, obj := range []ctrlclient.Object{member.device, member.node} {
					if err := a.statusWriter.Delete(ctx, obj); err != nil {
						t.Fatal(err)
					}
				}
			}
			a.configurationAdmission = nil
			config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			before := journalState(t, a)
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			invalid := mode == "duplicate-physical" || mode == "unprovisioned" || mode == "late-settings" || mode == "disabled-unverified"
			condition := meta.FindStatusCondition(config.Status.Conditions, configapi.NetworkControllerConfigConditionIntentValid)
			if condition == nil {
				t.Fatal("missing intent condition")
			}
			if invalid {
				if condition.Reason != "ObservationFailed" || len(config.Status.Drift) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("incomplete evidence reported compliant", config.Status)
				}
			} else {
				expected := 0
				if mode != "existing" {
					expected = 1
				}
				if len(config.Status.Drift) != expected || (config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) != (expected == 0) {
					t.Fatal("incorrect report", config.Status)
				}
				if len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].Name != "inventory" || config.Status.SectionStatus[0].ResourceCount != 1 {
					t.Fatal("incorrect section", config.Status)
				}
				for _, row := range config.Status.Drift {
					if row.Section != "inventory" || row.Change != configapi.NetworkControllerDriftChangeUpdate {
						t.Fatal(row)
					}
				}
			}
			if !reflect.DeepEqual(before, journalState(t, a)) {
				t.Fatal("report acquired ownership")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
				t.Fatal("report acquired reservations", err)
			}
		})
	}
}

func TestFabricEWLCReportRejectsUnconsumedIntent(t *testing.T) {
	_, config, _, _, _ := ewlcJournalFixture(t, "existing")
	raw := string(config.Spec.Source.Inline.Raw)
	for _, source := range []string{
		strings.Replace(raw, `"inventory":`, `"sites":{"areas":[]},"inventory":`, 1),
		strings.Replace(raw, `"name":"edge"`, `"name":"edge","unknown":true`, 1),
	} {
		if source == raw {
			t.Fatal("test mutation missed")
		}
		if _, err := (&client{}).compileConfigReports([]byte(source), []string{"inventory"}); err == nil {
			t.Fatal("unconsumed intent accepted")
		}
	}
}
