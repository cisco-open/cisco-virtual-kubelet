// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func invalidationCampaignFixture(t *testing.T) (*ops.IOSXESoftwareRollout, *ops.IOSXESoftwareUpgrade, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	target := policyFenceTarget("edge-a", "device-a", "campaign-edge-a")
	r := policyFenceRollout([]ops.IOSXESoftwareRolloutPlannedTarget{target})
	r.Spec.Plan.Strategy = ops.IOSXESoftwareRolloutStrategyPrepareOnly
	r.Spec.Control.Cancel = true
	r.Status.Phase = ops.IOSXESoftwareRolloutPhaseCancelled
	u := policyFenceLeaf(r, target, "prepared-uid")
	u.Status.ManagerAdmission.State = ops.UpgradeManagerAdmissionSettled
	u.Status.ManagerControl.Cancel = true
	u.Status.Phase = ops.UpgradePhasePrepared
	u.Status.ExecutionModel = ops.UpgradeExecutionModelAtMostOnceV1
	u.Status.PrimarySupervisorInstalled = true
	u.Status.PrimarySupervisorInstallRequested = true
	u.Status.CompletionTime = &metav1.Time{Time: now.Add(-time.Minute)}
	u.Status.Conditions = []metav1.Condition{{Type: "DeviceMutationSettled", Status: metav1.ConditionTrue, Reason: "Prepared", LastTransitionTime: *u.Status.CompletionTime}}
	u.Status.WorkerControl = &ops.UpgradeWorkerControlStatus{ObservedAdmissionState: ops.UpgradeManagerAdmissionSettled, EffectiveState: ops.UpgradeWorkerControlSettled, ObservedPolicyEpoch: 1, ObservedControlRevision: r.Spec.Control.Revision}
	u.Status.ManagedMutationClaims = []ops.UpgradeManagedMutationClaimStatus{{Stage: ops.UpgradeManagedMutationPrimaryInstall, ReservationID: u.Status.ManagerAdmission.ReservationID, PolicyEpoch: 1, ClaimedAt: metav1.NewTime(now.Add(-time.Hour))}}
	u.Status.PreparedReceipt = &ops.UpgradePreparedReceiptStatus{
		ProtocolVersion: "prepare-v1", UpgradeUID: string(u.UID), DeviceUID: target.DeviceUID, NodeUID: target.NodeUID, PhysicalIdentity: target.PhysicalIdentity, DeviceGeneration: 1,
		CampaignUID: string(r.UID), PlanHash: r.Status.FrozenPlan.Hash, ManagedProtocolVersion: u.Status.ManagerAdmission.ProtocolVersion,
		PolicyUID: r.Status.FrozenPlan.Policy.UID, PolicyResourceVersion: "10", PolicyEpoch: 1, WorkerRevision: "sha256:" + strings.Repeat("e", 64),
		SourceDigest: "sha256:" + strings.Repeat("a", 64), SourceSize: 1024, SourceIdentityHash: "sha256:" + strings.Repeat("b", 64), TrustIdentityHash: "sha256:" + strings.Repeat("c", 64), ContentBinding: "source-digest-install-claim-v1",
		TargetVersion: "17.18.4", ValidatedVersion: "17.18.4", RunningVersion: "17.18.3", PrimarySupervisorInstalled: true,
		InstallStartedAt: metav1.NewTime(now.Add(-time.Hour)), PreparedAt: *u.Status.CompletionTime, ManagedMutationClaims: u.Status.ManagedMutationClaims,
	}
	u.Status.PreparedReceipt.ReceiptHash, _ = softwareupgrade.PreparedReceiptHash(*u.Status.PreparedReceipt)
	r.Spec.PreparationInvalidation = &ops.IOSXESoftwareRolloutPreparationInvalidation{PlanHash: r.Status.FrozenPlan.Hash, RequestedBy: "operator", RequestedAt: metav1.NewTime(now), Reason: "abandon preparation", Receipts: []ops.IOSXESoftwareRolloutActivationReceipt{{DeviceUID: target.DeviceUID, UpgradeUID: string(u.UID), ReceiptHash: u.Status.PreparedReceipt.ReceiptHash}}}
	return r, u, now
}

func settledInvalidationDrain(leaf *ops.IOSXESoftwareUpgrade, now time.Time) {
	leaf.Status.ManagerControl.Cancel = false
	a := leaf.Status.ManagerAdmission
	leaf.Status.ManagerDrain = &ops.UpgradeManagerDrainStatus{
		ProtocolVersion: ops.ManagedDrainProtocolPDBV1, State: ops.UpgradeManagerDrainSettled,
		SessionToken: "6a144e34-cd5d-4479-905d-6023cc60f42a", NodeUID: a.NodeUID,
		ReservationID: a.ReservationID, PolicyEpoch: a.PolicyEpoch, ControlRevision: leaf.Status.ManagerControl.Revision,
		StartedAt: metav1.NewTime(now.Add(-time.Hour)), DrainDeadline: metav1.NewTime(now.Add(time.Hour)),
		UpdatedAt: metav1.NewTime(now), RecoveryDeadline: &metav1.Time{Time: now.Add(time.Hour)},
		Pods: []ops.UpgradeDrainPodStatus{},
	}
}

