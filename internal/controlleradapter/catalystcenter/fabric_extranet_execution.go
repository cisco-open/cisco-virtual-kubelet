// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type fabricExtranetSnapshot struct {
	desired  fabricExtranetPolicy
	policies map[string]fabricExtranetPolicy
	physical []Device
	digest   string
}

// Staged native preflight, not a dispatch permit. Include all VN-associated
// fabrics, explicit scopes and zone parents, rather than only provider borders.
// Group admission must reserve the resulting physical set before activation.
func (c *client) extranetSnapshot(ctx context.Context, want fabricExtranetIntent) (*fabricExtranetSnapshot, error) {
	executor := &fabricExtranetExecutor{client: c, desired: []fabricExtranetIntent{want}}
	bindings, err := executor.bindings(ctx)
	if err != nil {
		return nil, err
	}
	desired := bindings.policies[0]
	// An empty/null module scope has controller-dependent deployment semantics.
	// Report supports it, but execution must not infer a bounded footprint.
	if len(desired.Fabrics) == 0 {
		return nil, errConfigurationDeviceAdmission
	}
	scope := append([]string{}, desired.Fabrics...)
	for _, vn := range bindings.networks {
		if !vn.membershipsKnown {
			return nil, errInvalidResponse
		}
		scope = append(scope, vn.Fabrics...)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]Site{}
	for _, s := range sites {
		byID[s.ID] = s
	}
	fabrics, err := c.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	zones, err := c.fabricZones(ctx)
	if err != nil {
		return nil, err
	}
	fabricByID := map[string]fabricSite{}
	zoneByID := map[string]fabricZone{}
	for _, f := range fabrics {
		fabricByID[f.ID] = f
	}
	for _, z := range zones {
		zoneByID[z.ID] = z
	}
	parents := map[string]string{}
	for _, id := range append([]string{}, scope...) {
		if z, ok := zoneByID[id]; ok {
			if _, collision := fabricByID[id]; collision {
				return nil, errInvalidResponse
			}
			zoneSite, ok := byID[z.SiteID]
			if !ok {
				return nil, errConfigurationStale
			}
			nearest, parent := "", ""
			for _, f := range fabricByID {
				site, ok := byID[f.SiteID]
				if !ok {
					return nil, errConfigurationStale
				}
				if strings.HasPrefix(zoneSite.NameHierarchy, site.NameHierarchy+"/") && len(site.NameHierarchy) > len(nearest) {
					nearest, parent = site.NameHierarchy, f.ID
				}
			}
			if parent == "" {
				return nil, errConfigurationBusy
			}
			parents[id] = parent
			scope = append(scope, parent)
		} else if _, ok := fabricByID[id]; !ok {
			return nil, errConfigurationStale
		}
	}
	sort.Strings(scope)
	scope = slices.Compact(scope)
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	members := map[string][]fabricDeviceMembership{}
	selectedSites := map[string]Site{}
	selectedFabrics := map[string]fabricSite{}
	selectedZones := map[string]fabricZone{}
	ids := []string{}
	for _, id := range scope {
		siteID := ""
		if f, ok := fabricByID[id]; ok {
			selectedFabrics[id] = f
			siteID = f.SiteID
		} else {
			z := zoneByID[id]
			selectedZones[id] = z
			siteID = z.SiteID
		}
		site, ok := byID[siteID]
		if !ok {
			return nil, errConfigurationStale
		}
		selectedSites[siteID] = site
		membership, err := c.fabricDeviceMemberships(ctx, id)
		if err != nil {
			return nil, err
		}
		native, devices, err := bindFabricMutationTargets(id, membership, inventory)
		if err != nil {
			return nil, err
		}
		members[id] = native
		for _, d := range devices {
			ids = append(ids, d.ID)
		}
	}
	physical, err := bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, err
	}
	policies, err := c.fabricExtranets(ctx)
	if err != nil {
		return nil, err
	}
	// The new policy itself is an expected postcondition. Bind every other policy
	// so recovery cannot silently accept an unrelated policy/association change.
	peers := map[string]fabricExtranetPolicy{}
	for name, p := range policies {
		if name != desired.Name {
			peers[name] = p
		}
	}
	evidence := struct {
		Desired  fabricExtranetPolicy
		Networks map[string]fabricL3Network
		Sites    map[string]Site
		Fabrics  map[string]fabricSite
		Zones    map[string]fabricZone
		Parents  map[string]string
		Members  map[string][]fabricDeviceMembership
		Physical []Device
		Peers    map[string]fabricExtranetPolicy
	}{desired, bindings.networks, selectedSites, selectedFabrics, selectedZones, parents, members, physical, peers}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	return &fabricExtranetSnapshot{desired: desired, policies: policies, physical: physical, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}
func extranetPolicyMatches(want, got fabricExtranetPolicy) bool {
	return want.Name == got.Name && want.Provider == got.Provider && slices.Equal(want.Subscribers, got.Subscribers) && slices.Equal(want.Fabrics, got.Fabrics)
}
func (s *fabricExtranetSnapshot) resource() string { return "extranet-create:" + s.digest }

// Private native create primitive. Public Apply remains composition-gated.
// Existing mismatched policies require ownership/replacement, never adoption.
func (c *client) planExtranetCreate(ctx context.Context, want fabricExtranetIntent) (*configurationWrite, error) {
	before, err := c.extranetSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	if got, exists := before.policies[before.desired.Name]; exists {
		if !extranetPolicyMatches(before.desired, got) {
			return nil, errConfigurationOwned
		}
		return nil, nil
	}
	want.policy.Subscribers = append([]string{}, want.policy.Subscribers...)
	want.paths = append([]string{}, want.paths...)
	body, err := json.Marshal([]fabricExtranetPolicy{before.desired})
	if err != nil {
		return nil, err
	}
	return &configurationWrite{resource: before.resource(), action: "Create", submit: func(ctx context.Context) (Task, error) {
		fresh, err := c.extranetSnapshot(ctx, want)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != before.digest || !reflect.DeepEqual(fresh.policies, before.policies) {
			return Task{}, errConfigurationStale
		}
		raw, err := c.mutate(ctx, http.MethodPost, fabricExtranetPath, body)
		if err != nil {
			return Task{}, err
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: receipt.Response.ID}, nil
	}}, nil
}
func (c *client) verifiedExtranetSnapshot(ctx context.Context, want fabricExtranetIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (*fabricExtranetSnapshot, error) {
	if op == nil || op.Phase != "Verifying" || op.Action != "Create" || op.SubmittedAt == nil || op.TargetID != "" || op.ParentID != "" || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return nil, errConfigurationTransition
	}
	complete, err := c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !complete {
		return nil, err
	}
	snapshot, err := c.extranetSnapshot(ctx, want)
	if err != nil {
		return nil, err
	}
	if op.Resource != snapshot.resource() {
		return nil, errConfigurationStale
	}
	got, exists := snapshot.policies[snapshot.desired.Name]
	if !exists || !extranetPolicyMatches(snapshot.desired, got) {
		return nil, nil
	}
	return snapshot, nil
}

func (c *client) verifyExtranetCreate(ctx context.Context, want fabricExtranetIntent, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	snapshot, err := c.verifiedExtranetSnapshot(ctx, want, op, now)
	return snapshot != nil, err
}
