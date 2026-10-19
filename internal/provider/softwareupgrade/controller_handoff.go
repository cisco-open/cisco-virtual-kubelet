// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package softwareupgrade

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerhandoff"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *Reconciler) runControllerHandoff(ctx context.Context, up *ops.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	wait := reconcile.Result{RequeueAfter: 15 * time.Second}
	if !r.ManagedTopology || r.MutationLeaser == nil || r.BeforeMutation == nil || !controllerhandoff.KnownExecution(up) {
		return wait, errors.New("controller handoff requires managed maintenance and its execution marker")
	}
	if up.Status.PreviousVersion == "" {
		gc, err := r.gnoiClient(ctx)
		if err != nil {
			return wait, err
		}
		observed, err := verifyOS(ctx, gc)
		if err != nil {
			return wait, err
		}
		if observed.Version == "" || observed.IndividualSupervisorInstall || observed.ActivationFailMessage != "" {
			return wait, errors.New("SWIM requires a healthy single-supervisor native baseline")
		}
		return r.updateStatus(ctx, up, func(cur *ops.IOSXESoftwareUpgrade) {
			cur.Status.PreviousVersion = observed.Version
			cur.Status.RunningVersion = observed.Version
		}, wait)
	}
	h, err := r.ensureControllerHandoff(ctx, up)
	if err != nil {
		return wait, err
	}
	if h.Status.Phase == "Succeeded" {
		grant := up.Status.ControllerHandoff
		if grant == nil || grant.UID != string(h.UID) || grant.VerifiedAt == nil || !versionMatches(grant.VerifiedVersion, up.Spec.TargetVersion) {
			return wait, errors.New("controller success has no bound native verification")
		}
		if controllerutil.ContainsFinalizer(h, controllerhandoff.Finalizer) {
			controllerutil.RemoveFinalizer(h, controllerhandoff.Finalizer)
			if err := r.Client.Update(ctx, h); err != nil {
				return wait, err
			}
		}
		return r.terminalAfterMutation(ctx, up, ops.UpgradePhaseSucceeded, "ControllerUpgradeVerified", "Catalyst Center upgrade completed and native device inventory verified", now)
	}
	if h.Status.Phase == "PreparationCancelled" {
		if !controllerhandoff.PreparationCancellationSettled(up, h) {
			return wait, errors.New("controller preparation cancellation is not settled")
		}
		gc, err := r.gnoiClient(ctx)
		if err != nil {
			return wait, err
		}
		observed, err := verifyOS(ctx, gc)
		if err != nil {
			return wait, err
		}
		if observed.Version == "" || observed.Version != up.Status.PreviousVersion || observed.IndividualSupervisorInstall || observed.ActivationFailMessage != "" {
			return wait, errors.New("cancelled preparation requires the unchanged healthy baseline")
		}
		observer, ok := r.Lifecycle.(softwarelifecycle.SWIMPreparationObserver)
		if !ok {
			return wait, errors.New("cancelled preparation requires native observation")
		}
		snapshot, err := observer.ObserveSWIMFlash(ctx, softwarelifecycle.SWIMFlashRequest{RunningVersion: observed.Version})
		if err != nil {
			return wait, err
		}
		for _, f := range up.Status.ControllerHandoff.Preparation[0].Files {
			if _, present := snapshot.Files[f.Path]; present {
				return wait, errors.New("cleanup absence no longer verified")
			}
		}
		// Keep the handoff journal/finalizer as audit evidence. The normal
		// terminal settlement and manager health gate release maintenance.
		return r.updateTerminalStatus(ctx, up, func(cur *ops.IOSXESoftwareUpgrade) {
			cur.Status.Phase = ops.UpgradePhaseCancelled
			cur.Status.FailureReason = "ControllerPreparationCancelled"
			cur.Status.Message = "preparation and inventory synchronization settled; no distribution or activation dispatched; native baseline verified"
			cur.Status.CompletionTime = &metav1.Time{Time: now}
			cur.Status.ControllerHandoff.VerifiedVersion = observed.Version
			cur.Status.ControllerHandoff.VerifiedAt = &metav1.Time{Time: now}
			cur.Status.RunningVersion = observed.Version
			r.setCondition(cur, conditionTypeMutationSettled, metav1.ConditionTrue, "PreparationCancellationVerified", cur.Status.Message, now)
			r.setReady(cur, metav1.ConditionFalse, cur.Status.FailureReason, cur.Status.Message, now)
		}, wait)
	}
	if h.Status.Phase == "Verifying" {
		gc, err := r.gnoiClient(ctx)
		if err != nil {
			return wait, err
		}
		observed, err := verifyOS(ctx, gc)
		if err != nil {
			r.resetGNOIClientIfTransient(ctx, err)
			return wait, err
		}
		if observed.ActivationFailMessage != "" || observed.IndividualSupervisorInstall || !versionMatches(observed.Version, up.Spec.TargetVersion) || !versionMatches(observed.Version, h.Spec.Source.ImageVersion) {
			return wait, errors.New("device has not verified the pinned controller target")
		}
		image, err := r.inspectTarget(ctx, up.Spec.TargetVersion)
		if err != nil {
			return wait, err
		}
		if image.State != softwarelifecycle.InventoryStateProvisionedCommitted || !versionMatches(observed.Version, image.Version) {
			return wait, errors.New("native target is not committed")
		}
		return r.updateStatus(ctx, up, func(cur *ops.IOSXESoftwareUpgrade) {
			if cur.Status.ControllerHandoff != nil && cur.Status.ControllerHandoff.UID == string(h.UID) {
				cur.Status.ControllerHandoff.VerifiedVersion = observed.Version
				cur.Status.ControllerHandoff.VerifiedAt = &metav1.Time{Time: r.now()}
				cur.Status.RunningVersion = observed.Version
			}
		}, wait)
	}
	if h.Status.Phase == "OutcomeUnknown" {
		message := fmt.Sprintf("Catalyst Center handoff %s has an unknown outcome; retaining maintenance and device mutation fence", h.Name)
		if up.Status.Message == message {
			return wait, nil
		}
		return r.updateStatus(ctx, up, func(cur *ops.IOSXESoftwareUpgrade) {
			cur.Status.Message = message
			r.setReady(cur, metav1.ConditionFalse, "ControllerOutcomeUnknown", message, now)
		}, wait)
	} // retain parent, reservation and lease
	// A paused/cancelled/revoked parent may observe existing work but cannot
	// renew a dispatch grant. The controller also repeats these live checks.
	decision := r.evaluateManagedLeaf(ctx, up)
	if !up.DeletionTimestamp.IsZero() || !decision.allowClaim {
		return wait, nil
	}
	if h.Status.Phase == "Preparing" && up.Spec.ImageSource.CatalystCenter.Preparation != nil {
		complete := false
		if up.Status.ControllerHandoff != nil {
			for _, p := range up.Status.ControllerHandoff.Preparation {
				if p.Stage == h.Status.ReadinessFor && p.Phase == "Complete" {
					complete = true
				}
			}
		}
		if !complete {
			if !up.Status.StagingRequested {
				_, _, err := r.claimStaging(ctx, up, "claiming bounded Catalyst Center preparation and distribution", now)
				return wait, err
			}
			ready, err := r.prepareManagedMutation(ctx, up, up.DeepCopy(), ops.UpgradeManagedMutationStaging, now, false)
			if err != nil || !ready {
				return wait, err
			}
			result, err := r.runSWIMPreparation(ctx, up, h)
			if err != nil {
				// Surface the current preparation blocker instead of leaving an
				// earlier, transient admission message in the parent status.
				return r.updateStatus(ctx, up, func(cur *ops.IOSXESoftwareUpgrade) {
					cur.Status.Message = boundedWorkerMessage("Catalyst Center preparation blocked: " + err.Error())
					r.setReady(cur, metav1.ConditionFalse, "SWIMPreparationBlocked", cur.Status.Message, now)
				}, wait)
			}
			return result, nil
		}
	}
	stage := "Readiness"
	switch h.Status.Phase {
	case "ReadyToDistribute":
		stage = string(ops.UpgradeManagedMutationStaging)
		if !up.Status.StagingRequested {
			_, _, err := r.claimStaging(ctx, up, "claiming Catalyst Center distribution", now)
			return wait, err
		}
	case "ReadyToActivate":
		stage = string(ops.UpgradeManagedMutationPrimaryActivation)
		if !up.Status.PrimarySupervisorActivationRequested {
			_, _, err := r.claimActivation(ctx, up, false, "ControllerActivationClaimed", "claiming Catalyst Center distribution/activation", now)
			return wait, err
		}
	case "DistributionClaimed", "ActivationClaimed", "Distributing", "Activating":
		return wait, nil
	}
	// Repeat native workload and manager/network checks before each grant. The
	// already-persisted claim is never replaced or reinterpreted as another stage.
	if stage != "Readiness" {
		current := up.DeepCopy()
		ready, err := r.prepareManagedMutation(ctx, up, current, ops.UpgradeManagedMutationStage(stage), now, false)
		if err != nil || !ready {
			return wait, err
		}
	}
	return r.updateStatus(ctx, up, func(cur *ops.IOSXESoftwareUpgrade) {
		old := cur.Status.ControllerHandoff
		token := uuid.NewString()
		if old != nil && old.UID == string(h.UID) && old.Stage == stage && old.PolicyEpoch == decision.policyEpoch && old.ControlRevision == decision.controlRevision {
			token = old.Token
		}
		var preparation []ops.SWIMDevicePreparation
		if old != nil {
			preparation = old.Preparation
		}
		cur.Status.ControllerHandoff = &ops.UpgradeControllerHandoffStatus{Preparation: preparation, Name: h.Name, UID: string(h.UID), Stage: stage, Token: token, WorkerPodUID: r.WorkerPodUID, PolicyEpoch: decision.policyEpoch, ControlRevision: decision.controlRevision, ExpiresAt: metav1.NewTime(now.Add(2 * time.Minute))}
		cur.Status.Message = fmt.Sprintf("Catalyst Center handoff %s: %s", h.Name, h.Status.Phase)
	}, wait)
}

