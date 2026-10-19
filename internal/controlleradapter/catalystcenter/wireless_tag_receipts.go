// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const wirelessTagPrivatePrefix = "tag-proof:v1:"
const wirelessTagCreatedPrefix = "wireless-tag-created:v1:"
const wirelessTagAppliedPrefix = "wireless-tag-applied:v1:"
const wirelessTagComparisonScope = "/network_profiles"

func wirelessTagComparisonConfig(c *configapi.NetworkControllerConfig) bool {
	return c != nil && len(c.Spec.ManagedSections) == 1 && c.Spec.ManagedSections[0] == "network_profiles" && len(c.Spec.SecretRefs) == 1 && c.Spec.SecretRefs[0].Section == "network_profiles" && c.Spec.SecretRefs[0].Path == wirelessTagComparisonScope && c.Spec.SecretRefs[0].Use == "PrivateComparisonKey"
}
func wirelessTagPrivateSelected(resource string) bool {
	for _, ns := range []string{"wireless-site-tags/", "wireless-policy-tags/"} {
		if local, ok := strings.CutPrefix(resource, ns); ok && strings.HasPrefix(local, "tag-proof:") {
			return true
		}
	}
	return false
}
func wirelessTagPrivateParts(resource string) (canonical, keyID, predecessor string, ok bool) {
	for _, ns := range []string{"wireless-site-tags/", "wireless-policy-tags/"} {
		local, found := strings.CutPrefix(resource, ns+wirelessTagPrivatePrefix)
		membership := false
		if !found {
			local, found = strings.CutPrefix(resource, ns+wirelessTagMembershipPrefix)
			membership = found
			if !found {
				local, found = strings.CutPrefix(resource, ns+wirelessTagPrunePrefix)
				membership = found
			}
		}
		if !found {
			local, found = strings.CutPrefix(resource, ns+wirelessTagBoundPrefix)
			if found {
				mode, rest, hasMode := strings.Cut(local, ":")
				if !hasMode || mode != "write" && mode != "members" && mode != "delete" {
					return "", "", "", false
				}
				local, membership = rest, mode != "write"
			}
		}
		if !found {
			local, found = strings.CutPrefix(resource, ns+wirelessTagLineagePrefix)
			if found {
				mode := wirelessTagLineageMode(resource)
				if mode == "" {
					return "", "", "", false
				}
				local = strings.TrimPrefix(local, mode+":")
				membership = mode == "members" || mode == "delete"
				// Remove the predecessor cohort digest before decoding the ordinary
				// group/key/journal suffix. It is not part of the native selector.
				parts := strings.Split(local, ":")
				at := len(parts) - 4
				if at < 1 || !profileSiteDigestValid(parts[at]) {
					return "", "", "", false
				}
				local = strings.Join(append(parts[:at], parts[at+1:]...), ":")
			}
		}
		if !found {
			continue
		}
		parts := strings.Split(local, ":")
		if len(parts) < 4 {
			return "", "", "", false
		}
		keyID, predecessor = parts[len(parts)-2], parts[len(parts)-1]
		legacy := parts[:len(parts)-3]
		if membership {
			if len(legacy) < 2 || !wirelessTagMembershipScopeValid(legacy[len(legacy)-1]) {
				return "", "", "", false
			}
			legacy = legacy[:len(legacy)-1]
		}
		canonical = ns + strings.Join(legacy, ":")
		if strings.Contains(canonical, "tag-proof:") || !profileSiteDigestValid(parts[len(parts)-3]) || !profileSiteDigestValid(keyID) || !profileSiteDigestValid(predecessor) {
			return "", "", "", false
		}
		kind, _, valid := networkProfileMetadataResource(canonical)
		return canonical, keyID, predecessor, valid && (kind == "site-tag" || kind == "policy-tag" || !membership && kind == "policy-cohort")
	}
	return "", "", "", false
}
func wirelessTagProducedGroup(resource string) string {
	if _, _, _, ok := wirelessTagPrivateParts(resource); !ok {
		return ""
	}
	p := strings.Split(resource, ":")
	return p[len(p)-3]
}
func wirelessTagCanonical(resource string) string {
	if base, _, _, ok := wirelessTagPrivateParts(resource); ok {
		return base
	}
	return resource
}
func wirelessTagPrivateEnvelope(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	if _, _, _, ok := wirelessTagPrivateParts(op.Resource); !ok {
		return false
	}
	if wirelessTagMembershipSelected(op.Resource) && op.Action != "Update" || wirelessTagPruneSelected(op.Resource) && op.Action != "Delete" || (wirelessTagMembershipSelected(op.Resource) || wirelessTagPruneSelected(op.Resource)) && len(op.Resource) > 512 {
		return false
	}
	if mode := wirelessTagBindingMode(op.Resource); mode != "" && (len(op.Resource) > 512 || mode == "write" && op.Action != "Create" && op.Action != "Update") {
		return false
	}
	if wirelessTagRecreateSelected(op.Resource) && op.Action != "Create" {
		return false
	}
	if wirelessTagLineageMode(op.Resource) == "write" && op.Action != "Update" {
		return false
	}
	if wirelessTagPruneSelected(op.Resource) {
		if _, _, _, ok := wirelessTagPruneParts(op.Resource); !ok {
			return false
		}
	}
	copy := op.DeepCopy()
	copy.Phase = "Unknown"
	return networkProfileMetadataEnvelope(copy)
}
func (a *adapter) compileWirelessTagComparison(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	if !wirelessTagComparisonConfig(c) || source == nil || c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return configurationApplyPlan{}, errConfigurationSecret
	}
	plan, err := a.client.compileConfigurationApply(source.raw, c.Spec.ManagedSections, source.templateFiles)
	if err != nil {
		return plan, err
	}
	e, ok := plan.executor.(*networkProfileConfiguration)
	if !ok {
		return plan, errConfigurationTransition
	}
	comparison, err := a.resolveTemplateComparison(nc, c, source, "network_profiles", wirelessTagComparisonScope, "cvk-wireless-tags-key-v1")
	if err != nil {
		return plan, err
	}
	e.comparison = comparison
	for _, tags := range e.tagExecutors {
		tags.comparison = comparison
	}
	return plan, nil
}

