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

package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

func TestRolloutLeafAnnotationsPropagateOnlyValidatedCorrelation(t *testing.T) {
	now := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	rollout := &opsv1alpha1.IOSXESoftwareRollout{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "network-devices", Name: "campaign", UID: "campaign-uid",
			Annotations: map[string]string{
				correlation.TraceparentAnnotation:    "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
				correlation.TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
				correlation.LifecycleIDAnnotation:    "release-2026-02",
				"example.invalid/credential":         "must-not-propagate",
			},
		},
		Status: opsv1alpha1.IOSXESoftwareRolloutStatus{FrozenPlan: &opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus{
			Hash: "sha256:" + strings.Repeat("a", 64),
			Policy: opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{
				LedgerUID: "ledger-uid",
			},
		}},
	}
	target := opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		DeviceName: "edge-a", DeviceUID: "device-uid", DeviceGeneration: 1,
		NodeName: "edge-a", NodeUID: "node-uid",
	}
	got := rolloutLeafAnnotations(rollout, target, "system:serviceaccount:network-devices:worker", now)
	for key, want := range map[string]string{
		correlation.TraceparentAnnotation:     rollout.Annotations[correlation.TraceparentAnnotation],
		correlation.TraceWindowEndAnnotation:  rollout.Annotations[correlation.TraceWindowEndAnnotation],
		correlation.LifecycleIDAnnotation:     rollout.Annotations[correlation.LifecycleIDAnnotation],
		managedprotocol.AnnotationCampaignUID: "campaign-uid",
		managedprotocol.AnnotationDeviceUID:   "device-uid",
	} {
		if got[key] != want {
			t.Errorf("annotation %q = %q, want %q", key, got[key], want)
		}
	}
	if _, found := got["example.invalid/credential"]; found {
		t.Fatal("an arbitrary campaign annotation propagated to the retained leaf")
	}
}

func TestCampaignStatusPatchRejectsStaleResourceVersion(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	rollout := &opsv1alpha1.IOSXESoftwareRollout{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "lab", Name: "campaign", UID: "campaign-uid", ResourceVersion: "1",
		},
		Status: opsv1alpha1.IOSXESoftwareRolloutStatus{
			Phase:      opsv1alpha1.IOSXESoftwareRolloutPhaseExecuting,
			FrozenPlan: &opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus{Hash: "sha256:" + strings.Repeat("a", 64)},
		},
	}
	kubeClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}).
		WithObjects(rollout).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: kubeClient, APIReader: kubeClient}
	var stale, current opsv1alpha1.IOSXESoftwareRollout
	key := client.ObjectKeyFromObject(rollout)
	if err := kubeClient.Get(context.Background(), key, &stale); err != nil {
		t.Fatal(err)
	}
	if err := kubeClient.Get(context.Background(), key, &current); err != nil {
		t.Fatal(err)
	}
	current.Status.Phase = opsv1alpha1.IOSXESoftwareRolloutPhaseCancelling
	current.Status.Message = "new leader fenced the campaign"
	if err := kubeClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}

	_, err := reconciler.patchExecutionStatus(
		context.Background(), &stale, map[string]opsv1alpha1.IOSXESoftwareRolloutTargetStatus{},
		opsv1alpha1.IOSXESoftwareRolloutPhaseExecuting, "stale leader update", time.Now().UTC(),
	)
	if err == nil || !apierrors.IsConflict(err) {
		t.Fatalf("stale campaign patch error = %v, want resourceVersion conflict", err)
	}
	if err := kubeClient.Get(context.Background(), key, &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Phase != opsv1alpha1.IOSXESoftwareRolloutPhaseCancelling ||
		current.Status.Message != "new leader fenced the campaign" {
		t.Fatalf("stale campaign patch overwrote current status: %+v", current.Status)
	}
}

func TestEffectiveAdmissionPolicyRejectsRequiredTopologySetChange(t *testing.T) {
	rollout, policy := rolloutPolicyFixture()
	policy.Config.RequiredTopologyKeys = append(policy.Config.RequiredTopologyKeys, "topology.cisco.vk/rack")

	_, err := effectiveAdmissionPolicy(rollout, policy, time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "required-topology set changed") {
		t.Fatalf("effectiveAdmissionPolicy() error = %v, want required-topology change rejection", err)
	}
}

func TestBuildFrozenPlanRejectsNonNeutralInitialControl(t *testing.T) {
	now := metav1.NewTime(time.Now().UTC())
	tests := []struct {
		name    string
		control opsv1alpha1.IOSXESoftwareRolloutControl
	}{
		{name: "nonzero revision", control: opsv1alpha1.IOSXESoftwareRolloutControl{Revision: 1}},
		{name: "pause", control: opsv1alpha1.IOSXESoftwareRolloutControl{Pause: true}},
		{name: "cancel", control: opsv1alpha1.IOSXESoftwareRolloutControl{Cancel: true}},
		{name: "requester", control: opsv1alpha1.IOSXESoftwareRolloutControl{RequestedBy: "operator"}},
		{name: "request time", control: opsv1alpha1.IOSXESoftwareRolloutControl{RequestedAt: &now}},
		{name: "reason", control: opsv1alpha1.IOSXESoftwareRolloutControl{Reason: "start paused"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rollout := &opsv1alpha1.IOSXESoftwareRollout{Spec: opsv1alpha1.IOSXESoftwareRolloutSpec{Control: tt.control}}
			reconciler := &IOSXESoftwareRolloutReconciler{}
			if _, _, err := reconciler.buildFrozenPlan(context.Background(), rollout,
				&topologyrollout.ParsedAdminPolicy{}, time.Now().UTC()); err == nil ||
				!strings.Contains(err.Error(), "neutral control revision zero") {
				t.Fatalf("buildFrozenPlan() error = %v, want neutral-control rejection", err)
			}
		})
	}
}

func TestCanaryAssignmentsRequireEveryQualificationCohort(t *testing.T) {
	devices := []ciskov1.CiscoDevice{
		{ObjectMeta: metav1.ObjectMeta{Name: "edge-a", Labels: map[string]string{
			managedprotocol.QualificationCohortLabel: "c9300",
		}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "edge-b", Labels: map[string]string{
			managedprotocol.QualificationCohortLabel: "c9400",
		}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "edge-c", Labels: map[string]string{
			managedprotocol.QualificationCohortLabel: "c9300",
		}}},
	}
	valid := []opsv1alpha1.IOSXESoftwareRolloutCanaryCohort{
		{Name: "c9300", Devices: []string{"edge-a"}},
		{Name: "c9400", Devices: []string{"edge-b"}},
	}
	assignments, err := canaryAssignments(valid, devices)
	if err != nil {
		t.Fatalf("canaryAssignments() error = %v", err)
	}
	if assignments["edge-a"] != "c9300" || assignments["edge-b"] != "c9400" {
		t.Fatalf("canary assignments = %#v", assignments)
	}
	if _, selected := assignments["edge-c"]; selected {
		t.Fatalf("non-canary cohort member was selected: %#v", assignments)
	}

	tests := []struct {
		name    string
		cohorts []opsv1alpha1.IOSXESoftwareRolloutCanaryCohort
		devices []ciskov1.CiscoDevice
		want    string
	}{
		{
			name: "uncovered target cohort",
			cohorts: []opsv1alpha1.IOSXESoftwareRolloutCanaryCohort{
				{Name: "c9300", Devices: []string{"edge-a"}},
			},
			devices: devices,
			want:    `qualification cohort "c9400" has no explicit canary`,
		},
		{
			name: "canary assigned to wrong cohort",
			cohorts: []opsv1alpha1.IOSXESoftwareRolloutCanaryCohort{
				{Name: "c9300", Devices: []string{"edge-b"}},
			},
			devices: devices,
			want:    `device "edge-b" belongs to qualification cohort "c9400"`,
		},
		{
			name:    "target missing cohort label",
			cohorts: valid,
			devices: append(append([]ciskov1.CiscoDevice(nil), devices...), ciskov1.CiscoDevice{
				ObjectMeta: metav1.ObjectMeta{Name: "edge-d"},
			}),
			want: "is missing required qualification label",
		},
		{
			name: "non-target canary",
			cohorts: []opsv1alpha1.IOSXESoftwareRolloutCanaryCohort{
				{Name: "c9300", Devices: []string{"edge-z"}},
			},
			devices: devices,
			want:    `names non-target device "edge-z"`,
		},
		{
			name: "repeated cohort",
			cohorts: []opsv1alpha1.IOSXESoftwareRolloutCanaryCohort{
				{Name: "c9300", Devices: []string{"edge-a"}},
				{Name: "c9300", Devices: []string{"edge-c"}},
			},
			devices: devices,
			want:    `canary cohort "c9300" is repeated`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := canaryAssignments(tt.cohorts, tt.devices)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("canaryAssignments() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTerminalRolloutPreservesAuditResultWhenDependenciesChange(t *testing.T) {
	for _, phase := range []opsv1alpha1.IOSXESoftwareRolloutPhase{
		opsv1alpha1.IOSXESoftwareRolloutPhaseSucceeded,
		opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled,
	} {
		t.Run(string(phase), func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := opsv1alpha1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			rollout := &opsv1alpha1.IOSXESoftwareRollout{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "lab", Name: "finished", UID: "campaign-uid", Finalizers: []string{rolloutSafetyFinalizer},
				},
				Status: opsv1alpha1.IOSXESoftwareRolloutStatus{
					Phase: phase, Message: "retained audit result",
					FrozenPlan: &opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus{
						Policy: opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{
							Namespace: "missing", Name: "deleted-policy", UID: "old-uid", ResourceVersion: "1",
						},
					},
				},
			}
			apiClient := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}).WithObjects(rollout).Build()
			reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
			result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(rollout)})
			if err != nil || result != (ctrl.Result{}) {
				t.Fatalf("Reconcile() = (%+v, %v), want terminal no-op", result, err)
			}
			var got opsv1alpha1.IOSXESoftwareRollout
			if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &got); err != nil {
				t.Fatal(err)
			}
			if got.Status.Phase != phase || got.Status.Message != "retained audit result" {
				t.Fatalf("terminal status changed to phase %q message %q", got.Status.Phase, got.Status.Message)
			}
		})
	}
}

