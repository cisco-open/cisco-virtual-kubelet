// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	core "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFabricPortChannelReport(t *testing.T) {
	for _, mode := range []string{"existing", "missing", "overlap", "not-member", "recovery-replaced", "vlan", "attributes"} {
		t.Run(mode, func(t *testing.T) {
			nativeMode := mode
			if mode == "attributes" {
				nativeMode = "existing"
			}
			a, config, _, managed, revised := portChannelJournalFixture(t, nativeMode)
			*revised = true
			if mode == "vlan" {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"id":7`, `"id":7,"native_vlan_id":100`, 1))
			}
			if mode == "attributes" {
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"protocol":"LACP"`, `"protocol":"ON"`, 1))
			}
			ctx := context.Background()
			// Report must work without managed-device objects, Nodes or reservation
			// configuration. Native dependencies are observed without physical authority.
			for _, member := range managed {
				for _, obj := range []ctrlclient.Object{member.device, member.node} {
					if err := a.statusWriter.Delete(ctx, obj); err != nil {
						t.Fatal(err)
					}
				}
			}
			config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			retained := journalState(t, a)
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			invalid := mode == "not-member" || mode == "recovery-replaced" || mode == "vlan"
			condition := meta.FindStatusCondition(config.Status.Conditions, configapi.NetworkControllerConfigConditionIntentValid)
			if condition == nil {
				t.Fatal("missing report condition")
			}
			if invalid {
				if condition.Reason != "ObservationFailed" || len(config.Status.Drift) != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("incomplete evidence published as drift/compliance", config.Status)
				}
			} else {
				expected := 0
				if mode == "missing" {
					expected = 1
				}
				if mode == "overlap" || mode == "attributes" {
					expected = 1
				}
				if len(config.Status.Drift) != expected {
					t.Fatal("drift count", config.Status.Drift)
				}
				if len(config.Status.SectionStatus) != 1 || config.Status.SectionStatus[0].ResourceCount != 1 {
					t.Fatal("missing channel count", config.Status.SectionStatus)
				}
				if (config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) != (expected == 0) {
					t.Fatal("report phase", config.Status.Phase)
				}
				for _, entry := range config.Status.Drift {
					change := configapi.NetworkControllerDriftChangeAdd
					if mode == "overlap" || mode == "attributes" {
						change = configapi.NetworkControllerDriftChangeUpdate
					}
					if entry.Section != "fabric" || entry.Change != change {
						t.Fatal("drift classification", entry)
					}
				}
			}
			if after := journalState(t, a); (retained == nil) != (after == nil) || after != nil && (after.Operation != nil || len(after.Owners) != 0) {
				t.Fatal("report acquired ownership")
			}
			var reservations ops.ControllerConfigurationReservationList
			if err := a.statusReader.List(ctx, &reservations); err != nil || len(reservations.Items) != 0 {
				t.Fatal("report created reservation", err)
			}
			var nodes core.NodeList
			if err := a.statusReader.List(ctx, &nodes); err != nil || len(nodes.Items) != 0 {
				t.Fatal("report unexpectedly requires nodes", err)
			}
		})
	}
}

func TestFabricPortChannelReportRejectsUnconsumedIntent(t *testing.T) {
	raw := string(portChannelTestModel("[" + portChannelTestRow + "]"))
	for _, source := range []string{
		strings.Replace(raw, `"inventory":`, `"sites":{"areas":[]},"inventory":`, 1),
		strings.Replace(raw, `"id":7`, `"id":7,"unknown":true`, 1),
		strings.Replace(raw, `"state":"PROVISION"`, `"state":"ASSIGN"`, 1),
	} {
		if source == raw {
			t.Fatal("test mutation missed")
		}
		if _, err := (&client{}).compileConfigReports([]byte(source), []string{"fabric"}); err == nil {
			t.Fatal("unconsumed intent accepted")
		}
	}
}
