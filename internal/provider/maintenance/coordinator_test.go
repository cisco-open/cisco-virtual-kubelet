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
	"strings"
	"testing"
	"time"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testCoordinator(t *testing.T, objects ...client.Object) *Coordinator {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{coordv1.AddToScheme, corev1.AddToScheme, opsv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return &Coordinator{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(),
		Namespace: "edge", DeviceName: "switch", NodeName: "switch", LeaseNamespace: "leases", MutationsEnabled: true}
}

func (c *Coordinator) leaseKey() types.NamespacedName {
	return types.NamespacedName{Namespace: c.LeaseNamespace,
		Name: engine.LeaseName(devicecoordination.DeviceKey(c.Namespace, c.DeviceName), devicecoordination.MutationLeaseFamily)}
}

func (c *Coordinator) activeTaint() bool {
	var node corev1.Node
	if err := c.Client.Get(context.Background(), types.NamespacedName{Name: c.NodeName}, &node); err != nil {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if taint.Key == TaintKey && taint.Value == TaintValue && taint.Effect == corev1.TaintEffectNoSchedule {
			return true
		}
	}
	return false
}

func TestWriteLeaseExcludesConcurrentWritesAndDisruptions(t *testing.T) {
	c := testCoordinator(t)
	ctx := context.Background()
	writeCtx, finish, err := c.AcquireWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { finish(nil) })
	if _, _, err := c.AcquireWrite(ctx); err == nil {
		t.Fatal("concurrent routine write acquired the same Lease")
	}
	leaser := &engine.FamilyLeaser{Client: c.Client, Namespace: c.LeaseNamespace, TTL: time.Hour}
	key := devicecoordination.DeviceKey(c.Namespace, c.DeviceName)
	got, err := leaser.Acquire(ctx, key, devicecoordination.MutationLeaseFamily, "software-upgrade/run")
	if err != nil || got.Owned {
		t.Fatalf("disruption acquired active write Lease: %+v %v", got, err)
	}
	if writeCtx.Err() != nil {
		t.Fatalf("write was cancelled: %v", writeCtx.Err())
	}
	finish(nil)
	got, err = leaser.Acquire(ctx, key, devicecoordination.MutationLeaseFamily, "software-upgrade/run")
	if err != nil || !got.Owned {
		t.Fatalf("completed write did not release Lease: %+v %v", got, err)
	}
	if _, _, err := c.AcquireWrite(ctx); err == nil {
		t.Fatal("ordinary write bypassed disruptive Lease")
	}
}

type failedRenewalClient struct{ client.Client }

func (c failedRenewalClient) Update(ctx context.Context, object client.Object, opts ...client.UpdateOption) error {
	if _, ok := object.(*coordv1.Lease); ok {
		return errors.New("API renewal unavailable")
	}
	return c.Client.Update(ctx, object, opts...)
}

func TestRenewalFailureCancelsWriteAndRetainsLease(t *testing.T) {
	c := testCoordinator(t)
	c.Client = failedRenewalClient{c.Client}
	c.renewInterval = 5 * time.Millisecond
	ctx, finish, err := c.AcquireWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer finish(nil)
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("lost renewal did not cancel write")
	}
	finish(nil)
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), c.leaseKey(), &lease); err != nil {
		t.Fatalf("cancelled write lost its quarantine: %v", err)
	}
}

func TestCancelledWriteRetainsLease(t *testing.T) {
	c := testCoordinator(t)
	ctx, cancel := context.WithCancel(context.Background())
	_, finish, err := c.AcquireWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	finish(nil)
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), c.leaseKey(), &lease); err != nil {
		t.Fatal(err)
	}
}