func TestEffectiveAdmissionPolicyRejectsTightenedTargetCap(t *testing.T) {
	rollout, policy := rolloutPolicyFixture()
	second := rollout.Status.FrozenPlan.Targets[0]
	second.DeviceName = "device-b"
	second.DeviceUID = "device-uid-b"
	rollout.Status.FrozenPlan.Targets = append(rollout.Status.FrozenPlan.Targets, second)
	policy.Config.MaxCampaignTargets = 1

	_, err := effectiveAdmissionPolicy(rollout, policy, time.Now().UTC())
	if err == nil || !strings.Contains(err.Error(), "target ceiling tightened") {
		t.Fatalf("effectiveAdmissionPolicy() error = %v, want target-cap rejection", err)
	}
}

func TestPolicyEpochIgnoresMetadataChurnAndKeepsTighteningMonotonic(t *testing.T) {
	rollout, policy := rolloutPolicyFixture()
	rollout.Namespace, rollout.Name, rollout.UID = "lab", "campaign", "campaign-uid"
	scheme := runtime.NewScheme()
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}).WithObjects(rollout).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	now := time.Now().UTC()

	var current opsv1alpha1.IOSXESoftwareRollout
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &current); err != nil {
		t.Fatal(err)
	}
	if _, handled, err := reconciler.reconcilePolicyEpoch(context.Background(), &current, policy, now); err != nil || !handled {
		t.Fatalf("metadata-only policy reconcile = handled %t, error %v", handled, err)
	}
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.EffectivePolicy.Epoch != 1 || current.Status.EffectivePolicy.Policy.ResourceVersion != policy.ResourceVersion ||
		current.Status.PolicyTransition != nil {
		t.Fatalf("metadata-only policy churn changed safety epoch: %#v", current.Status)
	}

	policy.Config.GlobalMaxConcurrentTransfers = 1
	policy.ResourceVersion = "12"
	if _, handled, err := reconciler.reconcilePolicyEpoch(context.Background(), &current, policy, now.Add(time.Second)); err != nil || !handled {
		t.Fatalf("tightening policy reconcile = handled %t, error %v", handled, err)
	}
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.PolicyTransition == nil || current.Status.PolicyTransition.Epoch != 2 ||
		current.Status.PolicyTransition.Policy.MaxConcurrentTransfers != 1 {
		t.Fatalf("tightening did not persist the next safety epoch: %#v", current.Status.PolicyTransition)
	}

	// Model a fully converged transition and then loosen the source policy. The
	// next epoch records the semantic edit but may not restore prior capacity.
	current.Status.EffectivePolicy = &opsv1alpha1.IOSXESoftwareRolloutEffectivePolicyStatus{
		Epoch: 2, Policy: current.Status.PolicyTransition.Policy, UpdatedAt: metav1.NewTime(now.Add(2 * time.Second)),
	}
	current.Status.PolicyTransition = nil
	if err := apiClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	policy.Config.GlobalMaxConcurrentTransfers = 3
	policy.ResourceVersion = "13"
	if _, handled, err := reconciler.reconcilePolicyEpoch(context.Background(), &current, policy, now.Add(3*time.Second)); err != nil || !handled {
		t.Fatalf("loosening policy reconcile = handled %t, error %v", handled, err)
	}
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.PolicyTransition == nil || current.Status.PolicyTransition.Epoch != 3 ||
		current.Status.PolicyTransition.Policy.MaxConcurrentTransfers != 1 {
		t.Fatalf("loosening restored capacity after tightening: %#v", current.Status.PolicyTransition)
	}
}

func TestPolicyEpochFenceReleasesOnlyZeroClaimLeaves(t *testing.T) {
	targets := []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		policyFenceTarget("device-a", "device-uid-a", "leaf-a"),
		policyFenceTarget("device-b", "device-uid-b", "leaf-b"),
	}
	rollout := policyFenceRollout(targets)
	next := rollout.Status.EffectivePolicy.Policy
	next.ResourceVersion = "11"
	next.SemanticHash = "sha256:" + strings.Repeat("e", 64)
	next.MaxConcurrentTransfers = 1
	rollout.Status.PolicyTransition = &opsv1alpha1.IOSXESoftwareRolloutPolicyTransitionStatus{
		Epoch: 2, Policy: next, StartedAt: metav1.NewTime(time.Now().UTC()),
	}
	unclaimed := policyFenceLeaf(rollout, targets[0], "leaf-uid-a")
	claimed := policyFenceLeaf(rollout, targets[1], "leaf-uid-b")
	claimed.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
		Stage:         opsv1alpha1.UpgradeManagedMutationPrimaryInstall,
		ReservationID: claimed.Status.ManagerAdmission.ReservationID,
		PolicyEpoch:   1, ControlRevision: rollout.Spec.Control.Revision, ClaimedAt: metav1.NewTime(time.Now().UTC()),
	}}
	ledgerCM := policyFenceLedger(t, rollout, targets,
		map[string]types.UID{targets[0].DeviceUID: unclaimed.UID, targets[1].DeviceUID: claimed.UID},
		topologyrollout.ReservationGranted)
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, ciskov1.AddToScheme, opsv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}, &ciskov1.CiscoDevice{}).
		WithObjects(rollout, unclaimed, claimed, ledgerCM).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	if err := reconciler.ensurePolicyEpochFences(context.Background(), rollout, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var gotUnclaimed, gotClaimed opsv1alpha1.IOSXESoftwareUpgrade
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(unclaimed), &gotUnclaimed); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(claimed), &gotClaimed); err != nil {
		t.Fatal(err)
	}
	if gotUnclaimed.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
		gotUnclaimed.Status.ManagerAdmission.RevocationReason != "PolicyEpochTransition" {
		t.Fatalf("zero-claim leaf was not reversibly fenced: %#v", gotUnclaimed.Status.ManagerAdmission)
	}
	if gotClaimed.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionGranted {
		t.Fatalf("claimed old-epoch leaf grant was revoked: %#v", gotClaimed.Status.ManagerAdmission)
	}
	store := topologyrollout.Store{Client: apiClient, APIReader: apiClient,
		Key: client.ObjectKeyFromObject(ledgerCM), ExpectedUID: ledgerCM.UID}
	_, ledger, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := ledger.Reservations[unclaimed.Status.ManagerAdmission.ReservationID]; exists {
		t.Fatal("zero-claim old-epoch reservation remains")
	}
	if _, exists := ledger.Reservations[claimed.Status.ManagerAdmission.ReservationID]; !exists {
		t.Fatal("claimed old-epoch reservation was released")
	}
}

