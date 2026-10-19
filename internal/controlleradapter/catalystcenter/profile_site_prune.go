// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

var errProfileSitePruneReferenced = errors.New("profile/site removal references are present or unavailable")

func profileSiteRemovalSelected(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	for _, ns := range []string{"sites/", "wireless-sites/"} {
		if local, ok := strings.CutPrefix(op.Resource, ns); ok && !strings.Contains(local, "/") && strings.HasPrefix(local, profileSiteRemoveFamily) {
			return true
		}
	}
	return false
}

// One bounded observation shares existing paginated readers across all retained
// pairs. Recovery creates a fresh reader for only the acknowledged pair.
type profileSitePruneReader struct {
	c             *client
	sites         map[string]Site
	profiles      map[string]map[string]string // namespace -> UUID -> name
	assigned      map[string]map[string]bool
	tags          map[string][]wirelessProfileTagMetadata
	empty         map[string]bool
	pnp           []pnpDevice
	pnpLoaded     bool
	projectedTags profileSiteTagProjection
}
type profileSitePruneObservation struct {
	Namespace, Key, ProfileID, SiteID, Name, Path string
	Assigned                                      map[string]bool
}

func (c *client) profileSitePruneReader() *profileSitePruneReader {
	return &profileSitePruneReader{c: c, profiles: map[string]map[string]string{}, assigned: map[string]map[string]bool{}, tags: map[string][]wirelessProfileTagMetadata{}, empty: map[string]bool{}}
}
func (r *profileSitePruneReader) observe(ctx context.Context, ns, key string, row cvk.NetworkControllerConfigurationActionApplication, references bool) (profileSitePruneObservation, error) {
	out := profileSitePruneObservation{Namespace: ns, Key: key, ProfileID: row.Operation.ParentID, SiteID: row.Operation.TargetID}
	if err := r.loadSites(ctx); err != nil {
		return out, err
	}
	profiles, ok := r.profiles[ns]
	if !ok {
		profiles = map[string]string{}
		names := map[string]bool{}
		put := func(id, name string) error {
			if !validAPIID(id) || !validProfileName(name) || profiles[id] != "" || names[name] {
				return errInvalidResponse
			}
			profiles[id] = name
			names[name] = true
			return nil
		}
		if ns == "sites/" {
			rows, err := r.c.listSwitchingProfiles(ctx)
			if err != nil {
				return out, err
			}
			for _, p := range rows {
				if err := put(p.ID, p.Name); err != nil {
					return out, err
				}
			}
		} else if ns == "wireless-sites/" {
			rows, err := r.c.wirelessProfileInventory(ctx)
			if err != nil {
				return out, err
			}
			for _, p := range rows {
				if err := put(p.id, p.name); err != nil {
					return out, err
				}
			}
		} else {
			return out, errConfigurationTransition
		}
		r.profiles[ns] = profiles
	}
	out.Name = profiles[out.ProfileID]
	out.Path = r.sites[out.SiteID].NameHierarchy
	if out.Name == "" || out.Path == "" || out.Path == "Global" || profileSiteDigest(tagMembershipIdentity(out.Path, out.Name)) != key {
		return out, errProfileSiteApplications
	}
	assigned, ok := r.assigned[out.ProfileID]
	if !ok {
		var err error
		assigned, err = r.c.profileAssignedSites(ctx, out.ProfileID)
		if err != nil {
			return out, err
		}
		r.assigned[out.ProfileID] = assigned
	}
	out.Assigned = assigned
	if !references {
		return out, nil
	}
	if err := r.unusedScope(ctx, out.Path); err != nil {
		return out, err
	}
	if ns == "wireless-sites/" {
		tags, ok := r.tags[out.ProfileID]
		if !ok {
			for _, policy := range []bool{false, true} {
				rows, err := r.c.wirelessProfileTagInventory(ctx, out.ProfileID, policy)
				if err != nil {
					return out, errProfileSitePruneReferenced
				}
				for _, tag := range rows {
					tags = append(tags, tag)
				}
			}
			r.tags[out.ProfileID] = tags
		}
		for _, tag := range tags {
			if tag.unmapped {
				return out, errProfileSitePruneReferenced
			}
			fields := tag.fields
			if projected, exists := r.projectedTags[profileSiteTagIdentity(tag.profileID, tag.id, tag.fields["policyTagName"] != nil)]; exists {
				if projected.actual == nil || !reflect.DeepEqual(*projected.actual, tag) {
					return out, errConfigurationStale
				}
				fields = projected.payload
			}
			ids, ok := fields["siteIds"].([]string)
			if !ok {
				return out, errProfileSitePruneReferenced
			}
			for _, id := range ids {
				path := r.sites[id].NameHierarchy
				if path == "" || path == out.Path || strings.HasPrefix(path, out.Path+"/") || strings.HasPrefix(out.Path, path+"/") {
					return out, errProfileSitePruneReferenced
				}
			}
		}
	}
	return out, nil
}