// Native fields and dependency values stay in memory. Public rows contain only
// UUID/name identities and domain-separated keyed comparisons of this snapshot.
func wirelessTagBaseline(profile wirelessProfileMetadata, actual *wirelessProfileTagMetadata, refs wirelessProfileTagReferences, policy bool) (any, error) {
	if actual == nil {
		return nil, nil
	}
	if actual.unmapped {
		return nil, errConfigurationOwned
	}
	baseline := map[string]any{"profile": profile.id, "id": actual.id, "fields": actual.fields}
	paths := map[string]string{}
	for path, id := range refs.sites {
		if paths[id] != "" {
			return nil, errInvalidResponse
		}
		paths[id] = path
	}
	sites := map[string]string{}
	ids, ok := actual.fields["siteIds"].([]string)
	if !ok {
		return nil, errInvalidResponse
	}
	for _, id := range ids {
		if paths[id] == "" || !refs.assigned[id] {
			return nil, errConfigurationOwned
		}
		sites[paths[id]] = id
	}
	baseline["sites"] = sites
	if policy {
		selected := map[string]any{}
		names, ok := actual.fields["apZones"].([]string)
		if !ok {
			return nil, errInvalidResponse
		}
		zones, _ := profile.fields["apZones"].([]map[string]any)
		for _, name := range names {
			if name == "default-zone" {
				selected[name] = true
				continue
			}
			for _, zone := range zones {
				if zone["apZoneName"] == name {
					selected[name] = zone
				}
			}
			if selected[name] == nil {
				return nil, errConfigurationOwned
			}
		}
		baseline["zones"] = selected
	} else {
		if name, _ := actual.fields["apProfileName"].(string); name != "" {
			id := refs.apProfiles[name]
			if !validAPIID(id) {
				return nil, errConfigurationOwned
			}
			baseline["apProfile"] = []string{name, id}
		}
		if name, _ := actual.fields["flexProfileName"].(string); name != "" && name != "default-flex-profile" {
			return nil, errWirelessCustomFlexInventory
		}
	}
	return baseline, nil
}
func wirelessTagMAC(comparison *templateInputComparison, ns, parent, key, id string, snapshot any) (string, error) {
	if comparison == nil || len(comparison.key) != 32 || !profileSiteDigestValid(comparison.keyID) || !validAPIID(parent) || !profileSiteDigestValid(key) || !validAPIID(id) || snapshot == nil {
		return "", errConfigurationSecret
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return "", errInvalidResponse
	}
	mac := hmac.New(sha256.New, comparison.key)
	mac.Write([]byte("cvk-wireless-tag-native-v1\x00" + comparison.keyID + "\x00" + ns + parent + "\x00" + key + "\x00" + id + "\x00"))
	mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}
