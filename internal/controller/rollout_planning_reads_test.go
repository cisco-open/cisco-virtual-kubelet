// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// Count the complete planning path, including live worker ancestry. This is a
// deterministic request-budget regression, not an API latency benchmark.
func TestRolloutPlanningReadBudget(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, count := range []int{1, 10, 50, 100} {
		t.Run(fmt.Sprintf("targets-%03d", count), func(t *testing.T) {
			objects := make([]client.Object, 0, 2300)
			for i := 0; i < 1000; i++ {
				d, n := fleetReadFixture(i)
				if i < count {
					objects = append(objects, planningReadyFixture(t, d, n, now)...)
				}
				objects = append(objects, d, n)
			}
			c := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).WithObjects(objects...).Build()
			rd := &fleetCountingReader{Reader: c}
			r := &IOSXESoftwareRolloutReconciler{Client: c, APIReader: rd, Now: func() time.Time { return now }}
			campaign := policyFenceRollout(nil)
			campaign.Namespace = "fleet"
			campaign.Spec.Control = ops.IOSXESoftwareRolloutControl{}
			campaign.Spec.Plan.Targets = ops.IOSXESoftwareRolloutTargetSpec{
				Selector: ops.IOSXESoftwareRolloutLabelSelector{MatchLabels: map[string]string{"test.cisco.vk/selected": "true"}}, MaxTargets: int32(count),
			}
			campaign.Spec.Plan.Canaries = []ops.IOSXESoftwareRolloutCanaryCohort{{Name: "c9300", Devices: []string{"fleet-0000"}}}
			campaign.Spec.Plan.Workloads.Policy = ops.IOSXESoftwareRolloutWorkloadBlockIfRunning
			campaign.Spec.Plan.Health.Network = &ops.IOSXESoftwareRolloutNetworkHealthSpec{Enabled: true, RequireCompleteEvidence: true}
			_, policy := rolloutPolicyFixture()
			policy.Selector = labels.Everything()
			plan, targets, err := r.buildFrozenPlan(context.Background(), campaign, policy, now)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Targets) != count || len(targets) != count {
				t.Fatal("planning omitted selected targets")
			}
			t.Logf("fleet=1000 targets=%d planningReads=%d", count, rd.requests)
			if rd.requests != 5 {
				t.Fatalf("planning used %d requests, want five bounded inventory reads", rd.requests)
			}
		})
	}
}

func planningReadyFixture(t *testing.T, d *ciskov1.CiscoDevice, n *corev1.Node, now time.Time) []client.Object {
	t.Helper()
	d.Generation = 1
	d.Spec.Driver = ciskov1.DeviceDriverXE
	d.Labels = map[string]string{"test.cisco.vk/selected": "true", managedprotocol.ImageFamilyLabel: "cat9k", managedprotocol.QualificationCohortLabel: "c9300"}
	d.Labels["topology.cisco.vk/site"] = "site-a"
	d.Labels["topology.cisco.vk/managed"] = "true"
	n.Labels = map[string]string{"topology.cisco.vk/site": "site-a"}
	d.Status.Phase = "Ready"
	d.Status.TopologyProjection.EffectiveLabelHash = "sha256:" + strings.Repeat("a", 64)
	d.Status.TopologyProjection.LastSuccessfulTime = metav1.NewTime(now.Add(-time.Minute))
	conditions := []string{ciskov1.CiscoDeviceConditionNodeIdentityReady, ciskov1.CiscoDeviceConditionTopologyReady, ciskov1.CiscoDeviceConditionGNOIConfigurationReady}
	for _, kind := range conditions {
		d.Status.Conditions = append(d.Status.Conditions, metav1.Condition{Type: kind, Status: metav1.ConditionTrue, ObservedGeneration: d.Generation, Reason: "FixtureReady", LastTransitionTime: metav1.NewTime(now.Add(-time.Minute))})
	}
	n.Annotations[managedprotocol.AnnotationWorkerUsername] = "system:serviceaccount:fleet:app-hosting"
	n.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(now), LastTransitionTime: metav1.NewTime(now.Add(-time.Minute))}}
	attachReadyManagedWorkerProof(t, d, n, now)
	objects := attachReadyManagedNetworkWorkerProof(t, d, now)
	deployment := objects[0].(*appsv1.Deployment)
	rs := objects[1].(*appsv1.ReplicaSet)
	pod := objects[2].(*corev1.Pod)
	deployment.UID = types.UID(d.Name + "-deployment")
	rs.Name, rs.UID = d.Name+"-rs", types.UID(d.Name+"-rs")
	rs.OwnerReferences[0].UID = deployment.UID
	pod.Name, pod.UID = d.Name+"-pod", types.UID(d.Name+"-pod")
	pod.OwnerReferences[0].Name, pod.OwnerReferences[0].UID = rs.Name, rs.UID
	d.Status.NetworkWorkerRevision.DeploymentUID = string(deployment.UID)
	d.Status.NetworkWorkerRevision.PodUID = string(pod.UID)
	if err := refreshManagedHealthObservation(d, n, now, conditions...); err != nil {
		t.Fatal(err)
	}
	d.Status.HealthObservation.AcceptedNetwork = &ciskov1.DeviceNetworkObservationStatus{
		CollectionStartedAt: metav1.NewTime(now.Add(-2 * time.Second)), CollectionEndedAt: metav1.NewTime(now.Add(-time.Second)),
		ObservedAt: metav1.NewTime(now.Add(-2 * time.Second)), SampleSequence: 1, Complete: true,
		WorkerPodUID: string(pod.UID), ProducerRevision: d.Status.NetworkWorkerRevision.DesiredRevision,
		DeviceIdentityHash: identityHashForPhysicalID(d.Spec.PhysicalIdentity),
	}
	return objects
}

