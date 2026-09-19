// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package maintenance

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
)

func TestVerifyAndPublishDrainInventoryIsCompleteDeterministicAndMonotonic(t *testing.T) {
	c, objects := drainInventoryCoordinatorFixture(t, func(o *drainFixtureObjects) {
		for _, suffix := range []string{"a", "b"} {
			other := o.leaf.Status.ManagerDrain.Pods[0]
			other.Name = "other-" + suffix
			other.UID = "other-uid-" + suffix
			other.EligibilityHash = ""
			hash, err := workloaddrain.EligibilityHash(&other)
			if err != nil {
				t.Fatal(err)
			}
			other.EligibilityHash = hash
			o.leaf.Status.ManagerDrain.Pods = append(o.leaf.Status.ManagerDrain.Pods, other)
		}
	})
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		if !finished {
			finish(errors.New("test cleanup"))
		}
	})

	managerBefore := getDrainLeaf(t, c, objects.leaf).Status.ManagerDrain
	first := []*corev1.Pod{
		deviceInventoryPod("workloads", "other-b", "other-uid-b"),
		deviceInventoryPod("workloads", "other-a", "other-uid-a"),
	}
	if err := c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		return first, nil
	}); err != nil {
		t.Fatalf("publish complete inventory: %v", err)
	}
	current := getDrainLeaf(t, c, objects.leaf)
	firstStatus := current.Status.WorkerDrain
	if firstStatus == nil || !firstStatus.InventoryComplete || firstStatus.InventoryRevision != 1 ||
		firstStatus.ForeignDeviceWorkloadCount != 0 || firstStatus.UnknownDeviceWorkloadCount != 0 ||
		!reflect.DeepEqual(firstStatus.RemainingAuthorizedPodUIDs, []string{"other-uid-a", "other-uid-b"}) {
		t.Fatalf("unexpected first inventory status: %#v", firstStatus)
	}
	if firstStatus.ProtocolVersion != opsv1alpha1.ManagedDrainProtocolPDBV1 ||
		firstStatus.ObservedSessionToken != drainSessionToken || firstStatus.ObservedPolicyEpoch != 2 ||
		firstStatus.ObservedControlRevision != 7 || firstStatus.ObservedWorkerConfigRevision != drainWorkerHash {
		t.Fatalf("inventory status is not bound to the exact drain authority: %#v", firstStatus)
	}
	if !apiequality.Semantic.DeepEqual(current.Status.ManagerDrain, managerBefore) {
		t.Fatalf("worker inventory publication changed manager-owned drain status: before=%#v after=%#v", managerBefore, current.Status.ManagerDrain)
	}

	second := slices.Clone(first)
	slices.Reverse(second)
	if err := c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		return second, nil
	}); err != nil {
		t.Fatalf("republish complete inventory: %v", err)
	}
	current = getDrainLeaf(t, c, objects.leaf)
	secondStatus := current.Status.WorkerDrain
	if secondStatus.InventoryRevision != 2 || secondStatus.InventoryObservedAt.Before(&firstStatus.InventoryObservedAt) {
		t.Fatalf("inventory publication was not monotonic and order independent: first=%#v second=%#v", firstStatus, secondStatus)
	}
	finish(nil)
	finished = true
}

