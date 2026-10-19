// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Attach to existing resource executors, including mixed credential/network and
// fabric/IPAM groups. The native planner, group prefixes and transport stay shared.
func (a *adapter) bindIPAMOwnership(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if nc == nil || config == nil || source == nil || plan == nil {
		return errConfigurationTransition
	}
	// An already accepted legacy operation retains its original recovery protocol.
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		legacy := strings.TrimPrefix(op.Resource, "network_settings/")
		if strings.HasPrefix(legacy, "ip-pool:") || strings.HasPrefix(legacy, "ip-reservation:") {
			return nil
		}
	}
	type boundIPAM struct {
		executor *ipamConfigurationExecutor
		prefix   string
	}
	var bound []boundIPAM
	seen := map[*ipamConfigurationExecutor]bool{}
	var walk func(configurationExecutor, string)
	walk = func(executor configurationExecutor, prefix string) {
		switch e := executor.(type) {
		case *configurationGroup:
			for _, part := range e.parts {
				walk(part.executor, prefix+part.name+"/")
			}
		case *networkSectionExecutor:
			if e == nil {
				return
			}
			walk(e.ipam, prefix)
		case *networkCredentialConfigurationExecutor:
			if e == nil {
				return
			}
			walk(e.network, prefix)
		case *fabricConfiguration:
			walk(e.configurationExecutor, prefix)
		case *fabricAnchorAdmissionGate:
			walk(e.configurationExecutor, prefix)
		case *fabricExtranetAdmissionGate:
			walk(e.configurationExecutor, prefix)
		case *fabricMulticastConfiguration:
			walk(e.foundation, prefix)
		case *fabricVLANSSIDConfiguration:
			walk(e.foundation, prefix)
		case *ipamConfigurationExecutor:
			if e == nil || (e.count() == 0 && !e.prune) || seen[e] {
				return
			}
			seen[e] = true
			e.owner = string(config.UID)
			if nc.Status.Configuration != nil {
				e.receipts = nc.Status.Configuration.DeepCopy().ActionApplications
			}
			bound = append(bound, boundIPAM{e, prefix})
		}
	}
	walk(plan.executor, "")
	if len(bound) == 0 {
		return nil
	}
	for _, b := range bound {
		if config.UID == "" || plan.section != "network_settings" && plan.section != "fabric" || plan.section == "network_settings" && b.prefix != "" || plan.section == "fabric" && b.prefix != "network_settings/" {
			return errConfigurationTransition
		}
	}
	find := func(op *cvk.NetworkControllerConfigurationOperation) *ipamConfigurationExecutor {
		if !validIPAMOwnedOperation(op) || op.ConfigUID != string(config.UID) || op.Section != plan.section {
			return nil
		}
		for _, b := range bound {
			if strings.HasPrefix(op.Resource, b.prefix+ipamOwnedPrefix) && b.executor.legacyOwnedResource(op.Resource) != "" {
				return b.executor
			}
		}
		return nil
	}
	for _, b := range bound {
		b.executor.submissionGuard = func(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
			return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, plan.section, b.prefix+resource, action, identity, validIPAMOwnedOperation, ipamOwnedCapacity)
		}
	}
	prior := *plan
	if prior.observeCompletion != nil {
		plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
			if ipamOwnedSelected(op.Resource) {
				if find(op) == nil {
					return false, errConfigurationTransition
				}
				return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
			}
			return prior.observeCompletion(ctx, op, now)
		}
	}
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return ipamOwnedSelected(op.Resource) || prior.recordCompletion != nil && (prior.receiptOperation == nil || prior.receiptOperation(op))
	}
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if ipamOwnedSelected(op.Resource) {
			return find(op) != nil && op.Phase == "Unknown" && validAPIID(op.TaskID) && op.SubmittedAt != nil && !op.SubmittedAt.IsZero()
		}
		return prior.canObserveUnknownTask(op)
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if !ipamOwnedSelected(op.Resource) {
			if prior.recordCompletion == nil {
				return errConfigurationTransition
			}
			return prior.recordCompletion(ctx, op)
		}
		e := find(op)
		if e == nil {
			return errConfigurationTransition
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.Generation != nc.Generation || current.UID != nc.UID || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) || op.Phase != "Verifying" || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != current.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
			return errConfigurationStale
		}
		if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return err
		}
		id, done, err := e.verifyOwned(ctx, op)
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		state := current.Status.Configuration
		if err := ipamOwnedCapacity(state, op); err != nil {
			return err
		}
		prefix := ipamResultKey(op.Resource, "")
		retained := state.ActionApplications[:0]
		for _, r := range state.ActionApplications {
			if !strings.HasPrefix(r.Resource, prefix) {
				retained = append(retained, r)
			}
		}
		state.ActionApplications = retained
		state.Operation.Phase = "Verified"
		now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		if op.SubmittedAt.After(now.Time) {
			now = *op.SubmittedAt.DeepCopy()
		}
		state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: prefix + id, Operation: *state.Operation.DeepCopy(), VerifiedAt: &now})
		return a.persistConfigurationActionReceipt(ctx, current)
	}
	return nil
}