func wirelessTagLogical(local string) (string, bool) {
	for _, prefix := range []string{"site-tag:", "policy-tag:"} {
		if rest, ok := strings.CutPrefix(local, prefix); ok {
			parts := strings.Split(rest, ":")
			if len(parts) == 2 && profileSiteDigestValid(parts[0]) && profileSiteDigestValid(parts[1]) {
				return parts[0], true
			}
		}
	}
	return "", false
}
func wirelessTagGroupKey(effects []string) string {
	keys := []string{}
	for _, effect := range effects {
		keys = append(keys, strings.Split(effect, ":")[0])
	}
	sort.Strings(keys)
	raw, _ := json.Marshal(keys)
	return profileSiteDigest(string(raw))
}
func wirelessTagReceiptBase(op *cvk.NetworkControllerConfigurationOperation, effects []string) string {
	canonical, keyID, _, _ := wirelessTagPrivateParts(op.Resource)
	ns := strings.SplitN(canonical, "/", 2)[0]
	prefix := wirelessTagAppliedPrefix
	if op.Action == "Create" {
		prefix = wirelessTagCreatedPrefix
	}
	return prefix + ns + ":" + keyID + ":" + wirelessTagGroupKey(effects)
}
func wirelessTagRowSeal(row cvk.NetworkControllerConfigurationActionApplication) string {
	row.Resource = wirelessTagReceiptBase(&row.Operation, row.EffectReceipts)
	raw, _ := json.Marshal(row)
	return profileSiteDigest(string(raw))
}
func wirelessTagBookkeeping(state *cvk.NetworkControllerConfigurationStatus, normalize string) string {
	return configurationBookkeepingDigest(state, func(row *cvk.NetworkControllerConfigurationActionApplication) {
		if normalize != "" && row.Operation.ID == normalize {
			row.Resource = wirelessTagReceiptBase(&row.Operation, row.EffectReceipts) + ":" + wirelessTagRowSeal(*row)
		}
	})
}
func wirelessTagRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	rows, err := wirelessTagCurrentRecords(state)
	if err != nil {
		return nil, err
	}
	if _, err := wirelessTagRetiredCohorts(state, rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func wirelessTagCurrentRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	out := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return out, nil
	}
	nonces := map[string]int{}
	for _, r := range state.ActionApplications {
		nonces[r.Operation.ID]++
	}
	for _, row := range state.ActionApplications {
		if strings.HasPrefix(row.Resource, wirelessTagRetiredPrefix) {
			continue
		}
		if !strings.HasPrefix(row.Resource, "wireless-tag-created:") && !strings.HasPrefix(row.Resource, "wireless-tag-applied:") && !wirelessTagPrivateSelected(row.Operation.Resource) {
			continue
		}
		op := &row.Operation
		canonical, _, _, ok := wirelessTagPrivateParts(op.Resource)
		kind, _, _ := networkProfileMetadataResource(canonical)
		if !ok || !wirelessTagPrivateEnvelope(op) || row.Section != "network_profiles" || op.Phase != "Verified" || nonces[op.ID] != 1 || len(row.EffectReceipts) == 0 || len(row.EffectReceipts) > 256 || kind != "policy-cohort" && len(row.EffectReceipts) != 1 || kind == "policy-cohort" && len(row.EffectReceipts) < 2 || row.VerifiedAt == nil || row.VerifiedAt.IsZero() || row.VerifiedAt.Before(op.SubmittedAt) || row.VerifiedAt.After(time.Now()) || op.SubmittedAt.After(time.Now()) {
			return nil, errConfigurationOwned
		}
		if !sort.StringsAreSorted(row.EffectReceipts) || wirelessTagGroupKey(row.EffectReceipts) != wirelessTagProducedGroup(op.Resource) {
			return nil, errConfigurationOwned
		}
		keys, ids := map[string]bool{}, map[string]bool{}
		for _, effect := range row.EffectReceipts {
			p := strings.Split(effect, ":")
			if len(p) != 3 || !profileSiteDigestValid(p[0]) || !validAPIID(p[1]) || !profileSiteDigestValid(p[2]) || keys[p[0]] || ids[p[1]] || (op.Action == "Update" || op.Action == "Delete") && op.TargetID != p[1] {
				return nil, errConfigurationOwned
			}
			keys[p[0]], ids[p[1]] = true, true
		}
		if kind != "policy-cohort" {
			key, valid := wirelessTagLogical(strings.SplitN(canonical, "/", 2)[1])
			if !valid || !keys[key] {
				return nil, errConfigurationOwned
			}
		}
		base := wirelessTagReceiptBase(op, row.EffectReceipts)
		suffix, found := strings.CutPrefix(row.Resource, base+":")
		p := strings.Split(suffix, ":")
		if !found || len(p) != 2 || p[0] != wirelessTagRowSeal(row) || !profileSiteDigestValid(p[1]) {
			return nil, errConfigurationOwned
		}
		owners := 0
		for _, owner := range state.Owners {
			if owner.Section == op.Section {
				if owner.ConfigName != op.ConfigName || owner.ConfigUID != op.ConfigUID {
					return nil, errConfigurationOwned
				}
				owners++
			}
		}
		if owners != 1 {
			return nil, errConfigurationOwned
		}
		if _, dup := out[base]; dup {
			return nil, errConfigurationOwned
		}
		out[base] = *row.DeepCopy()
	}
	if _, err := wirelessTagLatest(out); err != nil {
		return nil, err
	}
	return out, nil
}
func wirelessTagReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || op.Phase != "Verified" {
		return false
	}
	rows, err := wirelessTagRecords(state)
	if err != nil {
		return false
	}
	for base, row := range rows {
		if row.Operation.ID == op.ID {
			return reflect.DeepEqual(row.Operation, *op) && row.Resource == base+":"+wirelessTagRowSeal(row)+":"+wirelessTagBookkeeping(state, op.ID)
		}
	}
	return false
}