func TestVerifyAndPublishDrainInventoryUsesExactUIDAcrossIOSXESyntheticNames(t *testing.T) {
	const remainingUID = "22222222-2222-4222-8222-222222222222"
	c, objects := drainInventoryCoordinatorFixture(t, func(o *drainFixtureObjects) {
		other := o.leaf.Status.ManagerDrain.Pods[0]
		other.Name = "other"
		other.UID = remainingUID
		other.EligibilityHash = ""
		hash, err := workloaddrain.EligibilityHash(&other)
		if err != nil {
			t.Fatal(err)
		}
		other.EligibilityHash = hash
		o.leaf.Status.ManagerDrain.Pods = append(o.leaf.Status.ManagerDrain.Pods, other)
	})
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}

	// IOS XE derives the authoritative UID from the app name. Namespace/name
	// are stable placeholders because RunOpts labels can disappear in some app
	// lifecycle states; they must not turn another selected UID into ambiguity.
	err = c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		return []*corev1.Pod{deviceInventoryPod(
			"default", "cvk-22222222222242228222222222222222", remainingUID,
		)}, nil
	})
	if err != nil {
		finish(err)
		t.Fatalf("publish UID-authoritative IOS XE inventory: %v", err)
	}
	if err := finish(nil); err != nil {
		t.Fatalf("release after UID-authoritative IOS XE inventory: %v", err)
	}

	status := getDrainLeaf(t, c, objects.leaf).Status.WorkerDrain
	if status == nil || !status.InventoryComplete || status.UnknownDeviceWorkloadCount != 0 ||
		status.ForeignDeviceWorkloadCount != 0 ||
		!reflect.DeepEqual(status.RemainingAuthorizedPodUIDs, []string{remainingUID}) {
		t.Fatalf("synthetic IOS XE identity was not classified by exact UID: %#v", status)
	}
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity != nil {
		t.Fatalf("proven first serial deletion retained the mutation Lease: %#v", lease.Spec)
	}
}

func TestVerifyAndPublishDrainInventoryRefreshesProofAfterWorkerRotation(t *testing.T) {
	const rotatedWorkerRevision = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	const rotatedWorkerPodUID = "replacement-worker-pod-uid"
	tests := map[string]bool{
		"active":     false,
		"recovering": true,
	}
	for name, recovering := range tests {
		t.Run(name, func(t *testing.T) {
			previousInventoryAt := metav1.NewTime(time.Now().UTC().Add(time.Minute))
			previousUpdatedAt := metav1.NewTime(previousInventoryAt.Add(time.Second))
			c, objects := drainInventoryCoordinatorFixture(t, func(o *drainFixtureObjects) {
				if recovering {
					drain := o.leaf.Status.ManagerDrain
					duration := drain.DrainDeadline.Sub(drain.StartedAt.Time)
					setDrainFixtureRecovering(
						o,
						metav1.NewTime(drain.UpdatedAt.Add(duration+drainRecoveryExtraLimit)),
					)
				}
				o.leaf.Status.WorkerDrain = &opsv1alpha1.UpgradeWorkerDrainStatus{
					ProtocolVersion:              o.leaf.Status.ManagerDrain.ProtocolVersion,
					ObservedSessionToken:         o.leaf.Status.ManagerDrain.SessionToken,
					ObservedPolicyEpoch:          o.leaf.Status.ManagerDrain.PolicyEpoch,
					ObservedControlRevision:      o.leaf.Status.ManagerDrain.ControlRevision,
					ObservedWorkerConfigRevision: drainWorkerHash,
					InventoryRevision:            4,
					InventoryObservedAt:          previousInventoryAt,
					InventoryComplete:            true,
					RemainingAuthorizedPodUIDs:   []string{string(o.pod.UID)},
					UpdatedAt:                    previousUpdatedAt,
				}
				o.leaf.Status.WorkerControl.ObservedWorkerConfigRevision = rotatedWorkerRevision
				o.node.Annotations[managedprotocol.AnnotationWorkerObservedRevision] = rotatedWorkerRevision
				o.device.Status.WorkerRevision.DesiredRevision = rotatedWorkerRevision
				o.device.Status.WorkerRevision.ObservedRevision = rotatedWorkerRevision
				o.device.Status.WorkerRevision.DeploymentGeneration++
				o.device.Status.WorkerRevision.PodUID = rotatedWorkerPodUID
				rotatedPodStart := metav1.NewTime(time.Now().UTC().Add(-time.Minute))
				rotatedHeartbeat := metav1.Now()
				o.device.Status.WorkerRevision.PodStartTime = &rotatedPodStart
				o.device.Status.WorkerRevision.ReadyHeartbeatTime = &rotatedHeartbeat
			})
			c.WorkerRevision = rotatedWorkerRevision
			c.WorkerPodUID = rotatedWorkerPodUID

			deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
			if err != nil {
				t.Fatal(err)
			}
			if err := c.VerifyAndPublishDrainInventory(
				deleteCtx,
				objects.pod.DeepCopy(),
				func(context.Context) ([]*corev1.Pod, error) { return nil, nil },
			); err != nil {
				finish(err)
				t.Fatalf("publish inventory after worker rotation: %v", err)
			}
			if err := finish(nil); err != nil {
				t.Fatalf("finish inventory after worker rotation: %v", err)
			}

			status := getDrainLeaf(t, c, objects.leaf).Status.WorkerDrain
			if status == nil || status.ObservedWorkerConfigRevision != rotatedWorkerRevision ||
				status.InventoryRevision != 5 || !status.InventoryComplete ||
				len(status.RemainingAuthorizedPodUIDs) != 0 {
				t.Fatalf("worker rotation did not replace stale inventory proof: %#v", status)
			}
			if !status.InventoryObservedAt.After(previousUpdatedAt.Time) ||
				!status.UpdatedAt.After(previousUpdatedAt.Time) {
				t.Fatalf("worker rotation did not advance both inventory clocks: %#v", status)
			}
		})
	}
}

