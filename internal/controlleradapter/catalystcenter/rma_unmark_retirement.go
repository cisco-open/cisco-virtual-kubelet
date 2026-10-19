// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Remove only the original mark receipt, preserving the verified cleanup as a
// tombstone. The manager owns reservation retirement; this adapter cannot delete
// a reservation or discard evidence while either operation still needs it.
func (a *adapter) retireDeviceReplacementMarkReceipt(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name, cleanupID string) error {
	if a == nil || !configurationOperationID.MatchString(cleanupID) {
		return errConfigurationTransition
	}
	ref, want, err := rmaUnmarkPruneIntent(config, source, name)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	state := nc.Status.Configuration
	if state == nil || state.Operation != nil {
		return errConfigurationBusy
	}
	snapshot, err := a.client.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return err
	}
	cleanup, err := deviceReplacementUnmarkApplied(state, config, snapshot, time.Now())
	if err != nil {
		return err
	}
	if cleanup == nil || cleanup.Operation.ID != cleanupID {
		return errConfigurationStale
	}
	op := &cleanup.Operation
	binding, err := parseRMAUnmarkResource(op.Resource)
	if err != nil {
		return err
	}
	mark, err := ownedRMAMarkReceipt(state, config, binding.scope, op.TargetID, op.TargetSerial, op.ParentID, time.Now())
	if err != nil {
		return err
	}

	// Absence alone is never completion proof: the verified cleanup above is
	// mandatory even when both native inventory and reservation rows are absent.
	ids := []string{op.ID}
	if mark != nil {
		ids = append(ids, mark.Operation.ID)
	}
	absent := func() error { return a.requireRetiredRMAReservations(ctx, nc, ids...) }
	if err := absent(); err != nil {
		return err
	}
	if err := a.client.revalidateDeviceReplacement(ctx, ref, want, snapshot); err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return errConfigurationStale
	}
	if err := absent(); err != nil {
		return err
	}
	if mark == nil {
		return nil
	}
	return a.persistRetiredRMAReceipt(ctx, nc, mark)
}

func (a *adapter) requireRetiredRMAReservations(ctx context.Context, nc *cvk.NetworkController, ids ...string) error {
	for _, id := range ids {
		name, err := contract.ReservationName(string(nc.UID), id)
		if err != nil {
			return err
		}
		var row ops.ControllerConfigurationReservation
		err = a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: nc.Namespace, Name: name}, &row)
		if err == nil {
			return errConfigurationBusy
		}
		if !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// CAS and readback are shared by mark ownership retirement and completed cleanup
// tombstone retirement. Callers must establish native/source/reservation proof.
func (a *adapter) persistRetiredRMAReceipt(ctx context.Context, nc *cvk.NetworkController, receipt *cvk.NetworkControllerConfigurationActionApplication) error {
	kept := make([]cvk.NetworkControllerConfigurationActionApplication, 0, len(nc.Status.Configuration.ActionApplications)-1)
	for _, entry := range nc.Status.Configuration.ActionApplications {
		if entry.Operation.ID == receipt.Operation.ID {
			if entry.Resource != receipt.Resource || entry.Section != receipt.Section {
				return errConfigurationStale
			}
		} else {
			kept = append(kept, entry)
		}
	}
	nc.Status.Configuration.ActionApplications = kept
	// A conflicting or lost update is returned; reconciliation must re-read it.
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if stored.UID != nc.UID || !apiequality.Semantic.DeepEqual(stored.Status.Configuration, nc.Status.Configuration) {
		return errConfigurationStale
	}
	return nil
}
