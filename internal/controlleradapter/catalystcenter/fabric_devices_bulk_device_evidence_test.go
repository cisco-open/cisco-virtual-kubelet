// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
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
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func fabricBulkEvidenceFixture(t *testing.T, zone bool) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, *cvk.NetworkControllerConfigurationOperation, []*ops.ControllerConfigurationReservation, *fabricDevicesBulkFixture, time.Time) {
	t.Helper()
	ctx := context.Background()
	a, config, source, wants, f := fabricDevicesBulkPreparationFixture(t, "prepare")
	if zone {
		c, zoned, native, api := newManagedFabricDevicesBulkFixture(t, "zone")
		if err := configapi.AddToScheme(api.Scheme()); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var document map[string]any
		if json.Unmarshal(source.raw, &document) != nil {
			t.Fatal("source")
		}
		for _, row := range document["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["devices"].([]any) {
			row.(map[string]any)["fabric_zone"] = "Global/Lab/Building"
		}
		raw, _ := json.Marshal(document)
		config.Spec.Source.Inline.Raw = raw
		config.ResourceVersion = ""
		nc.ResourceVersion = ""
		if err := api.Create(ctx, config); err != nil {
			t.Fatal(err)
		}
		if err := api.Create(ctx, nc); err != nil {
			t.Fatal(err)
		}
		a.statusReader, a.statusWriter, a.client = api, api, c
		wants, f, source = zoned, native, &configSourceSnapshot{raw: raw}
	}
	if _, err := a.prepareFabricDevicesBulk(ctx, config, source, wants); err != nil {
		t.Fatal(err)
	}
	footprint := []configurationDispatchFixtureMember{}
	for _, name := range []string{"edge0", "edge1", "peer"} {
		var d cvk.CiscoDevice
		var n core.Node
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: name}, &d); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Name: name}, &n); err != nil {
			t.Fatal(err)
		}
		footprint = append(footprint, configurationDispatchFixtureMember{device: &d, node: &n})
	}
	permit, _ := configurationGroupDispatchPermitFixture(t, a, footprint)
	if _, err := a.dispatchFabricDevicesBulk(ctx, config, source, wants, permit); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.taskEnd = nc.Status.Configuration.Operation.SubmittedAt.Time
	members, err := contract.ReadAcknowledgedGroupReservations(ctx, a.statusReader, nc, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.beginFabricDevicesBulkVerification(ctx, config, source, members, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
		t.Fatal(err)
	}
	return a, config, source, journalState(t, a).Operation.DeepCopy(), members, f, f.taskEnd.Add(5 * time.Second)
}

