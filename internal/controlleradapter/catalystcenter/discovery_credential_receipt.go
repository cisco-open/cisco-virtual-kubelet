// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const discoveryCredentialReceiptPrefix = "discovery-credential-record:v1:"

func discoveryCredentialInputSelected(resource string) bool {
	// Reserve unknown versions too; they must not fall through to generic
	// completion if an older schema drops their credential bindings.
	return strings.HasPrefix(resource, "discovery-credential-input:")
}

// Input identity alone proves no native effect. A creator receipt additionally
// needs a correlated task, exact native UUID and the atomically applied versions.
func discoveryCredentialOperationInput(op *cvk.NetworkControllerConfigurationOperation) (string, bool) {
	if op == nil || !discoveryCredentialInputSelected(op.Resource) || op.Section != "inventory" || op.Action != "Create" ||
		op.TargetID != "" || op.ParentID != "" || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil ||
		op.DependencyVersions != nil || op.CancellationRequestedAt != nil || op.PreparedAt != nil || len(op.SSIDCredentialHistory) != 0 ||
		len(op.CredentialVersions) > len(discoveryCredentialFields) ||
		(len(op.CredentialVersions) == 0) != strings.HasPrefix(op.Resource, discoveryCredentialMetadataScopePrefix) {
		return "", false
	}
	base, _, valid := discoveryCredentialInputParts(op.Resource)
	if !valid {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(base, discoveryCredentialInputPrefix), ":")
	for _, part := range parts {
		if len(part) != 64 || strings.ToLower(part) != part {
			return "", false
		}
		if _, err := hex.DecodeString(part); err != nil {
			return "", false
		}
	}
	bindings := []discoveryCredentialVersionBinding{}
	previous, index := "", ""
	for _, version := range op.CredentialVersions {
		path := strings.Split(version.Path, "/")
		if len(path) != 4 || path[0] != "" || path[1] != "discovery" || version.Path <= previous || version.Key != "" ||
			version.Version < 1 || version.SourceAlias == "" || version.SecretName == "" || version.SecretKey == "" {
			return "", false
		}
		n, err := strconv.Atoi(path[2])
		if err != nil || n < 0 || n >= 100 || strconv.Itoa(n) != path[2] || index != "" && index != path[2] {
			return "", false
		}
		index, previous = path[2], version.Path
		field := ""
		for _, candidate := range discoveryCredentialFields {
			if candidate.model == path[3] {
				field = candidate.api
			}
		}
		if field == "" {
			return "", false
		}
		bindings = append(bindings, discoveryCredentialVersionBinding{Field: field, Version: version.Version, Alias: version.SourceAlias, SecretName: version.SecretName, SecretKey: version.SecretKey})
	}
	slices.SortFunc(bindings, func(a, b discoveryCredentialVersionBinding) int { return strings.Compare(a.Field, b.Field) })
	return parts[0], featureIdentityDigest(bindings) == parts[2]
}

func discoveryCredentialReceiptPairPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	name, valid := discoveryCredentialOperationInput(op)
	if !valid || state == nil || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || op.ConfigName == "" || op.ConfigUID == "" ||
		op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) {
		return false
	}
	owners := 0
	for _, owner := range state.Owners {
		if !configurationSectionsOverlap(owner.Section, op.Section) {
			continue
		}
		if owner.ConfigUID != op.ConfigUID || owner.ConfigName != op.ConfigName {
			return false
		}
		if owner.Section == op.Section {
			owners++
		}
	}
	if owners != 1 {
		return false
	}
	id, actions, credentials := "", 0, 0
	for _, receipt := range state.ActionApplications {
		if receipt.Operation.ID != op.ID {
			continue
		}
		identity, err := parseDiscoveryCredentialReceipt(receipt.Resource)
		if err != nil || identity.name != name || receipt.Section != "inventory" || !reflect.DeepEqual(receipt.Operation, *op) ||
			receipt.VerifiedAt == nil || receipt.VerifiedAt.Before(op.SubmittedAt) || receipt.VerifiedAt.After(time.Now()) {
			return false
		}
		if effect, scoped := discoveryCredentialRetainedEffect(op.Resource); scoped && (identity.legacy || identity.effect != effect) {
			return false
		}
		id = identity.id
		if !validAPIID(id) {
			return false
		}
		actions++
	}
	for _, receipt := range state.CredentialApplications {
		if receipt.OperationID != op.ID && !(receipt.Section == op.Section && receipt.Resource == op.Resource) {
			continue
		}
		if receipt.Section != op.Section || receipt.Resource != op.Resource || receipt.ConfigUID != op.ConfigUID || receipt.OperationID != op.ID ||
			receipt.TargetID != id || !slices.Equal(receipt.Versions, op.CredentialVersions) || len(receipt.SSIDCredentialHistory) != 0 {
			return false
		}
		credentials++
	}
	if strings.HasPrefix(op.Resource, discoveryCredentialMetadataScopePrefix) {
		return actions == 1 && credentials == 0
	}
	return actions == 1 && credentials == 1
}

