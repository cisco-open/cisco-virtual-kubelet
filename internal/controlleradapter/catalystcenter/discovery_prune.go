// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Distinguish a source-removal tombstone from the intermediate deletion used
// by same-revision replacement. Reintroduction requires a later bound input.
const discoveryPruneSuffix = ":prune"

func discoveryOperationNameHash(resource string) string {
	return strings.SplitN(strings.TrimPrefix(discoveryLogicalResource(resource), ownedDiscoveryPrefix), ":", 2)[0]
}
func (e *ownedDiscoveryExecutor) receiptByHash(hash string) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	prefix := ownedDiscoveryReceiptPrefix + hash + ":"
	var found *cvk.NetworkControllerConfigurationActionApplication
	for i := range e.receipts {
		raw := e.receipts[i]
		if e.retired[raw.Resource] && raw.Operation.ConfigUID != e.owner {
			continue
		}
		if !strings.HasPrefix(raw.Resource, prefix) {
			continue
		}
		if found != nil || raw.Operation.ConfigUID != e.owner || !validOwnedDiscoveryReceipt(raw) || discoveryOperationNameHash(raw.Operation.Resource) != hash {
			return nil, errConfigurationOwned
		}
		r := raw.DeepCopy()
		r.Section = "inventory"
		r.Operation = *localDiscoveryOperation(&raw.Operation)
		found = r
	}
	return found, nil
}

type discoveryPruneCandidate struct{ Resource, ID string }

func discoveryDispatchReceiptUnchanged(before, after []cvk.NetworkControllerConfigurationActionApplication, resource string) bool {
	selectReceipt := func(rows []cvk.NetworkControllerConfigurationActionApplication) []cvk.NetworkControllerConfigurationActionApplication {
		var selected []cvk.NetworkControllerConfigurationActionApplication
		prefix := ownedDiscoveryReceiptPrefix + discoveryOperationNameHash(resource) + ":"
		for _, r := range rows {
			if strings.HasPrefix(r.Resource, prefix) {
				selected = append(selected, r)
			}
		}
		return selected
	}
	return reflect.DeepEqual(selectReceipt(before), selectReceipt(after))
}

func (e *ownedDiscoveryExecutor) pruneCandidates(actual map[string]discoveryMetadata) ([]discoveryPruneCandidate, error) {
	if err := e.retiredAbsent(actual); err != nil {
		return nil, err
	}
	var out []discoveryPruneCandidate
	if !e.prune {
		return out, nil
	}
	wanted := map[string]bool{}
	for _, want := range e.base.desired {
		wanted[discoveryPublicDigest(want.name)] = true
	}
	seen := map[string]bool{}
	for _, r := range e.receipts {
		if r.Operation.ConfigUID != e.owner || !strings.HasPrefix(r.Resource, ownedDiscoveryReceiptPrefix) {
			continue
		}
		if !validOwnedDiscoveryReceipt(r) {
			return nil, errConfigurationOwned
		}
		hash := discoveryOperationNameHash(r.Operation.Resource)
		if seen[hash] {
			return nil, errConfigurationOwned
		}
		seen[hash] = true
		if wanted[hash] {
			continue
		}
		prefix := ownedDiscoveryReceiptPrefix + hash + ":"
		id := strings.TrimPrefix(r.Resource, prefix)
		var got *discoveryMetadata
		for name, row := range actual {
			if discoveryPublicDigest(name) == hash || row.ID == id {
				if discoveryPublicDigest(name) != hash || row.ID != id || got != nil {
					return nil, errConfigurationOwned
				}
				copy := row
				got = &copy
			}
		}
		if r.Operation.Action == "Delete" {
			if got != nil {
				return nil, errConfigurationOwned
			}
			continue
		}
		if got == nil {
			return nil, errConfigurationOwned
		}
		// This exact-ID API stops and removes the job. Native runtime status
		// is not an ownership signal and must not strand active/failed jobs.
		mask, digest, valid := ownedDiscoveryFingerprintParts(r.Operation.Resource)
		if !valid || mask == 0 {
			return nil, errConfigurationOwned
		}
		effect, err := discoveryNativeEffect(*got, mask)
		if err != nil || discoveryPublicDigest(effect) != digest {
			return nil, errConfigurationOwned
		}
		out = append(out, discoveryPruneCandidate{Resource: discoveryLogicalResource(r.Operation.Resource) + discoveryPruneSuffix, ID: id})
	}
	slices.SortFunc(out, func(a, b discoveryPruneCandidate) int { return strings.Compare(a.Resource, b.Resource) })
	return out, nil
}