func TestRolloutInvalidationAfterSettledDrainKeepsAudit(t *testing.T) {
	rollout, leaf, now := invalidationCampaignFixture(t)
	settledInvalidationDrain(leaf, now)
	rollout.Spec.Control.Revision++
	rollout.Spec.Plan.Workloads = ops.IOSXESoftwareRolloutWorkloadSpec{
		Policy: ops.IOSXESoftwareRolloutWorkloadDrain,
		Drain:  &ops.IOSXESoftwareRolloutDrainSpec{Namespaces: []string{"apps"}, MaxPods: 2, TimeoutSeconds: 1800, MaxTerminationGraceSeconds: 30},
	}
	scheme := drainTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout, leaf).WithStatusSubresource(rollout, leaf).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: c, APIReader: c}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(leaf), leaf); err != nil {
		t.Fatal(err)
	}
	before := leaf.DeepCopy()
	if _, err := r.reconcilePreparationInvalidation(context.Background(), rollout, now); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(leaf), leaf); err != nil {
		t.Fatal(err)
	}
	if leaf.Status.ManagerInvalidation == nil || leaf.Status.ManagerInvalidation.ControlRevision != rollout.Spec.Control.Revision {
		t.Fatal("separate retirement authority not published")
	}
	if !reflect.DeepEqual(before.Status.ManagerDrain, leaf.Status.ManagerDrain) || !reflect.DeepEqual(before.Status.ManagerControl, leaf.Status.ManagerControl) ||
		!reflect.DeepEqual(before.Status.ManagerAdmission, leaf.Status.ManagerAdmission) || !reflect.DeepEqual(before.Status.ManagedMutationClaims, leaf.Status.ManagedMutationClaims) ||
		!reflect.DeepEqual(before.Status.PreparedReceipt, leaf.Status.PreparedReceipt) {
		t.Fatal("retirement rewrote retained drain or receipt audit")
	}
}

func TestRolloutInvalidationPublishesOnlyExactSettledAuthority(t *testing.T) {
	for name, mutate := range map[string]func(*ops.IOSXESoftwareRollout, *ops.IOSXESoftwareUpgrade){
		"valid": func(*ops.IOSXESoftwareRollout, *ops.IOSXESoftwareUpgrade) {},
		"activation approved": func(r *ops.IOSXESoftwareRollout, u *ops.IOSXESoftwareUpgrade) {
			r.Spec.ActivationApproval = &ops.IOSXESoftwareRolloutActivationApproval{}
		},
		"not cancelled": func(r *ops.IOSXESoftwareRollout, u *ops.IOSXESoftwareUpgrade) {
			r.Status.Phase = ops.IOSXESoftwareRolloutPhaseSucceeded
		},
		"wrong receipt": func(r *ops.IOSXESoftwareRollout, u *ops.IOSXESoftwareUpgrade) {
			r.Spec.PreparationInvalidation.Receipts[0].ReceiptHash = "sha256:" + strings.Repeat("0", 64)
		},
		"wrong plan": func(r *ops.IOSXESoftwareRollout, u *ops.IOSXESoftwareUpgrade) {
			r.Spec.PreparationInvalidation.PlanHash = "sha256:" + strings.Repeat("0", 64)
		},
		"pending control ack": func(r *ops.IOSXESoftwareRollout, u *ops.IOSXESoftwareUpgrade) {
			u.Status.WorkerControl.ObservedControlRevision--
		},
		"activation consumer": func(r *ops.IOSXESoftwareRollout, u *ops.IOSXESoftwareUpgrade) {
			u.Annotations[managedprotocol.AnnotationPreparedUpgradeUID] = string(u.UID)
		},
	} {
		t.Run(name, func(t *testing.T) {
			rollout, leaf, now := invalidationCampaignFixture(t)
			mutate(rollout, leaf)
			scheme := runtime.NewScheme()
			if err := ops.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout, leaf).WithStatusSubresource(rollout, leaf).Build()
			r := &IOSXESoftwareRolloutReconciler{Client: c, APIReader: c}
			_, err := r.reconcilePreparationInvalidation(context.Background(), rollout, now)
			if (err == nil) != (name == "valid") {
				t.Fatalf("error=%v", err)
			}
			if err := c.Get(context.Background(), client.ObjectKeyFromObject(leaf), leaf); err != nil {
				t.Fatal(err)
			}
			if (leaf.Status.ManagerInvalidation != nil) != (name == "valid") {
				t.Fatal("unexpected authority publication")
			}
			if leaf.Status.Phase != ops.UpgradePhasePrepared || leaf.Status.PreparedInvalidation != nil {
				t.Fatal("manager fabricated native proof")
			}
			if err := r.ensureNoPreparedOwnershipConflict(context.Background(), leaf.Namespace, leaf.Status.PreparedReceipt.DeviceUID); err == nil {
				t.Fatal("request alone released ownership")
			}
		})
	}
}