func TestVerifyAndPublishDrainInventoryRetainsQuarantineForForeignWorkload(t *testing.T) {
	c, objects := drainInventoryCoordinatorFixture(t, nil)
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	err = c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		return []*corev1.Pod{deviceInventoryPod("workloads", "foreign", "foreign-uid")}, nil
	})
	if !errors.Is(err, ErrDrainInventoryUnproven) {
		finish(err)
		t.Fatalf("foreign workload error=%v, want ErrDrainInventoryUnproven", err)
	}
	finish(err)
	current := getDrainLeaf(t, c, objects.leaf)
	status := current.Status.WorkerDrain
	if status == nil || !status.InventoryComplete || status.ForeignDeviceWorkloadCount != 1 ||
		status.UnknownDeviceWorkloadCount != 0 || status.Reason != "ForeignWorkloadsPresent" {
		t.Fatalf("foreign workload classification was not published: %#v", status)
	}
	assertDrainLeaseRetained(t, c, objects)
}

func TestVerifyAndPublishDrainInventoryAllowsAttributableReplacementDuringRecovery(t *testing.T) {
	c, objects := drainInventoryCoordinatorFixture(t, func(o *drainFixtureObjects) {
		drain := o.leaf.Status.ManagerDrain
		duration := drain.DrainDeadline.Sub(drain.StartedAt.Time)
		setDrainFixtureRecovering(o, metav1.NewTime(drain.UpdatedAt.Add(duration+drainRecoveryExtraLimit)))
	})
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	err = c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		return []*corev1.Pod{deviceInventoryPod("workloads", "replacement", "replacement-uid")}, nil
	})
	if err != nil {
		finish(err)
		t.Fatalf("complete recovery inventory with an attributable replacement was rejected: %v", err)
	}
	finish(nil)
	status := getDrainLeaf(t, c, objects.leaf).Status.WorkerDrain
	if status == nil || !status.InventoryComplete || status.ForeignDeviceWorkloadCount != 1 ||
		status.UnknownDeviceWorkloadCount != 0 || status.Reason != "RecoveryInventoryComplete" {
		t.Fatalf("recovery inventory classification was not published: %#v", status)
	}
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.HolderIdentity != nil {
		t.Fatalf("proven recovery cleanup retained its mutation Lease: %#v", lease.Spec)
	}
}