func TestTerminalFailureRevokesEveryUnclaimedLeafBeforePublishingStop(t *testing.T) {
	targets := []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		policyFenceTarget("device-a", "device-uid-a", "leaf-a"),
		policyFenceTarget("device-b", "device-uid-b", "leaf-b"),
		policyFenceTarget("device-c", "device-uid-c", "leaf-c"),
		policyFenceTarget("device-d", "device-uid-d", "leaf-d"),
	}
	rollout := policyFenceRollout(targets)
	_, policy := rolloutPolicyFixture()
	policySnapshot, err := freezePolicy(policy)
	if err != nil {
		t.Fatal(err)
	}
	rollout.Status.FrozenPlan.Policy = policySnapshot
	rollout.Status.EffectivePolicy = &opsv1alpha1.IOSXESoftwareRolloutEffectivePolicyStatus{
		Epoch: 1, Policy: policySnapshot, UpdatedAt: metav1.NewTime(time.Now().UTC()),
	}

	failed := policyFenceLeaf(rollout, targets[0], "leaf-uid-a")
	failed.Status.Phase = opsv1alpha1.UpgradePhaseFailed
	pending := policyFenceLeaf(rollout, targets[1], "leaf-uid-b")
	pending.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionPending
	pending.Status.WorkerControl = &opsv1alpha1.UpgradeWorkerControlStatus{
		ObservedAdmissionState:  opsv1alpha1.UpgradeManagerAdmissionPending,
		ObservedPolicyEpoch:     1,
		ObservedControlRevision: rollout.Spec.Control.Revision,
		EffectiveState:          opsv1alpha1.UpgradeWorkerControlReady,
		UpdatedAt:               metav1.NewTime(time.Now().UTC()),
	}
	claimed := policyFenceLeaf(rollout, targets[2], "leaf-uid-c")
	claimed.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
		Stage:         opsv1alpha1.UpgradeManagedMutationPrimaryInstall,
		ReservationID: claimed.Status.ManagerAdmission.ReservationID,
		PolicyEpoch:   1, ControlRevision: rollout.Spec.Control.Revision,
		ClaimedAt: metav1.NewTime(time.Now().UTC()),
	}}
	ledgerCM := policyFenceLedger(t, rollout, targets[:3], map[string]types.UID{
		targets[0].DeviceUID: failed.UID,
		targets[1].DeviceUID: pending.UID,
		targets[2].DeviceUID: claimed.UID,
	}, topologyrollout.ReservationGranted)
	crashLedger, err := topologyrollout.Decode([]byte(ledgerCM.Data[topologyrollout.LedgerDataKey]), string(ledgerCM.UID))
	if err != nil {
		t.Fatal(err)
	}
	missing := targets[3]
	missingReservation := reservationID(string(rollout.UID), missing.DeviceUID)
	crashLedger.Reservations[missingReservation] = topologyrollout.Reservation{
		ReservationRequest: topologyrollout.ReservationRequest{
			ID: missingReservation, CampaignUID: string(rollout.UID), PlanHash: rollout.Status.FrozenPlan.Hash,
			PolicyUID: policySnapshot.UID, PolicyVersion: policySnapshot.ResourceVersion, PolicyEpoch: 1,
			TopologyLockID: strings.Repeat("4", 32), PhysicalID: missing.PhysicalIdentity,
			DeviceUID: missing.DeviceUID, NodeUID: missing.NodeUID, ChildNamespace: rollout.Namespace,
			ChildName: missing.ChildName, Domains: targetDomains(missing),
			ControlRevision: uint64(rollout.Spec.Control.Revision),
		},
		State: topologyrollout.ReservationReserved,
	}
	encoded, err := topologyrollout.Encode(crashLedger, topologyrollout.DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatal(err)
	}
	ledgerCM.Data[topologyrollout.LedgerDataKey] = string(encoded)

	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, ciskov1.AddToScheme, opsv1alpha1.AddToScheme} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}, &opsv1alpha1.IOSXESoftwareUpgrade{}, &ciskov1.CiscoDevice{}).
		WithObjects(rollout, failed, pending, claimed, ledgerCM).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, inner client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if leaf, ok := obj.(*opsv1alpha1.IOSXESoftwareUpgrade); ok && leaf.UID == "" {
					leaf.UID = "failure-tombstone-uid"
				}
				return inner.Create(ctx, obj, opts...)
			},
		}).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	now := time.Now().UTC()

	// The first pass must durably revoke all zero-claim leaves and recover the
	// reservation-before-child crash before publishing the campaign stop.
	// Worker-side sibling scans and manager admission revalidation close the
	// interval while those multi-object fences are being committed.
	if _, err := reconciler.reconcileExecution(context.Background(), rollout, policy, now); err != nil {
		t.Fatal(err)
	}
	var current opsv1alpha1.IOSXESoftwareRollout
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Phase != opsv1alpha1.IOSXESoftwareRolloutPhaseFailed {
		t.Fatalf("first pass phase = %q, want Failed", current.Status.Phase)
	}
	var afterFirst opsv1alpha1.IOSXESoftwareUpgrade
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(pending), &afterFirst); err != nil {
		t.Fatal(err)
	}
	if afterFirst.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
		afterFirst.Status.ManagerAdmission.RevocationReason != "CampaignTargetFailed" {
		t.Fatalf("first pass did not revoke pending admission: %#v", afterFirst.Status.ManagerAdmission)
	}

	// A second pass is idempotent. Accepted work remains retained under its
	// exact reservation while every zero-claim target stays fenced.
	if _, err := reconciler.reconcileExecution(context.Background(), &current, policy, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, original := range []*opsv1alpha1.IOSXESoftwareUpgrade{failed, pending} {
		var got opsv1alpha1.IOSXESoftwareUpgrade
		if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(original), &got); err != nil {
			t.Fatal(err)
		}
		if got.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
			got.Status.ManagerAdmission.RevocationReason != "CampaignTargetFailed" {
			t.Fatalf("zero-claim leaf %s admission = %#v", got.Name, got.Status.ManagerAdmission)
		}
	}
	var crashTombstone opsv1alpha1.IOSXESoftwareUpgrade
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: rollout.Namespace, Name: missing.ChildName},
		&crashTombstone); err != nil {
		t.Fatalf("read reservation-before-child failure tombstone: %v", err)
	}
	if crashTombstone.Status.ManagerAdmission == nil ||
		crashTombstone.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled ||
		crashTombstone.Status.ManagerAdmission.TopologyLockID != strings.Repeat("4", 32) {
		t.Fatalf("reservation-before-child failure tombstone admission=%#v", crashTombstone.Status.ManagerAdmission)
	}
	var gotClaimed opsv1alpha1.IOSXESoftwareUpgrade
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(claimed), &gotClaimed); err != nil {
		t.Fatal(err)
	}
	if gotClaimed.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionGranted {
		t.Fatalf("claimed admission changed to %q", gotClaimed.Status.ManagerAdmission.State)
	}
	store := topologyrollout.Store{Client: apiClient, APIReader: apiClient,
		Key: client.ObjectKeyFromObject(ledgerCM), ExpectedUID: ledgerCM.UID}
	_, ledger, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Reservations) != 1 {
		t.Fatalf("failure fence retained %d reservations, want only the claimed reservation", len(ledger.Reservations))
	}
	if _, ok := ledger.Reservations[claimed.Status.ManagerAdmission.ReservationID]; !ok {
		t.Fatal("failure fence released the claimed reservation")
	}
}

func TestValidateFrozenRequiredTopologyKeysRejectsPerTargetOmission(t *testing.T) {
	targets := []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		{DeviceName: "device-a", Topology: []opsv1alpha1.IOSXESoftwareRolloutTopologyValue{
			{Key: "topology.cisco.vk/site", Value: "site-a"},
			{Key: "topology.cisco.vk/rack", Value: "rack-a"},
		}},
		{DeviceName: "device-b", Topology: []opsv1alpha1.IOSXESoftwareRolloutTopologyValue{
			{Key: "topology.cisco.vk/site", Value: "site-a"},
		}},
	}
	if err := validateFrozenRequiredTopologyKeys(targets,
		[]string{"topology.cisco.vk/site", "topology.cisco.vk/rack"}); err == nil {
		t.Fatal("validateFrozenRequiredTopologyKeys() accepted an incomplete target")
	}
}

func attachReadyManagedWorkerProof(
	t *testing.T,
	device *ciskov1.CiscoDevice,
	node *corev1.Node,
	now time.Time,
) []client.Object {
	t.Helper()
	template := corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: perDeviceDeploymentLabels(device.Name)},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "cisco-vk", Image: "worker:test"}}},
	}
	revision, err := managedWorkerPodTemplateRevision(&template)
	if err != nil {
		t.Fatal(err)
	}
	template.Annotations = map[string]string{managedprotocol.AnnotationWorkerConfigRevision: revision}
	start := metav1.NewTime(now.Add(-time.Minute))
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: device.Name + deploymentSuffix,
			UID: "worker-deployment-uid", Generation: 2,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: ciskov1.GroupVersion.String(), Kind: "CiscoDevice", Name: device.Name,
				UID: device.UID, Controller: ptr.To(true),
			}},
		},
		Spec: appsv1.DeploymentSpec{Replicas: ptr.To[int32](1), Template: template},
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: 2, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1, AvailableReplicas: 1,
		},
	}
	replicaSet := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
		Namespace: device.Namespace, Name: "worker-rs", UID: "worker-rs-uid",
		OwnerReferences: []metav1.OwnerReference{{
			APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "Deployment", Name: deployment.Name,
			UID: deployment.UID, Controller: ptr.To(true),
		}},
	}}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: device.Namespace, Name: "worker-pod", UID: "worker-pod-uid",
			Labels:      perDeviceDeploymentLabels(device.Name),
			Annotations: map[string]string{managedprotocol.AnnotationWorkerConfigRevision: revision},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: appsv1.SchemeGroupVersion.String(), Kind: "ReplicaSet", Name: replicaSet.Name,
				UID: replicaSet.UID, Controller: ptr.To(true),
			}},
		},
		Status: corev1.PodStatus{
			StartTime:  &start,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	node.Annotations[managedprotocol.AnnotationWorkerObservedRevision] = revision
	foundManagedReady := false
	for i := range node.Status.Conditions {
		if node.Status.Conditions[i].Type == corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition) {
			foundManagedReady = true
			node.Status.Conditions[i].Status = corev1.ConditionTrue
			node.Status.Conditions[i].Reason = managedprotocol.ManagedWorkerReadyReason
			node.Status.Conditions[i].LastHeartbeatTime = metav1.NewTime(now)
		}
	}
	if !foundManagedReady {
		node.Status.Conditions = append(node.Status.Conditions, corev1.NodeCondition{
			Type:   corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition),
			Status: corev1.ConditionTrue, Reason: managedprotocol.ManagedWorkerReadyReason,
			LastHeartbeatTime: metav1.NewTime(now),
		})
	}
	device.Status.WorkerRevision = &ciskov1.DeviceWorkerRevisionStatus{
		DesiredRevision: revision, ObservedRevision: revision,
		DeploymentUID: string(deployment.UID), DeploymentGeneration: deployment.Generation,
		PodUID: string(pod.UID), PodStartTime: start.DeepCopy(),
		ReadyHeartbeatTime: &metav1.Time{Time: now}, ObservedAt: metav1.NewTime(now),
	}
	return []client.Object{deployment, replicaSet, pod}
}

