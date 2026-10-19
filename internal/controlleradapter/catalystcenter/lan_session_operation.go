// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func validLANSessionStartResource(resource, action string) bool {
	suffix, ok := strings.CutPrefix(resource, lanSessionStartPrefix)
	if !ok || action != "Create" || len(suffix) != 64 || strings.ToLower(suffix) != suffix {
		return false
	}
	_, err := hex.DecodeString(suffix)
	return err == nil
}

func lanSessionStartResource(want lanSessionIntent, before *lanSessionPreflight) (string, error) {
	digest := before.observationDigest()
	if digest == "" {
		return "", errConfigurationTransition
	}
	// Retain version/source semantics while excluding ephemeral secret values.
	metadata := want
	metadata.Password = ""
	if err := validateLANSessionIntent(metadata, false); err != nil {
		return "", err
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%x", lanSessionStartPrefix, sha256.Sum256(append(raw, []byte(digest)...))), nil
}

// Select only an exact declared entry from the existing resolved source. Other
// session declarations remain part of the source's ownership/generation bracket.
func declaredLANSessionStart(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want lanSessionIntent) error {
	if !supportedLANMutationConfig(config) {
		return errConfigurationTransition
	}
	var raw []byte
	if source != nil {
		if len(source.templateFiles) != 0 || len(source.floorImages) != 0 || source.templateComparison != nil {
			return errConfigurationTransition
		}
		raw = source.raw
	} else if config.Spec.Source.Inline != nil {
		raw = config.Spec.Source.Inline.Raw
	}
	wants, _, err := parseLANSessionIntents(raw, false)
	if err != nil {
		return err
	}
	metadata := want
	metadata.Password = ""
	for _, declared := range wants {
		if reflect.DeepEqual(declared, metadata) {
			return nil
		}
	}
	return errConfigurationStale
}

// Native identity matching is not admission for these targets. In particular,
// PnP IDs are not network-inventory enrollment and the declared list does not
// establish the complete DHCP/reset footprint. The shared physical protocol
// must validate real device/Node identities before any claim or dispatch.
func lanSessionDeclaredGroup(before *lanSessionPreflight, targets []contract.GroupTargetIdentity) error {
	if before == nil || before.dependencies == nil {
		return errConfigurationTransition
	}
	canonical, err := contract.CanonicalGroupTargets(targets)
	if err != nil {
		return err
	}
	expected := map[string]string{}
	for _, seed := range before.dependencies.seeds {
		expected[seed.ID] = seed.Serial
	}
	for _, device := range before.discovery {
		if device.Info == nil || device.Info.Stack != nil && *device.Info.Stack || len(device.Info.StackInfo.Members) != 0 {
			return errConfigurationDeviceAdmission
		}
		if _, exists := expected[device.ID]; exists {
			return errConfigurationStale
		}
		expected[device.ID] = device.Info.Serial
	}
	if len(expected) != len(canonical) {
		return errConfigurationStale
	}
	for _, target := range canonical {
		serial, exists := expected[target.InventoryID]
		if !exists || !strings.EqualFold(serial, target.PhysicalIdentity) {
			return errConfigurationStale
		}
	}
	return nil
}

// Validate an already retained Prepared/Submitting operation, under the common
// source/section/controller owner bracket. No journal transition, claim, native
// write or release is performed here. This is a prerequisite for a later group
// bridge, not a substitute for managed target/discovery-domain admission.
func (a *adapter) revalidateLANSessionStartOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want lanSessionIntent, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if a == nil || a.client == nil || config == nil {
		return errConfigurationTransition
	}
	if op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Section != "lan_automation" || !validLANSessionStartResource(op.Resource, op.Action) || op.TaskID != "" || op.CancellationRequestedAt != nil || op.TargetSerial != "" || len(op.CredentialVersions) != 0 || op.DependencyVersions != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero())) {
		return errConfigurationTransition
	}
	if err := contract.ValidateGroupJournalIdentity(op); err != nil {
		return err
	}
	var current configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &current); err != nil {
		return err
	}
	if !reflect.DeepEqual(current.Spec, config.Spec) {
		return errConfigurationStale
	}
	loaded, err := loadControllerSource(ctx, a.statusReader, &current)
	if err != nil {
		return err
	}
	if source != nil && (!bytes.Equal(source.raw, loaded.raw) || source.key != loaded.key || source.uid != loaded.uid || source.version != loaded.version) {
		return errConfigurationStale
	}
	if err := declaredLANSessionStart(config, loaded, want); err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "lan_automation", func(ctx context.Context) (bool, error) {
		before, err := a.client.lanSessionPreflight(ctx, want)
		if err != nil {
			return false, err
		}
		resource, err := lanSessionStartResource(want, before)
		if err != nil {
			return false, err
		}
		primary := ""
		for _, seed := range before.dependencies.seeds {
			if sameManagementIP(seed.ManagementIP, want.PrimaryIP) {
				primary = seed.ID
			}
		}
		if resource != op.Resource || op.TargetID != before.dependencies.site.ID || primary == "" || op.ParentID != primary {
			return false, errConfigurationStale
		}
		if err := lanSessionDeclaredGroup(before, op.PhysicalGroup.Targets); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return errConfigurationBusy
	}
	return nil
}