func TestVerifyAndPublishDrainInventoryDoesNotTreatDeleteSuccessAsDeviceClean(t *testing.T) {
	c, objects := drainInventoryCoordinatorFixture(t, nil)
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	err = c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		return []*corev1.Pod{deviceInventoryPod(objects.pod.Namespace, objects.pod.Name, string(objects.pod.UID))}, nil
	})
	if !errors.Is(err, ErrDrainInventoryUnproven) {
		finish(err)
		t.Fatalf("selected UID still present error=%v, want ErrDrainInventoryUnproven", err)
	}
	finish(err)

	current := getDrainLeaf(t, c, objects.leaf)
	status := current.Status.WorkerDrain
	if status == nil || !status.InventoryComplete ||
		!reflect.DeepEqual(status.RemainingAuthorizedPodUIDs, []string{string(objects.pod.UID)}) {
		t.Fatalf("remaining selected UID was not published: %#v", status)
	}
	managerPod := current.Status.ManagerDrain.Pods[0]
	if managerPod.Phase != opsv1alpha1.UpgradeDrainPodTerminationObserved ||
		managerPod.DeviceCleanAt != nil || managerPod.DeviceCleanInventoryRevision != 0 {
		t.Fatalf("worker inferred manager-owned DeviceClean from DeletePod success: %#v", managerPod)
	}
	assertDrainLeaseRetained(t, c, objects)
}

func TestDeviceCleanFenceAllowsCrossedCallbackEvidenceAndExactRetainedRetry(t *testing.T) {
	observedAt := metav1.Now()
	c, objects := drainInventoryCoordinatorFixture(t, func(o *drainFixtureObjects) {
		o.leaf.Status.WorkerDrain = &opsv1alpha1.UpgradeWorkerDrainStatus{
			ProtocolVersion:              opsv1alpha1.ManagedDrainProtocolPDBV1,
			ObservedSessionToken:         drainSessionToken,
			ObservedPolicyEpoch:          o.leaf.Status.ManagerDrain.PolicyEpoch,
			ObservedControlRevision:      o.leaf.Status.ManagerDrain.ControlRevision,
			ObservedWorkerConfigRevision: drainWorkerHash,
			InventoryRevision:            1,
			InventoryObservedAt:          observedAt,
			InventoryComplete:            true,
			UpdatedAt:                    observedAt,
		}
	})

	// This callback crossed the manager's final-proof boundary only after it
	// passed the fresh pre-dispatch authorization in TerminationObserved.
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	current := getDrainLeaf(t, c, objects.leaf)
	managerPod := &current.Status.ManagerDrain.Pods[0]
	managerPod.Phase = opsv1alpha1.UpgradeDrainPodDeviceClean
	managerPod.DeviceCleanAt = &observedAt
	managerPod.DeviceCleanInventoryRevision = 1
	if err := c.Client.Status().Update(context.Background(), current); err != nil {
		t.Fatal(err)
	}

	// A later scan supersedes the old clean proof. It must still publish while
	// DeviceClean is fenced, then retain the exact Lease on the unproven result.
	err = c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		return []*corev1.Pod{deviceInventoryPod(
			objects.pod.Namespace, objects.pod.Name, string(objects.pod.UID),
		)}, nil
	})
	if !errors.Is(err, ErrDrainInventoryUnproven) {
		finish(err)
		t.Fatalf("crossed DeviceClean callback error = %v, want ErrDrainInventoryUnproven", err)
	}
	if err := finish(err); err != nil {
		t.Fatal(err)
	}
	assertDrainLeaseRetained(t, c, objects)
	current = getDrainLeaf(t, c, objects.leaf)
	if current.Status.WorkerDrain == nil || current.Status.WorkerDrain.InventoryRevision != 2 ||
		!reflect.DeepEqual(current.Status.WorkerDrain.RemainingAuthorizedPodUIDs, []string{string(objects.pod.UID)}) {
		t.Fatalf("crossed callback did not publish superseding unclean evidence: %#v", current.Status.WorkerDrain)
	}

	// DeviceClean permits only this exact retained-holder repair. The retry is
	// the same UID-bound idempotent teardown and a fresh complete scan restores
	// proof before releasing the quarantine.
	retryCtx, retryFinish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatalf("exact retained DeviceClean retry was rejected: %v", err)
	}
	if err := c.VerifyAndPublishDrainInventory(retryCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		return nil, nil
	}); err != nil {
		retryFinish(err)
		t.Fatalf("exact retained DeviceClean retry inventory: %v", err)
	}
	if err := retryFinish(nil); err != nil {
		t.Fatalf("release exact retained DeviceClean retry: %v", err)
	}
	current = getDrainLeaf(t, c, objects.leaf)
	if current.Status.WorkerDrain == nil || current.Status.WorkerDrain.InventoryRevision != 3 ||
		!current.Status.WorkerDrain.InventoryComplete || len(current.Status.WorkerDrain.RemainingAuthorizedPodUIDs) != 0 {
		t.Fatalf("DeviceClean retry did not restore current clean evidence: %#v", current.Status.WorkerDrain)
	}

	// Once the exact Lease is idle, DeviceClean is closed to every new callback.
	if _, unexpectedFinish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy()); err == nil {
		unexpectedFinish(nil)
		t.Fatal("idle DeviceClean fence reopened device deletion")
	}
}