func TestFabricDevicesBulkDeviceEvidence(t *testing.T) {
	for _, mode := range []string{"observed", "zone", "pending", "missing-peer-query", "extra-query", "identity-only", "bad-query", "duplicate-query", "query-change", "wrong-worker", "wrong-owner", "wrong-serial", "wrong-command", "redacted", "truncated", "artifact", "stale-result", "before-task", "old-generation", "unbound", "failed", "missing-member", "revoked", "source-race", "journal-race", "native-race", "managed-peer-race", "completion-race", "native-id-race", "query-callback-race"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, op, members, f, now := fabricBulkEvidenceFixture(t, mode == "zone")
			queries := map[string][]string{}
			for _, target := range op.PhysicalGroup.Targets {
				queries[target.InventoryID] = []string{"show version", "show lisp session all"}
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			collect := func() (map[string]fabricDevicesBulkEvidence, error) {
				return a.collectFabricDevicesBulkEvidence(ctx, config, source, op, members, authorize, queries, now)
			}
			for range 2 {
				if got, err := collect(); !errors.Is(err, errFabricDevicesBulkEvidencePending) || got != nil {
					t.Fatal("unbound result accepted", err)
				}
			}
			var leaves ops.DeviceOperationList
			if err := a.statusReader.List(ctx, &leaves); err != nil || len(leaves.Items) != 3 {
				t.Fatal("complete incoming/peer/parent footprint not requested exactly once", err)
			}
			for i := range leaves.Items {
				leaf := &leaves.Items[i]
				if managedprotocol.NetworkObjectBindingComplete(leaf.Annotations) {
					t.Fatal("connector stamped protected worker binding")
				}
				var member *ops.ControllerConfigurationReservation
				for _, m := range members {
					if m.Spec.Device.Name == leaf.Spec.DeviceRef.Name {
						member = m
					}
				}
				if member == nil {
					t.Fatal("read escaped group")
				}
				stampLANProofEnvelope(leaf, member, now.Add(-2*time.Second), now.Add(-time.Second), now)
				leaf.Status.Outputs = []ops.DeviceOperationOutput{{Command: leaf.Spec.Operation.Commands[0], Output: "Processor board ID " + member.Spec.PhysicalIdentity + "\n"}, {Command: leaf.Spec.Operation.Commands[1], Output: "PRIVATE_CLI: nonempty output is not fabric convergence"}}
				if i == 0 {
					switch mode {
					case "pending":
						leaf.Status.Phase = ops.OperationPhasePending
					case "failed":
						leaf.Status.Phase = ops.OperationPhaseFailed
					case "old-generation":
						leaf.Status.ObservedGeneration = 0
					case "unbound":
						delete(leaf.Annotations, managedprotocol.AnnotationManaged)
					case "wrong-worker":
						leaf.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] = "foreign"
					case "wrong-owner":
						leaf.OwnerReferences[0].UID = "foreign"
					case "wrong-serial":
						leaf.Status.Outputs[0].Output = "Processor board ID FOREIGN\n"
					case "wrong-command":
						leaf.Status.Outputs[1].Command = "show clock"
					case "redacted":
						leaf.Status.Outputs[1].Redacted = true
					case "truncated":
						leaf.Status.Outputs[1].Truncated = true
					case "artifact":
						leaf.Status.ArtifactURIs = []string{"file:///private"}
					case "stale-result":
						now = now.Add(10 * time.Minute)
					case "before-task":
						leaf.CreationTimestamp = metav1.NewTime(f.taskEnd)
					}
				}
				if err := a.statusWriter.Update(ctx, leaf); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "missing-peer-query":
				delete(queries, "peer")
			case "extra-query":
				queries["foreign"] = []string{"show version", "show lisp session all"}
			case "identity-only":
				queries["edge-0"] = []string{"show version"}
			case "bad-query":
				queries["edge-0"] = []string{"show version", "configure terminal"}
			case "duplicate-query":
				queries["edge-0"] = []string{"show version", "show version"}
			case "query-change":
				queries["edge-0"] = []string{"show version", "show clock"}
			case "missing-member":
				members = members[:2]
			case "revoked":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error { return errors.New("revoked") }
			case "source-race":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error {
					changed := config.DeepCopy()
					changed.Generation++
					return a.statusWriter.Update(ctx, changed)
				}
			case "journal-race":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error {
					nc, err := a.currentController(ctx)
					if err != nil {
						return err
					}
					nc.Status.Configuration.Operation.TaskID = "foreign-task"
					return a.statusWriter.Status().Update(ctx, nc)
				}
			case "native-race":
				delete(f.created, "edge-0")
			case "managed-peer-race":
				var d cvk.CiscoDevice
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: "peer"}, &d); err != nil {
					t.Fatal(err)
				}
				d.Spec.Address = "198.51.100.199"
				if err := a.statusWriter.Update(ctx, &d); err != nil {
					t.Fatal(err)
				}
			case "completion-race":
				f.taskEnd = f.taskEnd.Add(time.Second)
			case "native-id-race":
				row := f.created["edge-0"]
				row.ID = "replaced-membership"
				f.created["edge-0"] = row
			case "query-callback-race":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error {
					queries["edge-0"][1] = "show clock"
					return nil
				}
			}
			result, err := collect()
			valid := mode == "observed" || mode == "zone" || mode == "query-callback-race"
			if (err == nil) != valid {
				t.Fatal("unqualified fabric evidence escaped or valid evidence refused", err)
			}
			if valid {
				if len(result) != 3 {
					t.Fatal("peer/parent evidence missing")
				}
				for _, format := range []string{"%v", "%+v", "%#v"} {
					if strings.Contains(fmt.Sprintf(format, result), "PRIVATE_CLI") {
						t.Fatal("formatter leaked evidence")
					}
				}
				if err := a.recordFabricDevicesBulkApplication(ctx, config, op.ID, members, authorize, nil, now); err == nil {
					t.Fatal("read output replaced semantic proof")
				}
				if mode != "query-callback-race" {
					restarted := *a
					again, err := restarted.collectFabricDevicesBulkEvidence(ctx, config, source, op, members, authorize, queries, now)
					if err != nil || !reflect.DeepEqual(result, again) {
						t.Fatal("restart lost evidence", err)
					}
				}
			}
			state := journalState(t, a)
			if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 || f.posts != 1 {
				t.Fatal("evidence collection changed mutation or completion state")
			}
			for _, m := range members {
				if m.Status.Grant == nil || m.Status.Grant.Phase != "Held" || m.Status.Completion != nil {
					t.Fatal("evidence collection released reservation")
				}
			}
		})
	}
}

