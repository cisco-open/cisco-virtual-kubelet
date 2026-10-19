// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"sort"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// Membership removal updates a retained tag incarnation; it does not infer
// creator exclusivity or authorize deleting the tag. The existing private
// baseline, complete-source ownership and one-use task preflight remain required.
func (e *networkProfileConfiguration) bindWirelessTagMembership(config *configapi.NetworkControllerConfig, latest map[string]wirelessTagMemberProof, proof func(context.Context, string) error, predecessor string) {
	if config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete {
		return
	}
	for _, tags := range e.tagExecutors {
		ns := "wireless-site-tags/"
		if tags.policy {
			ns = "wireless-policy-tags/"
		}
		tags.removal = func(ctx context.Context, row wirelessProfileTagObservation) error {
			if err := proof(ctx, predecessor); err != nil {
				return err
			}
			key, valid := wirelessTagLogical(row.resource)
			retained, exists := latest[ns+key]
			if !valid || !exists || row.actual == nil || row.profileID != retained.row.Operation.ParentID || row.tagID != retained.id {
				return errConfigurationOwned
			}
			baseline, err := wirelessTagBaselineForRecord(ctx, tags.client, row, tags.policy, retained)
			if err != nil {
				return err
			}
			mac, err := wirelessTagMAC(e.comparison, ns, row.profileID, key, row.tagID, baseline)
			if err != nil || mac != retained.mac {
				return errConfigurationOwned
			}
			ids, err := wirelessTagRemovedScopes(row)
			if err != nil {
				return err
			}
			scope, err := encodeWirelessTagMembershipScope(ids)
			if err != nil {
				return err
			}
			resource := ns + wirelessTagBoundPrefix + "members:" + row.resource + ":" + scope + ":" + strings.Repeat("a", 64) + ":" + e.comparison.keyID + ":" + predecessor
			if digest, ok := wirelessTagLineageDigest(retained.row.Operation.Resource); ok {
				resource, err = wirelessTagLineageResource(resource, "members", digest)
			}
			if err != nil || len(resource) > 512 {
				return errConfigurationOwned
			}
			return tags.client.wirelessTagUnusedScopes(ctx, ids)
		}
	}
}

func wirelessTagRemovedScopes(row wirelessProfileTagObservation) ([]string, error) {
	if row.actual == nil {
		return nil, errConfigurationOwned
	}
	old, ok := row.actual.fields["siteIds"].([]string)
	if !ok || len(old) == 0 {
		return nil, errInvalidResponse
	}
	next, ok := row.payload["siteIds"].([]string)
	if !ok || len(next) == 0 {
		return nil, errConfigurationOwned
	}
	// Zone removal can affect every old member, even when site membership stays
	// unchanged. Site-only removal checks the removed site subtrees.
	zonesChanged := !reflect.DeepEqual(row.actual.fields["apZones"], row.payload["apZones"])
	selected := map[string]bool{}
	for _, id := range old {
		keep := false
		for _, want := range next {
			if id == want {
				keep = true
			}
		}
		if zonesChanged || !keep {
			selected[id] = true
		}
	}
	if len(selected) == 0 {
		return nil, errConfigurationOwned
	}
	ids := []string{}
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
func (c *client) wirelessTagUnusedScopes(ctx context.Context, ids []string) error {
	reader := c.profileSitePruneReader()
	if err := reader.loadSites(ctx); err != nil {
		return err
	}
	for _, id := range ids {
		path := reader.sites[id].NameHierarchy
		if path == "" || path == "Global" {
			return errProfileSitePruneReferenced
		}
		if err := reader.unusedScope(ctx, path); err != nil {
			return err
		}
	}
	return nil
}

const wirelessTagMembershipPrefix = "tag-proof:v2:"

func wirelessTagMembershipSelected(resource string) bool {
	for _, ns := range []string{"wireless-site-tags/", "wireless-policy-tags/"} {
		if strings.HasPrefix(resource, ns+wirelessTagMembershipPrefix) || wirelessTagBindingMode(resource) == "members" {
			return true
		}
	}
	return false
}
func encodeWirelessTagMembershipScope(ids []string) (string, error) {
	if len(ids) == 0 || len(ids) > 256 || !sort.StringsAreSorted(ids) {
		return "", errConfigurationOwned
	}
	for i, id := range ids {
		if !validAPIID(id) || i > 0 && ids[i-1] == id {
			return "", errConfigurationOwned
		}
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
func wirelessTagMembershipScopeValid(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return false
	}
	var ids []string
	if json.Unmarshal(raw, &ids) != nil {
		return false
	}
	encoded, err := encodeWirelessTagMembershipScope(ids)
	return err == nil && encoded == value
}
func decodeWirelessTagMembershipScope(resource string) ([]string, error) {
	if (!wirelessTagMembershipSelected(resource) && !wirelessTagPruneSelected(resource)) || len(resource) > 512 {
		return nil, errConfigurationOwned
	}
	parts := strings.Split(resource, ":")
	if len(parts) < 5 {
		return nil, errConfigurationOwned
	}
	at := len(parts) - 4
	if wirelessTagLineageSelected(resource) {
		if _, ok := wirelessTagLineageDigest(resource); !ok || at < 1 {
			return nil, errConfigurationOwned
		}
		at--
	}
	value := parts[at]
	if !wirelessTagMembershipScopeValid(value) {
		return nil, errConfigurationOwned
	}
	raw, _ := base64.RawURLEncoding.DecodeString(value)
	var ids []string
	json.Unmarshal(raw, &ids)
	return ids, nil
}