// Used before the shared permit as well as immediately before receipt publication.
// An existing same-name creator is not adoptable or replaceable by this Create.
func discoveryCredentialCreateCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	name, valid := discoveryCredentialOperationInput(op)
	if !valid || state == nil || len(state.ActionApplications) >= 256 {
		return errConfigurationTransition
	}
	history, err := discoveryCredentialStructuralLineage(state, op.ConfigUID)
	if err != nil {
		return err
	}
	prototype := discoveryCredentialInputPrototype(op.Resource)
	expected, err := history.input(name, prototype)
	if err != nil || expected != op.Resource {
		return errConfigurationOwned
	}
	if latest := history.latest[name]; latest != nil {
		if retirement := history.retired[latest.receipt.Operation.ID]; retirement != nil && retirement.data.prune && !configurationInputAfter(op, &retirement.receipt.Operation) {
			return errConfigurationStale
		}
	}
	if latest := history.latest[name]; latest != nil && history.retired[latest.receipt.Operation.ID] == nil {
		return errConfigurationOwned
	}
	if err := history.transition(name, op.CredentialVersions); err != nil {
		return err
	}
	for _, receipt := range state.ActionApplications {
		if receipt.Operation.ID == op.ID || strings.HasPrefix(receipt.Resource, ownedDiscoveryReceiptPrefix+name+":") {
			return errConfigurationOwned
		}
	}
	for _, receipt := range state.CredentialApplications {
		if receipt.OperationID == op.ID || receipt.Section == op.Section && receipt.Resource == op.Resource {
			return errConfigurationOwned
		}
	}
	if err := configurationCredentialCapacity(state, op.ConfigUID, op.Section, op.Resource, op.CredentialVersions); err != nil {
		return err
	}
	reserved := op.DeepCopy()
	stamp := metav1.Now()
	reserved.Phase, reserved.TaskID, reserved.SubmittedAt = "Verified", strings.Repeat("x", 128), &stamp
	maxEffect := discoveryCredentialPublicEffect{mask: (1 << len(discoveryCredentialPublicFields)) - 1, digest: strings.Repeat("a", 64)}
	prospective := append(slices.Clone(state.ActionApplications), cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: discoveryCredentialEffectReceiptKey(name, strings.Repeat("x", 128), maxEffect), Operation: *reserved, VerifiedAt: &stamp})
	raw, err := json.Marshal(prospective)
	if err != nil || len(raw) > 256<<10 {
		return errConfigurationTransition
	}
	return nil
}

