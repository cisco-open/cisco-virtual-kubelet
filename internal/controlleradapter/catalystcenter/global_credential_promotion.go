// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Preserve the NOAUTH receipt as immutable origin evidence. The ordinary
// versioned receipt records successful promotion and subsequent rotations;
// neither receipt grants adoption, recreation or a return to NOAUTH. Keeping
// both in the existing journal avoids a second ownership or secret mechanism.
func (e *globalCredentialExecutor) credentialPromotionRoot(want globalCredentialIntent, prior *cvk.NetworkControllerConfigurationCredentialApplication) (*cvk.NetworkControllerConfigurationActionApplication, string, error) {
	root, id, err := e.metadataCredentialReceipt(want)
	if err != nil || root == nil {
		return root, id, err
	}
	if want.metadataOnly() || want.kind.model != "snmpv3_credentials" || (want.metadata.Mode != "AUTHNOPRIV" && want.metadata.Mode != "AUTHPRIV") ||
		prior != nil && (prior.TargetID != id || prior.OperationID == root.Operation.ID) {
		return nil, "", errConfigurationOwned
	}
	return root, id, nil
}

// Recheck the original metadata lineage and native target against the fresh
// controller read used by the shared receipt CAS. Losing or replacing the
// origin while a promotion is in flight must not mint versioned ownership.
func (a *adapter) checkGlobalCredentialPromotionCommit(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, nc *cvk.NetworkController, observedID string) error {
	op := nc.Status.Configuration.Operation
	if op.Section != "network_settings" || !strings.HasPrefix(op.Resource, "device_credentials/snmpv3_credentials/") {
		return nil
	}
	wants, _, err := splitNetworkCredentialConfiguration(source.raw)
	if err != nil {
		return err
	}
	e := &globalCredentialExecutor{client: a.client, desired: wants, owner: string(config.UID), configName: config.Name, secrets: source.secrets,
		receipts: nc.Status.Configuration.CredentialApplications, metadataReceipts: nc.Status.Configuration.ActionApplications}
	for _, want := range wants {
		if want.resource() != op.Resource {
			continue
		}
		root, _, err := e.metadataCredentialReceipt(want)
		if err != nil {
			return err
		}
		if !e.knownCredentialTask(op) {
			return errConfigurationOwned
		}
		if root == nil {
			return nil
		}
		id, done, err := e.verifyCredential(ctx, op)
		if err != nil {
			return err
		}
		if !done || id != observedID {
			return errConfigurationStale
		}
		return nil
	}
	return errConfigurationStale
}