func (r *Reconciler) ensureControllerHandoff(ctx context.Context, up *ops.IOSXESoftwareUpgrade) (*ops.CatalystCenterSWIMHandoff, error) {
	source := up.Spec.ImageSource.CatalystCenter
	if err := controllerhandoff.ValidateSource(source); err != nil {
		return nil, err
	}
	var device cvk.CiscoDevice
	if err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: up.Namespace, Name: up.Spec.DeviceRef.Name}, &device); err != nil {
		return nil, err
	}
	if string(device.UID) != r.DeviceUID || device.Spec.PhysicalIdentity == "" {
		return nil, errors.New("handoff device identity changed")
	}
	var nc cvk.NetworkController
	if err := r.apiReader().Get(ctx, client.ObjectKey{Namespace: up.Namespace, Name: source.ControllerName}, &nc); err != nil {
		return nil, err
	}
	if string(nc.UID) != source.ControllerUID || nc.Spec.Type != controllerhandoff.ControllerType || !nc.DeletionTimestamp.IsZero() || nc.Status.Worker == nil || nc.Status.Worker.Name == "" {
		return nil, errors.New("pinned Catalyst Center worker is unavailable")
	}
	desired := ops.CatalystCenterSWIMHandoffSpec{UpgradeName: up.Name, UpgradeUID: string(up.UID), DeviceName: device.Name, DeviceUID: string(device.UID), Serial: device.Spec.PhysicalIdentity, ControllerGeneration: nc.Generation, ControllerUsername: "system:serviceaccount:" + up.Namespace + ":" + nc.Status.Worker.Name, DeviceWorkerUsername: up.Annotations[managedprotocol.AnnotationWorkerUsername], DeviceWorkerPodUID: r.WorkerPodUID, Source: *source, TargetVersion: up.Spec.TargetVersion}
	h := &ops.CatalystCenterSWIMHandoff{}
	key := types.NamespacedName{Namespace: up.Namespace, Name: controllerhandoff.Name(string(up.UID))}
	err := r.apiReader().Get(ctx, key, h)
	if apierrors.IsNotFound(err) {
		// A published UID may never be replaced by another object under this name.
		if up.Status.ControllerHandoff != nil {
			return nil, errors.New("published handoff disappeared; refusing replacement")
		}
		h = &ops.CatalystCenterSWIMHandoff{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace, Finalizers: []string{controllerhandoff.Finalizer}}, Spec: desired}
		if err := r.Client.Create(ctx, h); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	desired.DeviceWorkerPodUID = h.Spec.DeviceWorkerPodUID
	if !reflect.DeepEqual(h.Spec, desired) || h.UID == "" || !h.DeletionTimestamp.IsZero() {
		return nil, errors.New("handoff identity/spec changed or deletion is pending")
	}
	if g := up.Status.ControllerHandoff; g != nil && (g.UID != string(h.UID) || g.Name != h.Name) {
		return nil, errors.New("handoff UID changed")
	}
	return h, nil
}

func (r *Reconciler) holdControllerDeletion(ctx context.Context, up *ops.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	// Do not remove the parent finalizer on an unresolved remote mutation. Its
	// continued existence is necessary to retain manager claims and maintenance.
	owned, _, _, err := r.acquireQuarantineLease(ctx, up, true, now)
	if err != nil || !owned {
		return reconcile.Result{RequeueAfter: 15 * time.Second}, err
	}
	return r.runControllerHandoff(ctx, up, now)
}