func TestPlanningSnapshotDoesNotCacheAdmission(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	d, n := fleetReadFixture(0)
	objects := planningReadyFixture(t, d, n, now)
	c := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).WithObjects(append(objects, d, n)...).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: c, APIReader: c, Now: func() time.Time { return now }}
	snapshot, err := newPlanningWorkerSnapshot(ctx, r.reader(), d.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	planner := *r
	planner.APIReader = snapshot
	if _, err := planner.currentReadyWorkerRevision(ctx, d); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, objects[2]); err != nil {
		t.Fatal(err)
	}
	// A proposal can age; it is never allowed to supply execution authority.
	if _, err := planner.currentReadyWorkerRevision(ctx, d); err != nil {
		t.Fatal("fixture did not retain the old planning-only snapshot")
	}
	if _, err := r.currentReadyWorkerRevision(ctx, d); err == nil {
		t.Fatal("planning snapshot escaped into the admission reader")
	}
	planner.APIReader, err = newPlanningWorkerSnapshot(ctx, r.reader(), d.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := planner.currentReadyWorkerRevision(ctx, d); err == nil {
		t.Fatal("a subsequent plan reused the old worker snapshot")
	}
}

func TestPlanningWorkerSnapshotRejectsAmbiguousAncestry(t *testing.T) {
	for _, scenario := range []string{"duplicate-pod", "missing-replicaset", "foreign-owner"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			d, n := fleetReadFixture(0)
			objects := planningReadyFixture(t, d, n, now)
			c := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).WithObjects(append(objects, d, n)...).Build()
			switch scenario {
			case "duplicate-pod":
				pod := objects[2].(*corev1.Pod).DeepCopy()
				pod.Name, pod.UID, pod.ResourceVersion = "duplicate", "duplicate", ""
				if err := c.Create(ctx, pod); err != nil {
					t.Fatal(err)
				}
			case "missing-replicaset":
				if err := c.Delete(ctx, objects[1]); err != nil {
					t.Fatal(err)
				}
			case "foreign-owner":
				var rs appsv1.ReplicaSet
				if err := c.Get(ctx, client.ObjectKeyFromObject(objects[1]), &rs); err != nil {
					t.Fatal(err)
				}
				rs.OwnerReferences[0].UID = "foreign-deployment"
				if err := c.Update(ctx, &rs); err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := newPlanningWorkerSnapshot(ctx, c, d.Namespace)
			if err != nil {
				t.Fatal(err)
			}
			r := &IOSXESoftwareRolloutReconciler{Client: c, APIReader: snapshot, Now: func() time.Time { return now }}
			if _, err := r.currentReadyWorkerRevision(ctx, d); err == nil {
				t.Fatal("ambiguous worker ownership passed planning")
			}
		})
	}
}

type unavailablePlanningReader struct {
	client.Reader
	failKind string
}

func (r unavailablePlanningReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if fmt.Sprintf("%T", list) == r.failKind {
		return fmt.Errorf("injected planning list outage")
	}
	return r.Reader.List(ctx, list, opts...)
}

func TestPlanningSnapshotFailsClosedOnIncompleteCollection(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(drainTestScheme(t)).Build()
	for _, kind := range []client.ObjectList{&corev1.PodList{}, &corev1.NodeList{}, &appsv1.ReplicaSetList{}, &appsv1.DeploymentList{}} {
		if s, err := newPlanningWorkerSnapshot(ctx, unavailablePlanningReader{c, fmt.Sprintf("%T", kind)}, "fleet"); err == nil || s != nil {
			t.Fatal("partial collection returned a usable snapshot")
		}
	}
	s, err := newPlanningWorkerSnapshot(ctx, c, "fleet")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Get(cancelled, client.ObjectKey{Namespace: "fleet", Name: "rs"}, &appsv1.ReplicaSet{}); err != context.Canceled {
		t.Fatalf("cancelled Get: %v", err)
	}
	if err := s.List(cancelled, &corev1.PodList{}, client.InNamespace("fleet")); err != context.Canceled {
		t.Fatalf("cancelled List: %v", err)
	}
}
