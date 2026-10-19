// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricAuthenticationPublicGroupLifecycle(t *testing.T) {
	for _, name := range []string{"Closed Authentication", "Open Authentication", "Low Impact"} {
		t.Run(name, func(t *testing.T) { testFabricAuthenticationPublicLifecycle(t, name) })
	}
}

func TestFabricAuthenticationPublicFailedTaskKeepsReservationFence(t *testing.T) {
	for _, code := range []string{"NCSP11008", "NCND00010", "NCSP11008: SECRET-MARKER"} {
		t.Run(code, func(t *testing.T) {
			a, config, _, f, members := fabricAuthManagedFixture(t)
			f.afterWrite = func() {
				now := time.Now().UnixMilli()
				f.taskRows = []map[string]any{{"id": "fabric-auth-task", "startTime": now, "endTime": now, "isError": true, "errorCode": code, "failureReason": "SECRET-MARKER", "data": "SECRET-MARKER"}}
			}
			testPublicHandoffPreparation(t, a, config)
			configurationGroupDispatchFixture(t, a, members, false)
			installFabricGroupAuthorityFixture(t, a)
			testPublicHandoffDispatch(t, a, config)
			for range 2 {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(context.Background(), ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(context.Background(), ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				condition := meta.FindStatusCondition(config.Status.Conditions, configapi.NetworkControllerConfigConditionIntentValid)
				if condition == nil || condition.Reason != "ControllerTaskFailed" || config.Status.Phase != configapi.NetworkControllerConfigPhaseFailed {
					t.Fatal("failed native group task hidden as generic waiting", config.Status)
				}
				if (code == "NCSP11008") != strings.Contains(condition.Message, "required controller application unavailable") {
					t.Fatal("unavailable application guidance incorrectly attributed", condition.Message)
				}
				raw, _ := json.Marshal(config.Status)
				if strings.Contains(string(raw), "SECRET-MARKER") {
					t.Fatal("untrusted native failure text leaked into status")
				}
				state := journalState(t, a)
				if state.Operation == nil || state.Operation.Phase != "Accepted" || state.Operation.TaskID != "fabric-auth-task" || len(state.ActionApplications) != 0 || f.writes != 1 {
					t.Fatal("failed task replayed, cleared or falsely verified")
				}
				var reservations ops.ControllerConfigurationReservationList
				if err := a.statusReader.List(context.Background(), &reservations); err != nil || len(reservations.Items) != 2 {
					t.Fatal("failed task lost reservations", err)
				}
				for _, r := range reservations.Items {
					if r.Status.Grant == nil || r.Status.Grant.Phase != "Held" || r.Status.Completion != nil || r.Status.ManagerSettledAt != nil {
						t.Fatal("failed task released a device")
					}
				}
			}
		})
	}
}

func testFabricAuthenticationPublicLifecycle(t *testing.T, name string) {
	t.Helper()
	a, config, _, f, members := fabricAuthManagedFixture(t, name)
	f.afterWrite = func() {
		now := time.Now().UnixMilli()
		f.taskRows = []map[string]any{{"id": "fabric-auth-task", "startTime": now, "endTime": now, "isError": false}}
	}
	testPublicHandoffPreparation(t, a, config)
	configurationGroupDispatchFixture(t, a, members, false)
	installFabricGroupAuthorityFixture(t, a)
	testPublicHandoffDispatch(t, a, config)
	a.configurationAdmission = nil
	var list ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(context.Background(), &list); err != nil {
		t.Fatal(err)
	}
	claims := make([]*ops.ControllerConfigurationReservation, len(list.Items))
	for i := range list.Items {
		claims[i] = list.Items[i].DeepCopy()
	}
	testPublicHandoffRecovery(t, a, config, claims, "dispatch-recovery-public")
	if f.writes != 1 {
		t.Fatal("public cycle did not perform one native update", f.writes)
	}
	state := journalState(t, a)
	if len(state.ActionApplications) != 1 {
		t.Fatal("verified result receipt missing")
	}
}