// Resource verification, both receipts and Verified form one status CAS on the
// existing controller journal. No network mutation or status-update retry occurs.
func (a *adapter) recordDiscoveryCredentialCreate(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, projection *discoveryCredentialProjection, index int, expected *cvk.NetworkControllerConfigurationOperation) error {
	if a == nil || config == nil || source == nil || projection == nil {
		return errConfigurationTransition
	}
	if _, valid := discoveryCredentialOperationInput(expected); !valid || expected.Phase != "Verifying" {
		return errConfigurationTransition
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if _, err := discoveryCredentialExecutionHistory(current, config, source); err != nil {
		return err
	}
	verified := expected.DeepCopy()
	verified.Phase = "Verified"
	if current.Status.Configuration != nil && reflect.DeepEqual(current.Status.Configuration.Operation, verified) && discoveryCredentialReceiptPairPresent(current.Status.Configuration, verified) && discoveryCredentialHistoryValid(current.Status.Configuration, expected.ConfigUID) {
		// A lost Kubernetes acknowledgement may already have persisted the pair.
		// Return its retained result without another native read or submission.
		if expected.ConfigUID != string(config.UID) || expected.ConfigName != config.Name || expected.ConfigGeneration != config.Generation || expected.ControllerGeneration != current.Generation ||
			expected.SourceUID != string(source.uid) || expected.SourceVersion != source.version || configurationOwnersMatch(current.Status.Configuration, config) != nil {
			return errConfigurationStale
		}
		request, err := projection.requestFields(current, config, source, index)
		if err != nil {
			return err
		}
		name, _ := discoveryCredentialOperationInput(expected)
		if !slices.Equal(request.versions, expected.CredentialVersions) || discoveryPublicDigest(projection.intents[index].base.name) != name {
			return errConfigurationStale
		}
		return a.checkConfigurationSnapshot(ctx, config, source)
	}
	observation, err := a.observeDiscoveryCredentialCreate(ctx, config, source, projection, index, expected)
	if err != nil {
		return err
	}
	current, err = a.currentController(ctx)
	if err != nil {
		return err
	}
	if current.ResourceVersion != observation.controllerVersion || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, expected) || observation.operationID != expected.ID || observation.input != expected.Resource {
		return errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if _, err := projection.requestFields(current, config, source, index); err != nil {
		return err
	}
	return a.publishObservedDiscoveryCredentialCreate(ctx, current, expected, observation)
}

// Shared atomic writer for a native observation. Callers must validate their
// current-source or retained-source authority before entering this CAS.
func (a *adapter) publishObservedDiscoveryCredentialCreate(ctx context.Context, current *cvk.NetworkController, expected *cvk.NetworkControllerConfigurationOperation, observation *discoveryCredentialCreateObservation) error {
	if a == nil || current == nil || expected == nil || observation == nil || expected.SubmittedAt == nil || expected.SubmittedAt.IsZero() || expected.SubmittedAt.After(time.Now()) || !validAPIID(expected.TaskID) || current.Generation != expected.ControllerGeneration || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, expected) || expected.Phase != "Verifying" || current.ResourceVersion != observation.controllerVersion || observation.operationID != expected.ID || observation.input != expected.Resource || !validAPIID(observation.id) {
		return errConfigurationStale
	}
	if _, err := a.discoveryCredentialOperationHistory(ctx, current, expected); err != nil {
		return err
	}
	state := current.Status.Configuration
	verified := expected.DeepCopy()
	verified.Phase = "Verified"
	if _, valid := discoveryCredentialOperationInput(expected); !valid {
		return errConfigurationTransition
	}
	if err := discoveryCredentialCreateCapacity(state, expected); err != nil {
		return err
	}
	name, _ := discoveryCredentialOperationInput(expected)
	stamp := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if stamp.Before(expected.SubmittedAt) {
		stamp = *expected.SubmittedAt.DeepCopy()
	}
	state.Operation = verified
	// Metadata-only incarnations have no write-only delivery to attest. Keep
	// their task/native proof in the existing action journal, not an empty
	// CredentialApplication (the shared schema requires at least one version).
	if len(expected.CredentialVersions) > 0 {
		state.CredentialApplications = append(state.CredentialApplications, cvk.NetworkControllerConfigurationCredentialApplication{Section: expected.Section, Resource: expected.Resource, ConfigUID: expected.ConfigUID, TargetID: observation.id, OperationID: expected.ID, Versions: slices.Clone(expected.CredentialVersions)})
	}
	state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: expected.Section, Resource: discoveryCredentialEffectReceiptKey(name, observation.id, observation.effect), Operation: *verified.DeepCopy(), VerifiedAt: &stamp})
	if !discoveryCredentialReceiptPairPresent(state, verified) {
		return errConfigurationTransition
	}
	if !discoveryCredentialHistoryValid(state, expected.ConfigUID) {
		return errConfigurationOwned
	}
	if err := a.persistConfigurationActionReceipt(ctx, current); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if _, err := a.discoveryCredentialOperationHistory(ctx, stored, verified); err != nil {
		return err
	}
	if stored.Status.Configuration == nil || !reflect.DeepEqual(stored.Status.Configuration.Operation, verified) || !discoveryCredentialReceiptPairPresent(stored.Status.Configuration, verified) || !discoveryCredentialHistoryValid(stored.Status.Configuration, expected.ConfigUID) {
		return errConfigurationStale
	}
	return nil
}
