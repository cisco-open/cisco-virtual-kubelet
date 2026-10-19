// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const discoveryCredentialRetirePrefix = "discovery-credential-retire:v1:"
const discoveryCredentialPrunePrefix = "discovery-credential-prune:v1:"
const discoveryCredentialRetiredPrefix = "discovery-credential-retired:v1:"

func discoveryCredentialRetirementSelected(resource string) bool {
	if parts := discoveryFinalizeParts(resource); parts != nil && strings.HasPrefix(parts[3], discoveryCredentialPrunePrefix) {
		return true
	}
	return strings.HasPrefix(resource, "discovery-credential-retire:") || strings.HasPrefix(resource, "discovery-credential-prune:")
}
func discoveryCredentialRetiredSelected(resource string) bool {
	return strings.HasPrefix(resource, "discovery-credential-retired:")
}

type discoveryCredentialRetirement struct {
	creator, pair, next, scope string
	prune                      bool
}

func discoveryCredentialRetirementParts(op *cvk.NetworkControllerConfigurationOperation) (discoveryCredentialRetirement, bool) {
	var result discoveryCredentialRetirement
	if op != nil && discoveryFinalizeSelected(op.Resource) {
		parts := discoveryFinalizeParts(op.Resource)
		if parts == nil || !strings.HasPrefix(parts[3], discoveryCredentialPrunePrefix) {
			return result, false
		}
		local := op.DeepCopy()
		local.Resource = parts[3]
		op = local
	}
	if op == nil || op.Section != "inventory" || op.Action != "Delete" || !validAPIID(op.TargetID) || op.ParentID != "" || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || op.PreparedAt != nil || len(op.SSIDCredentialHistory) > 0 || !(strings.HasPrefix(op.Resource, discoveryCredentialRetirePrefix) || strings.HasPrefix(op.Resource, discoveryCredentialPrunePrefix)) {
		return result, false
	}
	prefix := discoveryCredentialRetirePrefix
	prune := strings.HasPrefix(op.Resource, discoveryCredentialPrunePrefix)
	if prune {
		prefix = discoveryCredentialPrunePrefix
	}
	parts := strings.SplitN(strings.TrimPrefix(op.Resource, prefix), ":", 3)
	if len(parts) != 3 || !configurationOperationID.MatchString(parts[0]) || !lowerDiscoveryDigest(parts[1]) {
		return result, false
	}
	if prune {
		if !lowerDiscoveryDigest(parts[2]) || len(op.CredentialVersions) != 0 {
			return result, false
		}
		return discoveryCredentialRetirement{creator: parts[0], pair: parts[1], scope: parts[2], prune: true}, true
	}
	base, context, ok := discoveryCredentialInputParts(parts[2])
	metadata := strings.HasPrefix(parts[2], discoveryCredentialMetadataScopePrefix) && discoveryCredentialInputPrototype(parts[2]) == parts[2]
	if !ok || !metadata && base != parts[2] || context != "" {
		return result, false
	}
	probe := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Resource: parts[2], Action: "Create", CredentialVersions: op.CredentialVersions}
	if _, ok := discoveryCredentialOperationInput(probe); !ok {
		return result, false
	}
	return discoveryCredentialRetirement{creator: parts[0], pair: parts[1], next: parts[2]}, true
}

type discoveryCredentialCreatorProof struct {
	receipt       cvk.NetworkControllerConfigurationActionApplication
	application   cvk.NetworkControllerConfigurationCredentialApplication
	identity      discoveryCredentialReceiptIdentity
	base, context string
}

func (p *discoveryCredentialCreatorProof) digest() string {
	if strings.HasPrefix(p.receipt.Operation.Resource, discoveryCredentialMetadataScopePrefix) {
		return featureIdentityDigest([]any{p.receipt})
	}
	return featureIdentityDigest([]any{p.receipt, p.application})
}

type discoveryCredentialRetirementProof struct {
	receipt cvk.NetworkControllerConfigurationActionApplication
	data    discoveryCredentialRetirement
}

func (p *discoveryCredentialRetirementProof) digest() string { return featureIdentityDigest(p.receipt) }

type discoveryCredentialLineage struct {
	archives []*discoveryCredentialLineage
	pending  *cvk.NetworkControllerConfigurationOperation
	creators map[string]*discoveryCredentialCreatorProof
	retired  map[string]*discoveryCredentialRetirementProof
	latest   map[string]*discoveryCredentialCreatorProof
	tip      map[string]*discoveryCredentialRetirementProof
}