func TestFreezeTargetRequiresCompletedWorkerHandoff(t *testing.T) {
	const topologyKey = "topology.cisco.vk/site"
	now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
	projectionTime := metav1.NewTime(now.Add(-time.Minute))
	scheme := newTestScheme(t)
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "device-a", UID: types.UID("node-uid"),
			Labels: map[string]string{topologyKey: "site-a"},
			Annotations: map[string]string{
				managedprotocol.AnnotationManaged:         "true",
				managedprotocol.AnnotationDeviceNamespace: "lab",
				managedprotocol.AnnotationDeviceName:      "device-a",
				managedprotocol.AnnotationDeviceUID:       "device-uid",
				managedprotocol.AnnotationNodeUID:         "node-uid",
			},
		},
		Status: corev1.NodeStatus{
			NodeInfo: corev1.NodeSystemInfo{MachineID: "serial-a", SystemUUID: "serial-a"},
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue, LastHeartbeatTime: metav1.NewTime(now)},
				{
					Type: corev1.NodeConditionType(managedprotocol.ManagedWorkerReadyCondition), Status: corev1.ConditionTrue,
					Reason: managedprotocol.ManagedWorkerReadyReason, LastHeartbeatTime: metav1.NewTime(now),
				},
			},
		},
	}
	device := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "lab", Name: "device-a", UID: types.UID("device-uid"),
			Labels: map[string]string{
				topologyKey: "site-a", managedprotocol.ImageFamilyLabel: "cat9k",
				managedprotocol.QualificationCohortLabel: "c9300",
			},
		},
		Spec: ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE, PhysicalIdentity: "SERIAL-A"},
		Status: ciskov1.DeviceStatus{
			Phase: "Ready",
			Conditions: []metav1.Condition{
				{Type: ciskov1.CiscoDeviceConditionNodeIdentityReady, Status: metav1.ConditionTrue},
				{Type: ciskov1.CiscoDeviceConditionTopologyReady, Status: metav1.ConditionTrue},
				{Type: ciskov1.CiscoDeviceConditionGNOIConfigurationReady, Status: metav1.ConditionTrue},
			},
			NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{
				NodeName: "device-a", NodeUID: "node-uid", DeviceUID: "device-uid", PhysicalIdentity: "serial-a",
			},
			TopologyProjection: &ciskov1.DeviceTopologyProjectionStatus{
				EffectiveLabelHash: "sha256:" + strings.Repeat("a", 64), LastSuccessfulTime: projectionTime,
			},
		},
	}
	workerObjects := attachReadyManagedWorkerProof(t, device, node, now)
	objects := append([]client.Object{node}, workerObjects...)
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&corev1.Node{}).WithObjects(objects...).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient, Now: func() time.Time { return now }}
	if err := refreshManagedHealthObservation(device, node, now,
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	); err != nil {
		t.Fatal(err)
	}
	rollout := &opsv1alpha1.IOSXESoftwareRollout{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "campaign", UID: types.UID("campaign-uid")},
		Spec: opsv1alpha1.IOSXESoftwareRolloutSpec{Plan: opsv1alpha1.IOSXESoftwareRolloutPlan{
			Image: opsv1alpha1.IOSXESoftwareRolloutImageSpec{ImageFamily: "cat9k"},
		}},
	}
	frozenSource := opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot{
		Name: "global", URL: "https://images.example.test/cat9k.bin", SHA256: strings.Repeat("a", 64),
	}
	policy := &topologyrollout.ParsedAdminPolicy{Config: topologyrollout.AdminPolicyConfig{
		RequiredTopologyKeys: []string{topologyKey}, ProjectedTopologyKeys: []string{topologyKey},
	}}

	if _, err := reconciler.freezeTarget(context.Background(), rollout, device, policy, frozenSource, "canary", now); err == nil ||
		!strings.Contains(err.Error(), "managed worker handoff") {
		t.Fatalf("freezeTarget() error = %v, want incomplete worker handoff rejection", err)
	}

	var current corev1.Node
	if err := apiClient.Get(context.Background(), types.NamespacedName{Name: node.Name}, &current); err != nil {
		t.Fatal(err)
	}
	current.Annotations[managedprotocol.AnnotationWorkerProtocol] = managedprotocol.Version
	current.Annotations[managedprotocol.AnnotationWorkerUsername] = "system:serviceaccount:lab:cvk-device-a"
	if err := apiClient.Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	for i := range device.Status.HealthObservation.ConditionObservations {
		if device.Status.HealthObservation.ConditionObservations[i].Type == ciskov1.CiscoDeviceConditionGNOIConfigurationReady {
			device.Status.HealthObservation.ConditionObservations = append(
				device.Status.HealthObservation.ConditionObservations[:i],
				device.Status.HealthObservation.ConditionObservations[i+1:]...,
			)
			break
		}
	}
	if _, err := reconciler.freezeTarget(context.Background(), rollout, device, policy, frozenSource, "canary", now); err == nil ||
		!strings.Contains(err.Error(), "has no producer observation time") {
		t.Fatalf("freezeTarget() without explicit gNOI producer observation error = %v", err)
	}
	if err := refreshManagedHealthObservation(device, &current, now,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.freezeTarget(context.Background(), rollout, device, policy, frozenSource, "canary", now); err != nil {
		t.Fatalf("freezeTarget() after worker handoff error = %v", err)
	}

	// A bound worker may report live inventory, but it cannot choose rollout
	// identity. A post-binding NodeInfo forgery must close admission even while
	// every manager-owned binding and health heartbeat remains otherwise valid.
	current.Status.NodeInfo.MachineID = "serial-attacker"
	current.Status.NodeInfo.SystemUUID = "serial-attacker"
	if err := apiClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.freezeTarget(context.Background(), rollout, device, policy, frozenSource, "canary", now); err == nil ||
		!strings.Contains(err.Error(), "does not match declared authority") {
		t.Fatalf("freezeTarget() forged NodeInfo error = %v", err)
	}
	current.Status.NodeInfo.MachineID = "serial-a"
	current.Status.NodeInfo.SystemUUID = "serial-a"
	if err := apiClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}

	current.Status.Conditions[0].LastHeartbeatTime = metav1.NewTime(now.Add(-10 * time.Minute))
	if err := apiClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	if err := refreshManagedHealthObservation(device, &current, now,
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.freezeTarget(context.Background(), rollout, device, policy, frozenSource, "canary", now); err == nil ||
		!strings.Contains(err.Error(), "health observation is stale") {
		t.Fatalf("freezeTarget() stale error = %v, want stale managed-health rejection", err)
	}

	current.Status.Conditions[0].LastHeartbeatTime = metav1.NewTime(now)
	if err := apiClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	if err := refreshManagedHealthObservation(device, &current, now,
		ciskov1.CiscoDeviceConditionNodeIdentityReady,
		ciskov1.CiscoDeviceConditionTopologyReady,
		ciskov1.CiscoDeviceConditionGNOIConfigurationReady,
	); err != nil {
		t.Fatal(err)
	}
	current.Spec.Taints = []corev1.Taint{{Key: managedprotocol.TopologyInitializingTaint, Value: "true", Effect: corev1.TaintEffectNoSchedule}}
	if err := apiClient.Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.freezeTarget(context.Background(), rollout, device, policy, frozenSource, "canary", now); err == nil ||
		!strings.Contains(err.Error(), "initialization guard") {
		t.Fatalf("freezeTarget() taint error = %v, want initialization-guard rejection", err)
	}
}

func TestStablePhysicalIdentityRequiresDeclarationBindingAndLiveAgreement(t *testing.T) {
	device := &ciskov1.CiscoDevice{
		Spec: ciskov1.DeviceSpec{PhysicalIdentity: "FOC2416U0MV"},
		Status: ciskov1.DeviceStatus{NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{
			PhysicalIdentity: "foc2416u0mv",
		}},
	}
	node := &corev1.Node{Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{
		MachineID: "foc2416u0mv", SystemUUID: "FOC2416U0MV",
	}}}
	if got, err := stablePhysicalIdentity(device, node); err != nil || got != "foc2416u0mv" {
		t.Fatalf("stablePhysicalIdentity() = %q, %v", got, err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*ciskov1.CiscoDevice, *corev1.Node)
		want   string
	}{
		{name: "missing declaration", mutate: func(device *ciskov1.CiscoDevice, _ *corev1.Node) {
			device.Spec.PhysicalIdentity = ""
		}, want: "declared physical identity is invalid"},
		{name: "forged manager binding", mutate: func(device *ciskov1.CiscoDevice, _ *corev1.Node) {
			device.Status.NodeIdentity.PhysicalIdentity = "attacker"
		}, want: "does not match declaration"},
		{name: "worker observation drift", mutate: func(_ *ciskov1.CiscoDevice, node *corev1.Node) {
			node.Status.NodeInfo.MachineID = "different"
			node.Status.NodeInfo.SystemUUID = "different"
		}, want: "does not match declared authority"},
		{name: "worker observations conflict", mutate: func(_ *ciskov1.CiscoDevice, node *corev1.Node) {
			node.Status.NodeInfo.MachineID = "foc2416u0mv"
			node.Status.NodeInfo.SystemUUID = "different"
		}, want: "observations conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidateDevice := device.DeepCopy()
			candidateNode := node.DeepCopy()
			test.mutate(candidateDevice, candidateNode)
			if _, err := stablePhysicalIdentity(candidateDevice, candidateNode); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("stablePhysicalIdentity() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRevalidateFrozenTargetRejectsDeviceSpecDrift(t *testing.T) {
	const topologyKey = "topology.cisco.vk/site"
	baseDevice := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "lab", Name: "device-a", UID: "device-uid", Generation: 7,
			Labels: map[string]string{
				topologyKey: "site-a", managedprotocol.ImageFamilyLabel: "cat9k",
				managedprotocol.QualificationCohortLabel: "c9300",
			},
		},
		Spec: ciskov1.DeviceSpec{
			Driver: ciskov1.DeviceDriverXE, Address: "switch-a.example.test", PhysicalIdentity: "SERIAL-DEVICE-A",
		},
		Status: ciskov1.DeviceStatus{
			Conditions: []metav1.Condition{{
				Type: ciskov1.CiscoDeviceConditionGNOIConfigurationReady, Status: metav1.ConditionTrue,
			}},
			NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{
				NodeName: "device-a", NodeUID: "node-uid", DeviceUID: "device-uid",
				PhysicalIdentity: "serial-device-a",
			},
			TopologyProjection: &ciskov1.DeviceTopologyProjectionStatus{
				EffectiveLabelHash: "sha256:" + strings.Repeat("a", 64),
			},
		},
	}
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	target.DeviceGeneration = baseDevice.Generation
	target.NodeUID = baseDevice.Status.NodeIdentity.NodeUID
	target.QualificationCohort = "c9300"
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	policy := &topologyrollout.ParsedAdminPolicy{Selector: labels.Everything()}

	for _, tt := range []struct {
		name   string
		mutate func(*ciskov1.CiscoDevice)
	}{
		{name: "unchanged", mutate: func(*ciskov1.CiscoDevice) {}},
		{name: "address generation", mutate: func(device *ciskov1.CiscoDevice) {
			device.Spec.Address = "switch-b.example.test"
			device.Generation++
		}},
		{name: "driver", mutate: func(device *ciskov1.CiscoDevice) {
			device.Spec.Driver = ciskov1.DeviceDriverXR
		}},
		{name: "qualification cohort", mutate: func(device *ciskov1.CiscoDevice) {
			device.Labels[managedprotocol.QualificationCohortLabel] = "c9400"
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			device := baseDevice.DeepCopy()
			tt.mutate(device)
			scheme := newTestScheme(t)
			now := time.Date(2026, time.September, 12, 12, 0, 0, 0, time.UTC)
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: target.NodeName, UID: types.UID(target.NodeUID)}}
			workerObjects := attachReadyManagedWorkerProof(t, device, node, now)
			objects := append([]client.Object{device, node}, workerObjects...)
			apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient, Now: func() time.Time { return now }}
			err := reconciler.revalidateFrozenTarget(context.Background(), rollout, policy, target)
			if tt.name == "unchanged" {
				if err != nil {
					t.Fatalf("unchanged target rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("changed target retained an approved grant")
			}
		})
	}
}

