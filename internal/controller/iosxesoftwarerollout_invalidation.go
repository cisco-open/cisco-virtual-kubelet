// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/softwareupgrade"
)

// Recovery does not reopen admission or mutate the device. Only the already
// bound network worker may append native proof and retire preparation ownership.
func (r *IOSXESoftwareRolloutReconciler) reconcilePreparationInvalidation(ctx context.Context, rollout *ops.IOSXESoftwareRollout, now time.Time) (ctrl.Result, error) {
	request := rollout.Spec.PreparationInvalidation
	if request == nil || rollout.Status.FrozenPlan == nil || !rollout.Spec.Control.Cancel ||
		rollout.Status.Phase != ops.IOSXESoftwareRolloutPhaseCancelled ||
		rollout.Spec.Plan.Strategy != ops.IOSXESoftwareRolloutStrategyPrepareOnly || rollout.Spec.ActivationApproval != nil ||
		request.PlanHash != rollout.Status.FrozenPlan.Hash || request.RequestedAt.IsZero() ||
		request.RequestedAt.Time.After(now) || strings.TrimSpace(request.RequestedBy) == "" || strings.TrimSpace(request.Reason) == "" ||
		len(request.Receipts) == 0 {
		return ctrl.Result{}, fmt.Errorf("preparation invalidation requires cancelled, never-activated frozen intent and exact receipt references")
	}
	var leaves ops.IOSXESoftwareUpgradeList
	if err := r.reader().List(ctx, &leaves, client.InNamespace(rollout.Namespace)); err != nil {
		return ctrl.Result{}, err
	}
	byName := make(map[string]*ops.IOSXESoftwareUpgrade, len(leaves.Items))
	consumers := make(map[string]bool)
	for i := range leaves.Items {
		leaf := &leaves.Items[i]
		byName[leaf.Name] = leaf
		if uid := leaf.Annotations[managedprotocol.AnnotationPreparedUpgradeUID]; uid != "" {
			consumers[uid] = true
		}
	}
	type invalidation struct {
		leaf    *ops.IOSXESoftwareUpgrade
		request *ops.UpgradePreparedInvalidationRequest
	}
	updates := make([]invalidation, 0, len(request.Receipts))
	seen := make(map[string]bool)
	// Validate the entire requested subset before publishing any authority.
	for _, reference := range request.Receipts {
		if seen[reference.DeviceUID] {
			return ctrl.Result{}, fmt.Errorf("duplicate invalidation device UID")
		}
		seen[reference.DeviceUID] = true
		var leaf *ops.IOSXESoftwareUpgrade
		for _, target := range rollout.Status.FrozenPlan.Targets {
			if target.DeviceUID == reference.DeviceUID {
				leaf = byName[target.ChildName]
				if leaf != nil && leaf.Status.ManagerDrain != nil {
					if err := validateManagerDrainBinding(rollout, target, leaf); err != nil {
						return ctrl.Result{}, err
					}
					if err := validateDrainPodsComplete(leaf.Status.ManagerDrain); err != nil {
						return ctrl.Result{}, err
					}
				}
				if leaf != nil && (leaf.Status.PreparedReceipt == nil ||
					leaf.Status.PreparedReceipt.NodeUID != target.NodeUID ||
					leaf.Status.PreparedReceipt.PhysicalIdentity != target.PhysicalIdentity ||
					leaf.Status.PreparedReceipt.DeviceGeneration != target.DeviceGeneration) {
					return ctrl.Result{}, fmt.Errorf("invalidation receipt no longer binds frozen target")
				}
				break
			}
		}
		if leaf == nil || string(leaf.UID) != reference.UpgradeUID || consumers[reference.UpgradeUID] ||
			leaf.Status.PreparedReceipt == nil || leaf.Status.PreparedReceipt.DeviceUID != reference.DeviceUID {
			return ctrl.Result{}, fmt.Errorf("invalidation leaf is missing, replaced, or referenced by activation")
		}
		authority := &ops.UpgradePreparedInvalidationRequest{
			ReceiptHash: reference.ReceiptHash, PlanHash: request.PlanHash, CampaignUID: string(rollout.UID),
			ControlRevision: rollout.Spec.Control.Revision, RequestedBy: request.RequestedBy,
			RequestedAt: request.RequestedAt, Reason: request.Reason,
		}
		if leaf.Status.ManagerInvalidation != nil {
			// Later cancellation revisions do not rewrite the original authority.
			authority.ControlRevision = leaf.Status.ManagerInvalidation.ControlRevision
			if !reflect.DeepEqual(authority, leaf.Status.ManagerInvalidation) {
				return ctrl.Result{}, fmt.Errorf("invalidation authority is immutable")
			}
		}
		candidate := leaf.DeepCopy()
		candidate.Status.ManagerInvalidation = authority
		if err := softwareupgrade.ValidatePreparedInvalidationRequest(candidate); err != nil {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, err
		}
		updates = append(updates, invalidation{leaf, authority})
	}
	complete := true
	for _, update := range updates {
		if softwareupgrade.PreparedReceiptInvalidated(update.leaf) {
			continue
		}
		complete = false
		if err := r.patchLeafManagerFields(ctx, client.ObjectKeyFromObject(update.leaf), func(current *ops.IOSXESoftwareUpgrade) error {
			if current.UID != update.leaf.UID ||
				(current.Status.ManagerInvalidation != nil && !reflect.DeepEqual(current.Status.ManagerInvalidation, update.request)) {
				return fmt.Errorf("invalidation leaf or authority changed")
			}
			current.Status.ManagerInvalidation = update.request
			return softwareupgrade.ValidatePreparedInvalidationRequest(current)
		}); err != nil {
			return ctrl.Result{}, err
		}
	}
	before := rollout.DeepCopy()
	status, reason, message := metav1.ConditionFalse, "NativeProofPending", "waiting for bound workers to prove quiescence and unchanged running OS; ownership remains retained"
	if complete {
		status, reason, message = metav1.ConditionTrue, "NativeProofRecorded", "requested preparations invalidated; original receipts retained; no device mutation performed"
	}
	setRolloutCondition(rollout, "PreparationInvalidated", status, reason, message, now)
	if !reflect.DeepEqual(before.Status.Conditions, rollout.Status.Conditions) {
		if err := r.Status().Patch(ctx, rollout, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return ctrl.Result{}, err
		}
	}
	if !complete {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}
