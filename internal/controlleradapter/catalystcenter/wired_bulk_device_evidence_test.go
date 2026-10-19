// Copyright © 2026 Cisco Systems Inc.
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
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Local HTTPS and explicitly simulated manager/device workers. No installed
// actor, rendered Catalyst Center configuration or live switch proof is claimed.
func wiredBulkEvidenceFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig, *configSourceSnapshot, wiredBulkBatch, *cvk.NetworkControllerConfigurationOperation, []*ops.ControllerConfigurationReservation, *wiredBulkFixture, time.Time) {
	t.Helper()
	ctx := context.Background()
	a, config, source, fixtureMembers, f := wiredBulkJournalFixture(t, false, true)
	permit, _ := configurationGroupDispatchPermitFixture(t, a, fixtureMembers)
	if _, err := a.dispatchWiredBulkProvision(ctx, config, source, permit); err != nil {
		t.Fatal(err)
	}
	var list ops.ControllerConfigurationReservationList
	if err := a.statusReader.List(ctx, &list); err != nil {
		t.Fatal(err)
	}
	members := []*ops.ControllerConfigurationReservation{}
	for i := range list.Items {
		members = append(members, &list.Items[i])
	}
	if err := a.beginWiredBulkVerification(ctx, config, source, members, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, time.Now()); err != nil {
		t.Fatal(err)
	}
	op := journalState(t, a).Operation.DeepCopy()
	batch, err := a.retainedWiredBulkBatch(ctx, source, op)
	if err != nil {
		t.Fatal(err)
	}
	return a, config, source, batch, op, members, f, f.taskEnd.Add(5 * time.Second)
}

