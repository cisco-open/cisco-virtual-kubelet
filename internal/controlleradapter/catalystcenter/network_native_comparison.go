// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const networkComparisonScope = "/network_settings"
const networkPrivateUpdatePrefix = networkSettingUpdateFamily + "v2:"
const networkPrivateAppliedPrefix = "network-setting-applied:v2:"

var errNetworkNativeBaseline = errors.New("private network native baseline unavailable or changed")

func networkComparisonConfig(config *configapi.NetworkControllerConfig) bool {
	return config != nil && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "network_settings" && len(config.Spec.SecretRefs) == 1 && config.Spec.SecretRefs[0].Use == "PrivateComparisonKey" && config.Spec.SecretRefs[0].Section == "network_settings" && config.Spec.SecretRefs[0].Path == networkComparisonScope
}

func (a *adapter) compileNetworkComparisonApply(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	if !networkComparisonConfig(config) || source == nil || config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete || config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return configurationApplyPlan{}, errConfigurationSecret
	}
	// Validate the complete product model before resolving operational key bytes.
	if _, err := parseNetworkSettings(source.raw); err != nil {
		return configurationApplyPlan{}, err
	}
	plan, err := a.client.compileConfigurationApply(source.raw, config.Spec.ManagedSections, source.templateFiles)
	if err != nil {
		return configurationApplyPlan{}, err
	}
	settings := networkSettingsExecutor(plan.executor)
	if settings == nil {
		return configurationApplyPlan{}, errConfigurationTransition
	}
	comparison, err := a.resolveTemplateComparison(nc, config, source, "network_settings", networkComparisonScope, "cvk-network-settings-key-v1")
	if err != nil {
		return configurationApplyPlan{}, err
	}
	settings.comparison = comparison
	return plan, nil
}

func networkPrivateParts(resource string) ([]string, bool) {
	if !strings.HasPrefix(resource, networkPrivateUpdatePrefix) {
		return nil, false
	}
	parts := strings.Split(strings.TrimPrefix(resource, networkPrivateUpdatePrefix), ":")
	if len(parts) != 4 {
		return nil, false
	}
	for _, part := range parts {
		if !networkSettingDigestValid(part) {
			return nil, false
		}
	}
	return parts, true
}
func networkAppliedBase(resource, key string) string {
	if _, ok := networkPrivateParts(resource); ok {
		return networkPrivateAppliedPrefix + key
	}
	return networkSettingAppliedPrefix + key
}

// Canonicalize only known telemetry fields plus exact site ancestry identities.
// Unknown native fields have already failed the strict reader. No native values
// or unkeyed native fingerprints enter the journal; only this keyed proof does.
func networkNativeMAC(ctx context.Context, comparison *templateInputComparison, base, path string, sites map[string]networkSubmissionSite, values map[string]map[string]any) (string, error) {
	parts := strings.Split(strings.TrimPrefix(base, networkPrivateUpdatePrefix), ":")
	if !strings.HasPrefix(base, networkPrivateUpdatePrefix) || len(parts) != 3 || !validSitePath(path) || !networkSettingDigestValid(parts[2]) || parts[0] != networkSettingLogicalKey(networkSettingIdentity(path, "telemetry")) || !networkSettingDigestValid(parts[1]) || comparison == nil || len(comparison.key) != 32 || comparison.keyID != parts[2] || comparison.check == nil {
		return "", errNetworkNativeBaseline
	}
	if err := comparison.check(ctx); err != nil {
		return "", err
	}
	type identity struct{ ID, Parent, Kind, Path string }
	chain := []identity{}
	for at, depth := path, 0; ; at, depth = parentSitePath(at), depth+1 {
		site, ok := sites[at]
		if !ok || depth >= len(sites) || site.path != at || !validAPIID(site.id) {
			return "", errConfigurationStale
		}
		chain = append(chain, identity{site.id, site.parent, site.kind, site.path})
		if at == "Global" {
			break
		}
		parent, ok := sites[parentSitePath(at)]
		if !ok || parent.id != site.parent {
			return "", errConfigurationStale
		}
	}
	for _, group := range telemetryGroups {
		if len(values[group]) == 0 {
			return "", errNetworkNativeBaseline
		}
	}
	payload, err := json.Marshal(struct {
		Sites     []identity
		Telemetry map[string]map[string]any
	}{chain, values})
	if err != nil {
		return "", errConfigurationTransition
	}
	mac := hmac.New(sha256.New, comparison.key)
	mac.Write([]byte("cvk-network-expected-state-v1\x00" + base + "\x00"))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil)), nil
}
func networkPrivateResource(ctx context.Context, comparison *templateInputComparison, path, predecessor string, observed networkSubmissionObservation, submission networkSetting) (string, error) {
	if submission.kind != "telemetry" || !networkSettingDigestValid(predecessor) || comparison == nil {
		return "", errNetworkNativeBaseline
	}
	base := networkPrivateUpdatePrefix + networkSettingLogicalKey(networkSettingIdentity(path, "telemetry")) + ":" + predecessor + ":" + comparison.keyID
	proof, err := networkNativeMAC(ctx, comparison, base, path, observed.sites, submission.telemetry)
	if err != nil {
		return "", err
	}
	return base + ":" + proof, nil
}
func networkMatchPrivate(ctx context.Context, comparison *templateInputComparison, resource, path string, observed networkSubmissionObservation) (bool, error) {
	parts, valid := networkPrivateParts(resource)
	if !valid {
		return false, errNetworkNativeBaseline
	}
	actual, err := networkNativeMAC(ctx, comparison, networkPrivateUpdatePrefix+strings.Join(parts[:3], ":"), path, observed.sites, observed.effective[networkSettingIdentity(path, "telemetry")].telemetry)
	if err != nil {
		return false, err
	}
	return hmac.Equal([]byte(actual), []byte(parts[3])), nil
}
func checkNetworkPrivateKey(comparison *templateInputComparison, records map[string]cvk.NetworkControllerConfigurationActionApplication) error {
	for _, record := range records {
		if parts, private := networkPrivateParts(record.Operation.Resource); private {
			if comparison == nil || len(comparison.key) != 32 || comparison.keyID != parts[2] || comparison.check == nil {
				return errNetworkNativeBaseline
			}
		}
	}
	return nil
}