func TestRevalidateFrozenTargetRejectsWorkerProtocolVersionSkew(t *testing.T) {
	for _, version := range []string{"", "managed-upgrade/v0", managedprotocol.Version + "-future"} {
		t.Run(version, func(t *testing.T) {
			target := policyFenceTarget("device-a", "device-uid", "leaf-a")
			target.WorkerProtocolVersion = version
			reconciler := &IOSXESoftwareRolloutReconciler{}
			err := reconciler.revalidateFrozenTarget(
				context.Background(),
				&opsv1alpha1.IOSXESoftwareRollout{ObjectMeta: metav1.ObjectMeta{Namespace: "lab"}},
				&topologyrollout.ParsedAdminPolicy{Selector: labels.Everything()},
				target,
			)
			if err == nil || !strings.Contains(err.Error(), "does not match manager protocol") {
				t.Fatalf("revalidateFrozenTarget() error = %v, want protocol-version rejection", err)
			}
		})
	}
}

func TestContinuousHealthySoakStartsFromObservedHealthyTransition(t *testing.T) {
	completion := time.Date(2026, time.September, 11, 10, 0, 0, 0, time.UTC)
	healthyAt := completion.Add(20 * time.Minute)
	summary := opsv1alpha1.IOSXESoftwareRolloutTargetStatus{
		Phase: opsv1alpha1.IOSXESoftwareRolloutTargetSoaking, Reason: "HealthyPostMutationSoak",
		LastTransitionTime: metav1.NewTime(healthyAt),
	}
	deadline, started := continuousHealthySoakDeadline(summary, completion, 10*time.Minute)
	if !started || !deadline.Equal(healthyAt.Add(10*time.Minute)) {
		t.Fatalf("continuousHealthySoakDeadline() = %s, %v; want %s, true", deadline, started, healthyAt.Add(10*time.Minute))
	}

	summary.Reason = "PostMutationHealthGate"
	if _, started := continuousHealthySoakDeadline(summary, completion, 10*time.Minute); started {
		t.Fatal("unhealthy gate incorrectly retained the earlier healthy soak interval")
	}
}

func TestCanaryResumeRequiresControlRevisionAfterObservedGate(t *testing.T) {
	rollout := &opsv1alpha1.IOSXESoftwareRollout{
		ObjectMeta: metav1.ObjectMeta{Generation: 5},
		Spec: opsv1alpha1.IOSXESoftwareRolloutSpec{
			Control: opsv1alpha1.IOSXESoftwareRolloutControl{Revision: 9},
		},
		Status: opsv1alpha1.IOSXESoftwareRolloutStatus{
			Control: &opsv1alpha1.IOSXESoftwareRolloutControlStatus{RequestedRevision: 9},
		},
	}
	if canaryResumeAuthorized(rollout) {
		t.Fatal("a control revision made before the canary gate authorized wider rollout")
	}
	setRolloutCondition(rollout, "CanaryResumeRequired", metav1.ConditionTrue,
		"CanariesPassed", "resume required", time.Now().UTC())
	if canaryResumeAuthorized(rollout) {
		t.Fatal("the control revision observed at the canary gate authorized itself")
	}

	rollout.Generation++
	rollout.Spec.Control.Revision++
	if !canaryResumeAuthorized(rollout) {
		t.Fatal("a newer post-gate control revision did not authorize wider rollout")
	}
	rollout.Spec.Control.Pause = true
	if canaryResumeAuthorized(rollout) {
		t.Fatal("a newer paused control authorized wider rollout")
	}
	rollout.Spec.Control.Pause = false
	setRolloutCondition(rollout, "CanaryResumeRequired", metav1.ConditionFalse,
		"ResumeAuthorized", "resume authorized", time.Now().UTC())
	if !canaryResumeAuthorized(rollout) {
		t.Fatal("persisted canary resume authorization was forgotten")
	}
}

func TestTrySettleLeafIsIdempotentAfterManagerSettlement(t *testing.T) {
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
		ManagerAdmission: &opsv1alpha1.UpgradeManagerAdmissionStatus{State: opsv1alpha1.UpgradeManagerAdmissionSettled},
	}}
	settled, reason, _, err := (&IOSXESoftwareRolloutReconciler{}).trySettleLeaf(
		context.Background(), nil, nil, opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{}, leaf,
		opsv1alpha1.IOSXESoftwareRolloutTargetStatus{}, time.Now().UTC())
	if err != nil || !settled || reason != "HealthGatePassed" {
		t.Fatalf("trySettleLeaf() = settled %v, reason %q, error %v", settled, reason, err)
	}
}

func TestHealthFreshnessUsesWorkerHeartbeatAndRequiresPostOperationEvidence(t *testing.T) {
	projectionEpoch := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	completion := projectionEpoch.Add(24 * time.Hour)
	heartbeat := completion.Add(time.Minute)
	condition := &corev1.NodeCondition{
		Type: corev1.NodeReady, Status: corev1.ConditionTrue,
		LastHeartbeatTime: metav1.NewTime(heartbeat), LastTransitionTime: metav1.NewTime(projectionEpoch),
	}
	if got := nodeReadyObservation(condition); !got.Equal(heartbeat) {
		t.Fatalf("nodeReadyObservation() = %s, want heartbeat %s", got, heartbeat)
	}
	if !isPostOperationObservation(nodeReadyObservation(condition), completion) {
		t.Fatal("fresh post-operation worker heartbeat was not accepted")
	}
	condition.LastHeartbeatTime = metav1.NewTime(completion)
	if isPostOperationObservation(nodeReadyObservation(condition), completion) {
		t.Fatal("pre-existing/equal-time heartbeat was accepted as post-operation evidence")
	}
}

func TestTruncateRolloutTextPreservesUTF8(t *testing.T) {
	if got := truncateRolloutText("ready-✓-after-reload", 8); got != "ready-✓-" || !strings.Contains(got, "✓") {
		t.Fatalf("truncateRolloutText() = %q, want valid rune-bounded text", got)
	}
}

func TestRevalidateCampaignExecutionUsesValueEqualityAndRejectsNewerControl(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	requestedAt := metav1.NewTime(time.Now().UTC())
	rollout := &opsv1alpha1.IOSXESoftwareRollout{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "campaign", UID: "campaign-uid"},
		Spec: opsv1alpha1.IOSXESoftwareRolloutSpec{
			Approval: &opsv1alpha1.IOSXESoftwareRolloutApproval{PlanHash: "sha256:" + strings.Repeat("a", 64)},
			Control: opsv1alpha1.IOSXESoftwareRolloutControl{
				Revision: 1, RequestedBy: "operator", RequestedAt: &requestedAt,
			},
		},
		Status: opsv1alpha1.IOSXESoftwareRolloutStatus{FrozenPlan: &opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus{
			Hash: "sha256:" + strings.Repeat("a", 64),
		}, EffectivePolicy: &opsv1alpha1.IOSXESoftwareRolloutEffectivePolicyStatus{
			Epoch: 1, Policy: opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{
				SemanticHash: "sha256:" + strings.Repeat("b", 64),
			},
		}},
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(rollout).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	// The fake client returns a deep copy, including a distinct RequestedAt
	// pointer. Equal control values must not look like a concurrent change.
	var stored opsv1alpha1.IOSXESoftwareRollout
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: rollout.Namespace, Name: rollout.Name}, &stored); err != nil {
		t.Fatal(err)
	}
	observed := stored.DeepCopy()
	if err := reconciler.revalidateCampaignExecution(context.Background(), observed); err != nil {
		t.Fatalf("revalidateCampaignExecution() equal-value error = %v", err)
	}

	var current opsv1alpha1.IOSXESoftwareRollout
	if err := apiClient.Get(context.Background(), types.NamespacedName{Namespace: rollout.Namespace, Name: rollout.Name}, &current); err != nil {
		t.Fatal(err)
	}
	current.Spec.Control.Revision = 2
	current.Spec.Control.Pause = true
	if err := apiClient.Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	if err := reconciler.revalidateCampaignExecution(context.Background(), observed); err == nil ||
		!strings.Contains(err.Error(), "control changed") {
		t.Fatalf("revalidateCampaignExecution() changed-control error = %v", err)
	}
}

