//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type siteLifecycleCheckpointClient struct {
	ctrlclient.Client
	checkpoint func() error
}

func (c *siteLifecycleCheckpointClient) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.UpdateOption) error {
	if err := c.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return c.checkpoint()
}
func (c *siteLifecycleCheckpointClient) Status() ctrlclient.SubResourceWriter {
	return &siteLifecycleCheckpointWriter{SubResourceWriter: c.Client.Status(), checkpoint: c.checkpoint}
}

type siteLifecycleCheckpointWriter struct {
	ctrlclient.SubResourceWriter
	checkpoint func() error
}

func (w *siteLifecycleCheckpointWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return w.checkpoint()
}

// Test-only allowlist applies to every authenticated session. A failed mutation
// consumes its allowance too; a transport error is never permission to retry.
type siteLifecycleNativeTrace struct {
	mu             sync.Mutex
	base           http.RoundTripper
	name, parent   string
	posts, deletes int
	maxPosts       int
	allowedDeletes map[string]bool
	allowChild     func(map[string]any) bool
	allowCreate    func(string, map[string]any) bool
}

func (g *siteLifecycleNativeTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	var body map[string]any
	if req.Method == http.MethodPost && req.URL.Path != authPath {
		if req.GetBody == nil {
			return nil, errors.New("site lifecycle missing replay-free body inspection")
		}
		copy, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		err = json.NewDecoder(io.LimitReader(copy, 4096)).Decode(&body)
		copy.Close()
		if err != nil {
			return nil, err
		}
	}
	qualifiedCreate := g.allowCreate != nil && g.allowCreate(req.URL.Path, body)
	g.mu.Lock()
	base := g.base
	allowed := req.Method == http.MethodGet || req.URL.Path == authPath
	matched := body["name"] == g.name && body["parentId"] == g.parent
	if !matched && g.allowChild != nil {
		matched = g.allowChild(body)
	}
	if !allowed && req.Method == http.MethodPost && (req.URL.Path == siteDesignMutationEndpoint("area") && matched && len(body) == 2 || qualifiedCreate) && g.posts < g.maxPosts {
		g.posts++
		allowed = true
	}
	if !allowed && req.Method == http.MethodDelete && g.allowedDeletes[req.URL.Path] {
		delete(g.allowedDeletes, req.URL.Path)
		g.deletes++
		allowed = true
	}
	g.mu.Unlock()
	if !allowed {
		return nil, errors.New("site lifecycle refused mutation outside generated identity or allowance")
	}
	return base.RoundTrip(req)
}
func (g *siteLifecycleNativeTrace) permitDelete(id string) {
	g.permitKindDelete("area", id)
}
func (g *siteLifecycleNativeTrace) permitKindDelete(kind, id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.allowedDeletes[siteDesignMutationEndpoint(kind)+"/"+id] = true
}

// Native component qualification with simulated Kubernetes persistence and
// restart. This deliberately does not claim installed worker qualification.
func TestLiveCatalystCenterSiteRetirementRecreation(t *testing.T) {
	qualifyLiveSiteLifecycle(t, false)
}

func TestLiveCatalystCenterPublicSiteLifecycle(t *testing.T) {
	if os.Getenv("CATC_SITE_PUBLIC_LIFECYCLE") != "isolated-area-tree" {
		t.Skip("explicit public site lifecycle qualification not enabled")
	}
	t.Setenv("CATC_SITE_LIFECYCLE", "isolated-parent-handoff")
	t.Setenv("CATC_SITE_TASK_RECOVERY", "isolated-acknowledged-tasks")
	qualifyLiveSiteLifecycle(t, true)
}

func TestLiveCatalystCenterPublicHierarchyLifecycle(t *testing.T) {
	if os.Getenv("CATC_SITE_PUBLIC_HIERARCHY") != "isolated-area-building-floor" {
		t.Skip("explicit generated hierarchy lifecycle qualification not enabled")
	}
	t.Setenv("CATC_SITE_LIFECYCLE", "isolated-parent-handoff")
	t.Setenv("CATC_SITE_TASK_RECOVERY", "isolated-acknowledged-tasks")
	qualifyLiveSiteLifecycle(t, true)
}