func TestWiredBulkDeviceWorkerEvidence(t *testing.T) {
	for _, mode := range []string{"observed", "pending", "missing-member", "missing-query", "extra-query", "identity-only", "bad-query", "failed", "old-generation", "unbound", "wrong-worker", "wrong-owner", "wrong-command", "wrong-serial", "truncated", "redacted", "artifact", "before-task", "future-result", "stale-result", "duplicate-ready", "source-race", "native-race", "completion-race", "authority-revoked", "journal-race", "query-change", "foreign-section-owner"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, source, batch, op, members, f, now := wiredBulkEvidenceFixture(t)
			queries := map[string][]string{"a": {"show version", "show ntp associations"}, "b": {"show version", "show ntp associations"}}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }
			collect := func() (map[string]wiredBulkDeviceEvidence, error) {
				return a.collectWiredBulkDeviceEvidence(ctx, config, source, batch, op, members, authorize, queries, f.taskEnd, now)
			}
			// Stable pending reads are retained across connector restarts, never
			// recreated on every reconcile or trusted without manager bindings.
			for range 2 {
				if result, err := collect(); !errors.Is(err, errWiredBulkDeviceEvidencePending) || result != nil {
					t.Fatal("missing worker results accepted", err)
				}
			}
			var leaves ops.DeviceOperationList
			if err := a.statusReader.List(ctx, &leaves); err != nil || len(leaves.Items) != 2 {
				t.Fatal("worker request missing/duplicated", err)
			}
			for i := range leaves.Items {
				leaf := &leaves.Items[i]
				if managedprotocol.NetworkObjectBindingComplete(leaf.Annotations) {
					t.Fatal("connector stamped protected worker identity")
				}
				var member *ops.ControllerConfigurationReservation
				for _, candidate := range members {
					if candidate.Spec.Device.Name == leaf.Spec.DeviceRef.Name {
						member = candidate
					}
				}
				if member == nil {
					t.Fatal("read escaped group")
				}
				stampLANProofEnvelope(leaf, member, now.Add(-2*time.Second), now.Add(-time.Second), now)
				leaf.Status.Outputs = []ops.DeviceOperationOutput{{Command: leaf.Spec.Operation.Commands[0], Output: "Processor board ID " + member.Spec.PhysicalIdentity + "\n"}, {Command: leaf.Spec.Operation.Commands[1], Output: "nonempty CLI is deliberately not semantic configuration proof"}}
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
						leaf.Annotations[managedprotocol.AnnotationNetworkWorkerPodUID] = "other-worker"
					case "wrong-owner":
						leaf.OwnerReferences[0].UID = "other-controller"
					case "wrong-command":
						leaf.Status.Outputs[1].Command = "show clock"
					case "wrong-serial":
						leaf.Status.Outputs[0].Output = "Processor board ID FOREIGN\n"
					case "truncated":
						leaf.Status.Outputs[1].Truncated = true
					case "redacted":
						leaf.Status.Outputs[1].Redacted = true
					case "artifact":
						leaf.Status.ArtifactURIs = []string{"file:///untrusted"}
					case "before-task":
						leaf.CreationTimestamp = metav1.NewTime(f.taskEnd)
					case "future-result":
						leaf.Status.CompletionTime = &metav1.Time{Time: now.Add(time.Second)}
					case "stale-result":
						now = now.Add(10 * time.Minute)
					case "duplicate-ready":
						leaf.Status.Conditions = append(leaf.Status.Conditions, leaf.Status.Conditions[0])
					}
				}
				if err := a.statusWriter.Update(ctx, leaf); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "missing-member":
				members = members[:1]
			case "missing-query":
				delete(queries, "a")
			case "extra-query":
				queries["foreign"] = queries["a"]
			case "identity-only":
				queries["a"] = []string{"show version"}
			case "bad-query":
				queries["a"] = []string{"show version", "configure terminal"}
			case "query-change":
				queries["a"] = []string{"show version", "show clock"}
			case "authority-revoked":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error { return errors.New("revoked") }
			case "source-race":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error {
					changed := config.DeepCopy()
					changed.Labels = map[string]string{"changed": "true"}
					return a.statusWriter.Update(ctx, changed)
				}
			case "journal-race":
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error {
					nc, err := a.currentController(ctx)
					if err != nil {
						return err
					}
					nc.Status.Configuration.Operation.TaskID = "replacement"
					return a.statusWriter.Status().Update(ctx, nc)
				}
			case "native-race":
				calls := 0
				authorize = func(context.Context, []*ops.ControllerConfigurationReservation) error {
					calls++
					if calls > 1 {
						f.serialB = "FOREIGN"
					}
					return nil
				}
			case "completion-race":
				f.taskEnd = f.taskEnd.Add(time.Second)
			case "foreign-section-owner":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			// Keep the original completion boundary even if the native task changes.
			originalCompletion := f.taskEnd
			if mode == "completion-race" {
				originalCompletion = originalCompletion.Add(-time.Second)
			}
			result, err := a.collectWiredBulkDeviceEvidence(ctx, config, source, batch, op, members, authorize, queries, originalCompletion, now)
			if (err == nil) != (mode == "observed") {
				t.Fatal("unqualified worker evidence escaped", err)
			}
			if mode == "observed" {
				if len(result) != 2 || len(result["a"].outputs) != 2 {
					t.Fatal("incomplete evidence returned")
				}
				for _, format := range []string{"%v", "%+v", "%#v"} {
					if strings.Contains(fmt.Sprintf(format, result), "CLI") {
						t.Fatal("evidence formatter leaked output")
					}
				}
				restarted := *a
				again, err := restarted.collectWiredBulkDeviceEvidence(ctx, config, source, batch, op, members, authorize, queries, originalCompletion, now)
				if err != nil || !reflect.DeepEqual(again, result) {
					t.Fatal("restart lost evidence", err)
				}
				if err := a.recordWiredBulkApplication(ctx, config, op.ID, batch, members, authorize, nil, now); err == nil {
					t.Fatal("collected output replaced semantic proof")
				}
			}
			state := journalState(t, a)
			if state.Operation.Phase != "Verifying" || len(state.ActionApplications) != 0 || f.calls != 1 {
				t.Fatal("evidence collection published convergence or replayed provisioning")
			}
			for _, member := range members {
				if member.Status.Claim == nil || member.Status.Grant == nil || member.Status.Grant.Phase != "Held" || member.Status.Completion != nil {
					t.Fatal("evidence collection changed reservation lifecycle")
				}
			}
		})
	}
}

func TestWiredBulkDeviceEvidenceWaitsForPostTaskBoundary(t *testing.T) {
	ctx := context.Background()
	a, config, source, batch, op, members, f, _ := wiredBulkEvidenceFixture(t)
	queries := map[string][]string{"a": {"show version", "show ntp associations"}, "b": {"show version", "show ntp associations"}}
	for _, now := range []time.Time{f.taskEnd.Add(-time.Second), f.taskEnd, f.taskEnd.Add(500 * time.Millisecond)} {
		result, err := a.collectWiredBulkDeviceEvidence(ctx, config, source, batch, op, members, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, queries, f.taskEnd, now)
		if !errors.Is(err, errWiredBulkDeviceEvidencePending) || result != nil {
			t.Fatal("early observation did not remain pending", err)
		}
	}
	var leaves ops.DeviceOperationList
	if err := a.statusReader.List(ctx, &leaves); err != nil || len(leaves.Items) != 0 {
		t.Fatal("worker read created without a post-task timestamp boundary", err)
	}
}