func TestFailedWriteRetainsLease(t *testing.T) {
	c := testCoordinator(t)
	_, finish, err := c.AcquireWrite(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finish(errors.New("response lost after device accepted install"))
	var lease coordv1.Lease
	if err := c.Client.Get(context.Background(), c.leaseKey(), &lease); err != nil {
		t.Fatal(err)
	}
	if lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds < int32(maxWriteDuration/time.Second) {
		t.Fatal("error quarantine shorter than maximum app lifecycle")
	}
}

func TestDisabledMutationGateStillHonorsAndClearsQuarantine(t *testing.T) {
	c := testCoordinator(t, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch"}})
	ctx := context.Background()
	leaser := &engine.FamilyLeaser{Client: c.Client, Namespace: c.LeaseNamespace, TTL: 26 * time.Hour}
	key := devicecoordination.DeviceKey(c.Namespace, c.DeviceName)
	if _, err := leaser.Acquire(ctx, key, devicecoordination.MutationLeaseFamily, "software-upgrade/run"); err != nil {
		t.Fatal(err)
	}
	if err := c.BeforeMutation(ctx); err != nil {
		t.Fatal(err)
	}
	c.MutationsEnabled = false
	if _, _, err := c.AcquireWrite(ctx); err == nil {
		t.Fatal("disabled gate bypassed quarantine")
	}
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if !c.activeTaint() {
		t.Fatal("disabled gate cleared retained taint")
	}
	if err := leaser.Release(ctx, key, devicecoordination.MutationLeaseFamily, "software-upgrade/run"); err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	_, finish, err := c.AcquireWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	finish(nil)
	var lease coordv1.Lease
	if err := c.Client.Get(ctx, c.leaseKey(), &lease); !apierrors.IsNotFound(err) {
		t.Fatalf("idle disabled runtime acquired lease: %v", err)
	}
	if c.activeTaint() {
		t.Fatal("completed quarantine taint remained")
	}
}

func TestMaintenanceTaintTracksQuarantineAndPreservesOperatorState(t *testing.T) {
	operatorTaint := corev1.Taint{Key: "operator", Value: "keep", Effect: corev1.TaintEffectNoExecute}
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch"},
		Spec: corev1.NodeSpec{Unschedulable: true, Taints: []corev1.Taint{operatorTaint}}}
	c := testCoordinator(t, node)
	ctx := context.Background()
	leaser := &engine.FamilyLeaser{Client: c.Client, Namespace: c.LeaseNamespace, TTL: time.Hour}
	key := devicecoordination.DeviceKey(c.Namespace, c.DeviceName)
	if _, err := leaser.Acquire(ctx, key, devicecoordination.MutationLeaseFamily, "software-upgrade/run"); err != nil {
		t.Fatal(err)
	}
	if err := c.BeforeMutation(ctx); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{func() error { return c.Sync(ctx) }, func() error { return c.BeforeMutation(ctx) }} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
		if err := c.Client.Get(ctx, types.NamespacedName{Name: "switch"}, node); err != nil {
			t.Fatal(err)
		}
		if !node.Spec.Unschedulable || len(node.Spec.Taints) != 2 || node.Spec.Taints[0] != operatorTaint || !c.activeTaint() {
			t.Fatalf("active maintenance lost operator state or taint: %+v", node.Spec)
		}
	}
	if err := leaser.Release(ctx, key, devicecoordination.MutationLeaseFamily, "software-upgrade/run"); err != nil {
		t.Fatal(err)
	}
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Client.Get(ctx, types.NamespacedName{Name: "switch"}, node); err != nil {
		t.Fatal(err)
	}
	if !node.Spec.Unschedulable || len(node.Spec.Taints) != 1 || node.Spec.Taints[0] != operatorTaint || c.activeTaint() {
		t.Fatalf("cleanup changed operator state: %+v", node.Spec)
	}
}

func TestBeforeMutationFailsWhenNodeCannotBePrepared(t *testing.T) {
	c := testCoordinator(t)
	if err := c.BeforeMutation(context.Background()); !apierrors.IsNotFound(err) {
		t.Fatalf("missing node allowed mutation: %v", err)
	}
}

type conflictingNodePatchClient struct {
	client.Client
	patches int
}

func (c *conflictingNodePatchClient) Patch(ctx context.Context, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if _, ok := object.(*corev1.Node); ok {
		c.patches++
		if c.patches == 1 {
			var node corev1.Node
			if err := c.Client.Get(ctx, client.ObjectKeyFromObject(object), &node); err != nil {
				return err
			}
			node.Spec.Taints = append(node.Spec.Taints, corev1.Taint{Key: "operator-added", Effect: corev1.TaintEffectNoExecute})
			if err := c.Client.Update(ctx, &node); err != nil {
				return err
			}
			return apierrors.NewConflict(schema.GroupResource{Resource: "nodes"}, node.Name, errors.New("operator updated node"))
		}
	}
	return c.Client.Patch(ctx, object, patch, opts...)
}

func TestMaintenanceTaintRetriesConflictWithoutLosingOperatorChange(t *testing.T) {
	c := testCoordinator(t, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch"}})
	conflicting := &conflictingNodePatchClient{Client: c.Client}
	c.Client = conflicting
	if err := c.BeforeMutation(context.Background()); err != nil {
		t.Fatal(err)
	}
	var node corev1.Node
	if err := c.Client.Get(context.Background(), types.NamespacedName{Name: "switch"}, &node); err != nil {
		t.Fatal(err)
	}
	if conflicting.patches != 2 || len(node.Spec.Taints) != 2 || node.Spec.Taints[0].Key != "operator-added" || !c.activeTaint() {
		t.Fatalf("conflict retry lost operator state: patches=%d taints=%+v", conflicting.patches, node.Spec.Taints)
	}
}

func TestLegacyMutationBlocksOrdinaryWrites(t *testing.T) {
	up := &opsv1alpha1.IOSXESoftwareUpgrade{ObjectMeta: metav1.ObjectMeta{Namespace: "edge", Name: "legacy"},
		Spec:   opsv1alpha1.IOSXESoftwareUpgradeSpec{DeviceRef: configv1alpha1.DeviceRef{Name: "switch"}},
		Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{Phase: opsv1alpha1.UpgradePhaseTransferring}}
	c := testCoordinator(t, up)
	c.MutationsEnabled = false
	if !c.GuardRecovery(context.Background()) {
		t.Fatal("legacy operation without Lease did not protect recovery")
	}
	if _, _, err := c.AcquireWrite(context.Background()); err == nil || !strings.Contains(err.Error(), "legacy") {
		t.Fatalf("legacy dispatched upgrade did not block ordinary write: %v", err)
	}
}
