// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricEWLCPublicLifecycle(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-roles-%t", inherited), func(t *testing.T) { testFabricEWLCPublicLifecycle(t, inherited) })
	}
}
func testFabricEWLCPublicLifecycle(t *testing.T, inherited bool) {
	ctx := context.Background()
	a, config, _, members, _ := ewlcJournalFixture(t, "valid")
	if inherited {
		config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"EDGE_NODE",`, ``, 1))
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	var enabled atomic.Bool
	var percentage atomic.Int32
	percentage.Store(25)
	var writes atomic.Int32
	var stamp atomic.Int64
	c, _ := ewlcComposedFixture(t, "valid", func(w http.ResponseWriter, r *http.Request) bool {
		reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
		if r.URL.Path == "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting" {
			if r.Method == "PUT" {
				var body fabricEWLCSetting
				if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "edge-id" || !body.Enabled || !body.Rolling.Enabled {
					t.Error("bad public payload")
					w.WriteHeader(400)
					return true
				}
				writes.Add(1)
				enabled.Store(true)
				percentage.Store(int32(body.Rolling.Percentage))
				stamp.Store(time.Now().UnixMilli())
				fmt.Fprint(w, `{"response":{"taskId":"public-task"}}`)
				return true
			}
			reply([]fabricEWLCSetting{{ID: "edge-id", Enabled: true, Rolling: fabricEWLCRolling{Enabled: enabled.Load(), Percentage: int(percentage.Load())}}})
			return true
		}
		if r.URL.Path == taskPath+"public-task/tree" {
			n := stamp.Load()
			reply([]map[string]any{{"id": "public-task", "isError": false, "startTime": n - 1000, "endTime": n}})
			return true
		}
		return false
	})
	a.client = c

	for cycle, desiredPercentage := range []int{25, 15, 5} {
		if cycle > 0 {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			var root map[string]any
			if err := json.Unmarshal(config.Spec.Source.Inline.Raw, &root); err != nil {
				t.Fatal(err)
			}
			row := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["devices"].([]any)[0].(map[string]any)
			row["ap_reboot_percentage"] = desiredPercentage
			raw, err := json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline.Raw = raw
			config.Generation++
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			for _, member := range members {
				for _, object := range []ctrlclient.Object{member.device, member.node} {
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(object), object); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		a.configurationAdmission = nil
		testPublicHandoffPreparation(t, a, config)
		configurationGroupDispatchFixture(t, a, members, false)
		installFabricGroupAuthorityFixture(t, a)
		testPublicHandoffDispatch(t, a, config)
		a.configurationAdmission = nil
		if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
			t.Fatal(err)
		}
		for _, phase := range []string{"Verifying", "Verified"} {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if journalState(t, a).Operation.Phase != phase {
				t.Fatal("public recovery phase", journalState(t, a).Operation.Phase)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].Name != "inventory" {
				t.Fatal("wrong section status", config.Status.SectionStatus)
			}
		}
		a.configurationAdmission = nil
		testFabricHandoffCompletion(t, a, config, true)
		a.configurationAdmission = nil
		if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
			t.Error("converged operation requested admission")
			return errConfigurationDeviceAdmission
		}); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || percentage.Load() != int32(desiredPercentage) || writes.Load() != int32(cycle+1) || len(journalState(t, a).ActionApplications) != 1 || journalState(t, a).Operation != nil {
				t.Fatal("public lifecycle failed to converge", config.Status.Phase)
			}
		}
		var settled ops.ControllerConfigurationReservationList
		if err := a.statusReader.List(ctx, &settled); err != nil {
			t.Fatal(err)
		}
		for i := range settled.Items {
			if err := contract.RetireSettledGroupReservation(ctx, a.statusReader, a.statusWriter, &settled.Items[i], func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		var leases coordination.LeaseList
		if err := a.statusReader.List(ctx, &leases); err != nil {
			t.Fatal(err)
		}
		for i := range leases.Items {
			if err := a.statusWriter.Delete(ctx, &leases.Items[i]); err != nil {
				t.Fatal(err)
			}
		}
		for _, object := range []ctrlclient.Object{&apps.Deployment{}, &apps.ReplicaSet{}, &core.Pod{}} {
			if err := a.statusWriter.DeleteAllOf(ctx, object, ctrlclient.InNamespace(config.Namespace)); err != nil {
				t.Fatal(err)
			}
		}

	}
}

func TestFabricEWLCPublicPreflightAllIntent(t *testing.T) {
	for _, mode := range []string{"disable", "later-unready", "duplicate-physical"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, _, _ := ewlcJournalFixture(t, "valid")
			if mode == "disable" {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"name":"edge"`, `"name":"edge","enable_wireless":false`, 1))
			} else {
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
				second["name"] = "zzz-peer"
				if mode == "later-unready" {
					second["serial_number"] = "FOC87654321"
				}
				inventory["devices"] = append(rows, second)
				raw, err := json.Marshal(root)
				if err != nil {
					t.Fatal(err)
				}
				config.Spec.Source.Inline.Raw = raw
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			a.configurationAdmission = nil
			a.workerPodName, a.workerPodUID = "connector", "connector-uid"
			if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
				t.Error("preflight requested mutation authority")
				return errConfigurationDeviceAdmission
			}); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var status configapi.NetworkControllerConfigStatus
			_, valid, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, &status)
			if mode == "disable" && (valid || reason != "UnsupportedIntent") {
				t.Fatal("disable not explicitly rejected", valid, reason)
			}
			if mode != "disable" && reason != "GroupPreparationPending" {
				t.Fatal("unexpected preflight result", reason)
			}
			state := journalState(t, a)
			if state != nil && state.Operation != nil {
				t.Fatal("partially prepared invalid manifest")
			}
		})
	}
}