func TestFabricDevicesBulkEvidencePostTaskBoundary(t *testing.T) {
	ctx := context.Background()
	a, config, source, op, members, f, _ := fabricBulkEvidenceFixture(t, false)
	queries := map[string][]string{}
	for _, m := range members {
		queries[m.Spec.InventoryID] = []string{"show version", "show lisp session all"}
	}
	for _, now := range []time.Time{f.taskEnd.Add(-time.Second), f.taskEnd, f.taskEnd.Add(500 * time.Millisecond)} {
		if result, err := a.collectFabricDevicesBulkEvidence(ctx, config, source, op, members, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, queries, now); err == nil || result != nil {
			t.Fatal("pre-completion evidence accepted")
		}
	}
	var leaves ops.DeviceOperationList
	if err := a.statusReader.List(ctx, &leaves); err != nil || len(leaves.Items) != 0 {
		t.Fatal("post-task gate created premature reads", err)
	}
}

func TestFabricDevicesBulkPublicDeviceEvidence(t *testing.T) {
	ctx := context.Background()
	a, config, source, op, members, f, _ := fabricBulkEvidenceFixture(t, false)
	// Model an appliance clock behind the worker within the existing task skew.
	// The read request still begins after the exact observed completion boundary.
	f.taskEnd = op.SubmittedAt.Add(-3 * time.Second)
	r := &configReconciler{a: a}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := r.recoverAcknowledgedFabricGroup(ctx, nc, config, source)
	if !handled || !errors.Is(err, errFabricDevicesBulkEvidencePending) {
		t.Fatal("public recovery did not create read-only requests", err)
	}
	var leaves ops.DeviceOperationList
	if err := a.statusReader.List(ctx, &leaves); err != nil || len(leaves.Items) != 3 {
		t.Fatal("public recovery missed complete footprint", err)
	}
	now := time.Now().UTC()
	for i := range leaves.Items {
		leaf := &leaves.Items[i]
		var member *ops.ControllerConfigurationReservation
		for _, m := range members {
			if m.Spec.Device.Name == leaf.Spec.DeviceRef.Name {
				member = m
			}
		}
		if member == nil {
			t.Fatal("foreign read")
		}
		stampLANProofEnvelope(leaf, member, now.Add(-time.Second), now, now)
		leaf.Status.Outputs = []ops.DeviceOperationOutput{{Command: "show version", Output: "Processor board ID " + member.Spec.PhysicalIdentity + "\n"}, {Command: "show lisp session all", Output: "PRIVATE_CLI: not semantic convergence"}}
		if err := a.statusWriter.Update(ctx, leaf); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		restarted := *a
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		handled, err := (&configReconciler{a: &restarted}).recoverAcknowledgedFabricGroup(ctx, nc, config, source)
		if !handled || !errors.Is(err, errFabricDevicesConvergenceUnavailable) {
			t.Fatal("nonempty CLI minted convergence or lost restart binding", err)
		}
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var status configapi.NetworkControllerConfigStatus
	_, valid, reason, _ := r.evaluateConfigurationApplySource(ctx, nc, config, &status, source)
	if !valid || reason != "GroupConvergenceVerificationUnavailable" || status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
		t.Fatal("public status hid semantic verifier gap", reason)
	}
	if f.posts != 1 || len(journalState(t, a).ActionApplications) != 0 || journalState(t, a).Operation.Phase != "Verifying" {
		t.Fatal("observational public recovery replayed or completed mutation")
	}
}