func TestPolicyChangeFenceReleasesUnclaimedAndRetainsClaimedReservation(t *testing.T) {
	targets := []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		policyFenceTarget("device-a", "device-uid-a", "leaf-a"),
		policyFenceTarget("device-b", "device-uid-b", "leaf-b"),
	}
	rollout := policyFenceRollout(targets)
	unclaimed := policyFenceLeaf(rollout, targets[0], "leaf-uid-a")
	claimed := policyFenceLeaf(rollout, targets[1], "leaf-uid-b")
	claimed.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
		Stage: opsv1alpha1.UpgradeManagedMutationPrimaryInstall, ReservationID: claimed.Status.ManagerAdmission.ReservationID,
		PolicyEpoch:     claimed.Status.ManagerAdmission.PolicyEpoch,
		ControlRevision: rollout.Spec.Control.Revision, ClaimedAt: metav1.NewTime(time.Now().UTC()),
	}}
	ledgerCM := policyFenceLedger(t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{targets[0], targets[1]},
		map[string]types.UID{targets[0].DeviceUID: unclaimed.UID, targets[1].DeviceUID: claimed.UID},
		topologyrollout.ReservationGranted)

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(rollout, unclaimed, claimed, ledgerCM).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	now := time.Now().UTC()
	currentPolicy := &topologyrollout.ParsedAdminPolicy{PolicyUID: "policy-uid", ResourceVersion: "11"}
	result, err := reconciler.reconcilePolicyChanged(context.Background(), rollout, currentPolicy, now)
	if err != nil {
		t.Fatalf("reconcilePolicyChanged() error = %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("policy fence did not retain a reconciliation retry")
	}

	var gotUnclaimed, gotClaimed opsv1alpha1.IOSXESoftwareUpgrade
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(unclaimed), &gotUnclaimed); err != nil {
		t.Fatal(err)
	}
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(claimed), &gotClaimed); err != nil {
		t.Fatal(err)
	}
	if gotUnclaimed.Status.ManagerAdmission == nil ||
		gotUnclaimed.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
		t.Fatalf("unclaimed leaf admission = %#v, want Settled", gotUnclaimed.Status.ManagerAdmission)
	}
	if gotClaimed.Status.ManagerAdmission == nil ||
		gotClaimed.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionRevoked ||
		len(gotClaimed.Status.ManagedMutationClaims) != 1 {
		t.Fatalf("claimed leaf fence = %#v, claims=%#v", gotClaimed.Status.ManagerAdmission, gotClaimed.Status.ManagedMutationClaims)
	}
	store := topologyrollout.Store{Client: apiClient, APIReader: apiClient,
		Key: types.NamespacedName{Namespace: ledgerCM.Namespace, Name: ledgerCM.Name}, ExpectedUID: ledgerCM.UID}
	_, ledger, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := ledger.Reservations[unclaimed.Status.ManagerAdmission.ReservationID]; exists {
		t.Fatal("unclaimed policy-drift reservation was not released after its leaf CAS fence")
	}
	if reservation, exists := ledger.Reservations[claimed.Status.ManagerAdmission.ReservationID]; !exists ||
		reservation.ChildUID != string(claimed.UID) {
		t.Fatalf("claimed reservation was not durably retained: %#v", reservation)
	}

	var gotRollout opsv1alpha1.IOSXESoftwareRollout
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &gotRollout); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(gotRollout.Status.Conditions, "PolicyChanged")
	if gotRollout.Status.Phase != opsv1alpha1.IOSXESoftwareRolloutPhasePaused || condition == nil ||
		condition.Status != metav1.ConditionTrue || condition.Reason != "ReplanRequired" {
		t.Fatalf("policy-change campaign status = phase %q, condition %#v", gotRollout.Status.Phase, condition)
	}
}

func TestPolicyChangeFenceCreatesTombstoneForReservationBeforeChildCrash(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid-a", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	ledgerCM := policyFenceLedger(t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target}, nil,
		topologyrollout.ReservationReserved)
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(rollout, ledgerCM).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, inner client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if leaf, ok := obj.(*opsv1alpha1.IOSXESoftwareUpgrade); ok && leaf.UID == "" {
					leaf.UID = types.UID("policy-tombstone-uid")
				}
				return inner.Create(ctx, obj, opts...)
			},
		}).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	if err := reconciler.ensurePolicyChangeFences(context.Background(), rollout, time.Now().UTC()); err != nil {
		t.Fatalf("ensurePolicyChangeFences() error = %v", err)
	}

	var tombstone opsv1alpha1.IOSXESoftwareUpgrade
	key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.ChildName}
	if err := apiClient.Get(context.Background(), key, &tombstone); err != nil {
		t.Fatalf("read retained policy tombstone: %v", err)
	}
	if tombstone.UID != "policy-tombstone-uid" || tombstone.Status.ManagerAdmission == nil ||
		tombstone.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
		t.Fatalf("retained policy tombstone = uid %q admission %#v", tombstone.UID, tombstone.Status.ManagerAdmission)
	}
	store := topologyrollout.Store{Client: apiClient, APIReader: apiClient,
		Key: types.NamespacedName{Namespace: ledgerCM.Namespace, Name: ledgerCM.Name}, ExpectedUID: ledgerCM.UID}
	_, ledger, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Reservations) != 0 {
		t.Fatalf("reservation-before-child crash was not safely settled: %#v", ledger.Reservations)
	}
}

func TestReconcileFencesPublishedGrantBeforeParsingChangedPolicy(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid-a", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Finalizers = []string{rolloutSafetyFinalizer}
	leaf := policyFenceLeaf(rollout, target, "leaf-uid-a")
	ledgerCM := policyFenceLedger(t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target},
		map[string]types.UID{target.DeviceUID: leaf.UID}, topologyrollout.ReservationGranted)
	// The policy identity changed and its body is deliberately malformed. The
	// controller must fence the published grant from object identity before it
	// attempts policy decoding.
	policyCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: rollout.Status.FrozenPlan.Policy.Namespace,
		Name:      rollout.Status.FrozenPlan.Policy.Name,
		UID:       "replacement-policy-uid", ResourceVersion: "11",
	}, Data: map[string]string{topologyrollout.PolicyDataKey: "not-json"}}

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(rollout, leaf, ledgerCM, policyCM).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{
		Client: apiClient, APIReader: apiClient,
		TopologyPolicyNamespace: policyCM.Namespace, TopologyPolicyName: policyCM.Name,
	}
	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(rollout)})
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("Reconcile() did not retain a policy-fence retry")
	}

	var gotLeaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &gotLeaf); err != nil {
		t.Fatal(err)
	}
	if gotLeaf.Status.ManagerAdmission == nil || gotLeaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
		t.Fatalf("changed-policy grant was not fenced: %#v", gotLeaf.Status.ManagerAdmission)
	}
	var gotRollout opsv1alpha1.IOSXESoftwareRollout
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &gotRollout); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(gotRollout.Status.Conditions, "PolicyChanged")
	if condition == nil || condition.Status != metav1.ConditionTrue || condition.Reason != "ReplanRequired" {
		t.Fatalf("PolicyChanged condition = %#v", condition)
	}
}

func TestSourceIdentityChangeFencesUnclaimedGrant(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid-a", "leaf-a")
	target.Source = opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot{
		Name: "lab-sftp", URL: "sftp://images.example.test/cat9k.bin", SHA256: strings.Repeat("a", 64),
		SecretName: "image-source", SecretUID: "source-uid",
	}
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Spec.Plan.Image.Sources[0] = opsv1alpha1.IOSXESoftwareRolloutSourceSpec{
		Name: "lab-sftp", Priority: 100, URL: target.Source.URL,
		URLSecretRef: &corev1.LocalObjectReference{Name: target.Source.SecretName},
	}
	leaf := policyFenceLeaf(rollout, target, "leaf-uid-a")
	ledgerCM := policyFenceLedger(t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target},
		map[string]types.UID{target.DeviceUID: leaf.UID}, topologyrollout.ReservationGranted)

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := ciskov1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(rollout, leaf, ledgerCM).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	result, err := reconciler.reconcileSourceChanged(context.Background(), rollout,
		"source Secret incarnation changed", time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcileSourceChanged() error = %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("source identity fence did not retain a reconciliation retry")
	}

	var gotLeaf opsv1alpha1.IOSXESoftwareUpgrade
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &gotLeaf); err != nil {
		t.Fatal(err)
	}
	if gotLeaf.Status.ManagerAdmission == nil || gotLeaf.Status.ManagerAdmission.State != opsv1alpha1.UpgradeManagerAdmissionSettled {
		t.Fatalf("source-drift leaf admission = %#v, want Settled", gotLeaf.Status.ManagerAdmission)
	}
	store := topologyrollout.Store{Client: apiClient, APIReader: apiClient,
		Key: types.NamespacedName{Namespace: ledgerCM.Namespace, Name: ledgerCM.Name}, ExpectedUID: ledgerCM.UID}
	if _, ledger, err := store.Read(context.Background()); err != nil {
		t.Fatal(err)
	} else if len(ledger.Reservations) != 0 {
		t.Fatalf("source-drift reservation was not released: %#v", ledger.Reservations)
	}
	var gotRollout opsv1alpha1.IOSXESoftwareRollout
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &gotRollout); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(gotRollout.Status.Conditions, "SourceChanged")
	if gotRollout.Status.Phase != opsv1alpha1.IOSXESoftwareRolloutPhasePaused || condition == nil ||
		condition.Status != metav1.ConditionTrue || condition.Reason != "ReplanRequired" {
		t.Fatalf("source-change campaign status = phase %q, condition %#v", gotRollout.Status.Phase, condition)
	}
}

