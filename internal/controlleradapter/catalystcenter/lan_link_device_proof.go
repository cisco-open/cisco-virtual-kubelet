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

var errLANLinkDeviceProofPending = errors.New("post-task LAN device operation evidence is pending")

const lanLinkProofBindingAnnotation = "cisco.vk/lan-link-proof"

// Read-only device work is delegated to existing DeviceOperation workers. The
// CC connector never receives device credentials or opens a device transport.
// Stable names retain at most one read per operation/worker/result. Existing
// terminal TTL cleanup permits retries without accumulating offline-worker reads
// or taking over the configuration reservation lifecycle.
func (a *adapter) lanLinkDeviceConvergenceProof(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want lanLinkIntent, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) configurationGroupConvergenceProof {
	return a.lanLinkWorkerProof(config, source, members, authorize, now, func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (*lanLinkProofState, error) {
		native, current, err := a.client.lanLinkCompletionTargets(ctx, a.statusReader, config.Namespace, want, op, now)
		if err != nil {
			return nil, err
		}
		result, err := lanLinkAddResultDigest(native)
		if err != nil {
			return nil, err
		}
		return &lanLinkProofState{native.dependencies.adjacency.endpoints, current, result}, nil
	}, lanLinkProofCommands, verifyLANLinkCLI)
}

type lanLinkProofState struct {
	endpoints *lanLinkEndpointSnapshot
	targets   []contract.GroupTargetIdentity
	result    string
}

// One shared worker lifecycle and evidence envelope for both LAN actions.
func (a *adapter) lanLinkWorkerProof(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time, collect func(context.Context, *cvk.NetworkControllerConfigurationOperation) (*lanLinkProofState, error), commands func(string) []string, verify func(lanLinkEndpoint, lanLinkEndpoint, []string) error) configurationGroupConvergenceProof {
	return func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, targets []contract.GroupTargetIdentity, completed time.Time) error {
		if authorize == nil || !now.UTC().Truncate(time.Second).After(completed) {
			return errLANLinkDeviceProofPending
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) {
			return errConfigurationStale
		}
		bound, err := contract.ValidateGroupReservationSet(nc, members)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return errConfigurationStale
		}
		state, err := collect(ctx, op)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(state.targets, targets) {
			return errConfigurationStale
		}
		endpoints := state.endpoints

		pending := false
		for _, member := range bound {
			if err := authorize(ctx, bound); err != nil {
				return err
			}
			endpoint, peer := endpoints.primary, endpoints.secondary
			if member.Spec.InventoryID == endpoints.secondary.device.ID {
				endpoint, peer = peer, endpoint
			}
			if member.Spec.InventoryID != endpoint.device.ID {
				return errConfigurationStale
			}
			expected := lanLinkProofOperationWithCommands(nc, op, member, state.result, completed, commands(endpoint.port.PortName))
			outputs, err := a.readConfigurationDeviceProof(ctx, expected, member, lanLinkProofBindingAnnotation, completed, now, errLANLinkDeviceProofPending)
			if errors.Is(err, errLANLinkDeviceProofPending) {
				pending = true
				continue
			}
			if err != nil {
				return err
			}
			if err := verify(endpoint, peer, outputs); err != nil {
				return err
			}
		}
		if pending {
			return errLANLinkDeviceProofPending
		}
		return authorize(ctx, bound)
	}
}

func lanLinkProofOperation(nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation, member *ops.ControllerConfigurationReservation, port, result string, completed time.Time) *ops.DeviceOperation {
	return lanLinkProofOperationWithCommands(nc, op, member, result, completed, lanLinkProofCommands(port))
}

func lanLinkProofOperationWithCommands(nc *cvk.NetworkController, op *cvk.NetworkControllerConfigurationOperation, member *ops.ControllerConfigurationReservation, result string, completed time.Time, commands []string) *ops.DeviceOperation {
	return configurationDeviceProofOperation(nc, op, member, result, completed, commands, "cc-lan-proof-", lanLinkProofBindingAnnotation)
}

func validateLANLinkProofOperation(leaf, expected *ops.DeviceOperation, member *ops.ControllerConfigurationReservation, completed, now time.Time) error {
	return validateConfigurationDeviceProofOperation(leaf, expected, member, completed, now, lanLinkProofBindingAnnotation, errLANLinkDeviceProofPending)
}
