// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	apps "k8s.io/api/apps/v1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func TestMulticastVNCreateThenUpdate(t *testing.T)       { testMulticastVNCreateThenUpdate(t, false) }
func TestMulticastVNPublicCreateThenUpdate(t *testing.T) { testMulticastVNCreateThenUpdate(t, true) }
func TestMulticastVNPublicReplicationSequence(t *testing.T) {
	testMulticastVNCreateThenUpdate(t, true, true)
}
func testMulticastVNCreateThenUpdate(t *testing.T, public bool, replication ...bool) {
	withReplication := len(replication) > 0 && replication[0]
	steps := 2
	if withReplication {
		steps = 4
	}
	replicationMode := "HEADEND_REPLICATION"
	ctx := context.Background()
	var native map[string]any
	posts := 0
	tasks := map[string]int64{}
	a, config, _, physical := multicastVNJournalFixture(t, "create", func(w http.ResponseWriter, r *http.Request) bool {
		reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
		if r.URL.Path == fabricMulticastPath {
			if r.Method == "GET" {
				reply([]fabricMulticastReplication{{FabricID: "fabric", Mode: replicationMode}})
				return true
			}
			var rows []fabricMulticastReplication
			if r.Method != "PUT" || native == nil || json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 || rows[0].FabricID != "fabric" {
				t.Error("replication before VN or malformed payload")
				w.WriteHeader(400)
				return true
			}
			replicationMode = rows[0].Mode
			posts++
			id := fmt.Sprintf("task-%d", posts)
			tasks[id] = time.Now().UnixMilli()
			reply(map[string]string{"taskId": id})
			return true
		}
		if r.URL.Path == fabricMulticastVNsPath {
			if r.Method == "GET" {
				if native == nil {
					reply([]any{})
				} else {
					reply([]map[string]any{native})
				}
				return true
			}
			expected := "POST"
			if native != nil {
				expected = "PUT"
			}
			var rows []map[string]any
			if r.Method != expected || json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
				t.Error("invalid multicast request")
				w.WriteHeader(400)
				return true
			}
			native = rows[0]
			native["id"] = "multicast-Corp"
			native["multicastRPs"].([]any)[0].(map[string]any)["ipv4Address"] = "192.0.2.1"
			posts++
			id := fmt.Sprintf("task-%d", posts)
			tasks[id] = time.Now().UnixMilli()
			reply(map[string]string{"taskId": id})
			return true
		}
		if strings.HasPrefix(r.URL.Path, taskPath) && strings.HasSuffix(r.URL.Path, "/tree") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, exists := tasks[id]
			if !exists {
				w.WriteHeader(404)
				return true
			}
			reply([]map[string]any{{"id": id, "isError": false, "startTime": stamp - 1000, "endTime": stamp}})
			return true
		}
		return false
	})
	if withReplication {
		config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"multicast":{`, `"multicast":{"replication_mode":"NATIVE_MULTICAST",`, 1))
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
	for cycle := 0; cycle < steps; cycle++ {
		if cycle > 0 && (!withReplication || cycle == 2) {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			config.Generation++
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"232.0.0.0/8"`, `"232.0.0.0/8","233.0.0.0/8"`, 1))
			if withReplication {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), "NATIVE_MULTICAST", "HEADEND_REPLICATION", 1))
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			for _, member := range physical {
				for _, obj := range []ctrlclient.Object{member.device, member.node} {
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), obj); err != nil {
						t.Fatal(err)
					}
				}
			}
		}
		for _, member := range physical {
			for _, obj := range []ctrlclient.Object{member.device, member.node} {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), obj); err != nil {
					t.Fatal(err)
				}
			}
		}
		source, err := loadControllerSource(ctx, a.statusReader, config)
		if err != nil {
			t.Fatal(err)
		}

		expected := "Create"
		if cycle > 0 {
			expected = "Update"
		}
		if public {
			a.configurationAdmission = nil
			testPublicHandoffPreparation(t, a, config)
			op := journalState(t, a).Operation
			target := "vn"
			if withReplication && cycle%2 == 1 {
				target = "fabric"
			}
			if op.Action != expected || op.TargetID != target {
				t.Fatal("public logical identity", op)
			}
			configurationGroupDispatchFixture(t, a, physical, false)
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
			}
		} else {
			op, err := a.prepareFabricMulticastVN(ctx, config, source, "Global/Lab", "Corp")
			if err != nil || op == nil {
				t.Fatal("prepare", cycle, err)
			}
			if op.Action != expected || op.TargetID != "vn" {
				t.Fatal("unstable logical identity", op)
			}
			permit, _ := configurationGroupDispatchFixture(t, a, physical, true)
			if _, err := a.dispatchFabricMulticastVN(ctx, config, source, "Global/Lab", "Corp", permit); err != nil {
				t.Fatal("dispatch", err)
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil {
				t.Fatal(err)
			}
			members := make([]*ops.ControllerConfigurationReservation, len(reservations.Items))
			for i := range reservations.Items {
				members[i] = reservations.Items[i].DeepCopy()
			}
			if err := a.beginFabricMulticastVNVerification(ctx, config, source, "Global/Lab", "Corp", members, authorize, time.Now()); err != nil {
				t.Fatal("recover", err)
			}
			if err := a.recordFabricMulticastVNApplication(ctx, config, op.ID, "Global/Lab", "Corp", members, authorize, time.Now()); err != nil {
				t.Fatal("publish", cycle, err)
			}
		}
		state := journalState(t, a)
		receiptCount := 1
		if withReplication && cycle > 0 {
			receiptCount = 2
		}
		if len(state.ActionApplications) != receiptCount || state.Operation.Action != expected {
			t.Fatal("receipt not replaced", state)
		}
		a.configurationAdmission = nil
		testFabricHandoffCompletion(t, a, config, true)
		var reservations ops.ControllerConfigurationReservationList
		if err := a.statusReader.List(ctx, &reservations); err != nil {
			t.Fatal(err)
		}
		for i := range reservations.Items {
			if err := contract.RetireSettledGroupReservation(ctx, a.statusReader, a.statusWriter, &reservations.Items[i], func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
				t.Fatal("retire", err)
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
		if public {
			for _, obj := range []ctrlclient.Object{&apps.Deployment{}, &apps.ReplicaSet{}, &core.Pod{}} {
				if err := a.statusWriter.DeleteAllOf(ctx, obj, ctrlclient.InNamespace(config.Namespace)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	if public {
		a.configurationAdmission = nil
		if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
			t.Error("converged state requested authority")
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
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || posts != steps {
				t.Fatal("public convergence", config.Status, posts)
			}
		}
	}
	if op, err := a.prepareFabricMulticastVN(ctx, config, source, "Global/Lab", "Corp"); err != nil || op != nil || posts != steps {
		t.Fatal("converged intent replayed", op, posts, err)
	}
}
