// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func sdaTransitDeviceEvidenceFixture(t *testing.T, aged ...bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, sdaTransitIntent, *cvk.NetworkControllerConfigurationOperation, []*ops.ControllerConfigurationReservation, *sdaTransitFixture, time.Time) {
	t.Helper()
	ctx := context.Background()
	a, config, want, f, physical := sdaTransitJournalFixture(t)
	if len(aged) > 0 && aged[0] {
		f.stamp -= 10000
	}
	if _, err := a.prepareSDATransit(ctx, config, nil, want); err != nil {
		t.Fatal(err)
	}
	permit, _ := configurationGroupDispatchPermitFixture(t, a, physical)
	if _, err := a.dispatchSDATransit(ctx, config, nil, want, permit); err != nil {
		t.Fatal(err)
	}
	var list ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(ctx, &list); err != nil {
		t.Fatal(err)
	}
	members := make([]*ops.ControllerConfigurationReservation, len(list.Items))
	for i := range list.Items {
		members[i] = list.Items[i].DeepCopy()
	}
	if err := a.beginSDATransitVerification(ctx, config, nil, want, members, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	a.workerPodName, a.workerPodUID = "connector", "connector-uid"
	a.configurationAdmission = &configurationAdmission{protocol: contract.GroupReservationProtocol, leaseNamespace: "leases", authorize: func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }}
	return a, config, source, want, journalState(t, a).Operation.DeepCopy(), members, f, time.UnixMilli(f.stamp).Add(5 * time.Second)
}

func TestSDATransitDeviceEvidence(t *testing.T) {
	for _, mode := range []string{"observed", "opaque-session-text", "pending", "missing-member", "unbound", "wrong-worker", "wrong-owner", "wrong-command", "wrong-serial", "truncated", "redacted", "artifact", "before-task", "stale-result", "future-result", "failed", "old-generation", "duplicate-ready", "source-race", "native-race", "completion-race", "authority-denied", "result-replaced", "foreign-owner", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, want, op, members, f, now := sdaTransitDeviceEvidenceFixture(t)
			sessionText := "Sessions for VRF default, total: 0, established: 0\n"
			if mode == "opaque-session-text" {
				sessionText = "PRIVATE_SYNTHETIC_LISP_OUTPUT: no semantic contract implied"
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			collect := func() (map[string]sdaTransitDeviceEvidence, error) {
				return a.collectSDATransitDeviceEvidence(ctx, config, source, want, op, members, authorize, now)
			}
			for i := 0; i < 2; i++ {
				if data, err := collect(); !errors.Is(err, errSDATransitDeviceEvidencePending) || data != nil {
					t.Fatal("unbound worker result accepted", err)
				}
			}
			var leaves ops.DeviceOperationList
			if err := a.statusReader.List(ctx, &leaves); err != nil || len(leaves.Items) != 2 {
				t.Fatal("requests duplicated or incomplete", err, len(leaves.Items))
			}
			for i := range leaves.Items {
				leaf := &leaves.Items[i]
				if managedprotocol.NetworkObjectBindingComplete(leaf.Annotations) {
					t.Fatal("connector supplied protected worker binding")
				}
				var member *ops.ControllerConfigurationReservation
				for _, m := range members {
					if m.Spec.Device.Name == leaf.Spec.DeviceRef.Name {
						member = m
					}
				}
				if member == nil {
					t.Fatal("read escaped admitted group")
				}
				stampLANProofEnvelope(leaf, member, now.Add(-2*time.Second), now.Add(-time.Second), now)
				leaf.Status.Outputs = []ops.DeviceOperationOutput{{Command: leaf.Spec.Operation.Commands[0], Output: "Processor board ID " + member.Spec.PhysicalIdentity + "\n"}, {Command: leaf.Spec.Operation.Commands[1], Output: sessionText}}
				if i == 0 {
					switch mode {
					case "pending":
						leaf.Status.Phase = ops.OperationPhasePending
					case "unbound":
						delete(leaf.Annotations, managedprotocol.AnnotationManaged)
					case "wrong-worker":
						leaf.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] = "foreign-worker"
					case "wrong-owner":
						leaf.OwnerReferences[0].UID = "foreign-controller"
					case "wrong-command":
						leaf.Status.Outputs[1].Command = "show clock"
					case "wrong-serial":
						leaf.Status.Outputs[0].Output = "Processor board ID FOREIGN\n"
					case "truncated":
						leaf.Status.Outputs[1].Truncated = true
					case "redacted":
						leaf.Status.Outputs[1].Redacted = true
					case "artifact":
						leaf.Status.ArtifactURIs = []string{"untrusted-artifact"}
					case "before-task":
						leaf.CreationTimestamp = metav1.NewTime(time.UnixMilli(f.stamp))
					case "stale-result":
						leaf.Status.CompletionTime = &metav1.Time{Time: now.Add(-6 * time.Minute)}
					case "future-result":
						leaf.Status.CompletionTime = &metav1.Time{Time: now.Add(time.Second)}
					case "failed":
						leaf.Status.Phase = ops.OperationPhaseFailed
					case "old-generation":
						leaf.Status.ObservedGeneration = 0
					case "duplicate-ready":
						leaf.Status.Conditions = append(leaf.Status.Conditions, leaf.Status.Conditions[0])
					}
				}
				if err := a.statusWriter.Update(ctx, leaf); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing-member" {
				members = members[:1]
			}
			if mode == "authority-denied" {
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error { return errors.New("denied") }
			}
			if mode == "disabled" {
				a.configurationAdmission = nil
			}
			if mode == "result-replaced" {
				f.transits[0].ID = "replacement-transit"
			}
			if mode == "native-race" {
				f.afterTask = func() { f.provisionID = "replacement" }
			}
			if mode == "completion-race" {
				f.afterTask = func() { f.stamp += 1000 }
			}
			if mode == "foreign-owner" {
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "source-race" {
				calls := 0
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error {
					calls++
					if calls == 2 {
						config.Generation++
						return a.statusWriter.Update(ctx, config)
					}
					return nil
				}
			}
			before := journalState(t, a).DeepCopy()
			data, err := collect()
			positive := mode == "observed" || mode == "opaque-session-text"
			if positive {
				if err != nil || len(data) != 2 {
					t.Fatal("complete worker evidence not collected", err)
				}
				formatted := fmt.Sprintf("%v/%#v", data, data)
				if strings.Contains(formatted, "PRIVATE_SYNTHETIC") || strings.Contains(formatted, "Sessions for VRF") {
					t.Fatal("private CLI leaked via formatting")
				}
				for _, entry := range data {
					if entry.outputs[1] != sessionText {
						t.Fatal("collector silently rewrote raw session evidence")
					}
				}
			} else if err == nil || data != nil {
				t.Fatal("invalid or partial evidence accepted", err)
			}
			if !reflect.DeepEqual(before, journalState(t, a)) || f.posts != 1 || len(journalState(t, a).ActionApplications) != 0 {
				t.Fatal("collection mutated the journal, published convergence or replayed")
			}
		})
	}
}