func TestFreezeSourceBindsCampaignDigestSecretUIDAndEndpointAuthorization(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "lab", Name: "image-source", UID: types.UID("source-uid"), ResourceVersion: "10",
			Labels: map[string]string{"cisco.vk/purpose": "software-image-source"},
		},
		Data: map[string][]byte{
			"password": []byte("must-not-enter-status"),
			softwareupgrade.URLSecretAllowedSchemeKey: []byte("sftp"),
			softwareupgrade.URLSecretAllowedHostKey:   []byte("images.example.test"),
			softwareupgrade.URLSecretAllowedPortKey:   []byte("22"),
		},
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	rollout := &opsv1alpha1.IOSXESoftwareRollout{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lab"},
		Spec: opsv1alpha1.IOSXESoftwareRolloutSpec{Plan: opsv1alpha1.IOSXESoftwareRolloutPlan{
			Image: opsv1alpha1.IOSXESoftwareRolloutImageSpec{
				SHA256: strings.Repeat("a", 64), ImageFamily: "cat9k",
				Sources: []opsv1alpha1.IOSXESoftwareRolloutSourceSpec{{
					Name: "lab-sftp", Priority: 100, URL: "sftp://images.example.test/cat9k.bin",
					URLSecretRef: &corev1.LocalObjectReference{Name: secret.Name},
				}},
			},
		}},
	}
	snapshot, err := reconciler.freezeSource(
		context.Background(), rollout.Namespace, rollout.Spec.Plan.Image, rollout.Spec.Plan.Image.Sources[0],
	)
	if err != nil {
		t.Fatalf("freezeSource() error = %v", err)
	}
	if snapshot.SecretName != secret.Name || snapshot.SecretUID != string(secret.UID) {
		t.Fatalf("freezeSource() identity = %+v, want name/UID", snapshot)
	}
	if snapshot.SHA256 != rollout.Spec.Plan.Image.SHA256 {
		t.Fatalf("freezeSource() SHA256 = %q, want campaign digest %q", snapshot.SHA256, rollout.Spec.Plan.Image.SHA256)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "must-not-enter-status") {
		t.Fatal("freezeSource() exposed Secret data")
	}
}

func TestVerifyFrozenSourceAllowsSameUIDCredentialRotation(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid-a", "leaf-a")
	target.Source = opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot{
		Name: "lab-sftp", Priority: 100, URL: "sftp://images.example.test/cat9k.bin", SHA256: strings.Repeat("a", 64),
		SecretName: "image-source", SecretUID: "source-uid",
	}
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: rollout.Namespace, Name: "image-source", UID: types.UID("source-uid"), ResourceVersion: "11",
		Labels: map[string]string{"cisco.vk/purpose": "software-image-source"},
	}, Data: map[string][]byte{
		softwareupgrade.URLSecretAllowedSchemeKey: []byte("sftp"),
		softwareupgrade.URLSecretAllowedHostKey:   []byte("images.example.test"),
		softwareupgrade.URLSecretAllowedPortKey:   []byte("22"),
	}}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	if err := reconciler.verifyFrozenSource(context.Background(), rollout); err != nil {
		t.Fatalf("verifyFrozenSource() same-UID rotation error = %v", err)
	}
}

func TestVerifyFrozenSourceRejectsSecretReplacementOrEndpointChange(t *testing.T) {
	for _, test := range []struct {
		name string
		uid  types.UID
		host string
		want string
	}{
		{name: "replacement UID", uid: "replacement-uid", host: "images.example.test", want: "incarnation changed"},
		{name: "changed endpoint", uid: "source-uid", host: "other.example.test", want: "endpoint authorization changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := policyFenceTarget("device-a", "device-uid-a", "leaf-a")
			target.Source = opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot{
				Name: "lab-sftp", Priority: 100, URL: "sftp://images.example.test/cat9k.bin", SHA256: strings.Repeat("a", 64),
				SecretName: "image-source", SecretUID: "source-uid",
			}
			rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Namespace: rollout.Namespace, Name: "image-source", UID: test.uid,
				Labels: map[string]string{"cisco.vk/purpose": "software-image-source"},
			}, Data: map[string][]byte{
				softwareupgrade.URLSecretAllowedSchemeKey: []byte("sftp"),
				softwareupgrade.URLSecretAllowedHostKey:   []byte(test.host),
				softwareupgrade.URLSecretAllowedPortKey:   []byte("22"),
			}}
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			apiClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
			reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
			if err := reconciler.verifyFrozenSource(context.Background(), rollout); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verifyFrozenSource() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLeafMutationOutcomeRequiresConclusiveDeviceEvidenceAfterClaim(t *testing.T) {
	completion := metav1.NewTime(time.Now().UTC())
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{Status: opsv1alpha1.IOSXESoftwareUpgradeStatus{
		Phase: opsv1alpha1.UpgradePhaseRebootTimeout, CompletionTime: &completion,
		ManagedMutationClaims: []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
			Stage: opsv1alpha1.UpgradeManagedMutationPrimaryActivation,
		}},
		WorkerControl: &opsv1alpha1.UpgradeWorkerControlStatus{
			EffectiveState: opsv1alpha1.UpgradeWorkerControlSettled,
		},
		Conditions: []metav1.Condition{{
			Type: "DeviceMutationSettled", Status: metav1.ConditionFalse,
			Reason: "MutationOutcomeUnresolved", LastTransitionTime: completion,
		}},
	}}
	if leafMutationOutcomeResolved(leaf) {
		t.Fatal("worker-control settlement released an ambiguous claimed mutation")
	}
	leaf.Status.Conditions[0].Status = metav1.ConditionTrue
	if !leafMutationOutcomeResolved(leaf) {
		t.Fatal("conclusive device-mutation evidence was not accepted")
	}
	leaf.Status.ManagedMutationClaims = nil
	leaf.Status.Conditions = nil
	if !leafMutationOutcomeResolved(leaf) {
		t.Fatal("terminal leaf with no device mutation claim should be settled")
	}
}

func TestRolloutControlStatusDoesNotReportPauseBeforeWorkerAcknowledgement(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid-a", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Spec.Control.Pause = true
	leaf := policyFenceLeaf(rollout, target, "leaf-uid-a")
	leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionPending
	leaf.Status.WorkerControl = nil

	scheme := runtime.NewScheme()
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(rollout, leaf).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	status := reconciler.rolloutControlStatus(context.Background(), rollout)
	if !status.PausePending || status.Paused || status.EffectiveRevision != 0 {
		t.Fatalf("unacknowledged pause status = %#v", status)
	}

	var current opsv1alpha1.IOSXESoftwareUpgrade
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(leaf), &current); err != nil {
		t.Fatal(err)
	}
	current.Status.WorkerControl = &opsv1alpha1.UpgradeWorkerControlStatus{
		ObservedAdmissionState:  opsv1alpha1.UpgradeManagerAdmissionPending,
		ObservedControlRevision: rollout.Spec.Control.Revision,
		EffectiveState:          opsv1alpha1.UpgradeWorkerControlPaused,
		UpdatedAt:               metav1.NewTime(time.Now().UTC()),
	}
	if err := apiClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	status = reconciler.rolloutControlStatus(context.Background(), rollout)
	if status.PausePending || !status.Paused || status.EffectiveRevision != rollout.Spec.Control.Revision {
		t.Fatalf("acknowledged pause status = %#v", status)
	}
}

func TestRolloutControlStatusTreatsSettledCancellationTombstoneAsEffective(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid-a", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	rollout.Spec.Control.Cancel = true
	rollout.Status.Phase = opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled
	leaf := policyFenceLeaf(rollout, target, "leaf-uid-a")
	leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionSettled
	leaf.Status.WorkerControl = nil

	scheme := runtime.NewScheme()
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(rollout, leaf).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	status := reconciler.rolloutControlStatus(context.Background(), rollout)
	if status.CancellationPending || !status.Cancelled || status.EffectiveRevision != rollout.Spec.Control.Revision {
		t.Fatalf("settled cancellation status = %#v", status)
	}
}

