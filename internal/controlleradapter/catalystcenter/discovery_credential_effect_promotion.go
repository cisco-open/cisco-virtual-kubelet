// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

var errDiscoveryCredentialEffectCapacity = errors.New("discovery effect receipt exceeds status capacity")

// Enrich a complete original v1 creator/version pair using fresh public intent
// and native metadata. Preserve the original delivery operation and its time.
// This is an idle status-only CAS, never a native replacement/dispatch permit.
func (a *adapter) promoteDiscoveryCredentialEffect(ctx context.Context, c *configapi.NetworkControllerConfig, source *configSourceSnapshot, observed *cvk.NetworkController, receipt cvk.NetworkControllerConfigurationActionApplication, request *discoveryCredentialRequest, input string, got discoveryMetadata) error {
	if a == nil || c == nil || source == nil || observed == nil || request == nil || !discoveryCredentialApplyShape(c) {
		return errConfigurationTransition
	}
	identity, err := parseDiscoveryCredentialReceipt(receipt.Resource)
	if err != nil || !identity.legacy || identity.id != got.ID || receipt.Operation.Resource != input || receipt.Operation.ConfigUID != string(c.UID) || receipt.Operation.ConfigName != c.Name || !discoveryCredentialReceiptPairPresent(observed.Status.Configuration, &receipt.Operation) {
		return errConfigurationOwned
	}
	// Validate the closed public vocabulary and authorized version labels before
	// computing input hashes; the full credential-bearing body is never hashed.
	effect, err := checkedDiscoveryCredentialEffect(request.public, got)
	if err != nil {
		return err
	}
	if err := validateConfigurationCredentialVersions(source, "inventory", request.versions); err != nil {
		return err
	}
	base, _, validInput := discoveryCredentialInputParts(input)
	parts := strings.Split(strings.TrimPrefix(base, discoveryCredentialInputPrefix), ":")
	bound := receipt.Operation.DeepCopy()
	bound.CredentialVersions = request.versions
	_, valid := discoveryCredentialOperationInput(bound)
	if !validInput || !valid || len(parts) != 3 || parts[0] != identity.name || parts[1] != featureIdentityDigest(request.public) || discoveryPublicDigest(request.public["name"]) != identity.name {
		return errConfigurationOwned
	}
	if got.Condition != "Complete" || got.Status != "Inactive" {
		return errConfigurationBusy
	}
	if err := a.checkConfigurationSnapshot(ctx, c, source); err != nil {
		return err
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if current.UID != observed.UID || current.Generation != observed.Generation || current.ResourceVersion != observed.ResourceVersion || current.Status.Configuration == nil || current.Status.Configuration.Operation != nil || !reflect.DeepEqual(current.Status.Configuration, observed.Status.Configuration) {
		return errConfigurationStale
	}
	if err := configurationOwnersMatch(current.Status.Configuration, c); err != nil {
		return err
	}
	index := -1
	for i, r := range current.Status.Configuration.ActionApplications {
		if r.Operation.ID == receipt.Operation.ID {
			if index >= 0 || !reflect.DeepEqual(r, receipt) {
				return errConfigurationOwned
			}
			index = i
		}
	}
	if index < 0 {
		return errConfigurationOwned
	}
	key := discoveryCredentialEffectReceiptKey(identity.name, identity.id, effect)
	if key == "" {
		return errConfigurationTransition
	}
	current.Status.Configuration.ActionApplications[index].Resource = key
	raw, err := json.Marshal(current.Status.Configuration.ActionApplications)
	if err != nil || len(current.Status.Configuration.ActionApplications) > 256 || len(raw) > 256<<10 {
		return errDiscoveryCredentialEffectCapacity
	}
	if !discoveryCredentialReceiptPairPresent(current.Status.Configuration, &receipt.Operation) {
		return errConfigurationTransition
	}
	if err := a.statusWriter.Status().Update(ctx, current); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(stored.Status.Configuration, current.Status.Configuration) {
		return errConfigurationStale
	}
	return nil
}

func (e *discoveryCredentialExecutor) promoteLegacyEffect(ctx context.Context, nc *cvk.NetworkController, actual map[string]discoveryMetadata, requests []*discoveryCredentialRequest, inputs []string) (bool, error) {
	if nc.Status.Configuration == nil {
		return false, nil
	}
	for i, want := range e.projection.intents {
		if len(want.credentials) == 0 {
			continue
		}
		for _, r := range nc.Status.Configuration.ActionApplications {
			if !discoveryCredentialReceiptForName(r.Resource, discoveryPublicDigest(want.base.name)) {
				continue
			}
			identity, err := parseDiscoveryCredentialReceipt(r.Resource)
			if err != nil {
				return true, err
			}
			if !identity.legacy {
				continue
			}
			got, present := actual[want.base.name]
			if !present {
				return true, errConfigurationOwned
			}
			err = e.a.promoteDiscoveryCredentialEffect(ctx, e.config, e.source, nc, r, requests[i], inputs[i], got)
			if errors.Is(err, errDiscoveryCredentialEffectCapacity) {
				continue
			} // Preserve compatible read-only v1 no-op; no lifecycle authority is invented.
			return true, err
		}
	}
	return false, nil
}
