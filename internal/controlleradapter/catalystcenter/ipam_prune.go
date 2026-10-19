// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

var errIPAMPruneReferenced = errors.New("owned IPAM allocation is referenced or its dependency evidence is incomplete")

// Prune is deliberately scoped to IPAM resources. Other setting/credential
// executors must not silently retain omitted resources under a Delete policy.
func parseIPAMPruneIntent(raw []byte) (networkReportIntent, error) {
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return networkReportIntent{}, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return networkReportIntent{}, errConfigurationTransition
	}
	for key := range root {
		if key != "network_settings" && key != "sites" {
			return networkReportIntent{}, errConfigurationTransition
		}
	}
	settings, ok := root["network_settings"].(map[string]any)
	if !ok || len(settings) > 1 {
		return networkReportIntent{}, errConfigurationTransition
	}
	if len(settings) == 1 {
		if _, ok := settings["ip_pools"].([]any); !ok {
			return networkReportIntent{}, errConfigurationTransition
		}
	}
	intent, err := parseNetworkReportIntent(raw)
	if err != nil || len(intent.bindings) != 0 || len(intent.credentials) != 0 || len(intent.assignments) != 0 {
		return networkReportIntent{}, errConfigurationTransition
	}
	return intent, nil
}
func (a *adapter) compileIPAMPruneApply(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var zero configurationApplyPlan
	if len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "network_settings" || len(config.Spec.SecretRefs) != 0 || config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return zero, errConfigurationTransition
	}
	intent, err := parseIPAMPruneIntent(source.raw)
	if err != nil {
		return zero, err
	}
	if nc.Status.Configuration != nil {
		state := nc.Status.Configuration
		for _, r := range state.CredentialApplications {
			if r.ConfigUID == string(config.UID) {
				return zero, errConfigurationOwned
			}
		}
		for _, r := range state.ActionApplications {
			if r.Operation.ConfigUID == string(config.UID) && !strings.HasPrefix(r.Resource, ipamResultPrefix) {
				return zero, errConfigurationOwned
			}
		}
		// A former mixed owner must retain its other section's lifecycle explicitly.
		for _, owner := range state.Owners {
			if owner.ConfigUID == string(config.UID) && owner.Section != "network_settings" {
				return zero, errConfigurationOwned
			}
		}
	}
	e := &ipamConfigurationExecutor{client: a.client, intent: intent, prune: true}
	return configurationApplyPlan{executor: e, section: "network_settings", sections: []configapi.NetworkControllerSectionStatus{{Name: "network_settings", ResourceCount: int64(e.count())}}}, nil
}

type ipamPruneCandidate struct {
	Resource, ID, Parent, Name, Kind string
}
type ipamPruneSnapshot struct {
	Candidates   []ipamPruneCandidate
	Pools        []ipPool
	Reservations []ipReservation
	Sites        []Site
}