func TestVerifyAndPublishDrainInventoryWaitsForManagerTerminationEvidence(t *testing.T) {
	c, objects := drainCoordinatorFixture(t, nil)
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	scanned := false
	err = c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		scanned = true
		return nil, nil
	})
	if !errors.Is(err, ErrDrainInventoryUnproven) {
		finish(err)
		t.Fatalf("pre-termination inventory error=%v, want ErrDrainInventoryUnproven", err)
	}
	finish(err)
	if scanned {
		t.Fatal("device inventory was scanned before manager termination evidence was durable")
	}
	current := getDrainLeaf(t, c, objects.leaf)
	if current.Status.WorkerDrain != nil {
		t.Fatalf("pre-termination callback published device-clean evidence: %#v", current.Status.WorkerDrain)
	}
	assertDrainLeaseRetained(t, c, objects)
}

func TestVerifyAndPublishDrainInventoryFailsClosedOnIncompleteAndAmbiguousScans(t *testing.T) {
	tests := map[string]struct {
		pods        []*corev1.Pod
		scanErr     error
		wantUnknown int32
	}{
		"scan error": {
			scanErr: errors.New("device response truncated"),
		},
		"nil record": {
			pods:        []*corev1.Pod{nil},
			wantUnknown: 1,
		},
		"missing UID": {
			pods:        []*corev1.Pod{deviceInventoryPod("workloads", "unknown", "")},
			wantUnknown: 1,
		},
		"duplicate UID": {
			pods: []*corev1.Pod{
				deviceInventoryPod("workloads", "one", "duplicate-uid"),
				deviceInventoryPod("workloads", "two", "duplicate-uid"),
			},
			wantUnknown: 2,
		},
		"selected UID with malformed identity": {
			pods:        []*corev1.Pod{deviceInventoryPod("", "wrong-name", "pod-uid")},
			wantUnknown: 1,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c, objects := drainInventoryCoordinatorFixture(t, nil)
			deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
			if err != nil {
				t.Fatal(err)
			}
			err = c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
				return tc.pods, tc.scanErr
			})
			if !errors.Is(err, ErrDrainInventoryUnproven) {
				finish(err)
				t.Fatalf("incomplete inventory error=%v, want ErrDrainInventoryUnproven", err)
			}
			finish(err)
			current := getDrainLeaf(t, c, objects.leaf)
			status := current.Status.WorkerDrain
			if status == nil || status.InventoryComplete || status.UnknownDeviceWorkloadCount != tc.wantUnknown ||
				status.InventoryRevision != 1 {
				t.Fatalf("incomplete inventory did not publish fail-closed evidence: %#v", status)
			}
			if name == "selected UID with malformed identity" &&
				!reflect.DeepEqual(status.RemainingAuthorizedPodUIDs, []string{"pod-uid"}) {
				t.Fatalf("malformed selected identity hid exact UID presence: %#v", status)
			}
			assertDrainLeaseRetained(t, c, objects)
		})
	}
}