func (e *ownedDiscoveryExecutor) retiredAbsent(actual map[string]discoveryMetadata) error {
	if e.retiredInvalid {
		return errConfigurationOwned
	}
	for _, r := range e.receipts {
		if !e.retired[r.Resource] {
			continue
		}
		if !validOwnedDiscoveryReceipt(r) || r.Operation.Action != "Delete" || !strings.HasSuffix(r.Operation.Resource, discoveryPruneSuffix) {
			return errConfigurationOwned
		}
		for _, row := range actual {
			if row.ID == r.Operation.TargetID {
				return errConfigurationOwned
			}
		}
	}
	return nil
}
func (e *ownedDiscoveryExecutor) guard(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
	if e.submissionGuard != nil {
		return e.submissionGuard(ctx, resource, action, identity)
	}
	return nil // Standalone native planner tests have no Kubernetes journal.
}
func (e *ownedDiscoveryExecutor) prunePlan(candidates []discoveryPruneCandidate, resolved []map[string]any) (*configurationWrite, error) {
	candidate := candidates[0]
	snapshot, _ := json.Marshal(resolved)
	var used atomic.Bool
	return &configurationWrite{resource: candidate.Resource, action: "Delete", identity: configurationResourceIdentity{target: candidate.ID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		actual, fresh, err := e.base.observe(ctx)
		if err != nil {
			return Task{}, err
		}
		raw, _ := json.Marshal(fresh)
		if !bytes.Equal(snapshot, raw) {
			return Task{}, errConfigurationStale
		}
		if _, _, err := e.selectAction(actual, fresh); err != nil && !errors.Is(err, errConfigurationBusy) {
			return Task{}, err
		}
		current, err := e.pruneCandidates(actual)
		if err != nil {
			return Task{}, err
		}
		if !slices.Equal(candidates, current) {
			return Task{}, errConfigurationStale
		}
		if err := e.guard(ctx, candidate.Resource, "Delete", configurationResourceIdentity{target: candidate.ID}); err != nil {
			return Task{}, err
		}
		return e.base.client.deleteDiscovery(ctx, candidate.ID)
	}}, nil
}

func pureDiscoverySource(raw []byte) bool {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return false
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return false
	}
	inventory, ok := root["inventory"].(map[string]any)
	if !ok {
		return false
	}
	for key := range inventory {
		if key != "discovery" {
			return false
		}
	}
	return true
}
func discoveryPruneSelected(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	if pureDiscoverySource(source.raw) {
		return true
	}
	if node, err := nacmodel.ParseDocument(source.raw); err == nil {
		var root map[string]any
		if node.Decode(&root) == nil {
			if inventory, ok := root["inventory"].(map[string]any); ok {
				if _, present := inventory["discovery"]; present {
					return true
				}
			}
		}
	}
	if nc.Status.Configuration != nil {
		for _, r := range nc.Status.Configuration.ActionApplications {
			if r.Operation.ConfigUID == string(config.UID) && strings.HasPrefix(r.Resource, ownedDiscoveryReceiptPrefix) {
				return true
			}
		}
	}
	return false
}
func validateDiscoveryPruneScope(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) error {
	if !pureDiscoverySource(source.raw) || config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && !(discoveryConfigSpecShape(config) && hasDiscoveryConfigFinalizer(config) && config.DeletionTimestamp.IsZero()) {
		return errConfigurationTransition
	}
	if nc.Status.Configuration == nil {
		return nil
	}
	state := nc.Status.Configuration
	for _, r := range state.CredentialApplications {
		if r.ConfigUID == string(config.UID) {
			return errConfigurationOwned
		}
	}
	for _, r := range state.ActionApplications {
		if r.Operation.ConfigUID == string(config.UID) && (r.Section != "inventory" || !strings.HasPrefix(r.Resource, ownedDiscoveryReceiptPrefix)) {
			return errConfigurationOwned
		}
	}
	for _, owner := range state.Owners {
		if owner.ConfigUID == string(config.UID) && owner.Section != "inventory" {
			return errConfigurationOwned
		}
	}
	return nil
}
