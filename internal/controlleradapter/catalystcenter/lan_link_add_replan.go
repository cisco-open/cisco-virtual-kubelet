// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Cancellation is limited to a positively observed unused baseline with the
// same immutable intent/ownership/physical scope. Missing evidence is not drift.
type lanLinkPreparationScope struct{ family, logical, recovery, owner, physical, declaration, scope, prepared string }

func parseLANLinkPreparationScope(resource, action string) (lanLinkPreparationScope, error) {
	var zero lanLinkPreparationScope
	if strings.HasPrefix(resource, lanLinkAddFamily) {
		binding, err := parseLANLinkAddResource(resource, action)
		if err != nil || binding.version != 2 {
			return zero, errConfigurationTransition
		}
		return lanLinkPreparationScope{family: lanLinkAddFamily, logical: binding.logical, recovery: binding.recovery, prepared: binding.prepared}, nil
	}
	binding, err := parseLANLinkDeleteResource(resource, action)
	if err != nil {
		return zero, err
	}
	return lanLinkPreparationScope{family: lanLinkDeleteFamily, logical: binding.logical, owner: binding.owner, physical: binding.physical, declaration: binding.declaration, scope: binding.scope, prepared: binding.prepared}, nil
}

func staleLANLinkPreparation(op *cvk.NetworkControllerConfigurationOperation, observed map[string]lanLinkAddObservation) (string, error) {
	if op == nil || op.Phase != "Prepared" || op.SubmittedAt != nil || op.TaskID != "" || op.CancellationRequestedAt != nil || op.Section != "lan_automation" || contract.ValidateGroupJournalIdentity(op) != nil {
		return "", errConfigurationTransition
	}
	original, err := parseLANLinkPreparationScope(op.Resource, op.Action)
	if err != nil {
		return "", err
	}
	selected := ""
	for name, current := range observed {
		if current.owned || !reflect.DeepEqual(current.targets, op.PhysicalGroup.Targets) {
			continue
		}
		binding, err := parseLANLinkPreparationScope(current.resource, "Update")
		if err != nil || binding.prepared == original.prepared {
			continue
		}
		binding.prepared = original.prepared
		if binding != original {
			continue
		}
		if selected != "" {
			return "", errConfigurationStale
		}
		selected = name
	}
	return selected, nil
}

// Cancel instead of rewriting the original operation or manufacturing another
// permit. Existing cancellation actors retain/release every original member;
// only their complete settlement allows ordinary public Apply to replan.
func (a *adapter) cancelStaleLANLinkPreparation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, observed map[string]lanLinkAddObservation) (bool, error) {
	if a == nil || !a.configurationGroupAdmissionReady() || !supportedLANLinkAddConfig(config) {
		return false, errConfigurationDeviceAdmission
	}
	name, err := staleLANLinkPreparation(op, observed)
	if err != nil || name == "" {
		return false, err
	}
	// Freeze caller-owned inputs across external authorizers.
	config, op = config.DeepCopy(), op.DeepCopy()
	baseline := make(map[string]lanLinkAddObservation, len(observed))
	for key, value := range observed {
		value.targets = append([]contract.GroupTargetIdentity(nil), value.targets...)
		baseline[key] = value
	}
	check := func(ctx context.Context) error {
		done, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "lan_automation", func(ctx context.Context) (bool, error) {
			lifecycle, err := a.publicLANLinkLifecycle(source)
			if err != nil {
				return false, err
			}
			current, _, err := lifecycle.observe(ctx, config, source)
			if err != nil {
				return false, err
			}
			selected, err := staleLANLinkPreparation(op, current)
			if err != nil {
				return false, err
			}
			if selected != name || !reflect.DeepEqual(current, baseline) {
				return false, errConfigurationStale
			}
			return true, nil
		})
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationStale
		}
		return nil
	}
	if err := check(ctx); err != nil {
		return false, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) || len(nc.Status.Configuration.GroupCancellations) >= 32 {
		return false, errConfigurationBusy
	}
	authorize := func(ctx context.Context, current *cvk.NetworkController) error {
		if err := check(ctx); err != nil {
			return err
		}
		candidates, err := contract.ReadGroupReservationCandidates(ctx, a.statusReader, current, time.Now())
		if err != nil {
			return err
		}
		// Candidates are observation-only. Every original member must already
		// exist with that exact binding; the connector creates no recovery rows.
		before, err := a.readLANReplanMembers(ctx, candidates)
		if err != nil {
			return err
		}
		if _, err := contract.ValidateGroupReservationSet(current, before); err != nil {
			return err
		}
		for _, member := range before {
			if err := a.authorizeConfigurationGroupMember(ctx, member.DeepCopy()); err != nil {
				return err
			}
		}
		fresh, err := contract.ReadGroupReservationCandidates(ctx, a.statusReader, current, time.Now())
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(candidates, fresh) {
			return errConfigurationStale
		}
		after, err := a.readLANReplanMembers(ctx, fresh)
		if err != nil {
			return err
		}
		for i, prior := range before {
			if prior != nil && (after[i] == nil || after[i].UID != prior.UID) {
				return errConfigurationStale
			}
		}
		return check(ctx)
	}
	err = contract.BeginGroupCancellation(ctx, a.statusReader, a.statusWriter, nc, authorize, time.Now())
	return err == nil, err
}

func (a *adapter) readLANReplanMembers(ctx context.Context, candidates []*ops.ControllerConfigurationReservation) ([]*ops.ControllerConfigurationReservation, error) {
	members := make([]*ops.ControllerConfigurationReservation, len(candidates))
	for i, candidate := range candidates {
		var member ops.ControllerConfigurationReservation
		err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(candidate), &member)
		if err != nil {
			return nil, err
		}
		if member.UID == "" || member.ResourceVersion == "" || member.DeletionTimestamp != nil || !reflect.DeepEqual(member.Spec, candidate.Spec) || member.Status.Completion != nil || member.Status.ManagerSettledAt != nil || member.Status.GroupCancellation != nil || member.Status.PreparationCancelledAt != nil {
			return nil, errConfigurationStale
		}
		if claim := member.Status.Claim; claim != nil && (claim.OperationID != candidate.Spec.OperationID || claim.OperationDigest != candidate.Spec.OperationDigest || claim.ClaimedAt.IsZero() || claim.ClaimedAt.After(time.Now())) {
			return nil, errConfigurationStale
		}
		members[i] = member.DeepCopy()
	}
	return members, nil
}
