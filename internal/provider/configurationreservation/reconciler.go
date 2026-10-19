// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
// Package configurationreservation owns the device-side Lease lifecycle for
// controller configuration. It contains no controller API or device commands.
package configurationreservation

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

// Reconciler supports recovery without new-work admission and preparation
// hooks. Missing hooks prevent new grants. Reader must be uncached and Leaser
// must use the same authoritative client. Only a device worker may run it.
type Reconciler struct {
	Reader                                   client.Reader
	Client                                   client.Client
	Leaser                                   *engine.FamilyLeaser
	Namespace, DeviceName, DeviceUID, PodUID string
	ControllerType                           cvk.NetworkControllerType
	// Authorize checks controller/source bindings, deployed admission/RBAC and
	// the worker's rollout contract. It must not mutate devices or acquire Leases.
	Authorize func(context.Context, *ops.ControllerConfigurationReservation) error
	// Prepare is idempotent maintenance preparation after Lease ownership.
	Prepare func(context.Context, *ops.ControllerConfigurationReservation) error
	// StrictDevicePodLister must be the driver's ListPodsForDrain capability.
	// Missing capability prevents new grants, but never prevents recovery.
	StrictDevicePodLister func(context.Context) ([]*core.Pod, error)
	Now                   func() time.Time
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ops.ControllerConfigurationReservation{}).
		Complete(r)
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	wait := reconcile.Result{RequeueAfter: 10 * time.Second}
	if r.Reader == nil || r.Client == nil || r.Leaser == nil || !r.Leaser.RequireExisting || r.Leaser.Namespace == "" || r.Namespace == "" || r.DeviceName == "" || r.DeviceUID == "" || r.PodUID == "" {
		return wait, errors.New("configuration worker gates are incomplete")
	}
	if req.Namespace != r.Namespace {
		return reconcile.Result{}, nil
	}
	var reservation ops.ControllerConfigurationReservation
	if err := r.Reader.Get(ctx, req.NamespacedName, &reservation); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}
	s := &reservation.Spec
	if s.Device.Name != r.DeviceName || s.Device.UID != r.DeviceUID || s.DeviceWorkerPod.UID != r.PodUID {
		return reconcile.Result{}, nil
	}
	if s.Protocol != contract.Protocol && s.Protocol != contract.GroupReservationProtocol {
		return wait, errors.New("configuration reservation protocol is unsupported")
	}
	holder, err := contract.Holder(r.Namespace, &reservation)
	if err != nil {
		return wait, err
	}
	key := devicecoordination.DeviceKey(r.Namespace, r.DeviceName)
	g := reservation.Status.Grant
	// Fence uncertain claimed work before attempting reads needed for settlement.
	// Missing controller inventory or malformed completion must not stop renewal.
	if reservation.Status.Claim != nil && contract.RequiresQuarantine(&reservation, r.now()) {
		if _, err := mutationguard.EnsureCanonicalQuarantine(ctx, r.Reader, r.Leaser, r.Namespace, r.DeviceName, key, holder, r.now()); err != nil {
			return wait, err
		}
	}
	if s.Protocol == contract.GroupReservationProtocol {
		// Observe the durable cancellation journal before new preparation or
		// renewal. A member marker alone never establishes cancellation authority.
		var controller cvk.NetworkController
		if err := r.Reader.Get(ctx, client.ObjectKey{Namespace: reservation.Namespace, Name: s.Controller.Name}, &controller); err == nil {
			if controller.Status.Configuration != nil && controller.Status.Configuration.Operation != nil && controller.Status.Configuration.Operation.Phase == "Cancelling" {
				if reservation.Status.GroupCancellation != nil {
					return wait, contract.RecoverCancelledGroupMemberRelease(ctx, r.Reader, r.Client, &controller, &reservation, r.PodUID, r.Leaser.Namespace, r.Authorize, func(ctx context.Context) error {
						return r.Leaser.Release(ctx, key, devicecoordination.MutationLeaseFamily, holder)
					}, r.now())
				}
				return wait, contract.AcknowledgeGroupCancellation(ctx, r.Reader, r.Client, &controller, &reservation, r.PodUID, r.Authorize, r.now())
			}
		}
		if reservation.Status.GroupCancellation != nil {
			return wait, errors.New("group cancellation lacks its current journal fence")
		}
		if reservation.Status.Claim == nil && g == nil {
			return wait, r.prepareGroup(ctx, &reservation, key, holder)
		}
		if reservation.Status.Claim == nil && g != nil && g.Phase == "Held" {
			if r.Authorize == nil {
				return wait, errors.New("group grant renewal authority is unavailable")
			}
			if err := contract.ReadGroupHeldMember(ctx, r.Reader, &reservation, r.Leaser.Namespace, r.now()); err != nil {
				return wait, err
			}
			if err := r.Authorize(ctx, reservation.DeepCopy()); err != nil {
				return wait, err
			}
			if err := contract.ReadGroupHeldMember(ctx, r.Reader, &reservation, r.Leaser.Namespace, r.now()); err != nil {
				return wait, err
			}
			// Retain the canonical fence, then recheck the original grant's lease
			// incarnation. Quarantine renewal must never republish an old grant.
			guard, err := mutationguard.EnsureCanonicalQuarantine(ctx, r.Reader, r.Leaser, r.Namespace, r.DeviceName, key, holder, r.now())
			if err != nil {
				return wait, err
			}
			if !guard.CallerOwnsRisk {
				return wait, errors.New("group member does not own canonical quarantine")
			}
			return wait, contract.ReadGroupHeldMember(ctx, r.Reader, &reservation, r.Leaser.Namespace, r.now())
		}
		// The installed authorizer must explicitly support group recovery.
		err := contract.RecoverGroupMemberRelease(ctx, r.Reader, r.Client, &reservation, r.PodUID, r.Leaser.Namespace, r.Authorize, func(ctx context.Context) error {
			return r.Leaser.Release(ctx, key, devicecoordination.MutationLeaseFamily, holder)
		}, r.now())
		if err != nil {
			return wait, err
		}
		return reconcile.Result{}, nil
	}
	if cancelled := reservation.Status.PreparationCancelledAt; cancelled != nil {
		if cancelled.IsZero() || cancelled.After(r.now()) || g != nil || reservation.Status.Claim != nil || reservation.Status.Completion != nil {
			return wait, errors.New("configuration cancellation evidence is inconsistent")
		}
		// The admitted cancellation marker permanently excludes publication of
		// a grant. Release only this reservation's holder; never acquire a new
		// Lease or rewrite controller/manager evidence while cancelling.
		if err := r.Leaser.Release(ctx, key, devicecoordination.MutationLeaseFamily, holder); err != nil {
			return wait, err
		}
		return wait, nil
	}
	if g != nil && (g.LeaseNamespace != r.Leaser.Namespace || g.Lease.Name != engine.LeaseName(key, devicecoordination.MutationLeaseFamily) || g.Holder != holder) {
		return wait, errors.New("configuration grant lease binding differs from runtime")
	}
	if g != nil && g.Phase == "Released" {
		if contract.RequiresQuarantine(&reservation, r.now()) {
			return wait, errors.New("released configuration evidence is incomplete")
		}
		return reconcile.Result{}, nil
	}
	if reservation.Status.Claim != nil && reservation.Status.Completion != nil {
		var nc cvk.NetworkController
		if err := r.Reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: s.Controller.Name}, &nc); err != nil {
			return wait, err
		}
		if err := contract.ValidateCompletionReceipt(&reservation, &nc, r.now()); err != nil {
			return wait, err
		}
		if err := r.Leaser.Release(ctx, key, devicecoordination.MutationLeaseFamily, holder); err != nil {
			return wait, err
		}
		return wait, contract.MarkVerifiedReleased(ctx, r.Reader, r.Client, &reservation, r.now())
	}
	if g != nil && g.Phase == "Revoked" && reservation.Status.Claim == nil {
		if err := r.Leaser.Release(ctx, key, devicecoordination.MutationLeaseFamily, holder); err != nil {
			return wait, err
		}
		// CAS preserves a competing claim or status change, even though admission
		// already forbids new claims against a revoked grant.
		reservation.Status.Grant.Phase = "Released"
		return wait, r.Client.Status().Update(ctx, &reservation)
	}
	if reservation.Status.Claim != nil {
		// A remote task can outlive worker health and intent changes. Renew its
		// canonical quarantine without re-authorizing or reissuing the operation.
		return wait, nil
	}
	if g != nil && g.Phase != "Held" {
		return wait, errors.New("configuration grant phase is unsupported")
	}
	if r.ControllerType == "" || r.Authorize == nil || r.Prepare == nil || r.StrictDevicePodLister == nil {
		if g != nil {
			return wait, contract.RevokeUnclaimed(ctx, r.Client, &reservation)
		}
		return wait, r.cancelPreparation(ctx, &reservation, errors.New("configuration new-work preparation gates are incomplete"))
	}
	if g != nil && contract.ValidateGrantFreshness(&reservation, r.now()) != nil {
		return wait, contract.RevokeUnclaimed(ctx, r.Client, &reservation)
	}
	bindingErr := contract.ReadDeviceWorkerBinding(ctx, r.Reader, &reservation, r.now())
	if bindingErr == nil {
		bindingErr = r.authorize(ctx, &reservation)
	}
	if bindingErr != nil {
		if g != nil {
			return wait, contract.RevokeUnclaimed(ctx, r.Client, &reservation)
		}
		return wait, r.cancelPreparation(ctx, &reservation, bindingErr)
	}
	guard, err := mutationguard.EnsureCanonicalQuarantine(ctx, r.Reader, r.Leaser, r.Namespace, r.DeviceName, key, holder, r.now())
	if err != nil {
		return wait, err
	}
	if guard.Risk != nil && !guard.CallerOwnsRisk {
		return wait, nil
	}
	acquired, err := r.Leaser.Acquire(ctx, key, devicecoordination.MutationLeaseFamily, holder)
	if err != nil || !acquired.Owned {
		return wait, err
	}
	if g != nil {
		var lease coordination.Lease
		if err := r.Reader.Get(ctx, client.ObjectKey{Namespace: r.Leaser.Namespace, Name: g.Lease.Name}, &lease); err != nil {
			return wait, err
		}
		if err := contract.ValidateLiveGrant(&reservation, &lease, r.Leaser.Namespace, r.now()); err != nil {
			return wait, contract.RevokeUnclaimed(ctx, r.Client, &reservation)
		}
		return wait, nil
	}
	return wait, r.prepareAndPublish(ctx, &reservation, false)
}

