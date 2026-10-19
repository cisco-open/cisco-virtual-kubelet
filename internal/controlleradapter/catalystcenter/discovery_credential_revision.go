// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Current revision authority does not rewrite the original custody link. At its
// original generation the spec remains exact; a ConfigMap revision must change
// UID/RV before its body can change. Later intent still requires a fresh scope.
func discoveryCredentialRevisionContext(record *discoveryOwnershipRetirement, c *configapi.NetworkControllerConfig, uid, version, model string) (bool, error) {
	if record == nil || record.CredentialCustody == nil || c == nil || !lowerDiscoveryDigest(model) || (uid == "") != (version == "") {
		return false, errConfigurationOwned
	}
	link := record.CredentialCustody
	if c.Generation < link.ConfigGeneration || c.Generation == link.ConfigGeneration && featureIdentityDigest(c.Spec) != link.SpecDigest {
		return false, errConfigurationStale
	}
	if c.Spec.Source.Inline != nil {
		if uid != "" || version != "" || model != featureIdentityDigest(c.Spec.Source.Inline.Raw) {
			return false, errConfigurationStale
		}
	} else if c.Spec.Source.ConfigMapRef == nil || uid == "" || version == "" {
		return false, errConfigurationOwned
	} else if uid == link.SourceUID && version == link.SourceVersion && model != link.ModelDigest {
		return false, errConfigurationStale
	}
	return c.Generation == link.ConfigGeneration && featureIdentityDigest(c.Spec) == link.SpecDigest && uid == link.SourceUID && version == link.SourceVersion && model == link.ModelDigest, nil
}

func discoveryCredentialCurrentCustody(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (*discoveryOwnershipRetirement, *discoveryCredentialCohort, error) {
	if c == nil || source == nil || !c.DeletionTimestamp.IsZero() {
		return nil, nil, errConfigurationOwned
	}
	record, cohort, err := discoveryCredentialCustodyRoot(nc, c)
	if err != nil {
		return nil, nil, err
	}
	if _, err := discoveryCredentialRevisionContext(record, c, string(source.uid), source.version, featureIdentityDigest(source.raw)); err != nil {
		return nil, nil, err
	}
	if !discoveryCredentialAttestationMatchesSource(nc, c, source) {
		return nil, nil, errConfigurationOwned
	}
	return record, cohort, nil
}

// A retained operation uses its canonical current scope, not an invented old
// source. Deletion may advance generation without changing the attested spec.
func discoveryCredentialScopedCustody(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, scope *discoverySourceScope) (*discoveryOwnershipRetirement, *discoveryCredentialCohort, error) {
	if nc == nil || c == nil || scope == nil || scope.ControllerUID != string(nc.UID) || scope.ControllerGeneration != nc.Generation || scope.ConfigUID != string(c.UID) || scope.SpecDigest != featureIdentityDigest(c.Spec) || scope.ConfigGeneration != c.Generation && (c.DeletionTimestamp.IsZero() || scope.ConfigGeneration+1 != c.Generation) {
		return nil, nil, errConfigurationOwned
	}
	record, cohort, err := discoveryCredentialCustodyRoot(nc, c)
	if err != nil {
		return nil, nil, err
	}
	current := c.DeepCopy()
	current.Generation = scope.ConfigGeneration
	if _, err := discoveryCredentialRevisionContext(record, current, scope.SourceUID, scope.SourceVersion, scope.ModelDigest); err != nil {
		return nil, nil, err
	}
	return record, cohort, nil
}
