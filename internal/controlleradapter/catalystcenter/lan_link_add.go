// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
)

const lanLinkAddFamily = "lan-link-add:"
const lanLinkAddPrefix = lanLinkAddFamily + "v1:"
const lanLinkAddRecoveryPrefix = lanLinkAddFamily + "v2:"

type lanLinkAddBinding struct {
	version                     int
	logical, recovery, prepared string
}

func parseLANLinkAddResource(resource, action string) (lanLinkAddBinding, error) {
	var result lanLinkAddBinding
	if action != "Update" || len(resource) > 512 {
		return result, errConfigurationTransition
	}
	prefix, count := lanLinkAddPrefix, 2
	result.version = 1
	if strings.HasPrefix(resource, lanLinkAddRecoveryPrefix) {
		prefix, count = lanLinkAddRecoveryPrefix, 3
		result.version = 2
	}
	if !strings.HasPrefix(resource, prefix) {
		return result, errConfigurationTransition
	}
	parts := strings.Split(strings.TrimPrefix(resource, prefix), ":")
	if len(parts) != count {
		return result, errConfigurationTransition
	}
	for _, part := range parts {
		if len(part) != 64 || strings.ToLower(part) != part {
			return result, errConfigurationTransition
		}
		if _, err := hex.DecodeString(part); err != nil {
			return result, errConfigurationTransition
		}
	}
	result.logical = parts[0]
	result.prepared = parts[len(parts)-1]
	if result.version == 2 {
		result.recovery = parts[1]
	}
	return result, nil
}

func validLANLinkAddResource(resource, action string) bool {
	_, err := parseLANLinkAddResource(resource, action)
	return err == nil
}

func supportedLANLinkAddConfig(config *configapi.NetworkControllerConfig) bool {
	return supportedLANMutationConfig(config)
}

// Shared metadata policy; resource-specific source/admission gates stay with
// the link/session caller. This does not enable session Apply.
func supportedLANMutationConfig(config *configapi.NetworkControllerConfig) bool {
	if config == nil {
		return false
	}
	s := config.Spec
	return s.Mode == configapi.NetworkControllerApplyModeApply && len(s.ManagedSections) == 1 && s.ManagedSections[0] == "lan_automation" && (s.Scope == "" || s.Scope == "global") && len(s.SecretRefs) == 0 && s.ModelSource.Resolved && s.ModelSource.Format == nacmodel.Format && s.ModelSource.ModelVersion == nacmodel.Version && s.ModelSource.SchemaDigest == "" && (s.PrunePolicy == "" || s.PrunePolicy == configapi.NetworkControllerRetentionPolicyRetain) && (s.DeletionPolicy == "" || s.DeletionPolicy == configapi.NetworkControllerRetentionPolicyRetain) && len(config.Status.Tasks) == 0
}

func declaredLANLinks(source *configSourceSnapshot) ([]lanLinkIntent, error) {
	if source == nil || source.secrets != nil || len(source.templateFiles) != 0 || len(source.floorImages) != 0 {
		return nil, errConfigurationTransition
	}
	wants, err := parseLANLinks(source.raw)
	if err != nil {
		return nil, err
	}
	return wants, nil
}

func declaredLANLinkAdds(source *configSourceSnapshot) ([]lanLinkIntent, error) {
	wants, err := declaredLANLinks(source)
	if err != nil {
		return nil, err
	}
	// Typed ADD entry points retain their strict contract. Public composition
	// validates both actions through the shared whole-model observer.
	for _, want := range wants {
		if want.action != "ADD_LINK" {
			return nil, errConfigurationTransition
		}
	}
	return wants, nil
}

func lanLinkAddLogicalDigest(want lanLinkIntent, endpoints *lanLinkEndpointSnapshot) (string, error) {
	if endpoints == nil {
		return "", errConfigurationTransition
	}
	raw, err := json.Marshal(struct{ Name, Primary, PrimaryPort, Secondary, SecondaryPort, Pool string }{want.name, endpoints.primary.device.ID, endpoints.primary.port.ID, endpoints.secondary.device.ID, endpoints.secondary.port.ID, want.pool})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func lanLinkAddResource(want lanLinkIntent, snapshot *lanLinkPreflight) (string, error) {
	if snapshot == nil || snapshot.dependencies == nil || snapshot.dependencies.adjacency == nil || snapshot.dependencies.adjacency.link == nil {
		return "", errConfigurationBusy
	}
	endpoints := snapshot.dependencies.adjacency.endpoints
	link := snapshot.dependencies.adjacency.link
	if link.StartIP != "" || link.EndIP != "" || link.StartMask != "" || link.EndMask != "" {
		return "", errConfigurationOwned
	}
	// v2 records the observed empty IPv4 baseline. Addressed ports require a separate
	// owned idempotency/adoption lifecycle, not a fresh ADD ownership claim.
	for _, endpoint := range []lanLinkEndpoint{endpoints.primary, endpoints.secondary} {
		if endpoint.port.IPv4 != "" || endpoint.port.Mask != "" {
			return "", errConfigurationOwned
		}
	}
	logical, err := lanLinkAddLogicalDigest(want, endpoints)
	if err != nil {
		return "", err
	}
	recovery, err := lanLinkAddRecoveryDigest(snapshot.dependencies)
	if err != nil {
		return "", err
	}
	return lanLinkAddRecoveryPrefix + logical + ":" + recovery + ":" + snapshot.observationDigest(), nil
}

// Native execution is private and one-use; the supplied guard must be backed by
// the shared all-member dispatch permit. Planning makes no native mutation.
func (c *client) planManagedLANLinkAdd(ctx context.Context, reader ctrlclient.Reader, namespace string, want lanLinkIntent, guard func(context.Context) error) (*configurationWrite, error) {
	if want.action != "ADD_LINK" || guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	before, targets, err := c.lanLinkPreflightTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, err
	}
	resource, err := lanLinkAddResource(want, before)
	if err != nil {
		return nil, err
	}
	check := func(ctx context.Context) error {
		fresh, current, err := c.lanLinkPreflightTargets(ctx, reader, namespace, want)
		if err != nil {
			return err
		}
		if before.observationDigest() != fresh.observationDigest() || !reflect.DeepEqual(targets, current) {
			return errConfigurationStale
		}
		return nil
	}
	endpoints := before.dependencies.adjacency.endpoints
	return c.planLANLinkMutation(want, resource, configurationResourceIdentity{target: endpoints.primary.device.ID, parent: endpoints.secondary.device.ID, groupTargets: targets}, check, guard)
}

type lanLinkAddObservation struct {
	owned    bool
	resource string
	targets  []contract.GroupTargetIdentity
}

func (a *adapter) observeLANLinkAddModel(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (map[string]lanLinkAddObservation, map[string]lanLinkIntent, error) {
	wants, err := declaredLANLinkAdds(source)
	if err != nil {
		return nil, nil, err
	}
	return a.observeLANLinkModel(ctx, config, source, wants)
}