type privateWirelessTagExecutor struct {
	base      *wirelessProfileTagExecutor
	namespace string
	plan      func(context.Context) (string, error)
	proof     func(context.Context, string) error
	lineage   func([]string) (string, error)
	recreate  func(context.Context, *configurationWrite, []string) error
}

func (e *privateWirelessTagExecutor) count() int { return e.base.count() }
func (e *privateWirelessTagExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	copy := op.DeepCopy()
	full := e.namespace + copy.Resource
	if wirelessTagBindingMode(full) != "" {
		copy.Resource = full
		view := &networkProfileConfiguration{tagExecutors: []*wirelessProfileTagExecutor{e.base}}
		_, done, err := view.boundTagReadback(ctx, copy)
		return done, err
	}
	canonical, _, _, ok := wirelessTagPrivateParts(full)
	if ok {
		copy.Resource = strings.TrimPrefix(canonical, e.namespace)
	}
	return e.base.verify(ctx, copy)
}
func (e *privateWirelessTagExecutor) next(ctx context.Context) (*configurationWrite, error) {
	predecessor, err := e.plan(ctx)
	if err != nil {
		return nil, err
	}
	write, err := e.base.next(ctx)
	if err != nil || write == nil {
		return write, err
	}
	legacy := write.resource
	frozen, err := e.base.observe(ctx)
	if err != nil {
		return nil, err
	}
	bound, err := bindWirelessTagRows(ctx, e.base.client, frozen, e.base.policy)
	if err != nil {
		return nil, err
	}
	selected := []wirelessProfileTagObservation{}
	members := []string{}
	for i, row := range frozen {
		if strings.HasPrefix(legacy, wirelessPolicyCohortPrefix) && row.profileID == write.identity.parent || row.resource == legacy {
			key, _ := wirelessTagLogical(row.resource)
			members = append(members, key+":")
			selected = append(selected, bound[i])
		}
	}
	if len(members) == 0 || len(members) > 256 {
		return nil, errConfigurationBusy
	}
	canonical := selected[0].resource
	if strings.HasPrefix(legacy, wirelessPolicyCohortPrefix) {
		canonical, _, err = wirelessPolicyCohortResource(selected)
		if err != nil {
			return nil, err
		}
	}
	write.resource = wirelessTagBoundPrefix + "write:" + canonical + ":" + wirelessTagGroupKey(members) + ":" + e.base.comparison.keyID + ":" + predecessor
	for _, row := range frozen {
		if row.resource == legacy && row.actual != nil && !wirelessTagRetainsMembers(row.actual.fields, row.payload) {
			ids, err := wirelessTagRemovedScopes(row)
			if err != nil {
				return nil, err
			}
			scope, err := encodeWirelessTagMembershipScope(ids)
			if err != nil {
				return nil, err
			}
			write.resource = wirelessTagBoundPrefix + "members:" + canonical + ":" + scope + ":" + wirelessTagGroupKey(members) + ":" + e.base.comparison.keyID + ":" + predecessor
			if len(e.namespace+write.resource) > 512 {
				return nil, errConfigurationOwned
			}
		}
	}
	if write.action == "Update" && e.lineage != nil {
		digest, err := e.lineage(members)
		if err != nil {
			return nil, err
		}
		if digest != "" {
			resource := e.namespace + write.resource
			resource, err = wirelessTagLineageResource(resource, wirelessTagBindingMode(resource), digest)
			if err != nil {
				return nil, err
			}
			write.resource = strings.TrimPrefix(resource, e.namespace)
		}
	}
	if write.action == "Create" && e.recreate != nil {
		if err := e.recreate(ctx, write, members); err != nil {
			return nil, err
		}
	}
	if len(e.namespace+write.resource) > 512 {
		return nil, errConfigurationOwned
	}

	guardConfigurationPreflight(write, func(ctx context.Context) error {
		if err := e.proof(ctx, predecessor); err != nil {
			return err
		}
		for range 2 {
			fresh, err := e.base.observe(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(frozen, fresh) {
				return errConfigurationStale
			}
			freshBound, err := bindWirelessTagRows(ctx, e.base.client, fresh, e.base.policy)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(bound, freshBound) {
				return errConfigurationStale
			}
		}
		return nil
	})
	return write, nil
}
func (e *networkProfileConfiguration) privateTagMembers(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (any, bool, error) {
	canonical, keyID, _, ok := wirelessTagPrivateParts(op.Resource)
	if !ok || e.comparison == nil || keyID != e.comparison.keyID {
		return nil, false, errConfigurationOwned
	}
	if wirelessTagPruneSelected(op.Resource) {
		if e.tagPrune == nil {
			return nil, false, errConfigurationOwned
		}
		return e.tagPrune.observe(ctx, op)
	}
	if wirelessTagBindingMode(op.Resource) != "" {
		rows, done, err := e.boundTagReadback(ctx, op)
		if err != nil || !done {
			return nil, false, err
		}
		ns := strings.SplitN(canonical, "/", 2)[0] + "/"
		effects := []string{}
		for _, row := range rows {
			key, _ := wirelessTagLogical(row.resource)
			baseline, err := wirelessTagOperationBaseline(op.Resource, row.baseline)
			if err != nil {
				return nil, false, err
			}
			mac, err := wirelessTagMAC(e.comparison, ns, row.profileID, key, row.tagID, baseline)
			if err != nil {
				return nil, false, err
			}
			effects = append(effects, key+":"+row.tagID+":"+mac)
		}
		sort.Strings(effects)
		return effects, true, nil
	}
	copy := op.DeepCopy()
	copy.Resource = canonical
	copy.Phase = "Unknown"
	_, done, err := e.observeProfileMetadata(ctx, copy)
	if err != nil || !done {
		return nil, false, err
	}
	if wirelessTagMembershipSelected(op.Resource) {
		ids, err := decodeWirelessTagMembershipScope(op.Resource)
		if err != nil {
			return nil, false, err
		}
		if err := e.tagExecutors[0].client.wirelessTagUnusedScopes(ctx, ids); err != nil {
			return nil, false, err
		}
	}
	kind, _, _ := networkProfileMetadataResource(canonical)
	ns := strings.SplitN(canonical, "/", 2)[0] + "/"
	effects := []string{}
	for _, tags := range e.tagExecutors {
		if tags.policy != (ns == "wireless-policy-tags/") {
			continue
		}
		rows, err := tags.observe(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, row := range rows {
			if kind == "policy-cohort" && row.profileID == op.ParentID || canonical == ns+row.resource {
				if row.actual == nil || row.profileID != op.ParentID || op.Action == "Update" && row.tagID != op.TargetID || !reflect.DeepEqual(row.payload, row.actual.fields) {
					return nil, false, errConfigurationStale
				}
				key, _ := wirelessTagLogical(row.resource)
				mac, err := wirelessTagMAC(e.comparison, ns, row.profileID, key, row.tagID, row.baseline)
				if err != nil {
					return nil, false, err
				}
				effects = append(effects, key+":"+row.tagID+":"+mac)
			}
		}
	}
	sort.Strings(effects)
	if len(effects) == 0 || len(effects) > 256 || wirelessTagGroupKey(effects) != wirelessTagProducedGroup(op.Resource) {
		return nil, false, errConfigurationOwned
	}
	return effects, true, nil
}

type wirelessTagMemberProof struct {
	row     cvk.NetworkControllerConfigurationActionApplication
	id, mac string
}

func wirelessTagLatest(rows map[string]cvk.NetworkControllerConfigurationActionApplication) (map[string]wirelessTagMemberProof, error) {
	out := map[string]wirelessTagMemberProof{}
	creators := map[string]wirelessTagMemberProof{}
	identities := map[string]string{}
	for _, row := range rows {
		canonical, _, _, _ := wirelessTagPrivateParts(row.Operation.Resource)
		ns := strings.SplitN(canonical, "/", 2)[0] + "/"
		for _, effect := range row.EffectReceipts {
			p := strings.Split(effect, ":")
			key := ns + p[0]
			native := ns + row.Operation.ParentID + "\x00" + p[1]
			if old, exists := identities[native]; exists && old != key {
				return nil, errConfigurationOwned
			}
			identities[native] = key
			proof := wirelessTagMemberProof{row: row, id: p[1], mac: p[2]}
			if row.Operation.Action == "Create" {
				if _, exists := creators[key]; exists {
					return nil, errConfigurationOwned
				}
				creators[key] = proof
			}
			prior, exists := out[key]
			if !exists || row.VerifiedAt.After(prior.row.VerifiedAt.Time) || row.VerifiedAt.Equal(prior.row.VerifiedAt) && row.Operation.Action != "Create" {
				out[key] = proof
			}
		}
	}
	for key, creator := range creators {
		latest := out[key]
		if latest.id != creator.id || latest.row.Operation.ParentID != creator.row.Operation.ParentID || latest.row.Operation.ConfigUID != creator.row.Operation.ConfigUID || latest.row.Operation.ConfigName != creator.row.Operation.ConfigName || latest.row.Operation.ConfigGeneration < creator.row.Operation.ConfigGeneration || latest.row.Operation.ControllerGeneration < creator.row.Operation.ControllerGeneration || latest.row.Operation.Action != "Create" && latest.row.Operation.SubmittedAt.Before(creator.row.VerifiedAt) {
			return nil, errConfigurationOwned
		}
		lineage, _ := wirelessTagLineageDigest(creator.row.Operation.Resource)
		latestLineage, _ := wirelessTagLineageDigest(latest.row.Operation.Resource)
		if lineage != latestLineage {
			return nil, errConfigurationOwned
		}
	}
	for key, latest := range out {
		if _, lineage := wirelessTagLineageDigest(latest.row.Operation.Resource); lineage {
			if _, exists := creators[key]; !exists {
				return nil, errConfigurationOwned
			}
		}
	}
	return out, nil
}
func (a *adapter) bindWirelessTagReceipts(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if plan == nil || plan.section != "network_profiles" {
		return nil
	}
	e, ok := plan.executor.(*networkProfileConfiguration)
	if !ok {
		return errConfigurationTransition
	}
	retained, err := wirelessTagRecords(nc.Status.Configuration)
	if err != nil {
		return err
	}
	active := nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && wirelessTagPrivateSelected(nc.Status.Configuration.Operation.Resource)
	if e.comparison == nil {
		if len(retained) > 0 || active {
			return errConfigurationSecret
		}
		return nil
	}
	for _, row := range retained {
		_, keyID, _, _ := wirelessTagPrivateParts(row.Operation.Resource)
		if keyID != e.comparison.keyID {
			return errConfigurationSecret
		}
	}
	if active {
		op := nc.Status.Configuration.Operation
		if _, keyID, _, valid := wirelessTagPrivateParts(op.Resource); !valid || keyID != e.comparison.keyID || op.Phase == "Verified" && !wirelessTagReceiptPresent(nc.Status.Configuration, op) {
			return errConfigurationOwned
		}
	}
	latest, err := wirelessTagLatest(retained)
	if err != nil {
		return err
	}
	proof := func(ctx context.Context, pred string) error {
		if err := e.comparison.check(ctx); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return err
		}
		rows, err := wirelessTagRecords(current.Status.Configuration)
		if err != nil || !reflect.DeepEqual(rows, retained) || wirelessTagBookkeeping(current.Status.Configuration, "") != pred {
			return errConfigurationOwned
		}
		return nil
	}
	e.bindWirelessTagMembership(config, latest, proof, wirelessTagBookkeeping(nc.Status.Configuration, ""))
	e.bindWirelessTagPrune(a.client, config, latest, proof, wirelessTagBookkeeping(nc.Status.Configuration, ""))
	if nc.Status.Configuration != nil {
		e.tagPrune.capacity = len(nc.Status.Configuration.ActionApplications)
	}
	if err := e.bindWirelessTagRecreation(nc, config, source); err != nil {
		return err
	}
	planning := func(ctx context.Context) (string, error) {
		if err := e.comparison.check(ctx); err != nil {
			return "", err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return "", err
		}
		if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return "", err
		}
		rows, err := wirelessTagRecords(current.Status.Configuration)
		if err != nil || !reflect.DeepEqual(rows, retained) {
			return "", errConfigurationOwned
		}
		pred := wirelessTagBookkeeping(current.Status.Configuration, "")
		slots := map[string]bool{}
		checkedRecreations := map[string]bool{}
		if err := e.tagPrune.reserve(ctx, slots); err != nil {
			return "", err
		}
		for _, tags := range e.tagExecutors {
			ns := "wireless-site-tags/"
			if tags.policy {
				ns = "wireless-policy-tags/"
			}
			observed, err := tags.observe(ctx)
			if err != nil {
				return "", err
			}
			for _, row := range observed {
				key, valid := wirelessTagLogical(row.resource)
				if !valid {
					return "", errConfigurationTransition
				}
				for oldKey, old := range latest {
					if row.actual != nil && oldKey != ns+key && strings.HasPrefix(oldKey, ns) && old.row.Operation.ParentID == row.profileID && old.id == row.tagID {
						return "", errConfigurationOwned
					}
				}
				if old, exists := latest[ns+key]; exists {
					if old.row.Operation.Action == "Delete" {
						cohort, allowed := e.tagPrune.recreate[ns+key]
						if !allowed || row.actual != nil || row.profileID != cohort.ParentID {
							return "", errConfigurationOwned
						}
						if !checkedRecreations[cohort.Digest] {
							if err := e.tagPrune.recreationAbsent(ctx, cohort); err != nil {
								return "", err
							}
							checkedRecreations[cohort.Digest] = true
						}
					} else {
						if row.actual == nil || row.profileID != old.row.Operation.ParentID || row.tagID != old.id {
							return "", errConfigurationOwned
						}
						baseline, err := wirelessTagBaselineForRecord(ctx, tags.client, row, tags.policy, old)
						if err != nil {
							return "", err
						}
						mac, err := wirelessTagMAC(e.comparison, ns, row.profileID, key, row.tagID, baseline)
						if err != nil || mac != old.mac {
							return "", errConfigurationOwned
						}
					}
				}
				if row.actual != nil && reflect.DeepEqual(row.payload, row.actual.fields) {
					continue
				}
				group := []wirelessProfileTagObservation{row}
				action := "Update"
				legacy := row.resource
				if row.actual == nil {
					action = "Create"
					if tags.policy {
						group = policyRowsForProfile(observed, row.profileID)
						if len(group) > 1 {
							var err error
							legacy, _, err = wirelessPolicyCohortResource(group)
							if err != nil {
								return "", err
							}
						}
					}
				}
				if len(group) > 256 {
					return "", errConfigurationBusy
				}
				effects := []string{}
				for _, member := range group {
					k, ok := wirelessTagLogical(member.resource)
					if !ok {
						return "", errConfigurationTransition
					}
					effects = append(effects, k+":")
				}
				op := &cvk.NetworkControllerConfigurationOperation{Action: action, Resource: ns + wirelessTagPrivatePrefix + legacy + ":" + wirelessTagGroupKey(effects) + ":" + e.comparison.keyID + ":" + pred}
				slot := wirelessTagReceiptBase(op, effects)
				if action == "Create" {
					cohort, recreating, err := e.tagPrune.recreationGroup(ns, effects)
					if err != nil {
						return "", err
					}
					if recreating {
						// Old rows are archived, not replaced; reserve a new slot.
						slot += ":" + cohort.Digest
					}
				}
				slots[slot] = true
			}
		}
		additional := 0
		for slot := range slots {
			if _, exists := rows[slot]; !exists {
				additional++
			}
		}
		if current.Status.Configuration == nil || len(current.Status.Configuration.ActionApplications)+additional > 256 {
			return "", errConfigurationBusy
		}
		return pred, nil
	}
	for i, part := range e.tags.parts {
		base, ok := part.executor.(*wirelessProfileTagExecutor)
		if !ok {
			return errConfigurationTransition
		}
		namespace := part.name + "/"
		lineage := func(members []string) (string, error) {
			digest, seen := "", false
			for _, member := range members {
				key := strings.Split(member, ":")[0]
				old := latest[namespace+key]
				current, _ := wirelessTagLineageDigest(old.row.Operation.Resource)
				if seen && current != digest {
					return "", errConfigurationOwned
				}
				digest, seen = current, true
			}
			return digest, nil
		}
		recreate := func(ctx context.Context, write *configurationWrite, members []string) error {
			cohort, selected, err := e.tagPrune.recreationGroup(namespace, members)
			if err != nil || !selected {
				return err
			}
			if write.identity.parent != cohort.ParentID {
				return errConfigurationOwned
			}
			if err := e.tagPrune.recreationAbsent(ctx, cohort); err != nil {
				return err
			}
			resource, err := wirelessTagRecreationResource(namespace+write.resource, cohort)
			if err != nil {
				return err
			}
			write.resource = strings.TrimPrefix(resource, namespace)
			guardConfigurationPreflight(write, func(ctx context.Context) error { return e.tagPrune.recreationAbsent(ctx, cohort) })
			return nil
		}
		e.tags.parts[i].executor = &privateWirelessTagExecutor{base: base, namespace: namespace, plan: planning, proof: proof, lineage: lineage, recreate: recreate}
	}
	prior := *plan
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && wirelessTagPrivateSelected(op.Resource) || prior.recordCompletion != nil && (prior.receiptOperation == nil || prior.receiptOperation(op))
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if op == nil || !wirelessTagPrivateSelected(op.Resource) {
			if prior.recordCompletion != nil {
				return prior.recordCompletion(ctx, op)
			}
			return errConfigurationTransition
		}
		return a.recordWirelessTagReceipt(ctx, config, source, e, op, retained)
	}
	return nil
}
func (a *adapter) recordWirelessTagReceipt(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, e *networkProfileConfiguration, op *cvk.NetworkControllerConfigurationOperation, expected map[string]cvk.NetworkControllerConfigurationActionApplication) error {
	if op == nil || op.Phase != "Verifying" || !wirelessTagPrivateEnvelope(op) {
		return errConfigurationTransition
	}
	_, keyID, pred, _ := wirelessTagPrivateParts(op.Resource)
	if e.comparison == nil || keyID != e.comparison.keyID {
		return errConfigurationSecret
	}
	current, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
		return errConfigurationStale
	}
	retained, err := wirelessTagRecords(current.Status.Configuration)
	if err != nil || !reflect.DeepEqual(retained, expected) || wirelessTagBookkeeping(current.Status.Configuration, "") != pred {
		return errConfigurationOwned
	}
	observed := op.DeepCopy()
	observed.Phase = "Unknown"
	var effects []string
	done, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "network_profiles", func(ctx context.Context) (bool, error) {
		before, done, err := e.privateTagMembers(ctx, observed)
		if err != nil || !done {
			return false, err
		}
		done, err = a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
		if err != nil || !done {
			return false, err
		}
		done, err = a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
		if err != nil || !done {
			return false, err
		}
		after, done, err := e.privateTagMembers(ctx, observed)
		if err != nil || !done {
			return false, err
		}
		if !reflect.DeepEqual(before, after) {
			return false, errConfigurationStale
		}
		effects = after.([]string)
		return true, nil
	})
	if err != nil {
		return err
	}
	if !done {
		return errConfigurationBusy
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if fresh.UID != current.UID || fresh.ResourceVersion != current.ResourceVersion || !reflect.DeepEqual(fresh.Status.Configuration.Operation, op) {
		return errConfigurationStale
	}
	if wirelessTagRecreateSelected(op.Resource) {
		staged, err := wirelessTagArchivePredecessor(current.Status.Configuration, op, effects)
		if err != nil {
			return err
		}
		current.Status.Configuration = staged
	}
	verified := op.DeepCopy()
	verified.Phase = "Verified"
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if at.Before(op.SubmittedAt) {
		return errConfigurationBusy
	}
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "network_profiles", Operation: *verified, VerifiedAt: &at, EffectReceipts: effects}
	base := wirelessTagReceiptBase(verified, effects)
	row.Resource = base + ":" + wirelessTagRowSeal(row)
	index := -1
	for i, old := range current.Status.Configuration.ActionApplications {
		if strings.HasPrefix(old.Resource, base+":") {
			index = i
		}
	}
	if index >= 0 {
		old := current.Status.Configuration.ActionApplications[index]
		if (op.Action != "Update" && op.Action != "Delete") || old.Operation.ParentID != op.ParentID || old.Operation.TargetID != op.TargetID || op.SubmittedAt.Before(old.VerifiedAt) || old.Operation.ConfigGeneration > op.ConfigGeneration || old.Operation.ControllerGeneration > op.ControllerGeneration {
			return errConfigurationOwned
		}
		current.Status.Configuration.ActionApplications[index] = row
	} else {
		if len(current.Status.Configuration.ActionApplications) >= 256 {
			return errConfigurationBusy
		}
		current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, row)
		index = len(current.Status.Configuration.ActionApplications) - 1
	}
	current.Status.Configuration.Operation = verified
	current.Status.Configuration.ActionApplications[index].Resource += ":" + wirelessTagBookkeeping(current.Status.Configuration, op.ID)
	if !wirelessTagReceiptPresent(current.Status.Configuration, verified) {
		return errConfigurationOwned
	}
	if err := a.persistConfigurationActionReceipt(ctx, current); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if !wirelessTagReceiptPresent(stored.Status.Configuration, verified) {
		return errConfigurationOwned
	}
	return nil
}