func TestVerifyAndPublishDrainInventoryRejectsAuthorityChangeDuringScan(t *testing.T) {
	c, objects := drainInventoryCoordinatorFixture(t, nil)
	deleteCtx, finish, err := c.AcquireDrainDelete(context.Background(), objects.pod.DeepCopy())
	if err != nil {
		t.Fatal(err)
	}
	err = c.VerifyAndPublishDrainInventory(deleteCtx, objects.pod.DeepCopy(), func(context.Context) ([]*corev1.Pod, error) {
		current := getDrainLeaf(t, c, objects.leaf)
		current.Status.ManagerControl.Revision++
		if updateErr := c.Client.Status().Update(context.Background(), current); updateErr != nil {
			t.Fatalf("change manager authority during scan: %v", updateErr)
		}
		return []*corev1.Pod{}, nil
	})
	if err == nil {
		finish(errors.New("expected stale authority rejection"))
		t.Fatal("inventory observed under changed authority was published")
	}
	finish(err)
	current := getDrainLeaf(t, c, objects.leaf)
	if current.Status.WorkerDrain != nil {
		t.Fatalf("stale scan published worker evidence: %#v", current.Status.WorkerDrain)
	}
}

func TestInspectDrainInventoryClassifiesSelectedAndForeignPods(t *testing.T) {
	selected := []opsv1alpha1.UpgradeDrainPodStatus{
		{Namespace: "apps", Name: "a", UID: "uid-a"},
		{Namespace: "apps", Name: "b", UID: "uid-b"},
	}
	pods := []*corev1.Pod{
		deviceInventoryPod("apps", "b", "uid-b"),
		deviceInventoryPod("apps", "foreign", "uid-foreign"),
	}
	first := inspectDrainInventory(pods, selected, "uid-a", nil)
	if !first.complete || first.targetPresent || first.foreignCount != 1 || first.unknownCount != 0 ||
		!reflect.DeepEqual(first.remainingSelectedUIDs, []string{"uid-b"}) {
		t.Fatalf("unexpected classifications: %#v", first)
	}
}

func deviceInventoryPod(namespace, name, uid string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: namespace,
		Name:      name,
		UID:       types.UID(uid),
	}}
}

func drainInventoryCoordinatorFixture(
	t *testing.T,
	mutate func(*drainFixtureObjects),
) (*Coordinator, *drainFixtureObjects) {
	t.Helper()
	return drainCoordinatorFixture(t, func(objects *drainFixtureObjects) {
		observedAt := metav1.Now()
		objects.leaf.Status.ManagerDrain.Pods[0].Phase = opsv1alpha1.UpgradeDrainPodTerminationObserved
		objects.leaf.Status.ManagerDrain.Pods[0].DeletionObservedAt = &observedAt
		if mutate != nil {
			mutate(objects)
		}
	})
}

func getDrainLeaf(
	t *testing.T,
	c *Coordinator,
	want *opsv1alpha1.IOSXESoftwareUpgrade,
) *opsv1alpha1.IOSXESoftwareUpgrade {
	t.Helper()
	var current opsv1alpha1.IOSXESoftwareUpgrade
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(want), &current); err != nil {
		t.Fatal(err)
	}
	return &current
}

func assertDrainLeaseRetained(t *testing.T, c *Coordinator, objects *drainFixtureObjects) {
	t.Helper()
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), client.ObjectKeyFromObject(objects.lease), &lease); err != nil {
		t.Fatal(err)
	}
	wantHolder := devicecoordination.HolderIdentity(
		"software-drain", objects.leaf.Namespace, objects.leaf.Name, string(objects.leaf.UID),
	)
	if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != wantHolder {
		t.Fatalf("unproven device cleanup released drain quarantine: %#v", lease.Spec)
	}
}
