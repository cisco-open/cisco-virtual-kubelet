// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const wirelessTagPrunePrefix = "tag-proof:v3:"

func wirelessTagPruneSelected(resource string) bool {
	for _, ns := range []string{"wireless-site-tags/", "wireless-policy-tags/"} {
		if strings.HasPrefix(resource, ns+wirelessTagPrunePrefix) || wirelessTagBindingMode(resource) == "delete" {
			return true
		}
	}
	return false
}

type wirelessTagPruneObservation struct {
	Namespace, Key, ParentID, TagID, Resource, Effect string
	Actual                                            *wirelessProfileTagMetadata
	Scope                                             []string
}
type wirelessTagPruneExecutor struct {
	c           *client
	comparison  *templateInputComparison
	latest      map[string]wirelessTagMemberProof
	wanted      map[string]bool
	recreate    map[string]wirelessTagRecreationCohort
	enabled     bool
	capacity    int
	predecessor string
	proof       func(context.Context, string) error
}

func (e *networkProfileConfiguration) bindWirelessTagPrune(c *client, config *configapi.NetworkControllerConfig, latest map[string]wirelessTagMemberProof, proof func(context.Context, string) error, pred string) {
	p := &wirelessTagPruneExecutor{c: c, comparison: e.comparison, latest: latest, wanted: map[string]bool{}, enabled: config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete, predecessor: pred, proof: proof}
	for _, tags := range e.tagExecutors {
		ns := "wireless-site-tags/"
		if tags.policy {
			ns = "wireless-policy-tags/"
		}
		for _, profile := range tags.desired {
			for _, tag := range tags.tags(profile) {
				if len(tag.sites) > 0 {
					key, _ := wirelessTagDigest([]string{profile.name, tag.name})
					p.wanted[ns+key] = true
				}
			}
		}
	}
	e.tagPrune = p
}
func wirelessTagPruneParts(resource string) (ns, key string, ids []string, ok bool) {
	if !wirelessTagPruneSelected(resource) {
		return
	}
	canonical, _, _, valid := wirelessTagPrivateParts(resource)
	if !valid {
		return
	}
	parts := strings.SplitN(canonical, "/", 2)
	ns = parts[0] + "/"
	key, valid = wirelessTagLogical(parts[1])
	if !valid {
		return "", "", nil, false
	}
	if wirelessTagProducedGroup(resource) != wirelessTagGroupKey([]string{key + ":"}) {
		return "", "", nil, false
	}
	ids, err := decodeWirelessTagMembershipScope(resource)
	return ns, key, ids, err == nil
}
func (e *wirelessTagPruneExecutor) scope(ctx context.Context, ns, key, parent, id string, ids []string, consumers, bound bool) (wirelessTagPruneObservation, error) {
	return e.scopeLineage(ctx, ns, key, parent, id, ids, consumers, bound, "")
}
func (e *wirelessTagPruneExecutor) scopeLineage(ctx context.Context, ns, key, parent, id string, ids []string, consumers, bound bool, lineage string) (wirelessTagPruneObservation, error) {
	out := wirelessTagPruneObservation{Namespace: ns, Key: key, ParentID: parent, TagID: id, Scope: append([]string{}, ids...)}
	profiles, err := e.c.wirelessProfileInventory(ctx)
	if err != nil {
		return out, err
	}
	var profile *wirelessProfileMetadata
	for _, row := range profiles {
		if row.id == parent {
			copy := row
			profile = &copy
		}
	}
	if profile == nil || profile.unmapped {
		return out, errConfigurationOwned
	}
	reader := e.c.profileSitePruneReader()
	if err := reader.loadSites(ctx); err != nil {
		return out, err
	}
	scopes := map[string]string{}
	for _, siteID := range ids {
		path := reader.sites[siteID].NameHierarchy
		if path == "" || path == "Global" {
			return out, errProfileSitePruneReferenced
		}
		scopes[path] = siteID
		if consumers {
			if err := reader.unusedScope(ctx, path); err != nil {
				return out, err
			}
		}
	}
	sealing := map[string]any{"deleteScope": scopes, "parentName": profile.name}
	if bound {
		graph, err := wirelessTagZoneGraph(ctx, e.c, *profile, ns == "wireless-policy-tags/")
		if err != nil {
			return out, err
		}
		sealing["version"], sealing["zoneReferences"] = 4, graph
	}
	var sealBaseline any = sealing
	if lineage != "" {
		if !bound || !profileSiteDigestValid(lineage) {
			return out, errConfigurationOwned
		}
		sealBaseline = map[string]any{"version": 5, "incarnation": lineage, "native": sealing}
	}
	seal, err := wirelessTagMAC(e.comparison, ns, parent, key, id, sealBaseline)
	if err != nil {
		return out, err
	}
	kind := "site-tag:"
	policy := ns == "wireless-policy-tags/"
	if policy {
		kind = "policy-tag:"
	}
	scope, err := encodeWirelessTagMembershipScope(ids)
	if err != nil {
		return out, err
	}
	effects := []string{key + ":"}
	out.Resource = ns + wirelessTagPrunePrefix + kind + key + ":" + seal + ":" + scope + ":" + wirelessTagGroupKey(effects) + ":" + e.comparison.keyID + ":" + e.predecessor
	if bound {
		out.Resource = strings.Replace(out.Resource, ns+wirelessTagPrunePrefix, ns+wirelessTagBoundPrefix+"delete:", 1)
	}
	if lineage != "" {
		out.Resource, err = wirelessTagLineageResource(out.Resource, "delete", lineage)
		if err != nil {
			return out, err
		}
	}
	if len(out.Resource) > 512 {
		return out, errConfigurationOwned
	}
	effectBaseline, err := wirelessTagOperationBaseline(out.Resource, map[string]any{"deleted": true, "scope": seal})
	if err != nil {
		return out, err
	}
	mac, err := wirelessTagMAC(e.comparison, ns, parent, key, id, effectBaseline)
	if err != nil {
		return out, err
	}
	out.Effect = key + ":" + id + ":" + mac
	inventory, err := e.c.wirelessProfileTagInventory(ctx, parent, policy)
	if err != nil {
		return out, err
	}
	for _, actual := range inventory {
		if actual.unmapped {
			return out, errConfigurationOwned
		}
		logical, _ := wirelessTagDigest([]string{profile.name, actual.name})
		if logical != key && actual.id != id {
			continue
		}
		if logical != key || actual.id != id || actual.unmapped {
			return out, errConfigurationOwned
		}
		copy := actual
		out.Actual = &copy
	}
	return out, nil
}
func (e *wirelessTagPruneExecutor) live(ctx context.Context, identity string, old wirelessTagMemberProof) (wirelessTagPruneObservation, error) {
	ns := strings.SplitN(identity, "/", 2)[0] + "/"
	key := strings.TrimPrefix(identity, ns)
	profiles, err := e.c.wirelessProfileInventory(ctx)
	if err != nil {
		return wirelessTagPruneObservation{}, err
	}
	var profile *wirelessProfileMetadata
	for _, row := range profiles {
		if row.id == old.row.Operation.ParentID {
			copy := row
			profile = &copy
		}
	}
	if profile == nil || profile.unmapped {
		return wirelessTagPruneObservation{}, errConfigurationOwned
	}
	policy := ns == "wireless-policy-tags/"
	inventory, err := e.c.wirelessProfileTagInventory(ctx, profile.id, policy)
	if err != nil {
		return wirelessTagPruneObservation{}, err
	}
	var actual *wirelessProfileTagMetadata
	for _, row := range inventory {
		if row.id == old.id {
			copy := row
			actual = &copy
		}
	}
	if actual == nil || actual.unmapped {
		return wirelessTagPruneObservation{}, errConfigurationOwned
	}
	logical, _ := wirelessTagDigest([]string{profile.name, actual.name})
	if logical != key {
		return wirelessTagPruneObservation{}, errConfigurationOwned
	}
	ids, ok := actual.fields["siteIds"].([]string)
	if !ok || len(ids) == 0 {
		return wirelessTagPruneObservation{}, errConfigurationOwned
	}
	want := wirelessProfileTag{name: actual.name, fields: actual.fields}
	refs, err := e.c.wirelessProfileTagReferences(ctx, *profile, want, actual, policy)
	if err != nil {
		return wirelessTagPruneObservation{}, err
	}
	baseline, err := wirelessTagBaseline(*profile, actual, refs, policy)
	if err != nil {
		return wirelessTagPruneObservation{}, err
	}
	bound := wirelessTagBindingMode(old.row.Operation.Resource) != ""
	if bound {
		graph, err := wirelessTagZoneGraph(ctx, e.c, *profile, policy)
		if err != nil {
			return wirelessTagPruneObservation{}, err
		}
		baseline = wirelessTagBoundBaseline(baseline, graph)
	}
	baseline, err = wirelessTagOperationBaseline(old.row.Operation.Resource, baseline)
	if err != nil {
		return wirelessTagPruneObservation{}, err
	}
	mac, err := wirelessTagMAC(e.comparison, ns, profile.id, key, old.id, baseline)
	if err != nil || mac != old.mac {
		return wirelessTagPruneObservation{}, errConfigurationOwned
	}
	lineage, _ := wirelessTagLineageDigest(old.row.Operation.Resource)
	out, err := e.scopeLineage(ctx, ns, key, profile.id, old.id, ids, true, bound, lineage)
	if err != nil {
		return out, err
	}
	if out.Actual == nil || !reflect.DeepEqual(*actual, *out.Actual) {
		return out, errConfigurationStale
	}
	return out, nil
}
func (e *wirelessTagPruneExecutor) snapshot(ctx context.Context) ([]wirelessTagPruneObservation, error) {
	if e == nil {
		return nil, nil
	}
	for key, old := range e.latest {
		if e.wanted[key] && old.row.Operation.Action == "Delete" {
			if _, allowed := e.recreate[key]; !allowed {
				return nil, errConfigurationOwned
			}
		}
	}
	if !e.enabled || len(e.latest) == 0 {
		return nil, nil
	}
	if err := e.proof(ctx, e.predecessor); err != nil {
		return nil, err
	}
	keys := []string{}
	for key := range e.latest {
		if !e.wanted[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	out := []wirelessTagPruneObservation{}
	for _, identity := range keys {
		old := e.latest[identity]
		if old.row.Operation.Action == "Delete" {
			ns, key, ids, valid := wirelessTagPruneParts(old.row.Operation.Resource)
			if !valid {
				return nil, errConfigurationOwned
			}
			lineage, _ := wirelessTagLineageDigest(old.row.Operation.Resource)
			observed, err := e.scopeLineage(ctx, ns, key, old.row.Operation.ParentID, old.id, ids, false, wirelessTagBindingMode(old.row.Operation.Resource) != "", lineage)
			if err != nil {
				return nil, err
			}
			// The historical predecessor differs after receipt publication; compare
			// the scope/key/native binding rather than minting a new operation.
			canonical, _, _, _ := wirelessTagPrivateParts(observed.Resource)
			if observed.Actual != nil || canonical != wirelessTagCanonical(old.row.Operation.Resource) || observed.Effect != identity[len(ns):]+":"+old.id+":"+old.mac {
				return nil, errConfigurationOwned
			}
			continue
		}
		observed, err := e.live(ctx, identity, old)
		if err != nil {
			return nil, err
		}
		out = append(out, observed)
	}
	additional := 0
	for _, row := range out {
		if e.latest[row.Namespace+row.Key].row.Operation.Action == "Create" {
			additional++
		}
	}
	if e.capacity+additional > 256 {
		return nil, errConfigurationBusy
	}
	return out, nil
}
func (e *wirelessTagPruneExecutor) reserve(ctx context.Context, slots map[string]bool) error {
	rows, err := e.snapshot(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		op := &cvk.NetworkControllerConfigurationOperation{Resource: row.Resource, Action: "Delete"}
		slots[wirelessTagReceiptBase(op, []string{row.Effect})] = true
	}
	return nil
}
func (e *wirelessTagPruneExecutor) next(ctx context.Context) (*configurationWrite, error) {
	rows, err := e.snapshot(ctx)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	row := rows[0]
	write := &configurationWrite{resource: row.Resource, action: "Delete", identity: configurationResourceIdentity{parent: row.ParentID, target: row.TagID}}
	write.submit = func(ctx context.Context) (Task, error) {
		raw, err := e.c.mutate(ctx, http.MethodDelete, wirelessProfileTagPath(row.ParentID, row.Namespace == "wireless-policy-tags/")+"/"+url.PathEscape(row.TagID), nil)
		if err != nil {
			return Task{}, err
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return receipt.Response, nil
	}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		for range 2 {
			fresh, err := e.snapshot(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(fresh, rows) {
				return errConfigurationStale
			}
		}
		return e.proof(ctx, e.predecessor)
	})
	return write, nil
}
func (e *wirelessTagPruneExecutor) selected(op *cvk.NetworkControllerConfigurationOperation) bool {
	if e == nil || !e.enabled || op == nil || op.Action != "Delete" {
		return false
	}
	ns, key, _, valid := wirelessTagPruneParts(op.Resource)
	old, exists := e.latest[ns+key]
	_, keyID, pred, ok := wirelessTagPrivateParts(op.Resource)
	lineage, _ := wirelessTagLineageDigest(op.Resource)
	oldLineage, _ := wirelessTagLineageDigest(old.row.Operation.Resource)
	if valid && exists && old.row.Operation.Action == "Delete" {
		return !e.wanted[ns+key] && reflect.DeepEqual(old.row.Operation, *op)
	}
	return valid && ok && exists && lineage == oldLineage && !e.wanted[ns+key] && old.row.Operation.Action != "Delete" && (wirelessTagBindingMode(op.Resource) != "") == (wirelessTagBindingMode(old.row.Operation.Resource) != "") && keyID == e.comparison.keyID && pred == e.predecessor && op.ParentID == old.row.Operation.ParentID && op.TargetID == old.id && op.ConfigUID == old.row.Operation.ConfigUID && op.ConfigName == old.row.Operation.ConfigName && op.ConfigGeneration >= old.row.Operation.ConfigGeneration && op.ControllerGeneration >= old.row.Operation.ControllerGeneration && (op.SubmittedAt == nil || !op.SubmittedAt.Before(old.row.VerifiedAt))
}
func (e *wirelessTagPruneExecutor) observe(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (any, bool, error) {
	if !e.selected(op) {
		return nil, false, errConfigurationOwned
	}
	ns, key, ids, _ := wirelessTagPruneParts(op.Resource)
	if err := e.proof(ctx, e.predecessor); err != nil {
		return nil, false, err
	}
	lineage, _ := wirelessTagLineageDigest(op.Resource)
	row, err := e.scopeLineage(ctx, ns, key, op.ParentID, op.TargetID, ids, e.latest[ns+key].row.Operation.Action != "Delete", wirelessTagBindingMode(op.Resource) != "", lineage)
	if err != nil {
		return nil, false, err
	}
	old := e.latest[ns+key]
	if old.row.Operation.Action == "Delete" {
		if wirelessTagCanonical(row.Resource) != wirelessTagCanonical(op.Resource) || row.Effect != key+":"+old.id+":"+old.mac {
			return nil, false, errConfigurationOwned
		}
	} else if row.Resource != op.Resource {
		return nil, false, errConfigurationStale
	}
	return []string{row.Effect}, row.Actual == nil, nil
}
func (e *wirelessTagPruneExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	var before any
	for i := 0; i < 2; i++ {
		result, done, err := e.observe(ctx, op)
		if err != nil || !done {
			return false, err
		}
		if i > 0 && !reflect.DeepEqual(before, result) {
			return false, errConfigurationStale
		}
		before = result
	}
	return true, nil
}