func (r *profileSitePruneReader) loadSites(ctx context.Context) error {
	if r.sites == nil {
		sites, err := r.c.ListSites(ctx)
		if err != nil {
			return err
		}
		r.sites = map[string]Site{}
		paths := map[string]bool{}
		for _, s := range sites {
			if !validAPIID(s.ID) || !validSitePath(s.NameHierarchy) || paths[s.NameHierarchy] {
				return errInvalidResponse
			}
			if _, ok := r.sites[s.ID]; ok {
				return errInvalidResponse
			}
			paths[s.NameHierarchy] = true
			r.sites[s.ID] = s
		}
	}
	return nil
}

// unusedScope is shared by association and tag-membership removal. Callers
// supply a complete, validated site inventory; unknown PnP scope fails closed.
func (r *profileSitePruneReader) unusedScope(ctx context.Context, scope string) error {
	if !validSitePath(scope) || scope == "Global" || len(r.sites) == 0 {
		return errProfileSitePruneReferenced
	}
	// An assignment is not a physical deployment. Nevertheless, refuse removing
	// its inherited metadata while any device in the selected subtree consumes it.
	for id, site := range r.sites {
		if site.NameHierarchy != scope && !strings.HasPrefix(site.NameHierarchy, scope+"/") {
			continue
		}
		if !r.empty[id] {
			devices, err := r.c.assignedDevices(ctx, id)
			if err != nil || len(devices) != 0 {
				return errProfileSitePruneReferenced
			}
			r.empty[id] = true
		}
	}
	if !r.pnpLoaded {
		rows, err := r.c.pnpDevices(ctx)
		if err != nil {
			return errProfileSitePruneReferenced
		}
		r.pnp = rows
		r.pnpLoaded = true
	}
	for _, device := range r.pnp {
		info := device.Info
		id := info.SiteID
		if id == "" {
			id = info.Location.SiteID
		}
		if id == "" {
			// Only a demonstrably unbound, uncontacted registration has no
			// current site consumer. Unknown/active workflows lack that proof.
			if info.State != "Unclaimed" || info.OnboardingState != "Not Contacted" || info.WorkflowID != "" || device.Workflow != nil || device.SystemWorkflow != nil || device.ResetWorkflow != nil {
				return errProfileSitePruneReferenced
			}
			continue
		}
		path := r.sites[id].NameHierarchy
		if path == "" || path == scope || strings.HasPrefix(path, scope+"/") || strings.HasPrefix(scope, path+"/") {
			return errProfileSitePruneReferenced
		}
	}
	return nil
}

type profileSitePruneExecutor struct {
	base        *networkProfileConfiguration
	client      *client
	rows        map[string]cvk.NetworkControllerConfigurationActionApplication
	wanted      map[string]bool
	enabled     bool
	predecessor string
	proof       func(context.Context, string) error
}

