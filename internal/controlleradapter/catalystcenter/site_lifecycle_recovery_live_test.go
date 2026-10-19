//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type siteLifecycleCheckpoint struct {
	Controller           cvk.NetworkController             `json:"controller"`
	Config               configapi.NetworkControllerConfig `json:"config"`
	Path, ProjectionRoot string
}

type siteRetainedReadOnlyTransport struct{ base http.RoundTripper }

func (t *siteRetainedReadOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && !(req.Method == http.MethodPost && req.URL.Path == authPath) {
		return nil, errors.New("retained lifecycle diagnostic refuses mutations")
	}
	return t.base.RoundTrip(req)
}

func TestLiveCatalystCenterRetainedSiteLifecycleDiagnostic(t *testing.T) {
	dir := os.Getenv("CATC_SITE_LIFECYCLE_RETAINED")
	if dir == "" {
		t.Skip("no retained lifecycle supplied")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved siteLifecycleCheckpoint
	if json.Unmarshal(raw, &saved) != nil {
		t.Fatal("invalid retained checkpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	r, _, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	k8s := fake.NewClientBuilder().WithScheme(r.a.statusWriter.Scheme()).WithStatusSubresource(&saved.Controller, &saved.Config).WithObjects(&saved.Controller, &saved.Config).Build()
	a := &adapter{client: c, key: ctrlclient.ObjectKeyFromObject(&saved.Controller), uid: saved.Controller.UID, generation: saved.Controller.Generation, statusReader: k8s, statusWriter: k8s, intentSecretPath: saved.ProjectionRoot}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wrap := func(base http.RoundTripper) http.RoundTripper {
		if base == nil {
			base = http.DefaultTransport
		}
		return &siteRetainedReadOnlyTransport{base}
	}
	c.mu.Lock()
	rest.HTTPClient.Transport = wrap(rest.HTTPClient.Transport)
	c.wrapTransport = wrap
	c.mu.Unlock()
	results := map[string]bool{}
	config := &saved.Config
	source, err := loadControllerSource(ctx, k8s, config)
	if err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	op := nc.Status.Configuration.Operation
	if op == nil || op.TaskID == "" {
		t.Fatal("no acknowledged retained operation")
	}
	comparison, err := a.resolveTemplateComparison(nc, config, source, "sites", siteComparisonScope, "cvk-site-native-key-v1")
	results["comparisonCurrent"] = err == nil
	if err != nil {
		t.Fatal("retained comparison unavailable", err)
	}
	results["sourceCurrent"] = a.checkConfigurationSnapshot(ctx, config, source) == nil
	child, err := siteRecreationAuthority(nc.Status.Configuration, op)
	results["cohortAuthority"] = err == nil
	archives, err := siteHistoryCohorts(nc.Status.Configuration)
	results["historyCurrent"] = err == nil
	parent, err := siteRecreationParentProof(nc.Status.Configuration, op, child, archives, true)
	results["parentAuthority"] = err == nil
	actual, err := c.ListSites(ctx)
	results["sitesComplete"] = err == nil
	if parent != nil && err == nil {
		creators, _ := siteCreatedRecords(nc.Status.Configuration)
		applied, _ := siteAppliedRecords(nc.Status.Configuration)
		e := &siteConfigurationExecutor{client: c, desired: []desiredSite{{kind: parent.kind, path: parentSitePath(saved.Path + "/LifecycleChild")}}, creators: creators, applied: applied, creatorOwner: string(config.UID), comparison: comparison}
		results["parentIdentity"] = e.checkCreatorIdentities(actual) == nil
		results["parentNativeBaseline"] = e.checkPrivateBaselines(ctx, actual) == nil
	}
	e, write, err := a.siteRecreationExecution(ctx, nc, config, source, saved.Path+"/LifecycleChild")
	results["observerReconstructed"] = err == nil && e != nil && write == nil
	results["recoveryStale"] = errors.Is(err, errConfigurationStale)
	if e != nil {
		done, err := c.siteTaskCompletion(e.desired)(ctx, op, time.Now())
		results["originalTaskComplete"] = done && err == nil
	}
	data, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "diagnostic.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("retained read-only diagnostic", results)
}

func TestLiveCatalystCenterRetainedSiteLifecycleSettle(t *testing.T) {
	dir := os.Getenv("CATC_SITE_LIFECYCLE_RETAINED")
	if dir == "" || os.Getenv("CATC_SITE_LIFECYCLE_SETTLE") != "original-task-and-cleanup" {
		t.Skip("explicit retained lifecycle settlement not enabled")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "checkpoint.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved siteLifecycleCheckpoint
	if json.Unmarshal(raw, &saved) != nil || !strings.HasPrefix(saved.Path, "Global/CVK-Q-Life-") || saved.Controller.Status.Configuration == nil {
		t.Fatal("invalid retained generated lifecycle")
	}
	expected := saved.Controller.Status.Configuration.Operation.DeepCopy()
	if expected == nil || expected.Phase != "Unknown" || !strings.HasPrefix(expected.Resource, siteRecreateHandoffPrefix) || !validAPIID(expected.TaskID) {
		t.Fatal("retained task is not the exact acknowledged handoff")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := liveClient(t)
	r, _, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	k8s := fake.NewClientBuilder().WithScheme(r.a.statusWriter.Scheme()).WithStatusSubresource(&saved.Controller, &saved.Config).WithObjects(&saved.Controller, &saved.Config).Build()
	a := &adapter{client: c, key: ctrlclient.ObjectKeyFromObject(&saved.Controller), uid: saved.Controller.UID, generation: saved.Controller.Generation, statusReader: k8s, statusWriter: k8s, intentSecretPath: saved.ProjectionRoot}
	h := &liveSiteManifest{a: a, config: &saved.Config, singular: true}
	checkpoint := func() error {
		if err := k8s.Get(ctx, a.key, &saved.Controller); err != nil {
			return err
		}
		if err := k8s.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), &saved.Config); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(saved, "", "  ")
		if err != nil {
			return err
		}
		tmp := filepath.Join(dir, "checkpoint.json.tmp")
		if err := os.WriteFile(tmp, raw, 0600); err != nil {
			return err
		}
		return os.Rename(tmp, filepath.Join(dir, "checkpoint.json"))
	}
	a.statusWriter = &siteLifecycleCheckpointClient{Client: k8s, checkpoint: checkpoint}
	trace := &siteLifecycleNativeTrace{name: lastSiteName(saved.Path), allowedDeletes: map[string]bool{}, maxPosts: 0}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wrap := func(base http.RoundTripper) http.RoundTripper {
		if base == nil {
			base = http.DefaultTransport
		}
		trace.mu.Lock()
		trace.base = base
		trace.mu.Unlock()
		return trace
	}
	c.mu.Lock()
	rest.HTTPClient.Transport = wrap(rest.HTTPClient.Transport)
	c.wrapTransport = wrap
	c.mu.Unlock()
	source, err := loadControllerSource(ctx, k8s, h.config)
	if err != nil {
		t.Fatal(err)
	}
	for {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		op := nc.Status.Configuration.Operation
		if op == nil || op.ID != expected.ID || op.TaskID != expected.TaskID {
			t.Fatal("original task binding changed")
		}
		e, writer, err := a.siteRecreationExecution(ctx, nc, h.config, source, saved.Path+"/LifecycleChild")
		if writer != nil {
			t.Fatal("retained recovery exposed writer")
		}
		if err != nil && !errors.Is(err, errConfigurationStale) && !errors.Is(err, errConfigurationBusy) {
			t.Fatal("retained observer refused", err)
		}
		if err == nil {
			done, err := c.siteTaskCompletion(e.desired)(ctx, op, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if done {
				if op.Phase != "Verifying" {
					if err := a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
						t.Fatal(err)
					}
				}
				nc, err = a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				err = a.recordSiteCreated(ctx, nc, h.config, source, e, nc.Status.Configuration.Operation)
				if err == nil {
					if err := a.finishConfiguration(ctx, op.ID); err != nil {
						t.Fatal(err)
					}
					break
				}
				if !errors.Is(err, errConfigurationStale) && !errors.Is(err, errConfigurationBusy) {
					t.Fatal(err)
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("retained original task wait expired")
		case <-time.After(2 * time.Second):
		}
	}
	t.Log("original acknowledged child task settled without Create", expected.TaskID)
	creators, err := siteCreatedRecords(journalState(t, a))
	if err != nil {
		t.Fatal(err)
	}
	parent, ok := siteCreatorForPath(creators, saved.Path)
	if !ok {
		t.Fatal("parent creator unavailable")
	}
	child, ok := siteCreatorForPath(creators, saved.Path+"/LifecycleChild")
	if !ok || child.receipt.Operation.TaskID != expected.TaskID || child.receipt.Operation.ParentID != parent.id {
		t.Fatal("retained child/parent creator proof unavailable")
	}
	trace.permitDelete(child.id)
	trace.permitDelete(parent.id)
	for _, id := range []string{child.id, parent.id} {
		h.update(t, ctx, nil)
		source, err := loadControllerSource(ctx, k8s, h.config)
		if err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		writer, err := a.siteRetirementWrite(ctx, nc, h.config, source, id)
		if err != nil {
			t.Fatal("retained exact cleanup planning refused", err)
		}
		if err := a.submitConfigurationWrite(ctx, h.config, source, "sites", writer); err != nil {
			t.Fatal("retained cleanup uncertain; no replay", err)
		}
		op := journalState(t, a).Operation
		if op == nil || op.Phase != "Accepted" || op.TaskID == "" {
			t.Fatal("cleanup acknowledgement missing")
		}
		for {
			err := a.recordSiteRetired(ctx, h.config, source, op.ID)
			if err == nil {
				break
			}
			if !errors.Is(err, errConfigurationBusy) {
				t.Fatal(err)
			}
			select {
			case <-ctx.Done():
				t.Fatal("retained cleanup wait expired")
			case <-time.After(2 * time.Second):
			}
		}
		if err := a.finishConfiguration(ctx, op.ID); err != nil {
			t.Fatal(err)
		}
	}
	var baseline []Site
	raw, err = os.ReadFile(filepath.Join(dir, "baseline-sites.json"))
	if err != nil || json.Unmarshal(raw, &baseline) != nil {
		t.Fatal("retained baseline unavailable")
	}
	actual, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	order := func(rows []Site) { sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID }) }
	order(actual)
	order(baseline)
	if !reflect.DeepEqual(actual, baseline) {
		t.Fatal("retained baseline not restored")
	}
	trace.mu.Lock()
	posts, deletes := trace.posts, trace.deletes
	trace.mu.Unlock()
	state := journalState(t, a)
	if posts != 0 || deletes != 2 || state.Operation != nil || len(state.ActionApplications) != 8 {
		t.Fatal("retained settlement replayed or lost history")
	}
	result := map[string]any{"originalTask": expected.TaskID, "newCreates": posts, "cleanupDeletes": deletes, "baselineRestored": true, "rows": len(state.ActionApplications), "originalTaskSettled": true, "installed": false}
	raw, _ = json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "settlement-result.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("retained cycle completed: zero Creates, two exact Deletes, original task and baseline verified")
}
