// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

var errFabricDevicesBulkEvidencePending = errors.New("post-task bulk fabric-device evidence is pending")

const fabricDevicesBulkEvidenceAnnotation = "cisco.vk/fabric-bulk-device-evidence"

type fabricDevicesBulkEvidence = configurationDeviceEvidence

// Collect read-only results for every admitted incoming/peer/parent device.
// Results are not semantic convergence, creation ownership or release proof.
// This does not enable public dispatch or invent an installed fabric verifier.
func (a *adapter) collectFabricDevicesBulkEvidence(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, queries map[string][]string, now time.Time) (map[string]fabricDevicesBulkEvidence, error) {
	if a == nil || a.client == nil || config == nil || source == nil || op == nil || op.Phase != "Verifying" || op.Section != "fabric" || op.SubmittedAt == nil || authorize == nil || now.IsZero() || !a.configurationGroupAdmissionReady() || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationDeviceAdmission
	}
	config = config.DeepCopy()
	op = op.DeepCopy()
	requested, err := freezeConfigurationDeviceProofQueries(queries, op.PhysicalGroup.Targets)
	if err != nil {
		return nil, err
	}
	check := func() (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, error) {
		var nc *cvk.NetworkController
		var bound []*ops.ControllerConfigurationReservation
		ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
			var err error
			nc, err = a.currentController(ctx)
			if err != nil {
				return false, err
			}
			bound, err = contract.ValidateGroupReservationSet(nc, members)
			if err != nil {
				return false, err
			}
			if err := authorize(ctx, bound); err != nil {
				return false, err
			}
			return true, nil
		})
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, errConfigurationBusy
		}
		return nc, bound, nil
	}
	collect := func() (*cvk.NetworkController, *fabricDevicesBulkSnapshot, []*ops.ControllerConfigurationReservation, time.Time, error) {
		nc, bound, err := check()
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		wants, err := a.retainedFabricDevicesBulk(ctx, source, op)
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		completed, err := a.client.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		if completed.IsZero() || !now.UTC().Truncate(time.Second).After(completed) {
			return nil, nil, nil, time.Time{}, errFabricDevicesBulkEvidencePending
		}
		done, err := a.client.verifyFabricDevicesBulk(ctx, wants, op)
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		if !done {
			return nil, nil, nil, time.Time{}, errConfigurationBusy
		}
		native, targets, err := a.client.fabricDevicesBulkTargets(ctx, a.statusReader, config.Namespace, wants)
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		if native.deferredParent || native.mask != 0 || native.fabric != op.ParentID || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return nil, nil, nil, time.Time{}, errConfigurationStale
		}
		fresh, _, err := check()
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		if fresh.ResourceVersion != nc.ResourceVersion {
			return nil, nil, nil, time.Time{}, errConfigurationStale
		}
		return nc, native, bound, completed, nil
	}
	nc, before, bound, completed, err := collect()
	if err != nil {
		return nil, err
	}
	// Full post-task native membership UUIDs and dependency state enter the read
	// identity through observationDigest; all queries and parent peers are bound.
	result := featureIdentityDigest(struct {
		Resource, Native string
		Queries          map[string][]string
	}{op.Resource, before.observationDigest(), requested})
	physical := map[string]Device{}
	for _, d := range before.physical {
		physical[d.ID] = d
	}
	evidence, proofErr := a.readConfigurationDeviceProofBatch(ctx, nc, op, bound, physical, requested, result, completed, now, "cc-fabric-evidence-", fabricDevicesBulkEvidenceAnnotation, errFabricDevicesBulkEvidencePending, func() error {
		current, _, err := check()
		if err != nil {
			return err
		}
		if current.ResourceVersion != nc.ResourceVersion {
			return errConfigurationStale
		}
		return nil
	})
	if proofErr != nil && !errors.Is(proofErr, errFabricDevicesBulkEvidencePending) {
		return nil, proofErr
	}
	current, after, _, currentCompleted, err := collect()
	if err != nil {
		return nil, err
	}
	if current.ResourceVersion != nc.ResourceVersion || !completed.Equal(currentCompleted) || !reflect.DeepEqual(before, after) {
		return nil, errConfigurationStale
	}
	if proofErr != nil {
		return nil, proofErr
	}
	return evidence, nil
}