func (e *ipamConfigurationExecutor) pruningSnapshot(ctx context.Context) (*ipamPruneSnapshot, error) {
	pools, reservations, _, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, p := range e.intent.pools {
		wanted[ipamOwnedResource(ipamPoolIdentity(p.Name))] = true
	}
	for _, r := range e.intent.reservations {
		wanted[ipamOwnedResource(ipamReservationIdentity(r.site, r.Name))] = true
	}
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	bySite := map[string]Site{}
	for _, s := range sites {
		bySite[s.ID] = s
	}
	for _, r := range reservations {
		if _, ok := bySite[r.SiteID]; !ok {
			return nil, errConfigurationBusy
		}
	}
	snapshot := &ipamPruneSnapshot{Pools: pools, Reservations: reservations, Sites: sites}
	seen := map[string]bool{}
	candidateIDs := map[string]bool{}
	for _, receipt := range e.receipts {
		if !strings.HasPrefix(receipt.Resource, ipamResultPrefix) {
			continue
		}
		if receipt.Operation.ConfigUID != "" && receipt.Operation.ConfigUID != e.owner {
			continue
		}
		id, err := ipamReceiptTarget(receipt)
		if err != nil || receipt.Operation.ConfigUID != e.owner {
			return nil, errConfigurationOwned
		}
		resource := strings.TrimPrefix(receipt.Operation.Resource, "network_settings/")
		if seen[resource] {
			return nil, errConfigurationOwned
		}
		seen[resource] = true
		if wanted[resource] {
			continue
		}
		candidate := ipamPruneCandidate{Resource: resource, ID: id, Parent: receipt.Operation.ParentID}
		key, _ := ipamOwnedKey(resource)
		if strings.HasPrefix(key, "pool:") {
			candidate.Kind = "pool"
			for _, p := range pools {
				if p.ID == id {
					candidate.Name = p.Name
					if ipamOwnedResource(ipamPoolIdentity(p.Name)) != resource {
						return nil, errConfigurationOwned
					}
				}
			}
		} else {
			candidate.Kind = "reservation"
			for _, r := range reservations {
				if r.ID == id {
					site, ok := bySite[r.SiteID]
					if !ok || r.SiteID != candidate.Parent || ipamOwnedResource(ipamReservationIdentity(site.NameHierarchy, r.Name)) != resource {
						return nil, errConfigurationOwned
					}
					candidate.Name = r.Name
				}
			}
		}
		if receipt.Operation.Action == "Delete" {
			if candidate.Name != "" {
				return nil, errConfigurationOwned
			}
			continue // Verified absence is retained as a tombstone, never replayed.
		}
		if candidate.Name == "" {
			return nil, errConfigurationOwned
		} // External disappearance is not a verified CVK deletion.
		snapshot.Candidates = append(snapshot.Candidates, candidate)
		candidateIDs[id] = true
	}
	if len(snapshot.Candidates) == 0 {
		return snapshot, nil
	}
	for _, candidate := range snapshot.Candidates {
		if candidate.Kind == "pool" {
			for _, r := range reservations {
				for _, family := range []*poolAddressSpace{r.V4, r.V6} {
					if family != nil && family.GlobalPoolID == candidate.ID && !candidateIDs[r.ID] {
						return nil, errIPAMPruneReferenced
					}
				}
			}
		}
	}
	gateways, err := e.client.anycastGateways(ctx)
	if err != nil {
		return nil, err
	}
	sessions, err := e.client.lanSessions(ctx)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, gateway := range gateways {
		if gateway.unknownFields || !gateway.secondaryKnown {
			return nil, errIPAMPruneReferenced
		}
		used[gateway.Pool] = true
		if secondary, ok := gateway.fields["additionalIpPools"].([]map[string]any); ok {
			for _, p := range secondary {
				used[p["name"].(string)] = true
			}
		}
	}
	for _, session := range sessions {
		for _, pool := range session.Pools {
			used[pool.Name] = true
		}
	}
	for _, candidate := range snapshot.Candidates {
		if candidate.Kind == "reservation" && used[candidate.Name] {
			return nil, errIPAMPruneReferenced
		}
	}
	// Freeze the owned cohort and its parents, not unrelated controller churn.
	// Complete gateway/session inventories are checked again for every submission;
	// new relevant references refuse dispatch without treating unrelated changes as drift.
	keepPools, keepReservations, keepSites := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, candidate := range snapshot.Candidates {
		if candidate.Kind == "pool" {
			keepPools[candidate.ID] = true
		} else {
			keepReservations[candidate.ID] = true
			keepSites[candidate.Parent] = true
		}
	}
	for _, r := range reservations {
		if keepReservations[r.ID] {
			for _, space := range []*poolAddressSpace{r.V4, r.V6} {
				if space != nil {
					keepPools[space.GlobalPoolID] = true
				}
			}
		}
	}
	snapshot.Pools = nil
	snapshot.Reservations = nil
	snapshot.Sites = nil
	for _, p := range pools {
		if keepPools[p.ID] {
			snapshot.Pools = append(snapshot.Pools, p)
		}
	}
	for _, r := range reservations {
		if keepReservations[r.ID] {
			snapshot.Reservations = append(snapshot.Reservations, r)
		}
	}
	for _, site := range sites {
		if keepSites[site.ID] {
			snapshot.Sites = append(snapshot.Sites, Site{ID: site.ID, Name: site.Name, NameHierarchy: site.NameHierarchy, ParentID: site.ParentID, Type: site.Type})
		}
	}
	sort.Slice(snapshot.Pools, func(i, j int) bool { return snapshot.Pools[i].ID < snapshot.Pools[j].ID })
	sort.Slice(snapshot.Reservations, func(i, j int) bool { return snapshot.Reservations[i].ID < snapshot.Reservations[j].ID })
	sort.Slice(snapshot.Sites, func(i, j int) bool { return snapshot.Sites[i].ID < snapshot.Sites[j].ID })
	sort.Slice(snapshot.Candidates, func(i, j int) bool {
		a, b := snapshot.Candidates[i], snapshot.Candidates[j]
		if a.Kind != b.Kind {
			return a.Kind == "reservation"
		}
		return a.Resource < b.Resource
	})
	return snapshot, nil
}
func (e *ipamConfigurationExecutor) prunePlan(ctx context.Context) (*ipamWritePlan, error) {
	snapshot, err := e.pruningSnapshot(ctx)
	if err != nil || len(snapshot.Candidates) == 0 {
		return nil, err
	}
	selected := snapshot.Candidates[0]
	// Parent candidates remain deferred until every child reservation is absent.
	if selected.Kind == "pool" {
		for _, r := range snapshot.Reservations {
			for _, space := range []*poolAddressSpace{r.V4, r.V6} {
				if space != nil && space.GlobalPoolID == selected.ID {
					return nil, errIPAMPruneReferenced
				}
			}
		}
	}
	path := globalPoolsPath
	if selected.Kind == "reservation" {
		path = sitePoolsPath
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	write := &configurationWrite{resource: selected.Resource, action: "Delete", identity: configurationResourceIdentity{target: selected.ID, parent: selected.Parent}, submit: func(ctx context.Context) (Task, error) {
		return e.client.ipamMutationTask(ctx, http.MethodDelete, path+"/"+selected.ID, nil)
	}}
	return &ipamWritePlan{write: write, payload: string(raw)}, nil
}
func (e *ipamConfigurationExecutor) executionPlan(ctx context.Context) (*ipamWritePlan, error) {
	normal, err := e.plan(ctx)
	if err != nil {
		return nil, err
	}
	if e.prune {
		prune, err := e.prunePlan(ctx)
		if err != nil || prune != nil {
			return prune, err
		}
	}
	return normal, nil
}
func (e *ipamConfigurationExecutor) ownedDeleteReceipt(resource, id string) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	key, ok := ipamOwnedKey(resource)
	if !ok {
		return nil, errConfigurationTransition
	}
	var found *cvk.NetworkControllerConfigurationActionApplication
	for i := range e.receipts {
		r := &e.receipts[i]
		other, valid := ipamOwnedKey(r.Operation.Resource)
		if !valid || other != key {
			continue
		}
		target, err := ipamReceiptTarget(*r)
		if found != nil || err != nil || target != id || r.Operation.ConfigUID != e.owner || r.Operation.Action == "Delete" {
			return nil, errConfigurationOwned
		}
		found = r
	}
	if found == nil {
		return nil, errConfigurationOwned
	}
	return found, nil
}
func (e *ipamConfigurationExecutor) verifyDelete(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, bool, error) {
	if !e.prune || !validIPAMOwnedOperation(op) || op.Action != "Delete" || op.ConfigUID != e.owner {
		return "", false, errConfigurationTransition
	}
	if _, err := e.ownedDeleteReceipt(op.Resource, op.TargetID); err != nil {
		return "", false, err
	}
	done, err := e.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil || !done {
		return "", false, err
	}
	key, _ := ipamOwnedKey(op.Resource)
	for range 2 {
		pools, err := e.client.listIPPools(ctx)
		if err != nil {
			return "", false, err
		}
		reservations, err := e.client.listIPReservations(ctx)
		if err != nil {
			return "", false, err
		}
		sites, err := e.client.ListSites(ctx)
		if err != nil {
			return "", false, err
		}
		if err := validateIPAMAllocation(networkReportIntent{}, pools, reservations); err != nil {
			return "", false, err
		}
		sitePaths := map[string]string{}
		for _, s := range sites {
			sitePaths[s.ID] = s.NameHierarchy
		}
		for _, r := range reservations {
			if sitePaths[r.SiteID] == "" {
				return "", false, errConfigurationBusy
			}
		}
		if strings.HasPrefix(key, "pool:") {
			for _, p := range pools {
				if p.ID == op.TargetID {
					return "", false, nil
				}
				if ipamOwnedResource(ipamPoolIdentity(p.Name)) == strings.TrimPrefix(op.Resource, "network_settings/") {
					return "", false, errConfigurationOwned
				}
			}
		} else {
			for _, r := range reservations {
				if r.ID == op.TargetID {
					return "", false, nil
				}
				if ipamOwnedResource(ipamReservationIdentity(sitePaths[r.SiteID], r.Name)) == strings.TrimPrefix(op.Resource, "network_settings/") {
					return "", false, errConfigurationOwned
				}
			}
		}
	}
	return op.TargetID, true, nil
}