func TestCancellationDoesNotRevokeAnAlreadySettledClaim(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid-a", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	leaf := policyFenceLeaf(rollout, target, "leaf-uid-a")
	leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionSettled
	leaf.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
		Stage: opsv1alpha1.UpgradeManagedMutationPrimaryInstall, ReservationID: leaf.Status.ManagerAdmission.ReservationID,
		PolicyEpoch:     leaf.Status.ManagerAdmission.PolicyEpoch,
		ControlRevision: rollout.Spec.Control.Revision, ClaimedAt: metav1.NewTime(time.Now().UTC()),
	}}
	rollout.Spec.Control.Revision++
	rollout.Spec.Control.Cancel = true
	ledgerCM := policyFenceLedger(t, rollout, nil, nil, topologyrollout.ReservationGranted)

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&opsv1alpha1.IOSXESoftwareRollout{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(rollout, leaf, ledgerCM).Build()
	reconciler := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	result, err := reconciler.reconcileCancellation(context.Background(), rollout, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("reconcileCancellation() error = %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Fatalf("settled cancellation result = %#v, want terminal result", result)
	}
	var current opsv1alpha1.IOSXESoftwareRollout
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(rollout), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Phase != opsv1alpha1.IOSXESoftwareRolloutPhaseCancelled || current.Status.Counts.Cancelled != 1 {
		t.Fatalf("settled cancellation status = phase %q counts %#v", current.Status.Phase, current.Status.Counts)
	}
}

func policyFenceTarget(deviceName, deviceUID, childName string) opsv1alpha1.IOSXESoftwareRolloutPlannedTarget {
	return opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{
		DeviceName: deviceName, DeviceUID: deviceUID, DeviceGeneration: 1,
		PhysicalIdentity: "serial-" + deviceName,
		NodeName:         deviceName, NodeUID: "node-uid-" + deviceName, Driver: string(ciskov1.DeviceDriverXE),
		ImageFamily: "cat9k",
		Source: opsv1alpha1.IOSXESoftwareRolloutSourceSnapshot{
			Name: "global", Priority: 100, URL: "https://images.example.test/cat9k.bin", SHA256: strings.Repeat("a", 64),
		},
		QualificationCohort: "c9300", WorkerProtocolVersion: managedprotocol.Version,
		ProjectionHash: "sha256:" + strings.Repeat("a", 64),
		Topology:       []opsv1alpha1.IOSXESoftwareRolloutTopologyValue{{Key: "topology.cisco.vk/site", Value: "site-a"}},
		ChildName:      childName,
	}
}

func policyFenceRollout(targets []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) *opsv1alpha1.IOSXESoftwareRollout {
	rollout := &opsv1alpha1.IOSXESoftwareRollout{
		ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "campaign", UID: "campaign-uid"},
		Spec: opsv1alpha1.IOSXESoftwareRolloutSpec{
			Plan: opsv1alpha1.IOSXESoftwareRolloutPlan{
				Image: opsv1alpha1.IOSXESoftwareRolloutImageSpec{
					SHA256: strings.Repeat("a", 64), ImageFamily: "cat9k",
					Sources: []opsv1alpha1.IOSXESoftwareRolloutSourceSpec{{
						Name: "global", Priority: 100, URL: "https://images.example.test/cat9k.bin",
					}},
				},
				TargetVersion: "17.18.4", Strategy: opsv1alpha1.IOSXESoftwareRolloutStrategyReload,
			},
			Control: opsv1alpha1.IOSXESoftwareRolloutControl{Revision: 7},
		},
		Status: opsv1alpha1.IOSXESoftwareRolloutStatus{
			Phase: opsv1alpha1.IOSXESoftwareRolloutPhaseExecuting,
			FrozenPlan: &opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus{
				Hash: "sha256:" + strings.Repeat("b", 64),
				Policy: opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{
					Namespace: "cvk-system", Name: "topology-policy", UID: "policy-uid", ResourceVersion: "10",
					SemanticHash: "sha256:" + strings.Repeat("c", 64), StructuralHash: "sha256:" + strings.Repeat("d", 64),
					MaxTargets: 100, MaxConcurrentTransfers: 1, MaxUnavailable: 1, HealthFreshnessSeconds: 300,
					LedgerNamespace: "cvk-system", LedgerName: "topology-ledger", LedgerUID: "ledger-uid",
					MaxActiveReservations: 256, MaxLedgerSizeBytes: 256 * 1024,
				},
				Targets: targets,
			},
		},
	}
	rollout.Status.EffectivePolicy = &opsv1alpha1.IOSXESoftwareRolloutEffectivePolicyStatus{
		Epoch: 1, Policy: rollout.Status.FrozenPlan.Policy, UpdatedAt: metav1.NewTime(time.Now().UTC()),
	}
	return rollout
}

func policyFenceLeaf(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	uid types.UID,
) *opsv1alpha1.IOSXESoftwareUpgrade {
	worker := "system:serviceaccount:" + rollout.Namespace + ":cvk-" + target.DeviceName
	revision := rollout.Spec.Control.Revision
	leaf := &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: rollout.Namespace, Name: target.ChildName, UID: uid,
			Labels:      map[string]string{managedprotocol.AnnotationCampaignUID: string(rollout.UID)},
			Annotations: expectedLeafAnnotations(rollout, target, worker)},
		Spec: expectedLeafSpec(rollout, target),
	}
	leaf.Status.ManagerAdmission = &opsv1alpha1.UpgradeManagerAdmissionStatus{
		ProtocolVersion: opsv1alpha1.ManagedUpgradeProtocolRolloutV1, State: opsv1alpha1.UpgradeManagerAdmissionGranted,
		CampaignUID: string(rollout.UID), PlanHash: rollout.Status.FrozenPlan.Hash,
		PolicyUID: rollout.Status.FrozenPlan.Policy.UID, PolicyResourceVersion: rollout.Status.FrozenPlan.Policy.ResourceVersion,
		PolicyEpoch:    rollout.Status.EffectivePolicy.Epoch,
		LedgerUID:      rollout.Status.FrozenPlan.Policy.LedgerUID,
		ReservationID:  reservationID(string(rollout.UID), target.DeviceUID),
		TopologyLockID: strings.Repeat("1", 32), LeafUID: string(uid),
		DeviceUID: target.DeviceUID, DeviceGeneration: target.DeviceGeneration,
		PhysicalIdentity: target.PhysicalIdentity, NodeUID: target.NodeUID,
		ControlRevision: &revision, UpdatedAt: metav1.NewTime(time.Now().UTC()),
	}
	leaf.Status.ManagerControl = &opsv1alpha1.UpgradeManagerControlStatus{
		Revision: revision, UpdatedAt: metav1.NewTime(time.Now().UTC()), Reason: "CampaignControl",
	}
	return leaf
}

func policyFenceLedger(
	t *testing.T,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	targets []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	childUIDs map[string]types.UID,
	state topologyrollout.ReservationState,
) *corev1.ConfigMap {
	t.Helper()
	ledger, err := topologyrollout.NewLedger(rollout.Status.FrozenPlan.Policy.LedgerUID)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		id := reservationID(string(rollout.UID), target.DeviceUID)
		ledger.Reservations[id] = topologyrollout.Reservation{
			ReservationRequest: topologyrollout.ReservationRequest{
				ID: id, CampaignUID: string(rollout.UID), PlanHash: rollout.Status.FrozenPlan.Hash,
				PolicyUID: rollout.Status.FrozenPlan.Policy.UID, PolicyVersion: rollout.Status.FrozenPlan.Policy.ResourceVersion, PolicyEpoch: 1,
				TopologyLockID: strings.Repeat("1", 32),
				PhysicalID:     target.PhysicalIdentity, DeviceUID: target.DeviceUID, NodeUID: target.NodeUID,
				ChildNamespace: rollout.Namespace, ChildName: target.ChildName, Domains: targetDomains(target),
				ControlRevision: uint64(rollout.Spec.Control.Revision),
			},
			ChildUID: string(childUIDs[target.DeviceUID]), State: state,
		}
	}
	encoded, err := topologyrollout.Encode(ledger, topologyrollout.DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatal(err)
	}
	return &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Namespace: rollout.Status.FrozenPlan.Policy.LedgerNamespace,
		Name:      rollout.Status.FrozenPlan.Policy.LedgerName, UID: types.UID(rollout.Status.FrozenPlan.Policy.LedgerUID),
	}, Data: map[string]string{topologyrollout.LedgerDataKey: string(encoded)}}
}

func rolloutPolicyFixture() (*opsv1alpha1.IOSXESoftwareRollout, *topologyrollout.ParsedAdminPolicy) {
	const siteKey = "topology.cisco.vk/site"
	rollout := &opsv1alpha1.IOSXESoftwareRollout{
		Spec: opsv1alpha1.IOSXESoftwareRolloutSpec{
			Plan: opsv1alpha1.IOSXESoftwareRolloutPlan{
				Targets: opsv1alpha1.IOSXESoftwareRolloutTargetSpec{MaxTargets: 100},
				Health:  opsv1alpha1.IOSXESoftwareRolloutHealthSpec{MaxObservationAgeSeconds: 300},
			},
		},
		Status: opsv1alpha1.IOSXESoftwareRolloutStatus{
			FrozenPlan: &opsv1alpha1.IOSXESoftwareRolloutFrozenPlanStatus{
				Policy: opsv1alpha1.IOSXESoftwareRolloutPolicySnapshot{
					Namespace: "cvk-system", Name: "topology-policy", UID: "policy-uid", ResourceVersion: "10",
					MaxTargets: 100, MaxConcurrentTransfers: 3, MaxUnavailable: 2,
					Domains: []opsv1alpha1.IOSXESoftwareRolloutDomainBudget{{
						TopologyKey: siteKey, MaxConcurrentTransfers: int32Pointer(2), MaxUnavailable: int32Pointer(1),
					}},
					HealthFreshnessSeconds: 300,
					LedgerNamespace:        "cvk-system", LedgerName: "topology-ledger", LedgerUID: "ledger-uid",
					MaxActiveReservations: 256, MaxLedgerSizeBytes: 256 * 1024,
				},
				Targets: []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{{
					DeviceName: "device-a", DeviceUID: "device-uid-a",
					Topology: []opsv1alpha1.IOSXESoftwareRolloutTopologyValue{{Key: siteKey, Value: "site-a"}},
				}},
			},
		},
	}
	policy := &topologyrollout.ParsedAdminPolicy{
		Namespace: "cvk-system", Name: "topology-policy", PolicyUID: "policy-uid", ResourceVersion: "11",
		LedgerUID: "ledger-uid", HealthFreshness: 5 * time.Minute,
		Config: topologyrollout.AdminPolicyConfig{
			Version:                      topologyrollout.PolicyVersion,
			FleetSelector:                metav1.LabelSelector{MatchLabels: map[string]string{"topology.cisco.vk/managed": "true"}},
			RequiredTopologyKeys:         []string{siteKey},
			GlobalMaxConcurrentTransfers: 3, GlobalMaxUnavailable: 2,
			DomainMaxConcurrentTransfers: map[string]int{siteKey: 2}, DomainMaxUnavailable: map[string]int{siteKey: 1},
			HealthFreshnessSeconds: 300, MaxCampaignTargets: 100, MaxActiveReservations: 256,
			MaxLedgerBytes: 256 * 1024, LedgerName: "topology-ledger",
		},
	}
	semanticHash, structuralHash, err := topologyrollout.AdminPolicyHashes(policy.Config)
	if err != nil {
		panic(err)
	}
	rollout.Status.FrozenPlan.Policy.SemanticHash = semanticHash
	rollout.Status.FrozenPlan.Policy.StructuralHash = structuralHash
	rollout.Status.EffectivePolicy = &opsv1alpha1.IOSXESoftwareRolloutEffectivePolicyStatus{
		Epoch: 1, Policy: rollout.Status.FrozenPlan.Policy, UpdatedAt: metav1.NewTime(time.Now().UTC()),
	}
	return rollout, policy
}

func int32Pointer(value int32) *int32 { return &value }
