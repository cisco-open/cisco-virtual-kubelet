// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type wiredBulkOwnedCredential struct {
	Resource, TargetID, OperationID string
	Versions                        []cvk.NetworkControllerConfigurationCredentialVersion
	Metadata                        *cvk.NetworkControllerConfigurationActionApplication
	Native                          globalCredentialMetadata
}

// A prerequisite receipt, not a credential value or a transfer of ownership.
// Observe the current section owner and exact current definition, then reuse
// its persisted task-backed UUID/version receipt. Do not read Secret objects or
// another worker's projected secret files.
func (a *adapter) observeWiredBulkOwnedCredentials(ctx context.Context, wants []globalCredentialIntent) (string, error) {
	nc, err := a.currentController(ctx)
	if err != nil {
		return "", err
	}
	if nc.Status.Configuration == nil || len(wants) == 0 {
		return "", errConfigurationOwned
	}
	state := nc.Status.Configuration
	ownerName, ownerUID := "", ""
	for _, owner := range state.Owners {
		if owner.Section == "network_settings" {
			if ownerName != "" {
				return "", errConfigurationOwned
			}
			ownerName, ownerUID = owner.ConfigName, owner.ConfigUID
		}
	}
	if ownerName == "" || ownerUID == "" {
		return "", errConfigurationOwned
	}
	var config configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: a.key.Namespace, Name: ownerName}, &config); err != nil {
		return "", err
	}
	if string(config.UID) != ownerUID || !config.DeletionTimestamp.IsZero() || config.Spec.ControllerRef.Name != a.key.Name || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "network_settings" || !supportedConfigurationApply(&config) || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || config.Status.ObservedGeneration != config.Generation {
		return "", errConfigurationOwned
	}
	source, err := loadControllerSource(ctx, a.statusReader, &config)
	if err != nil {
		return "", err
	}
	definitions, _, err := splitNetworkCredentialConfiguration(source.raw)
	if err != nil {
		return "", err
	}
	byResource := map[string]globalCredentialIntent{}
	for _, definition := range definitions {
		byResource[definition.resource()] = definition
	}
	evidence := []wiredBulkOwnedCredential{}
	for _, want := range wants {
		definition, exists := byResource[want.resource()]
		if !exists || !reflect.DeepEqual(definition.metadata, want.metadata) || len(definition.credentials) != len(want.credentials) {
			return "", errConfigurationStale
		}
		wantedVersions := map[string]int64{}
		for _, leaf := range want.credentials {
			key := globalCredentialVersionIdentity(leaf.path)
			if key == "" || wantedVersions[key] != 0 {
				return "", errConfigurationSecret
			}
			wantedVersions[key] = leaf.version
		}
		for _, leaf := range definition.credentials {
			if wantedVersions[globalCredentialVersionIdentity(leaf.path)] != leaf.version {
				return "", errConfigurationStale
			}
		}
		entry := wiredBulkOwnedCredential{Resource: want.resource()}
		if want.metadataOnly() {
			e := &globalCredentialExecutor{owner: ownerUID, configName: ownerName, metadataReceipts: state.ActionApplications}
			receipt, target, err := e.metadataCredentialReceipt(want)
			if err != nil || receipt == nil || receipt.Operation.Resource != want.metadataResource() || receipt.Operation.ConfigGeneration > config.Generation || receipt.Operation.ControllerGeneration > nc.Generation {
				return "", errConfigurationOwned
			}
			entry.TargetID, entry.OperationID, entry.Metadata = target, receipt.Operation.ID, receipt.DeepCopy()
		} else {
			if len(want.credentials) == 0 {
				return "", errConfigurationSecret
			}
			var receipt *cvk.NetworkControllerConfigurationCredentialApplication
			for i := range state.CredentialApplications {
				r := &state.CredentialApplications[i]
				if r.Section == "network_settings" && r.Resource == want.resource() {
					if receipt != nil {
						return "", errConfigurationOwned
					}
					receipt = r
				}
			}
			if receipt == nil || receipt.ConfigUID != ownerUID || !validAPIID(receipt.TargetID) || !configurationOperationID.MatchString(receipt.OperationID) || len(receipt.SSIDCredentialHistory) != 0 || len(receipt.Versions) != len(definition.credentials) {
				return "", errConfigurationOwned
			}
			seen := map[string]bool{}
			previousPath := ""
			for _, version := range receipt.Versions {
				if version.Key != "" || seen[version.Path] || version.Path <= previousPath {
					return "", errConfigurationSecret
				}
				previousPath = version.Path
				seen[version.Path] = true
				matched := false
				for _, leaf := range definition.credentials {
					if version.Path == leaf.path && version.Version == leaf.version {
						matched = true
					}
				}
				if !matched || version.SourceAlias == "" || version.SecretName == "" || version.SecretKey == "" {
					return "", errConfigurationSecret
				}
				refs := 0
				for _, ref := range config.Spec.SecretRefs {
					if ref.Section == "network_settings" && ref.Path == version.Path {
						if ref.Source != version.SourceAlias || ref.Use != "" && ref.Use != "Value" {
							return "", errConfigurationSecret
						}
						refs++
					}
				}
				if refs != 1 {
					return "", errConfigurationSecret
				}
				authorized := 0
				for _, ref := range nc.Spec.IntentSecretSources {
					if ref.Alias == version.SourceAlias {
						if ref.Name != version.SecretName || ref.Key != version.SecretKey {
							return "", errConfigurationSecret
						}
						authorized++
					}
				}
				if authorized != 1 {
					return "", errConfigurationSecret
				}
			}
			entry.TargetID, entry.OperationID, entry.Versions = receipt.TargetID, receipt.OperationID, append([]cvk.NetworkControllerConfigurationCredentialVersion(nil), receipt.Versions...)
		}
		evidence = append(evidence, entry)
	}
	// Native public metadata and UUID equality are necessary in addition to the
	// persisted write-only receipt; neither is sufficient by itself.
	native, err := a.client.globalCredentials(ctx)
	if err != nil {
		return "", err
	}
	for i, want := range wants {
		got, exists := native[want.kind.model][want.metadata.Name]
		if !exists || got.ID != evidence[i].TargetID || !globalCredentialMatches(want.metadata, got) {
			return "", errConfigurationStale
		}
		evidence[i].Native = got
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].Resource < evidence[j].Resource })
	raw, err := json.Marshal(struct {
		Version, OwnerUID, OwnerName, SourceUID, SourceVersion string
		Generation, ControllerGeneration                       int64
		Evidence                                               []wiredBulkOwnedCredential
	}{"owned-bulk-credentials-v1", ownerUID, ownerName, string(source.uid), source.version, config.Generation, nc.Generation, evidence})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func (a *adapter) wiredBulkDependencies(ctx context.Context, source *configSourceSnapshot) ([]wiredBulkDependencyObserver, error) {
	model, err := compileWiredBulkSource(source)
	if err != nil {
		return nil, err
	}
	if model.network == nil || len(model.network.credentials) == 0 {
		return a.client.wiredBulkDependencies(source)
	}
	if !wiredBulkCredentialDefinitionsSupported(model.network) {
		return nil, errWiredBulkNetworkContextUnavailable
	}
	// Refuse missing ownership before any native policy/planning calls.
	if _, err := a.observeWiredBulkOwnedCredentials(ctx, model.network.credentials); err != nil {
		return nil, err
	}
	node, err := nacmodel.ParseDocument(source.raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil, errConfigurationTransition
	}
	delete(root["network_settings"].(map[string]any), "device_credentials")
	projected := *source
	projected.raw, err = json.Marshal(map[string]any{"catalyst_center": root})
	if err != nil {
		return nil, err
	}
	base, err := a.client.wiredBulkDependencies(&projected)
	if err != nil {
		return nil, err
	}
	return []wiredBulkDependencyObserver{func(ctx context.Context) (string, error) {
		before, err := a.observeWiredBulkOwnedCredentials(ctx, model.network.credentials)
		if err != nil {
			return "", err
		}
		dependency := ""
		for _, observer := range base {
			dependency, err = observer(ctx)
			if err != nil {
				return "", err
			}
		}
		after, err := a.observeWiredBulkOwnedCredentials(ctx, model.network.credentials)
		if err != nil {
			return "", err
		}
		if before != after {
			return "", errConfigurationStale
		}
		return fmt.Sprintf("%x", sha256.Sum256([]byte("owned-bulk-dependency-v1:"+before+":"+dependency))), nil
	}}, nil
}

func wiredBulkCredentialDefinitionsSupported(intent *networkReportIntent) bool {
	for _, assignment := range intent.assignments {
		if assignment.name == "" {
			return false
		}
	}
	for _, definition := range intent.credentials {
		if len(definition.credentials) == 0 && !definition.metadataOnly() {
			return false
		}
	}
	return true
}
