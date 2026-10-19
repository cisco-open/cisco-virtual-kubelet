// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func supportedPnPRegistrationConfig(config *configapi.NetworkControllerConfig) bool {
	return config != nil && config.Spec.ModelSource.Resolved && config.Spec.ModelSource.Format == nacmodel.Format && config.Spec.ModelSource.ModelVersion == nacmodel.Version && supportedConfigurationApply(config) && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "inventory" && len(config.Spec.SecretRefs) == 0 && config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete && config.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete
}

// Registration-only stage shared by public reconciliation and qualification.
// A true result attests owned PnP database
// records, never claim completion or permission for physical onboarding. Public
// PnP claiming still requires identity admission and workflow completion proof.
// Each invocation submits at most one POST or advances one retained journal step.
func (a *adapter) reconcilePnPRegistrationStage(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (bool, error) {
	if a == nil || !supportedPnPRegistrationConfig(config) {
		return false, errConfigurationTransition
	}
	var raw []byte
	if source != nil {
		raw = source.raw
	} else if config.Spec.Source.Inline != nil {
		raw = config.Spec.Source.Inline.Raw
	}
	wants, err := parsePnPInventory(raw)
	if err != nil {
		return false, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return false, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return false, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		if !isPnPRegistrationOperation(op.Section, op.Resource) {
			return false, errConfigurationBusy
		}
		var selected *pnpDeviceIntent
		for i := range wants {
			body, err := pnpRegistrationPayload(wants[i])
			if err != nil {
				return false, err
			}
			if fmt.Sprintf("pnp-register:%x", sha256.Sum256(body)) == op.Resource {
				selected = &wants[i]
			}
		}
		if selected == nil {
			return false, errConfigurationStale
		}
		// Use the existing owner/source/journal bracket even when no native
		// call is needed. Foreign or pruned work cannot be retired by this stage.
		if _, err := a.observeOwnedInventoryDeployment(ctx, config, source, op, func(context.Context) (bool, error) { return true, nil }); err != nil {
			return false, err
		}
		switch op.Phase {
		case "Prepared":
			if op.Action != "Create" || op.SubmittedAt != nil || op.CreatedResourceID != "" || op.TargetID != "" || op.TargetSerial != "" || op.ParentID != "" || op.TaskID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 {
				return false, errConfigurationTransition
			}
			return false, a.finishConfiguration(ctx, op.ID)
		case "Verified":
			return false, a.finishConfiguration(ctx, op.ID)
		case "Verifying":
			return false, a.recordOwnedPnPRegistration(ctx, config, source, *selected, op.ID)
		default:
			// A lost acknowledgement cannot be recovered by matching a serial.
			return false, errConfigurationBusy
		}
	}
	baseline, err := a.observeStablePnPRegistrationCohort(ctx, config, source, wants)
	if err != nil {
		return false, err
	}
	for _, want := range wants {
		body, _ := pnpRegistrationPayload(want)
		resource := fmt.Sprintf("pnp-register:%x", sha256.Sum256(body))
		if baseline[resource] != "" {
			continue
		}
		write, err := a.planPnPCohortRegistrationWrite(ctx, config, source, wants, want, baseline)
		if err != nil {
			return false, err
		}
		return false, a.submitConfigurationWrite(ctx, config, source, "inventory", write)
	}
	return true, nil
}

// Both registration entry points share the complete cohort's stable ownership,
// absence and capacity observation. Neither may narrow admission to one device.
func (a *adapter) observeStablePnPRegistrationCohort(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, wants []pnpDeviceIntent) (map[string]string, error) {
	baseline, err := a.observePnPRegistrationCohort(ctx, config, source, wants, nil)
	if err != nil {
		return nil, err
	}
	confirmed, err := a.observePnPRegistrationCohort(ctx, config, source, wants, nil)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(baseline, confirmed) {
		return nil, errConfigurationStale
	}
	return baseline, nil
}

func (a *adapter) planPnPCohortRegistrationWrite(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, wants []pnpDeviceIntent, want pnpDeviceIntent, baseline map[string]string) (*configurationWrite, error) {
	if a == nil || !supportedPnPRegistrationConfig(config) {
		return nil, errConfigurationTransition
	}
	// A private caller may retain and later update its input object. Dispatch
	// must keep the original authoritative revision, not follow that pointer.
	config = config.DeepCopy()
	if source != nil {
		frozen := *source
		frozen.raw = append([]byte(nil), source.raw...)
		source = &frozen
	}

	plan, err := a.client.planPnPRegistration(ctx, want)
	if err != nil {
		return nil, err
	}
	write, err := plan.configurationWrite()
	if err != nil {
		return nil, err
	}
	resource := write.resource
	if id, present := baseline[resource]; !present || id != "" {
		return nil, errConfigurationStale
	}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.Status.Configuration == nil || current.Status.Configuration.Operation == nil {
			return errConfigurationStale
		}
		op := current.Status.Configuration.Operation.DeepCopy()
		if op.Phase != "Submitting" || op.Resource != resource || op.Action != "Create" || op.CreatedResourceID != "" || op.TargetID != "" || op.TargetSerial != "" || op.ParentID != "" || op.TaskID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || len(op.CredentialVersions) != 0 {
			return errConfigurationStale
		}
		_, err = a.observeOwnedInventoryDeployment(ctx, config, source, op, func(ctx context.Context) (bool, error) {
			for range 2 {
				fresh, err := a.observePnPRegistrationCohort(ctx, config, source, wants, op)
				if err != nil {
					return false, err
				}
				if !reflect.DeepEqual(baseline, fresh) {
					return false, errConfigurationStale
				}
			}
			return true, nil
		})
		if err != nil {
			return err
		}
		return nil
	})
	return write, nil
}

