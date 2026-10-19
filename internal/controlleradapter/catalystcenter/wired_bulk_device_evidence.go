// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

var errWiredBulkDeviceEvidencePending = errors.New("post-task bulk device operation evidence is pending")

const wiredBulkEvidenceBindingAnnotation = "cisco.vk/wired-bulk-device-evidence"

type wiredBulkDeviceEvidence = configurationDeviceEvidence

// Observe the complete admitted batch through the existing read-only workers.
// Queries must cover every native ID and begin with independent chassis identity
// evidence. Resource-specific callers still own semantic comparison against the
// original rendered intent: this collector never supplies a convergence receipt,
// treats compliance as device proof, or changes claims/grants/operation phase.
func (a *adapter) collectWiredBulkDeviceEvidence(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, batch wiredBulkBatch, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, queries map[string][]string, completed, now time.Time) (map[string]wiredBulkDeviceEvidence, error) {
	if a == nil || a.client == nil || config == nil || source == nil || op == nil || authorize == nil || now.IsZero() || completed.IsZero() || !a.configurationGroupAdmissionReady() || op.Phase != "Verifying" || op.Section != "inventory" || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationDeviceAdmission
	}
	if !now.UTC().Truncate(time.Second).After(completed) {
		return nil, errWiredBulkDeviceEvidencePending
	}
	if err := declaredWiredBulkBatch(source, batch); err != nil {
		return nil, err
	}
	dependencies, err := a.wiredBulkDependencies(ctx, source)
	if err != nil {
		return nil, err
	}
	requested, err := freezeConfigurationDeviceProofQueries(queries, op.PhysicalGroup.Targets)
	if err != nil {
		return nil, err
	}
	check := func() (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, error) {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return nil, nil, err
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			return nil, nil, err
		}
		if nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
			return nil, nil, errConfigurationStale
		}
		owners := 0
		for _, owner := range nc.Status.Configuration.Owners {
			if owner.Section == "inventory" {
				if owner.ConfigName != config.Name || owner.ConfigUID != string(config.UID) {
					return nil, nil, errConfigurationOwned
				}
				owners++
			}
		}
		if owners != 1 {
			return nil, nil, errConfigurationOwned
		}
		bound, err := contract.ValidateGroupReservationSet(nc, members)
		if err != nil {
			return nil, nil, err
		}
		if err := authorize(ctx, bound); err != nil {
			return nil, nil, err
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return nil, nil, err
		}
		fresh, err := a.currentController(ctx)
		if err != nil {
			return nil, nil, err
		}
		if fresh.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(fresh.Status.Configuration, nc.Status.Configuration) {
			return nil, nil, errConfigurationStale
		}
		return nc, bound, nil
	}
	nc, bound, err := check()
	if err != nil {
		return nil, err
	}
	collect := func() (*wiredBulkSnapshot, error) {
		if err := a.client.checkWiredBulkNetworkContext(ctx, source, []wiredBulkBatch{batch}); err != nil {
			return nil, err
		}
		observed, err := a.client.observeWiredBulkProvisionCompletion(ctx, batch.path, batch.members, op, now, dependencies...)
		if err != nil {
			return nil, err
		}
		if !observed.Equal(completed) {
			return nil, errConfigurationStale
		}
		native, targets, err := a.client.wiredBulkTargets(ctx, a.statusReader, config.Namespace, batch.path, batch.members, dependencies...)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return nil, errConfigurationStale
		}
		return native, nil
	}
	before, err := collect()
	if err != nil {
		return nil, err
	}
	// Bind all observed post-task record UUIDs, the original footprint, and the
	// complete query plan. Never reuse a result after record/query replacement.
	identities := []string{op.Resource, before.anchor, before.state}
	physical := map[string]Device{}
	for _, entry := range before.entries {
		physical[entry.target.ID] = entry.target
		if _, exists := requested[entry.target.ID]; !exists {
			return nil, errConfigurationStale
		}
		identities = append(identities, entry.target.ID, entry.target.Serial, entry.site.ID, entry.before.ID)
	}
	raw, err := json.Marshal(struct {
		Identities []string
		Queries    map[string][]string
	}{identities, requested})
	if err != nil {
		return nil, err
	}
	result := fmt.Sprintf("%x", sha256.Sum256(raw))
	evidence, proofErr := a.readConfigurationDeviceProofBatch(ctx, nc, op, bound, physical, requested, result, completed, now, "cc-bulk-evidence-", wiredBulkEvidenceBindingAnnotation, errWiredBulkDeviceEvidencePending, func() error { _, _, err := check(); return err })
	if proofErr != nil && !errors.Is(proofErr, errWiredBulkDeviceEvidencePending) {
		return nil, proofErr
	}
	if _, _, err := check(); err != nil {
		return nil, err
	}
	after, err := collect()
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, after) {
		return nil, errConfigurationStale
	}
	if proofErr != nil {
		return nil, proofErr
	}
	return evidence, nil
}