func (e *profileSitePruneExecutor) count() int { return e.base.count() }
func (e *profileSitePruneExecutor) snapshot(ctx context.Context) ([]profileSitePruneObservation, error) {
	return e.snapshotWithTags(ctx, nil)
}
func (e *profileSitePruneExecutor) snapshotWithTags(ctx context.Context, projected profileSiteTagProjection) ([]profileSitePruneObservation, error) {
	out := []profileSitePruneObservation{}
	reader := e.baseClient().profileSitePruneReader()
	reader.projectedTags = projected
	keys := []string{}
	for key := range e.rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, identity := range keys {
		if e.wanted[identity] {
			continue
		}
		row := e.rows[identity]
		ns, key, _, _ := profileSiteApplicationParts(row.Operation.Resource)
		observed, err := reader.observe(ctx, ns, key, row, row.Operation.Action == "Update")
		if err != nil {
			return nil, err
		}
		if row.Operation.Action == "Delete" {
			if observed.Assigned[observed.SiteID] {
				return nil, errProfileSiteApplications
			}
			continue
		}
		// External disappearance is not proof that the original deletion ran.
		if !observed.Assigned[observed.SiteID] {
			return nil, errProfileSiteApplications
		}
		out = append(out, observed)
	}
	return out, nil
}
func (e *profileSitePruneExecutor) baseClient() *client { return e.client }
func (e *profileSitePruneExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if !e.enabled {
		return e.base.next(ctx)
	}
	observed, err := e.snapshot(ctx)
	if err != nil {
		if errors.Is(err, errProfileSitePruneReferenced) {
			return e.tagDependencyWrite(ctx)
		}
		return nil, err
	}
	// Check the complete ordinary source too; deletion must not mask invalid or
	// unavailable future profile/tag intent. Additive work remains first.
	next, err := e.base.next(ctx)
	if err != nil {
		return nil, err
	}
	if next != nil {
		return next, nil
	}
	if len(observed) == 0 {
		return nil, nil
	}
	pair := observed[0]
	write := &configurationWrite{resource: pair.Namespace + profileSiteRemovePrefix + pair.Key + ":" + e.predecessor, action: "Delete", identity: configurationResourceIdentity{parent: pair.ProfileID, target: pair.SiteID}}
	write.submit = func(ctx context.Context) (Task, error) {
		raw, err := e.baseClient().mutate(ctx, http.MethodDelete, profileSiteAssignmentsPath(pair.ProfileID)+"/"+url.PathEscape(pair.SiteID), nil)
		if err != nil {
			return Task{}, err
		}
		var result struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &result) != nil || !validAPIID(result.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: result.Response.ID}, nil
	}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		for range 2 {
			fresh, err := e.snapshot(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(fresh, observed) {
				return errConfigurationStale
			}
		}
		return e.proof(ctx, e.predecessor)
	})
	return write, nil
}
func (e *profileSitePruneExecutor) selected(op *cvk.NetworkControllerConfigurationOperation) bool {
	if !profileSiteRemovalSelected(op) || op.Action != "Delete" {
		return false
	}
	ns, key, pred, ok := profileSiteApplicationParts(op.Resource)
	row, exists := e.rows[ns+key]
	return ok && exists && !e.wanted[ns+key] && row.Operation.Action == "Update" && pred == e.predecessor && op.ParentID == row.Operation.ParentID && op.TargetID == row.Operation.TargetID && op.ConfigName == row.Operation.ConfigName && op.ConfigUID == row.Operation.ConfigUID && op.ConfigGeneration >= row.Operation.ConfigGeneration && op.ControllerGeneration >= row.Operation.ControllerGeneration && (op.SubmittedAt == nil || !op.SubmittedAt.Before(row.VerifiedAt))
}
func (e *profileSitePruneExecutor) observe(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (any, bool, error) {
	if !e.selected(op) {
		return nil, false, errProfileSiteApplications
	}
	ns, key, _, _ := profileSiteApplicationParts(op.Resource)
	row := e.rows[ns+key]
	observed, err := e.baseClient().profileSitePruneReader().observe(ctx, ns, key, row, true)
	if err != nil {
		return nil, false, err
	}
	return observed, !observed.Assigned[op.TargetID], nil
}
func (e *profileSitePruneExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if !profileSiteRemovalSelected(op) {
		return e.base.verify(ctx, op)
	}
	var before any
	for i := 0; i < 2; i++ {
		observed, done, err := e.observe(ctx, op)
		if err != nil || !done {
			return false, err
		}
		if i > 0 && !reflect.DeepEqual(before, observed) {
			return false, errConfigurationStale
		}
		before = observed
	}
	return true, nil
}

func (a *adapter) bindProfileSitePrune(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if plan == nil || plan.section != "network_profiles" {
		return nil
	}
	active := nc.Status.Configuration != nil && profileSiteRemovalSelected(nc.Status.Configuration.Operation)
	if config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete && !active {
		return nil
	}
	base, ok := plan.executor.(*networkProfileConfiguration)
	if !ok || len(config.Spec.ManagedSections) != 1 || len(config.Spec.SecretRefs) != 0 && !wirelessTagComparisonConfig(config) || config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return errConfigurationTransition
	}
	rows, err := profileSiteApplicationRecords(nc.Status.Configuration)
	if err != nil {
		return err
	}
	e := &profileSitePruneExecutor{base: base, client: a.client, rows: rows, wanted: map[string]bool{}, enabled: config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete, predecessor: profileSiteBookkeepingDigest(nc.Status.Configuration, "")}
	for _, part := range base.parts {
		if child, ok := part.executor.(*profileSiteExecutor); ok {
			for _, p := range child.desired {
				for _, path := range p.sites {
					e.wanted[part.name+"/"+profileSiteDigest(tagMembershipIdentity(path, p.name))] = true
				}
			}
		}
	}
	e.proof = func(ctx context.Context, pred string) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if err := configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return err
		}
		if _, err := profileSiteApplicationRecords(current.Status.Configuration); err != nil {
			return err
		}
		if profileSiteBookkeepingDigest(current.Status.Configuration, "") != pred {
			return errProfileSiteApplications
		}
		return nil
	}
	prior := *plan
	plan.executor = e
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return profileSiteRemovalSelected(op) || prior.recordCompletion != nil && (prior.receiptOperation == nil || prior.receiptOperation(op))
	}
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if profileSiteRemovalSelected(op) {
			return networkProfileMetadataEnvelope(op) && e.selected(op) && op.Phase == "Unknown"
		}
		return prior.canObserveUnknownTask(op)
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if profileSiteRemovalSelected(op) {
			if !e.selected(op) {
				return false, errProfileSiteApplications
			}
			return a.observeOwnedProfileMetadataTask(ctx, config, source, op, now, e.observe)
		}
		if prior.observeCompletion != nil {
			return prior.observeCompletion(ctx, op, now)
		}
		return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if profileSiteRemovalSelected(op) {
			if !e.selected(op) {
				return errProfileSiteApplications
			}
			return a.recordProfileSiteApplication(ctx, config, source, e.observe, op, rows)
		}
		if prior.recordCompletion != nil {
			return prior.recordCompletion(ctx, op)
		}
		return errConfigurationTransition
	}
	return nil
}