func TestLiveCatalystCenterPublicSiteDefaultsLifecycle(t *testing.T) {
	if os.Getenv("CATC_SITE_DEFAULTS_LIFECYCLE") != "isolated-area-building-floor" {
		t.Skip("explicit site defaults qualification disabled")
	}
	t.Setenv("CATC_SITE_PUBLIC_HIERARCHY", "isolated-area-building-floor")
	t.Setenv("CATC_SITE_LIFECYCLE", "isolated-parent-handoff")
	t.Setenv("CATC_SITE_TASK_RECOVERY", "isolated-acknowledged-tasks")
	qualifyLiveSiteLifecycle(t, true)
}

func qualifyLiveSiteLifecycle(t *testing.T, public bool) {
	t.Helper()
	hierarchy := public && os.Getenv("CATC_SITE_PUBLIC_HIERARCHY") == "isolated-area-building-floor"
	handoff := os.Getenv("CATC_SITE_LIFECYCLE") == "isolated-parent-handoff"
	tree := os.Getenv("CATC_SITE_LIFECYCLE") == "isolated-area-tree" || handoff
	if os.Getenv("CATC_SITE_LIFECYCLE") != "isolated-area" && !tree {
		t.Skip("explicit generated-area lifecycle qualification not enabled")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	baseline, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := "CVK-Q-Life-" + time.Now().UTC().Format("0102-150405")
	path := "Global/" + name
	parent := ""
	for _, s := range baseline {
		if s.NameHierarchy == path || strings.HasPrefix(s.NameHierarchy, path+"/") {
			t.Fatal("generated identity exists")
		}
		if s.NameHierarchy == "Global" && s.Type == "global" {
			parent = s.ID
		}
	}
	if !validAPIID(parent) {
		t.Fatal("Global parent unavailable")
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-site-lifecycle-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private generated-site recovery checkpoint:", dir)
	desired := []desiredSite{{kind: "area", path: path, fields: map[string]any{}}}
	if hierarchy {
		desired = append(desired,
			desiredSite{kind: "building", path: path + "/LifecycleBuilding", fields: map[string]any{"country": "United States", "latitude": 37.33812, "longitude": -121.88612}},
			desiredSite{kind: "floor", path: path + "/LifecycleBuilding/LifecycleFloor", fields: map[string]any{"floor_number": float64(1), "rf_model": "Free Space", "width": float64(10), "length": float64(15), "height": float64(3), "units_of_measure": "meters"}})
	} else if tree {
		desired = append(desired, desiredSite{kind: "area", path: path + "/LifecycleChild", fields: map[string]any{}})
	}
	siteDefaults := hierarchy && os.Getenv("CATC_SITE_DEFAULTS_LIFECYCLE") == "isolated-area-building-floor"
	if siteDefaults {
		raw, err := siteManifestModel(desired)
		if err != nil {
			t.Fatal(err)
		}
		desired, err = parseSites(siteDefaultResolvedFixture(t, raw))
		if err != nil {
			t.Fatal(err)
		}
	}
	h := newLiveSiteManifest(t, c, desired)
	if siteDefaults {
		h.resolveSource = func(raw []byte) []byte { return siteDefaultResolvedFixture(t, raw) }
	}
	h.singular = true
	keyPath, key := configureSiteNativeComparisonFixture(t, h.a, h.config)
	rel, err := filepath.Rel(h.a.intentSecretPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	h.a.intentSecretPath = filepath.Join(dir, "projection")
	keyPath = filepath.Join(h.a.intentSecretPath, rel)
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		t.Fatal(err)
	}
	// This is a newly generated test-only key, never a lab API credential or a
	// key copied from the installed cluster's Kubernetes Secret.
	if err := os.WriteFile(keyPath, key, 0600); err != nil {
		t.Fatal(err)
	}
	h.update(t, ctx, desired)
	if public {
		h.config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		h.config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
		if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
			t.Fatal(err)
		}
	}
	underlying := h.a.statusWriter
	writeJSON := func(name string, value any) error {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		tmp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(tmp, raw, 0600); err != nil {
			return err
		}
		return os.Rename(tmp, filepath.Join(dir, name))
	}
	checkpoint := func() error {
		var nc cvk.NetworkController
		var config configapi.NetworkControllerConfig
		if err := underlying.Get(ctx, h.a.key, &nc); err != nil {
			return err
		}
		if err := underlying.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), &config); err != nil {
			return err
		}
		return writeJSON("checkpoint.json", struct {
			Controller           cvk.NetworkController             `json:"controller"`
			Config               configapi.NetworkControllerConfig `json:"config"`
			Path, ProjectionRoot string
		}{nc, config, path, h.a.intentSecretPath})
	}
	h.a.statusWriter = &siteLifecycleCheckpointClient{Client: underlying, checkpoint: checkpoint}
	if err := checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON("baseline-sites.json", baseline); err != nil {
		t.Fatal(err)
	}
	maxPosts := 2
	if handoff {
		maxPosts = 4
	}
	if hierarchy {
		maxPosts = 2 * len(desired)
	}
	trace := &siteLifecycleNativeTrace{name: name, parent: parent, allowedDeletes: map[string]bool{}, maxPosts: maxPosts}
	if tree && !hierarchy {
		trace.allowChild = func(body map[string]any) bool {
			if body["name"] != "LifecycleChild" {
				return false
			}
			nc, err := h.a.currentController(ctx)
			if err != nil {
				return false
			}
			records, err := siteCreatedRecords(nc.Status.Configuration)
			if err != nil {
				return false
			}
			parent, found := siteCreatorForPath(records, path)
			return found && body["parentId"] == parent.id
		}
	}
	if hierarchy {
		trace.allowCreate = func(endpoint string, body map[string]any) bool {
			for _, want := range desired {
				if endpoint != siteDesignMutationEndpoint(want.kind) || body["name"] != lastSiteName(want.path) {
					continue
				}
				parentID := parent
				if parentSitePath(want.path) != "Global" {
					nc, err := h.a.currentController(ctx)
					if err != nil {
						return false
					}
					records, err := siteCreatedRecords(nc.Status.Configuration)
					if err != nil {
						return false
					}
					creator, found := siteCreatorForPath(records, parentSitePath(want.path))
					if !found {
						return false
					}
					parentID = creator.id
				}
				// Exact independent allowlist, including geometry; no PUT or foreign site.
				expected := map[string]any{"name": lastSiteName(want.path), "parentId": parentID}
				keys := map[string]string{"country": "country", "latitude": "latitude", "longitude": "longitude", "floor_number": "floorNumber", "rf_model": "rfModel", "width": "width", "length": "length", "height": "height", "units_of_measure": "unitsOfMeasure"}
				for key, value := range want.fields {
					expected[keys[key]] = value
				}
				return reflect.DeepEqual(expected, body)
			}
			return false
		}
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	previous := c.wrapTransport
	wrap := func(base http.RoundTripper) http.RoundTripper {
		if previous != nil {
			base = previous(base)
		}
		if base == nil {
			base = http.DefaultTransport
		}
		trace.mu.Lock()
		trace.base = base
		trace.mu.Unlock()
		return trace
	}
	rest.HTTPClient.Transport = wrap(rest.HTTPClient.Transport)
	c.wrapTransport = wrap
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.wrapTransport = previous
		if c.rest != nil {
			trace.mu.Lock()
			c.rest.HTTPClient.Transport = trace.base
			trace.mu.Unlock()
		}
	}()
	// No deferred native cleanup. A failure leaves its exact operation, task,
	// generated projection and source checkpoint for supervised recovery.
	paths := make([]string, len(desired))
	for i, want := range desired {
		paths[i] = want.path
	}
	h.runCreates(t, ctx, paths)
	creator := func(at string) siteCreatorRecord {
		records, err := siteCreatedRecords(journalState(t, h.a))
		if err != nil || len(records) != len(desired) {
			t.Fatal("native creator lineage unavailable", err)
		}
		if r, found := siteCreatorForPath(records, at); found {
			return r
		}
		t.Fatal("missing creator")
		return siteCreatorRecord{}
	}
	first := creator(path)
	trace.permitDelete(first.id)
	t.Log("original area creator verified", first.id, first.receipt.Operation.TaskID)
	retire := func(id string) {
		h.update(t, ctx, nil)
		h.config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		h.config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
		if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
			t.Fatal(err)
		}
		source, err := loadControllerSource(ctx, h.a.statusReader, h.config)
		if err != nil {
			t.Fatal(err)
		}
		nc, err := h.a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		write, err := h.a.siteRetirementWrite(ctx, nc, h.config, source, id)
		if err != nil {
			t.Fatal("native retirement planning refused; retain checkpoint", err)
		}
		if err := h.a.submitConfigurationWrite(ctx, h.config, source, "sites", write); err != nil {
			t.Fatal("native retirement uncertain; retain original operation", err)
		}
		op := journalState(t, h.a).Operation
		if op == nil || op.Phase != "Accepted" || !validAPIID(op.TaskID) {
			t.Fatal("retirement acknowledgement unavailable")
		}
		if err := h.a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
			t.Fatal(err)
		}
		t.Log("retirement acknowledged; restarting observation of original task", op.TaskID)
		restarted := *h.a
		for {
			err := restarted.recordSiteRetired(ctx, h.config, source, op.ID)
			if err == nil {
				break
			}
			if !errors.Is(err, errConfigurationBusy) {
				t.Fatal("native retirement verification refused; retain checkpoint", err)
			}
			select {
			case <-ctx.Done():
				t.Fatal("retirement wait expired; retain checkpoint")
			case <-time.After(2 * time.Second):
			}
		}
		if err := restarted.finishConfiguration(ctx, op.ID); err != nil {
			t.Fatal(err)
		}
		t.Log("original retirement task and absence atomically verified", op.TaskID)
	}
	finish := func(firstID, secondID string, archives int) {
		final, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		order := func(rows []Site) { sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID }) }
		order(baseline)
		order(final)
		if !reflect.DeepEqual(baseline, final) {
			t.Fatal("typed site inventory baseline changed; retain evidence")
		}
		trace.mu.Lock()
		posts, deletes := trace.posts, trace.deletes
		trace.mu.Unlock()
		if posts != maxPosts || deletes != maxPosts || journalState(t, h.a).Operation != nil {
			t.Fatal("native lifecycle mutation count or terminal journal mismatch")
		}
		if err := writeJSON("result.json", map[string]any{"posts": posts, "deletes": deletes, "baselineRestored": true, "firstID": firstID, "secondID": secondID, "archivedCohorts": archives, "installed": false, "publicRetirementRouting": public, "parentChild": tree, "parentHandoff": handoff, "areaBuildingFloor": hierarchy, "siteDefaultsExport": siteDefaults, "defaultedFieldsPerHierarchy": func() int {
			if siteDefaults {
				return 5
			}
			return 0
		}(), "originalTaskRecoveries": len(h.recovered)}); err != nil {
			t.Fatal(err)
		}
		t.Log(fmt.Sprintf("native generated-site lifecycle passed: %d Creates, %d Deletes; original tasks, retained history and typed baseline verified; parent/child=%t; simulated Kubernetes journal", posts, deletes, tree))
	}
	recreate := func(at string) siteCreatorRecord {
		source, err := loadControllerSource(ctx, h.a.statusReader, h.config)
		if err != nil {
			t.Fatal(err)
		}
		nc, err := h.a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, write, err := h.a.siteRecreationExecution(ctx, nc, h.config, source, at)
		if err != nil || write == nil {
			t.Fatal("native recreation planning refused", err)
		}
		if err := h.a.submitConfigurationWrite(ctx, h.config, source, "sites", write); err != nil {
			t.Fatal("native recreation uncertain; retain checkpoint", err)
		}
		op := journalState(t, h.a).Operation
		if op == nil || op.Phase != "Accepted" || !validAPIID(op.TaskID) {
			t.Fatal("recreation acknowledgement unavailable")
		}
		if err := h.a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
			t.Fatal(err)
		}
		t.Log("recreation acknowledged; restarting observation of original task", op.TaskID)
		for {
			current, err := h.a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			restarted := *h.a
			e, retry, err := restarted.siteRecreationExecution(ctx, current, h.config, source, at)
			if err != nil || retry != nil || e == nil {
				if retry == nil && (errors.Is(err, errConfigurationStale) || errors.Is(err, errConfigurationBusy)) {
					// A Create can become visible between the typed and native
					// inventory reads. Reobserve only under the original task and
					// unchanged source; never reconstruct a submission writer.
					if current.Status.Configuration.Operation.ID != op.ID || current.Status.Configuration.Operation.TaskID != op.TaskID || h.a.checkConfigurationSnapshot(ctx, h.config, source) != nil {
						t.Fatal("retained recovery binding changed")
					}
					select {
					case <-ctx.Done():
						t.Fatal("consistent recreation observation wait expired; retain checkpoint")
					case <-time.After(2 * time.Second):
					}
					continue
				}
				t.Fatal("native recreation recovery refused; retain original task", err)
			}
			done, err := c.siteTaskCompletion(e.desired)(ctx, current.Status.Configuration.Operation, time.Now())
			if err != nil {
				t.Fatal("native recreation task failed; retain checkpoint", err)
			}
			if done {
				if current.Status.Configuration.Operation.Phase != "Verifying" {
					if err := h.a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
						t.Fatal(err)
					}
				}
				current, err = h.a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := restarted.recordSiteCreated(ctx, current, h.config, source, e, current.Status.Configuration.Operation); err != nil {
					if (errors.Is(err, errConfigurationBusy) || errors.Is(err, errConfigurationStale)) && h.a.checkConfigurationSnapshot(ctx, h.config, source) == nil {
						select {
						case <-ctx.Done():
							t.Fatal("original task readback wait expired; retain checkpoint")
						case <-time.After(2 * time.Second):
						}
						continue
					}
					t.Fatal("native recreation publication refused; retain checkpoint", err)
				}
				if err := restarted.finishConfiguration(ctx, op.ID); err != nil {
					t.Fatal(err)
				}
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("recreation wait expired; retain checkpoint")
			case <-time.After(2 * time.Second):
			}
		}
		return creator(at)
	}
	if public {
		originals := make([]siteCreatorRecord, len(desired))
		deletes := make([]string, len(desired))
		deleteActions := make([]string, len(desired))
		createActions := make([]string, len(desired))
		for i, want := range desired {
			originals[i] = creator(want.path)
			trace.permitKindDelete(want.kind, originals[i].id)
			deletes[len(desired)-1-i] = want.path
			deleteActions[i] = "Delete"
			createActions[i] = "Create"
		}
		h.update(t, ctx, nil)
		h.runSiteActions(t, ctx, deletes, deleteActions)
		h.update(t, ctx, desired)
		h.runSiteActions(t, ctx, paths, createActions)
		replacements := make([]siteCreatorRecord, len(desired))
		for i, want := range desired {
			replacements[i] = creator(want.path)
			if replacements[i].id == originals[i].id {
				t.Fatal("public manifest reused retired native identity")
			}
			if i > 0 && (replacements[i].receipt.Operation.ParentID != replacements[i-1].id || !strings.HasPrefix(replacements[i].receipt.Operation.Resource, siteRecreateHandoffPrefix)) {
				t.Fatal("public manifest did not authenticate parent handoff")
			}
			trace.permitKindDelete(want.kind, replacements[i].id)
		}
		h.update(t, ctx, nil)
		h.runSiteActions(t, ctx, deletes, deleteActions)
		archives, err := siteHistoryCohorts(journalState(t, h.a))
		if err != nil || len(archives) != len(desired) || len(journalState(t, h.a).ActionApplications) != 4*len(desired) {
			t.Fatal("public lifecycle history incomplete", err)
		}
		finish(replacements[0].id, replacements[len(replacements)-1].id, len(archives))
		return
	}

	if tree {
		child := creator(path + "/LifecycleChild")
		trace.permitDelete(child.id)
		retire(child.id)
		retire(first.id)
		state := journalState(t, h.a)
		retired, err := siteRetiredRecords(state)
		if err != nil || len(retired) != 2 || len(state.ActionApplications) != 4 {
			t.Fatal("native parent/child retirement lost receipts", err)
		}
		if handoff {
			h.update(t, ctx, desired)
			nextParent := recreate(path)
			nextChild := recreate(path + "/LifecycleChild")
			if nextParent.id == first.id || nextChild.id == child.id || nextChild.receipt.Operation.ParentID != nextParent.id || !strings.HasPrefix(nextChild.receipt.Operation.Resource, siteRecreateHandoffPrefix) {
				t.Fatal("native child did not authenticate the replacement parent")
			}
			trace.permitDelete(nextChild.id)
			trace.permitDelete(nextParent.id)
			retire(nextChild.id)
			retire(nextParent.id)
			archives, err := siteHistoryCohorts(journalState(t, h.a))
			if err != nil || len(archives) != 2 || len(journalState(t, h.a).ActionApplications) != 8 {
				t.Fatal("native parent handoff history lost", err)
			}
			finish(nextParent.id, nextChild.id, len(archives))
			return
		}
		finish(first.id, child.id, 0)
		return
	}
	retire(first.id)
	h.update(t, ctx, desired)
	second := recreate(path)
	if second.id == first.id || second.receipt.Operation.TaskID == first.receipt.Operation.TaskID {
		t.Fatal("recreation reused original native identity/task")
	}
	archives, err := siteHistoryCohorts(journalState(t, h.a))
	if err != nil || len(archives) != 1 {
		t.Fatal("native lifecycle archive incomplete", err)
	}
	trace.permitDelete(second.id)
	t.Log("new native creator and complete predecessor archive verified", second.id)
	retire(second.id)
	finish(first.id, second.id, len(archives))
}