// One pair of complete native inventories serves all declarations. The result
// contains only registration hashes and owned UUIDs, never later claim variables.
func (a *adapter) observePnPRegistrationCohort(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, wants []pnpDeviceIntent, active *cvk.NetworkControllerConfigurationOperation) (map[string]string, error) {
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	var op *cvk.NetworkControllerConfigurationOperation
	if nc.Status.Configuration != nil {
		op = nc.Status.Configuration.Operation
	}
	if !reflect.DeepEqual(op, active) {
		return nil, errConfigurationBusy
	}
	pnp, err := a.client.pnpDevices(ctx)
	if err != nil {
		return nil, err
	}
	inventory, err := a.client.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]pnpDevice{}
	for _, row := range pnp {
		byID[row.ID] = row
	}
	result := map[string]string{}
	for _, want := range wants {
		body, err := pnpRegistrationPayload(want)
		if err != nil {
			return nil, err
		}
		resource := fmt.Sprintf("pnp-register:%x", sha256.Sum256(body))
		receipt, err := ownedPnPRegistrationReceipt(nc, config, want)
		if err != nil {
			return nil, err
		}
		result[resource] = ""
		if receipt == nil {
			serials, err := validatePnPDeviceIntent(want)
			if err != nil {
				return nil, err
			}
			wanted := map[string]bool{}
			for _, serial := range serials {
				wanted[serial] = true
			}
			if err := requirePnPRegistrationAbsentFromInventory(wanted, pnp, inventory); err != nil {
				return nil, err
			}
			continue
		}
		row, ok := byID[receipt.CreatedResourceID]
		if !ok {
			return nil, errConfigurationBusy
		}
		serials, _ := validatePnPDeviceIntent(want)
		if !pnpRegistrationMatches(pnpRegistrationReceipt{deviceID: receipt.CreatedResourceID, serial: serials[0], pid: want.PID, hostname: want.Name, stack: len(serials) > 1}, row) {
			return nil, errConfigurationStale
		}
		result[resource] = receipt.CreatedResourceID
	}
	if err := pnpRegistrationCohortCapacity(nc, config, source, result); err != nil {
		return nil, err
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
	return result, nil
}