func TestSDATransitEvidenceCommandsAreIndependent(t *testing.T) {
	if err := diagnostic.ValidateCommandsForPlatform(diagnostic.CommandPlatformIOSXE, sdaTransitEvidenceCommands()); err != nil {
		t.Fatal("worker platform rejected SDA read-only query plan", err)
	}
	first := sdaTransitEvidenceCommands()
	first[0] = "show clock"
	if !reflect.DeepEqual(sdaTransitEvidenceCommands(), []string{"show version", "show lisp session all"}) {
		t.Fatal("worker query plan aliases caller state")
	}
}

func TestSDATransitPublicDeviceEvidenceBoundary(t *testing.T) {
	ctx := context.Background()
	a, config, _, _, _, members, f, _ := sdaTransitDeviceEvidenceFixture(t, true)
	before := journalState(t, a).DeepCopy()
	tick := func() string {
		t.Helper()
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		status := &configapi.NetworkControllerConfigStatus{}
		_, pending, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
		if !pending || status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
			t.Fatal("public evidence escaped its waiting boundary", reason)
		}
		return reason
	}
	for i := 0; i < 2; i++ {
		if reason := tick(); reason != "GroupDeviceEvidencePending" {
			t.Fatal("missing all-member reads were accepted", reason)
		}
	}
	var leaves ops.DeviceOperationList
	if err := a.statusReader.List(ctx, &leaves); err != nil || len(leaves.Items) != 2 {
		t.Fatal("public read requests missing or duplicated", err)
	}
	for i := range leaves.Items {
		leaf := &leaves.Items[i]
		var member *ops.ControllerConfigurationReservation
		for _, m := range members {
			if m.Spec.Device.Name == leaf.Spec.DeviceRef.Name {
				member = m
			}
		}
		if member == nil {
			t.Fatal("request outside admitted group")
		}
		now := time.Now()
		stampLANProofEnvelope(leaf, member, now.Add(-2*time.Second), now.Add(-time.Second), now)
		leaf.Status.Outputs = []ops.DeviceOperationOutput{{Command: leaf.Spec.Operation.Commands[0], Output: "Processor board ID " + member.Spec.PhysicalIdentity + "\n"}, {Command: leaf.Spec.Operation.Commands[1], Output: "Sessions for VRF default, total: 0, established: 0\n"}}
		if err := a.statusWriter.Update(ctx, leaf); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if reason := tick(); reason != "GroupDeviceEvidencePending" {
				t.Fatal("partial evidence advanced to semantic verification", reason)
			}
		}
	}
	for i := 0; i < 2; i++ {
		if reason := tick(); reason != "GroupConvergenceVerificationUnavailable" {
			t.Fatal("complete reads were mistaken for semantic convergence", reason)
		}
	}
	if !reflect.DeepEqual(before, journalState(t, a)) || f.posts != 1 {
		t.Fatal("diagnostic reads changed journal, receipt or submission count")
	}
}