func (r *Reconciler) prepareGroup(ctx context.Context, reservation *ops.ControllerConfigurationReservation, key, holder string) error {
	if r.ControllerType != "catalyst-center" || r.Authorize == nil || r.Prepare == nil || r.StrictDevicePodLister == nil {
		return errors.New("group preparation gates are incomplete")
	}
	if err := r.authorizeGroupPreparation(ctx, reservation); err != nil {
		return err
	}
	guard, err := mutationguard.EnsureCanonicalQuarantine(ctx, r.Reader, r.Leaser, r.Namespace, r.DeviceName, key, holder, r.now())
	if err != nil {
		return err
	}
	if guard.Risk != nil && !guard.CallerOwnsRisk {
		return errors.New("group preparation does not own canonical quarantine")
	}
	acquired, err := r.Leaser.Acquire(ctx, key, devicecoordination.MutationLeaseFamily, holder)
	if err != nil {
		return err
	}
	if !acquired.Owned {
		return errors.New("group preparation lease is unavailable")
	}
	return r.prepareAndPublish(ctx, reservation, true)
}

func (r *Reconciler) authorizeGroupPreparation(ctx context.Context, reservation *ops.ControllerConfigurationReservation) error {
	if err := contract.ReadGroupMemberPreparation(ctx, r.Reader, reservation, r.now()); err != nil {
		return err
	}
	if err := r.Authorize(ctx, reservation.DeepCopy()); err != nil {
		return err
	}
	return contract.ReadGroupMemberPreparation(ctx, r.Reader, reservation, r.now())
}

