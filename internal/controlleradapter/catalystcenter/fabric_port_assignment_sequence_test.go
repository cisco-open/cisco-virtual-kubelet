// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricPortAssignmentEmptyRangeSequence(t *testing.T) {
	ctx := context.Background()
	rows := []fabricPortAssignment{}
	posts := 0
	tasks := map[string]int64{}
	a, config, _, physical, _ := portChannelJournalFixture(t, "assignment/"+("assignment-sequence"), func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, taskPath) {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			at, ok := tasks[id]
			if !ok || r.Method != "GET" {
				t.Error("unknown task observation")
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": []map[string]any{{"id": id, "isError": false, "startTime": at, "endTime": at}}})
			return
		}
		if r.Method == "GET" {
			_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
			return
		}
		if r.Method != "POST" {
			t.Error("unexpected method")
			w.WriteHeader(400)
			return
		}
		var body []map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
			t.Error("invalid native request")
			w.WriteHeader(400)
			return
		}
		port, ok := body[0]["interfaceName"].(string)
		if !ok {
			t.Error("missing interface")
			return
		}
		for _, row := range rows {
			if row.Interface == port {
				t.Error("recreated completed port")
			}
		}
		posts++
		id := fmt.Sprintf("task-%d", posts)
		tasks[id] = time.Now().UnixMilli()
		rows = append(rows, fabricPortAssignment{ID: fmt.Sprintf("native-%d", posts), FabricID: "fabric", DeviceID: "edge-id", Interface: port, Type: "USER_DEVICE"})
		_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]string{"taskId": id}})
	})
	config.Spec.Source.Inline.Raw = portAssignmentTestModel(`[{"interfaces_range":"GigabitEthernet1/0/1-1/0/2","connected_device_type":"USER_DEVICE"}]`)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
	for index := 1; index <= 2; index++ {
		port := fmt.Sprintf("GigabitEthernet1/0/%d", index)
		for _, member := range physical {
			for _, obj := range []ctrlclient.Object{member.device, member.node} {
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(obj), obj); err != nil {
					t.Fatal(err)
				}
			}
		}
		op, err := a.prepareFabricPortAssignment(ctx, config, source, "edge", port)
		if err != nil || op == nil {
			t.Fatal("sequence preparation", err)
		}
		permit, _ := configurationGroupDispatchFixture(t, a, physical, true)
		if _, err := a.dispatchFabricPortAssignment(ctx, config, source, "edge", port, permit); err != nil {
			t.Fatal(err)
		}
		var list ops.ControllerConfigurationReservationList
		if err := a.statusReader.List(ctx, &list); err != nil {
			t.Fatal(err)
		}
		members := []*ops.ControllerConfigurationReservation{}
		for i := range list.Items {
			if list.Items[i].Status.Grant.Phase == "Held" {
				members = append(members, list.Items[i].DeepCopy())
			}
		}
		if err := a.beginFabricPortAssignmentVerification(ctx, config, source, "edge", port, members, authorize, time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := a.recordFabricPortAssignmentApplication(ctx, config, op.ID, "edge", port, members, authorize, time.Now()); err != nil {
			t.Fatal(err)
		}
		if len(journalState(t, a).ActionApplications) != index {
			t.Fatal("previous receipt lost")
		}
		// The terminal fixture installs its own simulated actor callback.
		a.configurationAdmission = nil
		testFabricHandoffCompletion(t, a, config, true)
		// Simulate the device actor's lease cleanup only after verified complete
		// release/manager settlement. Production adapter never performs this.
		var leases coordination.LeaseList
		if err := a.statusReader.List(ctx, &leases); err != nil {
			t.Fatal(err)
		}
		for i := range leases.Items {
			if err := a.statusWriter.Delete(ctx, &leases.Items[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if posts != 2 || len(rows) != 2 || len(journalState(t, a).ActionApplications) != 2 || journalState(t, a).Operation != nil {
		t.Fatal("incomplete sequence")
	}
	a.configurationAdmission = nil
	if err := a.ConfigureConfigurationGroupReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error {
		t.Fatal("converged manifest requested authority")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || posts != 2 {
			t.Fatal("sequence did not converge without replay", config.Status.Phase)
		}
	}

}
