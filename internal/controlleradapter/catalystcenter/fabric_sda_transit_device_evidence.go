// Copyright 2026 Cisco Systems Inc.
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

var errSDATransitDeviceEvidencePending = errors.New("post-task SDA transit device evidence is pending")

const sdaTransitEvidenceAnnotation = "cisco.vk/sda-transit-device-evidence"

type sdaTransitDeviceEvidence struct{ outputs []string }

func (sdaTransitDeviceEvidence) String() string   { return "<private SDA device evidence>" }
func (sdaTransitDeviceEvidence) GoString() string { return "<private SDA device evidence>" }

func sdaTransitEvidenceCommands() []string {
	return fabricDeviceEvidenceCommands()
}

// Read-only operations run on existing device workers. Complete task/readback,
// source/owner/journal and current group authority bracket all reads. Results
// remain private and are not a convergence, creator or reservation-release proof.
func (a *adapter) collectSDATransitDeviceEvidence(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want sdaTransitIntent, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) (map[string]sdaTransitDeviceEvidence, error) {
	want = freezeSDATransitIntent(want)
	if a == nil || a.client == nil || config == nil || source == nil || op == nil || op.Phase != "Verifying" || authorize == nil || !a.configurationGroupAdmissionReady() || now.IsZero() || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationDeviceAdmission
	}
	// Native/task inventories bracket the whole batch. Between individual reads
	// repeat source/owner/journal/authority checks without N full CP inventories.
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
	collect := func() (*cvk.NetworkController, *sdaTransitSnapshot, []*ops.ControllerConfigurationReservation, time.Time, error) {
		done, err := a.observeOwnedSDATransit(ctx, config, source, want, op, now)
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		if !done {
			return nil, nil, nil, time.Time{}, errConfigurationBusy
		}
		nc, bound, err := check()
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		if nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) {
			return nil, nil, nil, time.Time{}, errConfigurationStale
		}
		s, targets, err := a.client.sdaTransitTargets(ctx, a.statusReader, config.Namespace, want)
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) || s.actual == nil || sdaTransitResource(s) != op.Resource {
			return nil, nil, nil, time.Time{}, errConfigurationStale
		}
		completed, err := a.client.configurationTaskTreeCompletion(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		if completed.IsZero() || !now.UTC().Truncate(time.Second).After(completed) {
			return nil, nil, nil, time.Time{}, errSDATransitDeviceEvidencePending
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		fresh, err := a.currentController(ctx)
		if err != nil {
			return nil, nil, nil, time.Time{}, err
		}
		if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion || !reflect.DeepEqual(fresh.Status.Configuration.Operation, op) {
			return nil, nil, nil, time.Time{}, errConfigurationStale
		}
		return nc, s, bound, completed, nil
	}
	nc, before, bound, completed, err := collect()
	if err != nil {
		return nil, err
	}
	commands := sdaTransitEvidenceCommands()
	// Bind native result identity and complete query plan in addition to the
	// existing operation/group/task/worker identity and full task completion time.
	raw, err := json.Marshal([]any{op.Resource, before.actual, commands})
	if err != nil {
		return nil, err
	}
	result := fmt.Sprintf("%x", sha256.Sum256(raw))
	physical := map[string]Device{}
	for _, d := range before.physical {
		physical[d.ID] = d
	}
	evidence := make(map[string]sdaTransitDeviceEvidence, len(bound))
	pending := false
	for _, member := range bound {
		current, _, err := check()
		if err != nil {
			return nil, err
		}
		if current.ResourceVersion != nc.ResourceVersion {
			return nil, errConfigurationStale
		}
		d, exists := physical[member.Spec.InventoryID]
		if !exists {
			return nil, errConfigurationStale
		}
		expected := configurationDeviceProofOperation(nc, op, member, result, completed, commands, "cc-sda-evidence-", sdaTransitEvidenceAnnotation)
		outputs, err := a.readConfigurationDeviceProof(ctx, expected, member, sdaTransitEvidenceAnnotation, completed, now, errSDATransitDeviceEvidencePending)
		if errors.Is(err, errSDATransitDeviceEvidencePending) {
			pending = true
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := verifyConfigurationChassisSerial(d.Serial, outputs[0]); err != nil {
			return nil, err
		}
		evidence[member.Spec.InventoryID] = sdaTransitDeviceEvidence{outputs: outputs}
	}
	current, after, _, currentCompleted, err := collect()
	if err != nil {
		return nil, err
	}
	if current.ResourceVersion != nc.ResourceVersion || !completed.Equal(currentCompleted) || !reflect.DeepEqual(before, after) {
		return nil, errConfigurationStale
	}
	if pending {
		return nil, errSDATransitDeviceEvidencePending
	}
	return evidence, nil
}
