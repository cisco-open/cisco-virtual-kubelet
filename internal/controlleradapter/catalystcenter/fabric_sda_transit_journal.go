// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/apimachinery/pkg/api/equality"
)

func declaredSDATransit(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want sdaTransitIntent) error {
	if config == nil {
		return errConfigurationTransition
	}
	wants, err := declaredSDATransitSource(config, source)
	if err != nil {
		return err
	}
	for _, candidate := range wants {
		if candidate.name != want.name {
			continue
		}
		if candidate.kind != want.kind || !reflect.DeepEqual(candidate.multicast, want.multicast) || !reflect.DeepEqual(candidate.references, want.references) {
			return errConfigurationStale
		}
		members := map[string]bool{}
		for _, name := range want.controlPlanes {
			if members[name] {
				return errConfigurationStale
			}
			members[name] = true
		}
		if len(members) != len(candidate.controlPlanes) {
			return errConfigurationStale
		}
		for _, name := range candidate.controlPlanes {
			if !members[name] {
				return errConfigurationStale
			}
		}
		return nil
	}
	return errConfigurationStale
}

// Shared atomic owner/journal preparation. No reservation or native request is
// created; the global logical target is distinct from a product result UUID.
func (a *adapter) prepareSDATransit(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want sdaTransitIntent) (*cvk.NetworkControllerConfigurationOperation, error) {
	want = freezeSDATransitIntent(want)
	if a == nil || a.client == nil || config == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	if err := declaredSDATransit(config, source, want); err != nil {
		return nil, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	s, targets, err := a.preflightSDATransitSource(ctx, config, source, want)
	if err != nil {
		return nil, err
	}
	if s.actual != nil {
		return nil, errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	return a.prepareConfiguration(ctx, config, source, "fabric", sdaTransitResource(s), "Create", configurationResourceIdentity{target: s.digest, groupTargets: targets})
}

func (a *adapter) revalidateSDATransitOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want sdaTransitIntent, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	want = freezeSDATransitIntent(want)
	if op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.TaskID != "" || op.CreatedResourceID != "" || op.ParentID != "" || op.TargetSerial != "" || len(op.CredentialVersions) != 0 || op.CancellationRequestedAt != nil || phase == "Prepared" && op.SubmittedAt != nil || phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero()) {
		return errConfigurationTransition
	}
	target, err := sdaTransitOperationTarget(op.Resource, op.Action)
	if err != nil || target != op.TargetID || contract.ValidateGroupJournalIdentity(op) != nil {
		return errConfigurationStale
	}
	if err := declaredSDATransit(config, source, want); err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		s, targets, err := a.preflightSDATransitSource(ctx, config, source, want)
		if err != nil {
			return false, err
		}
		if s.actual != nil || op.Resource != sdaTransitResource(s) || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return errConfigurationBusy
	}
	return nil
}

func (a *adapter) dispatchSDATransit(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want sdaTransitIntent, permit *contract.GroupSubmissionPermit) (Task, error) {
	want = freezeSDATransitIntent(want)
	if a == nil || a.client == nil || config == nil || permit == nil {
		return Task{}, errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return Task{}, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return Task{}, errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	if err := a.revalidateSDATransitOperation(ctx, config, source, want, op, "Prepared"); err != nil {
		return Task{}, err
	}
	// Capture the real one-use dispatch proof only after native planning.
	var checkDispatch func(context.Context) error
	write, targets, err := a.client.planManagedSDATransitCreate(ctx, a.statusReader, config.Namespace, want, func(ctx context.Context, _ []Device) error {
		fresh, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if fresh.Status.Configuration == nil || fresh.Status.Configuration.Operation == nil {
			return errConfigurationStale
		}
		current := fresh.Status.Configuration.Operation
		expected := op.DeepCopy()
		expected.Phase = "Submitting"
		expected.SubmittedAt = current.SubmittedAt
		if fresh.UID != nc.UID || !equality.Semantic.DeepEqual(expected, current) {
			return errConfigurationStale
		}
		if err := a.revalidateSDATransitOperation(ctx, config, source, want, current, "Submitting"); err != nil {
			return err
		}
		if checkDispatch == nil {
			return errConfigurationTransition
		}
		return checkDispatch(ctx)
	})
	if err != nil {
		return Task{}, err
	}
	if write == nil || write.resource != op.Resource || write.identity.target != op.TargetID || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
		return Task{}, errConfigurationStale
	}
	proof, err := permit.BeginSubmissionFor(ctx, nc, time.Now())
	if err != nil {
		return Task{}, err
	}
	checkDispatch = func(ctx context.Context) error { return proof.Check(ctx, time.Now()) }
	task, err := write.submit(ctx)
	if err != nil {
		return Task{}, err
	}
	if err := proof.RecordTask(ctx, task.ID, time.Now()); err != nil {
		return task, err
	}
	return task, nil
}

func (a *adapter) observeOwnedSDATransit(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want sdaTransitIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	want = freezeSDATransitIntent(want)
	if op == nil || contract.ValidateGroupJournalIdentity(op) != nil {
		return false, errConfigurationStale
	}
	if err := declaredSDATransit(config, source, want); err != nil {
		return false, err
	}
	return a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		before, targets, err := a.client.sdaTransitTargets(ctx, a.statusReader, config.Namespace, want)
		if err != nil {
			return false, err
		}
		if sdaTransitResource(before) != op.Resource || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		done, err := a.client.observeSDATransitCreate(ctx, want, op, now)
		if err != nil || !done {
			return false, err
		}
		after, current, err := a.client.sdaTransitTargets(ctx, a.statusReader, config.Namespace, want)
		if err != nil {
			return false, err
		}
		if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(targets, current) {
			return false, errConfigurationStale
		}
		return true, nil
	})
}

func (a *adapter) beginSDATransitVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want sdaTransitIntent, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	want = freezeSDATransitIntent(want)
	if a == nil || config == nil || authorize == nil {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	return contract.BeginGroupVerification(ctx, a.statusReader, a.statusWriter, nc, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		done, err := a.observeOwnedSDATransit(ctx, config, source, want, op, now)
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		return nil
	}, now)
}

// Public acknowledged recovery only. New-intent compilation and automatic
// Prepared dispatch remain disabled until final device convergence is wired.
func (r *configReconciler) recoverSDATransitGroup(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error) error {
	if source == nil || op == nil {
		return errConfigurationTransition
	}
	wants, err := parseSDATransitIntents(source.raw)
	if err != nil {
		return err
	}
	var selected *sdaTransitIntent
	var observationErr error
	for _, want := range wants {
		s, targets, err := r.a.client.sdaTransitTargets(ctx, r.a.statusReader, config.Namespace, want)
		if err != nil {
			// After acknowledgement, an unrelated declaration must not prevent
			// observing this exact retained operation. Its own failed evidence
			// still cannot match the resource and admitted target footprint.
			if observationErr == nil {
				observationErr = err
			}
			continue
		}
		if sdaTransitResource(s) != op.Resource || s.digest != op.TargetID || op.PhysicalGroup == nil || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			continue
		}
		if selected != nil {
			return errConfigurationStale
		}
		copy := want
		selected = &copy
	}
	if selected == nil {
		if observationErr != nil {
			return observationErr
		}
		return errConfigurationStale
	}
	if op.Phase == "Verifying" {
		if _, err := r.a.collectSDATransitDeviceEvidence(ctx, config, source, *selected, op, members, authorize, time.Now()); err != nil {
			return err
		}
		return errSDATransitConvergenceUnavailable
	}
	return r.a.beginSDATransitVerification(ctx, config, source, *selected, members, authorize, time.Now())
}