// Both protocols share the maintenance/native-workload boundary. Only their
// identity/authorization checks and versioned grant publisher differ.
func (r *Reconciler) prepareAndPublish(ctx context.Context, reservation *ops.ControllerConfigurationReservation, group bool) error {
	if err := r.Prepare(ctx, reservation.DeepCopy()); err != nil {
		return err
	}
	// Keep the strict scan within a bounded interval immediately before grant.
	// Neither a scheduling guard nor an arbitrary preparation callback proves
	// absence of native/foreign applications.
	scanCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := workloaddrain.VerifyEmptyWorkloads(scanCtx, r.Reader, reservation.Spec.Node.Name, r.StrictDevicePodLister); err != nil {
		return err
	}
	// Preparation can take time. Recheck source/rollout gates immediately before
	// publishing; PublishGrant itself rereads all device and journal evidence.
	if group {
		if err := r.authorizeGroupPreparation(scanCtx, reservation); err != nil {
			return err
		}
	} else if err := r.authorize(scanCtx, reservation); err != nil {
		return r.cancelPreparation(ctx, reservation, err)
	}
	if err := scanCtx.Err(); err != nil {
		return err
	}
	if group {
		return contract.PublishGroupGrant(scanCtx, r.Reader, r.Client, reservation, r.Leaser.Namespace, r.PodUID, r.now())
	}
	return contract.PublishGrant(scanCtx, r.Reader, r.Client, reservation, r.Leaser.Namespace, r.PodUID, r.now())
}

func (r *Reconciler) authorize(ctx context.Context, reservation *ops.ControllerConfigurationReservation) error {
	if err := contract.ReadIntentBinding(ctx, r.Reader, reservation, r.ControllerType); err != nil {
		return err
	}
	if err := contract.ReadControllerWorkerBinding(ctx, r.Reader, reservation, r.now()); err != nil {
		return err
	}
	return r.Authorize(ctx, reservation.DeepCopy())
}

// Close the grant boundary before cleanup when admission is no longer valid.
// Do not release on this pass: a failed/ambiguous CAS must be resolved by a
// fresh read, and a competing published grant or claim must retain its fence.
func (r *Reconciler) cancelPreparation(ctx context.Context, reservation *ops.ControllerConfigurationReservation, cause error) error {
	return errors.Join(cause, contract.CancelPreparation(ctx, r.Reader, r.Client, reservation, r.PodUID, r.now()))
}
