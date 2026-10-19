// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/client-go/util/retry"
)

var errConfigurationNotDispatched = errors.New("native preflight refused before dispatch; replan required")

// This private capability is issued only before the original submit function is
// called. It is tied to that guard and can be claimed once, including through a
// configurationGroup's copy of the write. It is never inferred from API errors.
type configurationPreSubmitRefusal struct {
	cause       error
	guard       *byte
	operationID string
	claimed     atomic.Bool
	spent       atomic.Bool
}

func (e *configurationPreSubmitRefusal) Error() string { return e.cause.Error() }
func (e *configurationPreSubmitRefusal) Unwrap() error { return e.cause }

func guardWirelessDefinitionPreflight(write *configurationWrite, check func(context.Context) error) {
	guardConfigurationPreflight(write, check)
}

func guardConfigurationPreflight(write *configurationWrite, check func(context.Context) error) {
	if write == nil {
		return
	}
	guard := new(byte)
	prior := write.noDispatch
	write.noDispatch = func(err error) *configurationPreSubmitRefusal {
		refused, ok := err.(*configurationPreSubmitRefusal)
		if ok && refused.guard == guard && refused.claimed.CompareAndSwap(false, true) {
			return refused
		}
		if prior != nil {
			return prior(err)
		}
		return nil
	}
	guardConfigurationWrite(write, func(ctx context.Context) error {
		if err := check(ctx); err != nil {
			return &configurationPreSubmitRefusal{cause: err, guard: guard}
		}
		return nil
	})
}

func wirelessDefinitionNoDispatchResource(resource string) bool {
	for group, prefix := range map[string]string{"ap_profiles": "ap-profile:", "anchor_groups": "anchor-group:", "interfaces": "interface:", "power_profiles": "power-profile:", "dot11be_profiles": "dot11be-profile:", "security_settings": "pre-auth-acl:", "rf_profiles": "rf-profile:"} {
		local := strings.TrimPrefix(resource, group+"/")
		if name, ok := strings.CutPrefix(local, prefix); ok && validProfileName(name) {
			return true
		}
	}
	return false
}
func noDispatchKindSupported(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	if validTagOwnedOperation(op) || validTagDeleteOperation(op) {
		return true
	}
	if op.Section == "inventory" {
		if pnpRemovalSelected(op.Resource) {
			id, _, ok := pnpRemovalParts(op.Resource)
			return ok && op.Action == "Delete" && op.TargetID == id && op.ParentID == ""
		}
		if discoveryCredentialNoDispatchKind(op) {
			return true
		}
		if !isPnPRegistrationOperation(op.Section, op.Resource) || op.Action != "Create" || op.TargetID != "" || op.ParentID != "" {
			return false
		}
		digest := strings.TrimPrefix(op.Resource, "pnp-register:")
		decoded, err := hex.DecodeString(digest)
		return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == digest
	}
	if op.Section == "network_settings" {
		return networkSettingNoDispatchOperation(op)
	}
	if op.Section == "network_profiles" {
		return networkProfileMetadataKindSupported(op)
	}
	if op.Section == "templates" {
		return templateNoDispatchKindSupported(op)
	}
	if op.Section == "sites" {
		return siteSingularOperation(op) || siteDesignOperation(op) || siteRetirementDispatchKind(op)
	}
	if op.Section == "wireless" {
		return wirelessDefinitionNoDispatchResource(op.Resource) && (op.Action == "Create" && op.TargetID == "" || op.Action == "Update" && validAPIID(op.TargetID))
	}
	if op.Section != "feature_templates" {
		return false
	}
	if validFeatureCreateOperation(op) {
		return true
	}
	if op.Action == "Update" && canonicalWirelessFeatureUUID(op.TargetID) {
		for group, prefix := range map[string]string{"cleanair": "cleanair:", "rrm_fra": "rrm-fra:"} {
			local := strings.TrimPrefix(op.Resource, group+"/")
			if name, ok := strings.CutPrefix(local, prefix); ok && validProfileName(name) && len(name) <= 64 {
				return true
			}
		}
	}
	if _, ok := featureUpdateKey(op); ok {
		return true
	}
	_, ok := featureDeleteKey(op)
	return ok
}
func noDispatchWriteSupported(section string, write *configurationWrite) bool {
	if write == nil || write.noDispatch == nil {
		return false
	}
	op := &cvk.NetworkControllerConfigurationOperation{Section: section, Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent, CredentialVersions: write.identity.credentials}
	credential := discoveryCredentialNoDispatchKind(op)
	pnpRemoval := pnpRemovalSelected(op.Resource) && noDispatchKindSupported(op)
	if pnpRemoval && (write.submitCompleted == nil || write.submit != nil || write.submitCreated != nil) {
		return false
	}
	tag := validTagOwnedOperation(op) || validTagDeleteOperation(op)
	if tag && (write.submit == nil || write.submitCreated != nil || write.submitCompleted != nil) {
		return false
	}
	if section == "inventory" && !credential && !tag && !pnpRemoval && (write.submitCreated == nil || write.submit != nil || write.submitCompleted != nil) {
		return false
	}
	if credential && (write.submit == nil || write.submitCreated != nil || write.submitCompleted != nil) {
		return false
	}
	// Site, template and wireless-profile metadata bind their parent identity.
	// Only templates support a persisted dependency-settling interval.
	if write.identity.parent != "" || write.identity.preparationWait {
		if section != "templates" && section != "sites" && section != "network_profiles" || !validAPIID(write.identity.parent) || section != "templates" && write.identity.preparationWait {
			return false
		}
	}
	return noDispatchKindSupported(op) &&
		write.identity.serial == "" && write.identity.dependencies == nil && write.identity.groupTargets == nil && (len(write.identity.credentials) == 0 || credential) && len(write.identity.ssidCredentialHistory) == 0
}
func noDispatchOperationSupported(op *cvk.NetworkControllerConfigurationOperation) bool {
	return op != nil && configurationOperationID.MatchString(op.ID) && noDispatchKindSupported(op) &&
		op.ConfigName != "" && op.ConfigUID != "" && op.ConfigGeneration > 0 && op.ControllerGeneration > 0 &&
		op.TaskID == "" && op.CreatedResourceID == "" && op.SubmittedAt != nil && !op.SubmittedAt.IsZero() &&
		configurationMetadataPreparationSupported(op) && op.TargetSerial == "" && op.PhysicalGroup == nil && op.DependencyVersions == nil && op.CancellationRequestedAt == nil &&
		(len(op.CredentialVersions) == 0 || discoveryCredentialNoDispatchKind(op)) && len(op.SSIDCredentialHistory) == 0
}