// Pure validation of the existing shared audit. Retirement and successor links
// retain all predecessor versions; dropped history cannot reset a field's floor.
func discoveryCredentialLineages(state *cvk.NetworkControllerConfigurationStatus, owner string) (*discoveryCredentialLineage, error) {
	h := &discoveryCredentialLineage{creators: map[string]*discoveryCredentialCreatorProof{}, retired: map[string]*discoveryCredentialRetirementProof{}, latest: map[string]*discoveryCredentialCreatorProof{}, tip: map[string]*discoveryCredentialRetirementProof{}}
	if state == nil {
		return h, nil
	}
	if op := state.Operation; op != nil && op.ConfigUID == owner {
		if _, ok := discoveryCredentialOperationInput(op); ok {
			h.pending = op.DeepCopy()
		}
	}
	ids := map[string]bool{}
	operationIDs := map[string]int{}
	for _, r := range state.ActionApplications {
		// Operation IDs are shared-journal identities, including unrelated
		// resource families. A collision cannot be attributed to this lineage.
		operationIDs[r.Operation.ID]++
		if discoveryCredentialRetirementSelected(r.Operation.Resource) && !discoveryCredentialRetiredSelected(r.Resource) {
			return nil, errConfigurationOwned
		}
		if discoveryCredentialInputSelected(r.Operation.Resource) && !discoveryCredentialReceiptSelected(r.Resource) {
			return nil, errConfigurationOwned
		}
	}
	for _, r := range state.ActionApplications {
		if (discoveryCredentialReceiptSelected(r.Resource) || discoveryCredentialRetiredSelected(r.Resource)) && operationIDs[r.Operation.ID] != 1 {
			return nil, errConfigurationOwned
		}
	}
	for _, r := range state.ActionApplications {
		if !discoveryCredentialReceiptSelected(r.Resource) {
			continue
		}
		if r.Operation.ConfigUID != owner || !discoveryCredentialReceiptPairPresent(state, &r.Operation) || h.creators[r.Operation.ID] != nil {
			return nil, errConfigurationOwned
		}
		identity, err := parseDiscoveryCredentialReceipt(r.Resource)
		if err != nil || ids[identity.id] {
			return nil, errConfigurationOwned
		}
		ids[identity.id] = true
		base, context, ok := discoveryCredentialInputParts(r.Operation.Resource)
		if !ok {
			return nil, errConfigurationOwned
		}
		proof := &discoveryCredentialCreatorProof{receipt: *r.DeepCopy(), identity: identity, base: base, context: context}
		for _, app := range state.CredentialApplications {
			if app.OperationID == r.Operation.ID {
				proof.application = *app.DeepCopy()
			}
		}
		h.creators[r.Operation.ID] = proof
	}
	for _, app := range state.CredentialApplications {
		if discoveryCredentialInputSelected(app.Resource) && h.creators[app.OperationID] == nil {
			return nil, errConfigurationOwned
		}
	}
	retirementDigests := map[string]*discoveryCredentialRetirementProof{}
	for _, r := range state.ActionApplications {
		if !discoveryCredentialRetiredSelected(r.Resource) {
			continue
		}
		op := &r.Operation
		data, ok := discoveryCredentialRetirementParts(op)
		creator := h.creators[data.creator]
		if !ok || creator == nil || creator.identity.legacy || h.retired[data.creator] != nil || op.ConfigUID != owner || op.ConfigName != creator.receipt.Operation.ConfigName || op.ConfigGeneration < creator.receipt.Operation.ConfigGeneration || op.ControllerGeneration < 1 || !configurationOperationID.MatchString(op.ID) || op.Phase != "Verified" || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) || op.SubmittedAt.Before(creator.receipt.VerifiedAt) || r.Section != "inventory" || r.Resource != discoveryCredentialRetiredPrefix+data.creator+":"+op.TargetID || op.TargetID != creator.identity.id || data.pair != creator.digest() {
			return nil, errConfigurationOwned
		}
		if !data.prune {
			probe := &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Resource: data.next, Action: "Create", CredentialVersions: op.CredentialVersions}
			name, valid := discoveryCredentialOperationInput(probe)
			if !valid || name != creator.identity.name {
				return nil, errConfigurationOwned
			}
		}
		proof := &discoveryCredentialRetirementProof{receipt: *r.DeepCopy(), data: data}
		h.retired[data.creator] = proof
		retirementDigests[proof.digest()] = proof
	}
	for _, app := range state.CredentialApplications {
		if discoveryCredentialRetirementSelected(app.Resource) || discoveryCredentialRetiredSelected(app.Resource) {
			return nil, errConfigurationOwned
		}
		for _, retirement := range h.retired {
			// Delete binds the queued versions but never applies them. A generic
			// credential receipt cannot turn its successful task into delivery.
			if app.OperationID == retirement.receipt.Operation.ID {
				return nil, errConfigurationOwned
			}
		}
	}
	roots := map[string]*discoveryCredentialCreatorProof{}
	children := map[string]*discoveryCredentialCreatorProof{}
	for _, creator := range h.creators {
		name := creator.identity.name
		if creator.context == "" {
			if roots[name] != nil {
				return nil, errConfigurationOwned
			}
			roots[name] = creator
			continue
		}
		parent := retirementDigests[creator.context]
		if parent == nil || children[creator.context] != nil || (!parent.data.prune && !discoveryCredentialRetirementNextMatches(parent.data.next, creator.receipt.Operation.Resource)) || (parent.data.prune && !configurationInputAfter(&creator.receipt.Operation, &parent.receipt.Operation)) || creator.receipt.Operation.ConfigGeneration < parent.receipt.Operation.ConfigGeneration || creator.receipt.Operation.SubmittedAt.Before(parent.receipt.VerifiedAt) {
			return nil, errConfigurationOwned
		}
		previous := h.creators[parent.data.creator]
		if previous == nil || previous.identity.name != name {
			return nil, errConfigurationOwned
		}
		children[creator.context] = creator
	}
	visited := map[string]bool{}
	// Validate transitions against ancestors only. Looking at every retained
	// creator here would incorrectly compare old transitions with future floors.
	prefix := &discoveryCredentialLineage{creators: map[string]*discoveryCredentialCreatorProof{}, latest: map[string]*discoveryCredentialCreatorProof{}}
	for name, current := range roots {
		for {
			id := current.receipt.Operation.ID
			if visited[id] {
				return nil, errConfigurationOwned
			}
			visited[id] = true
			h.latest[name] = current
			if err := prefix.transition(name, current.receipt.Operation.CredentialVersions); err != nil {
				return nil, err
			}
			prefix.creators[id] = current
			prefix.latest[name] = current
			retirement := h.retired[id]
			if retirement == nil {
				break
			}
			if !retirement.data.prune {
				if err := prefix.transition(name, retirement.receipt.Operation.CredentialVersions); err != nil {
					return nil, err
				}
			}
			h.tip[name] = retirement
			next := children[retirement.digest()]
			if next == nil {
				break
			}
			current = next
		}
	}
	if len(visited) != len(h.creators) {
		return nil, errConfigurationOwned
	}
	return h, nil
}

func discoveryCredentialFieldForPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "discovery" {
		return ""
	}
	for _, field := range discoveryCredentialFields {
		if field.model == parts[3] {
			return field.api
		}
	}
	return ""
}

// Use the common active-field rotation rules, with retained creator history for
// removed fields. Reintroducing a removed leaf must advance its high-water mark.
func (h *discoveryCredentialLineage) transition(name string, desired []cvk.NetworkControllerConfigurationCredentialVersion) error {
	for _, history := range h.archives {
		if err := discoveryCredentialSuccessorVersions(history, name, desired); err != nil {
			return err
		}
	}
	floors := map[string]int64{}
	active := map[string]bool{}
	for _, creator := range h.creators {
		if creator.identity.name == name {
			for _, v := range creator.receipt.Operation.CredentialVersions {
				field := discoveryCredentialFieldForPath(v.Path)
				if field == "" {
					return errConfigurationSecret
				}
				if v.Version > floors[field] {
					floors[field] = v.Version
				}
			}
		}
	}
	var prior []cvk.NetworkControllerConfigurationCredentialVersion
	if latest := h.latest[name]; latest != nil {
		for _, v := range latest.receipt.Operation.CredentialVersions {
			active[discoveryCredentialFieldForPath(v.Path)] = true
			for _, next := range desired {
				if discoveryCredentialFieldForPath(next.Path) == discoveryCredentialFieldForPath(v.Path) {
					prior = append(prior, v)
				}
			}
		}
	}
	if _, err := configurationCredentialRotation(prior, desired, discoveryCredentialFieldForPath); err != nil {
		return err
	}
	for _, v := range desired {
		field := discoveryCredentialFieldForPath(v.Path)
		if v.Version < floors[field] || !active[field] && floors[field] > 0 && v.Version <= floors[field] {
			return errConfigurationSecret
		}
	}
	return nil
}

func (h *discoveryCredentialLineage) input(name, prototype string) (string, error) {
	base, context, ok := discoveryCredentialInputParts(prototype)
	if !ok || context != "" {
		return "", errConfigurationTransition
	}
	latest := h.latest[name]
	// An unchanged completed incarnation retains its original resource/version.
	// Acquiring a new wire format must never replay write-only delivery.
	if latest != nil && h.retired[latest.receipt.Operation.ID] == nil && latest.base == base {
		return latest.receipt.Operation.Resource, nil
	}
	bind := func(input string) (string, error) {
		if latest == nil {
			return input, nil
		}
		if retirement := h.retired[latest.receipt.Operation.ID]; retirement != nil {
			if !retirement.data.prune && !discoveryCredentialRetirementNextMatches(retirement.data.next, input) {
				return "", errConfigurationStale
			}
			return discoveryCredentialContinuation(input, retirement.digest()), nil
		}
		if latest.context != "" {
			return discoveryCredentialContinuation(input, latest.context), nil
		}
		return input, nil
	}
	if h.pending != nil && discoveryCredentialInputScopePrefix(h.pending.Resource) == "" {
		oldBase, _, valid := discoveryCredentialInputParts(h.pending.Resource)
		if valid && oldBase == base {
			expected, err := bind(base)
			if err == nil && expected == h.pending.Resource {
				return expected, nil
			}
		}
	}
	return bind(prototype)
}

func (h *discoveryCredentialLineage) absentRetired(actual map[string]discoveryMetadata) error {
	for _, history := range h.archives {
		if err := history.absentRetired(actual); err != nil {
			return err
		}
	}
	for creatorID := range h.retired {
		for _, job := range actual {
			if job.ID == h.creators[creatorID].identity.id {
				return errConfigurationOwned
			}
		}
	}
	return nil
}