// Only the typed credential executors may carry version labels through this
// common mechanism. Other inventory/credential families keep their old gates.
func discoveryCredentialNoDispatchKind(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Section != "inventory" {
		return false
	}
	if discoveryCredentialInputScopePrefix(op.Resource) != "" {
		_, valid := discoveryCredentialOperationInput(op)
		return valid
	}
	if discoveryFinalizeSelected(op.Resource) {
		// Source-uncommitted preparation-only envelopes cannot gain this path.
		if !strings.HasPrefix(op.Resource, "discovery-finalize:v2:") {
			return false
		}
	}
	_, valid := discoveryCredentialRetirementParts(op)
	return valid
}

func configurationMetadataPreparationSupported(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op.Section == "network_profiles" {
		return networkProfileMetadataKindSupported(op) && op.PreparedAt == nil
	}
	if op.Section == "sites" {
		return op.PreparedAt == nil && (validAPIID(op.ParentID) || siteDesignOperation(op))
	}
	if op.Section != "templates" {
		return op.ParentID == "" && op.PreparedAt == nil
	}
	return op.PreparedAt == nil || validAPIID(op.ParentID) && !op.PreparedAt.IsZero() && op.SubmittedAt != nil && !op.PreparedAt.After(op.SubmittedAt.Time)
}

func (a *adapter) retireProvenNoDispatch(ctx context.Context, before *cvk.NetworkControllerConfigurationOperation, proof *configurationPreSubmitRefusal) error {
	if proof == nil || before == nil || proof.operationID != before.ID || !proof.claimed.Load() || !proof.spent.CompareAndSwap(false, true) || !noDispatchOperationSupported(before) || before.Phase != "Submitting" {
		return errConfigurationTransition
	}
	receiptCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	ctx = receiptCtx
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		nc, err := a.configurationRecoveryController(ctx)
		if err != nil {
			return err
		}
		if nc.Generation != before.ControllerGeneration || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
			return errConfigurationStale
		}
		state := nc.Status.Configuration
		op := state.Operation
		if op.Phase != "Submitting" && op.Phase != "Unknown" {
			return errConfigurationStale
		}
		expected := before.DeepCopy()
		expected.Phase = op.Phase
		if !reflect.DeepEqual(expected, op) {
			return errConfigurationStale
		}
		owners := 0
		for _, owner := range state.Owners {
			if owner.Section == op.Section {
				if owner.ConfigName != op.ConfigName || owner.ConfigUID != op.ConfigUID {
					return errConfigurationOwned
				}
				owners++
			}
		}
		if owners != 1 {
			return errConfigurationOwned
		}
		for _, r := range state.ActionApplications {
			if r.Operation.ID == op.ID {
				return errConfigurationStale
			}
		}
		for _, r := range state.CredentialApplications {
			if r.OperationID == op.ID {
				return errConfigurationStale
			}
		}

		if err := a.retainPnPMetadataRefusal(state, before); err != nil {
			return err
		}
		retained := state.MetadataHandoff.DeepCopy()
		state.Operation = nil
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			return err
		}
		if retained != nil {
			fresh, err := a.configurationRecoveryController(ctx)
			if err != nil {
				return err
			}
			if fresh.Status.Configuration == nil || fresh.Status.Configuration.Operation != nil || featureIdentityDigest(fresh.Status.Configuration.MetadataHandoff) != featureIdentityDigest(retained) {
				return errConfigurationStale
			}
		}
		return nil
	})
}
