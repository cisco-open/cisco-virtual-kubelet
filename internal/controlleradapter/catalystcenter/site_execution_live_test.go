// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"net/http"
	"os"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

// This opt-in test qualifies resource APIs against an isolated site hierarchy.
// It does not claim qualification of an in-cluster journal or manager rollout.
func TestLiveCatalystCenterSiteExecution(t *testing.T) {
	qualifyLiveSiteExecution(t, false)
}

func TestLiveCatalystCenterSiteSingularManifest(t *testing.T) {
	if os.Getenv("CATC_SITE_SINGULAR_MANIFEST") != "isolated-hierarchy" {
		t.Skip("explicit isolated singular-site manifest qualification not enabled")
	}
	qualifyLiveSiteExecutionFlow(t, false, false, true)
}

func TestLiveCatalystCenterSitePrivateCreatorBaselines(t *testing.T) {
	if os.Getenv("CATC_SITE_PRIVATE_BASELINE") != "isolated-creator-baselines" {
		t.Skip("explicit private site creator qualification not enabled")
	}
	qualifyLiveSiteExecutionFlow(t, false, false, true)
}

func TestLiveCatalystCenterSitePrivateApplied(t *testing.T) {
	if os.Getenv("CATC_SITE_PRIVATE_BASELINE") != "isolated-latest-applied" {
		t.Skip("explicit private latest-applied qualification not enabled")
	}
	qualifyLiveSiteExecutionFlow(t, false, false, true)
}

func TestLiveCatalystCenterPrivateSiteDesign(t *testing.T) {
	if os.Getenv("CATC_SITE_PRIVATE_BASELINE") != "isolated-design" {
		t.Skip("explicit private site/design qualification not enabled")
	}
	qualifyLiveSiteExecutionFlow(t, false, false, true)
}

func TestLiveCatalystCenterDefaultResolvedSiteDesign(t *testing.T) {
	if os.Getenv("CATC_SITE_DESIGN_DEFAULTS") != "isolated-configmap-design" {
		t.Skip("explicit default-resolved design qualification disabled")
	}
	t.Setenv("CATC_SITE_EXECUTION", "isolated-hierarchy")
	t.Setenv("CATC_SITE_PRIVATE_BASELINE", "isolated-design")
	t.Setenv("CATC_SITE_TASK_RECOVERY", "isolated-acknowledged-tasks")
	qualifyLiveSiteExecutionFlow(t, false, false, true)
}

func TestLiveCatalystCenterSelectedSiteDesign(t *testing.T) {
	if os.Getenv("CATC_SITE_MANAGED_SELECTION") != "isolated-design" {
		t.Skip("explicit selected site design qualification disabled")
	}
	t.Setenv("CATC_SITE_DESIGN_DEFAULTS", "isolated-configmap-design")
	TestLiveCatalystCenterDefaultResolvedSiteDesign(t)
}

func TestLiveCatalystCenterMergedSiteDesign(t *testing.T) {
	if os.Getenv("CATC_SITE_MERGED_SOURCE") != "isolated-design" {
		t.Skip("explicit merged site design qualification disabled")
	}
	t.Setenv("CATC_SITE_MANAGED_SELECTION", "isolated-design")
	TestLiveCatalystCenterSelectedSiteDesign(t)
}

func TestLiveCatalystCenterSiteBulkExecution(t *testing.T) {
	if os.Getenv("CATC_SITE_BULK_EXECUTION") != "isolated-hierarchy" {
		t.Skip("explicit isolated bulk-site qualification not enabled")
	}
	qualifyLiveSiteExecutionWithBulk(t, false, true)
}

func TestLiveCatalystCenterSiteBulkManifest(t *testing.T) {
	if os.Getenv("CATC_SITE_BULK_MANIFEST_EXECUTION") != "isolated-hierarchy" {
		t.Skip("explicit isolated manifest bulk-site qualification not enabled")
	}
	qualifyLiveSiteExecutionFlow(t, false, true, true)
}

func TestLiveCatalystCenterFloorImageManifest(t *testing.T) {
	if os.Getenv("CATC_FLOOR_IMAGE_EXECUTION") != "isolated-hierarchy" {
		t.Skip("explicit isolated floor-image qualification not enabled")
	}
	qualifyLiveSiteExecutionFlow(t, false, true, true)
}

func TestLiveCatalystCenterPlannedAPManifest(t *testing.T) {
	if os.Getenv("CATC_PLANNED_AP_EXECUTION") != "isolated-hierarchy" {
		t.Skip("explicit isolated planned AP qualification not enabled")
	}
	qualifyLiveSiteExecutionFlow(t, false, true, true)
}

func TestLiveCatalystCenterNetworkSettingsExecution(t *testing.T) {
	if os.Getenv("CATC_NETWORK_EXECUTION") != "isolated-hierarchy" {
		t.Skip("explicit isolated network-settings qualification not enabled")
	}
	qualifyLiveSiteExecution(t, true)
}

func qualifyLiveSiteExecution(t *testing.T, network bool) {
	qualifyLiveSiteExecutionWithBulk(t, network, false)
}

func qualifyLiveSiteExecutionWithBulk(t *testing.T, network, bulk bool) {
	qualifyLiveSiteExecutionFlow(t, network, bulk, false)
}

func qualifyLiveSiteExecutionFlow(t *testing.T, network, bulk, manifest bool) {
	if os.Getenv("CATC_SITE_EXECUTION") != "isolated-hierarchy" {
		t.Skip("explicit isolated hierarchy qualification not enabled")
	}
	c := liveClient(t)
	duration := 8 * time.Minute
	if os.Getenv("CATC_AP_FLOOR_HANDOFF") == "isolated-original-task-effects" {
		duration = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	root := "Global/CVK-Q-" + time.Now().UTC().Format("20060102-150405-000000000")
	t.Logf("isolated qualification hierarchy: %s", root)
	model := fmt.Sprintf(`catalyst_center:
  sites:
    areas: [{name: %s, parent_name: Global}]
    buildings: [{name: TestBuilding, parent_name: %s, country: United States, latitude: 37.3381234, longitude: -121.8861234}]
    floors: [{name: TestFloor, parent_name: %s/TestBuilding, floor_number: 1, rf_model: Free Space, width: 10.123456, length: 15.123456, height: 3.123456, units_of_measure: meters}]
`, lastSiteName(root), root, root)
	if os.Getenv("CATC_AP_TYPE_TRANSITION") == "isolated-compatible-models" {
		model = strings.Replace(model, "height: 3.123456", "height: 6", 1)
	}
	if os.Getenv("CATC_SITE_DESIGN_DEFAULTS") == "isolated-configmap-design" {
		unresolved := strings.Replace(model, "rf_model: Free Space, ", "", 1)
		resolved, meta, err := ExportSiteDefaults([]byte(unresolved), nil, false)
		if err != nil || meta.DefaultedFields != 1 {
			t.Fatal("initial site source did not resolve pinned defaults", err)
		}
		model = string(resolved)
	}
	desired, err := parseSites([]byte(model))
	if err != nil {
		t.Fatal(err)
	}
	initial, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range initial {
		if s.NameHierarchy == root || strings.HasPrefix(s.NameHierarchy, root+"/") {
			t.Fatal("qualification identity already exists")
		}
	}
	owned := map[string]Site{}
	settled := true
	var afterHierarchyCleanup func()
	waitTask := func(ctx context.Context, task Task, submitted time.Time) error {
		for {
			done, err := c.configurationTaskComplete(ctx, task.ID, submitted, time.Now())
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	// Remove only verified identities created by this test, in reverse hierarchy
	// order. An uncertain write or foreign child retains the fixture for review.
	t.Cleanup(func() {
		defer func() {
			if afterHierarchyCleanup != nil {
				afterHierarchyCleanup()
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if !settled {
			t.Logf("retained uncertain hierarchy for inspection: %s", root)
			return
		}
		for _, kind := range []string{"floor", "building", "area"} {
			for id, site := range owned {
				if site.Type != kind {
					continue
				}
				current, err := c.ListSites(ctx)
				if err != nil {
					t.Errorf("cleanup observation: %v", err)
					return
				}
				found := false
				for _, s := range current {
					if s.NameHierarchy == site.NameHierarchy && s.ID != id {
						t.Error("cleanup refused replaced identity")
						return
					}
					if s.ID == id {
						if s.NameHierarchy != site.NameHierarchy {
							t.Error("cleanup refused moved identity")
							return
						}
						found = true
					}
					if strings.HasPrefix(s.NameHierarchy, site.NameHierarchy+"/") {
						t.Error("cleanup refused site with remaining children")
						return
					}
				}
				if !found {
					continue
				}
				endpoint := map[string]string{"area": "/dna/intent/api/v1/areas/", "building": "/dna/intent/api/v2/buildings/", "floor": "/dna/intent/api/v2/floors/"}[kind]
				submitted := time.Now()
				raw, err := c.mutate(ctx, http.MethodDelete, endpoint+id, nil)
				if err != nil {
					t.Errorf("cleanup submission: %v", err)
					return
				}
				var receipt struct {
					Response Task `json:"response"`
				}
				if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
					t.Error("cleanup missing receipt; no replay")
					return
				}
				if err := waitTask(ctx, receipt.Response, submitted); err != nil {
					t.Errorf("cleanup task: %v", err)
					return
				}
				current, err = c.ListSites(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				for _, s := range current {
					if s.ID == id || s.NameHierarchy == site.NameHierarchy {
						t.Error("cleanup deletion not verified")
						return
					}
				}
				delete(owned, id)
			}
		}
		if len(owned) == 0 {
			t.Log("isolated hierarchy cleanup verified")
		}
	})
	var manifestHarness *liveSiteManifest
	var comparisonKeyPath string
	var comparisonKey []byte
	if manifest {
		manifestHarness = newLiveSiteManifest(t, c, desired)
		manifestHarness.singular = !bulk
		if !bulk && (os.Getenv("CATC_SITE_PRIVATE_BASELINE") == "isolated-creator-baselines" || os.Getenv("CATC_SITE_PRIVATE_BASELINE") == "isolated-latest-applied" || os.Getenv("CATC_SITE_PRIVATE_BASELINE") == "isolated-design") {
			comparisonKeyPath, comparisonKey = configureSiteNativeComparisonFixture(t, manifestHarness.a, manifestHarness.config)
		}
	}
	if manifest && os.Getenv("CATC_SITE_DESIGN_DEFAULTS") == "isolated-configmap-design" {
		afterHierarchyCleanup = checkpointLiveSiteDefaultDesign(t, ctx, manifestHarness, root, initial, comparisonKeyPath, comparisonKey)
	}
	var faults *metadataNoDispatchTrace
	if manifest && !bulk && os.Getenv("CATC_SITE_NO_DISPATCH") == "isolated-preflight-reads" {
		faults = &metadataNoDispatchTrace{a: manifestHarness.a, section: "sites", preflightPath: sitesPath, injected: map[string]bool{}, retired: map[string]bool{}, allowMutation: func(req *http.Request) bool {
			for _, path := range []string{"/dna/intent/api/v1/areas", "/dna/intent/api/v2/buildings", "/dna/intent/api/v2/floors"} {
				if req.URL.Path == path || strings.HasPrefix(req.URL.Path, path+"/") {
					return true
				}
			}
			return false
		}}
		restore, err := installMetadataSessionTrace(ctx, c, faults)
		if err != nil {
			t.Fatal(err)
		}
		defer restore()
	}
	if manifest && !bulk {
		manifestHarness.update(t, ctx, desired)
		if os.Getenv("CATC_SITE_PRIVATE_BASELINE") == "isolated-design" {
			setupLivePrivateDesignSource(t, ctx, manifestHarness)
		}
		settled = false
		paths := []string{root, root + "/TestBuilding", root + "/TestBuilding/TestFloor"}
		receipts := manifestHarness.runCreates(t, ctx, paths)
		for _, receipt := range receipts {
			complete, err := c.configurationTaskTreeComplete(ctx, receipt.task.ID, receipt.submitted, time.Now())
			if err != nil || !complete {
				t.Fatal("singular hierarchy task tree unverified", err)
			}
		}
		actual, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if next, err := nextSiteMutation(desired, actual); err != nil || next != nil {
			t.Fatal("singular hierarchy native readback did not converge", err)
		}
		for _, site := range actual {
			if site.NameHierarchy == root || strings.HasPrefix(site.NameHierarchy, root+"/") {
				owned[site.ID] = site
			}
		}
		if len(owned) != len(desired) {
			t.Fatal("singular hierarchy identity set incomplete")
		}
		// Qualify the native creator-result contract before cleanup. Matching
		// desired state alone cannot prove which operation created a site.
		// This observation does not issue a durable ownership receipt.
		for index, receipt := range receipts {
			raw, err := c.GetTask(ctx, receipt.task.ID)
			if err != nil {
				t.Fatal("singular creator task unavailable", err)
			}
			done, err := decodeConfigurationTaskComplete(raw, receipt.task.ID, receipt.submitted, time.Now())
			if err != nil || !done {
				t.Fatal("singular creator task completion unverified", err)
			}
			var result struct {
				Response struct {
					Data string `json:"data"`
				} `json:"response"`
			}
			if json.Unmarshal(raw, &result) != nil || !canonicalTemplateUUID(result.Response.Data) {
				t.Fatal("singular creator task lacks canonical result identity")
			}
			site, exists := owned[result.Response.Data]
			if !exists || site.NameHierarchy != paths[index] || site.Type != desired[index].kind || site.Name != lastSiteName(paths[index]) {
				t.Fatal("singular creator result does not bind the exact requested site")
			}
			parent := ""
			for _, candidate := range actual {
				if candidate.NameHierarchy == parentSitePath(paths[index]) {
					parent = candidate.ID
				}
			}
			if parent == "" || site.ParentID != parent {
				t.Fatal("singular creator result parent identity does not match")
			}
		}
		t.Log("three original create-task result UUIDs bind exact native kind, path and parent; creator receipt identity independently verified")
		state := journalState(t, manifestHarness.a)
		records, err := siteCreatedRecords(state)
		if err != nil || len(records) != 3 {
			t.Fatal("durable site creator record set incomplete", err)
		}
		for index, path := range paths {
			record, ok := siteCreatorForPath(records, path)
			if !ok || record.receipt.Operation.TaskID != receipts[index].task.ID || record.receipt.Operation.ConfigUID != string(manifestHarness.config.UID) || !siteCreatedReceiptPresent(state, &record.receipt.Operation) {
				t.Fatal("durable creator receipt lost original operation identity")
			}
		}
		t.Log("three durable original-task site creator receipts retained through restarted reconciliation")
		settled = true
		t.Log("three singular manifest creates and complete native hierarchy verified")
		if os.Getenv("CATC_SITE_PRIVATE_BASELINE") == "isolated-creator-baselines" {
			for _, record := range records {
				if record.keyID == "" || record.mac == "" {
					t.Fatal("private native baseline not retained")
				}
			}
			for pass := 0; pass < 3; pass++ {
				restarted := *manifestHarness.a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(manifestHarness.config)}); err != nil {
					t.Fatal(err)
				}
				if err := manifestHarness.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(manifestHarness.config), manifestHarness.config); err != nil {
					t.Fatal(err)
				}
				if manifestHarness.config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || journalState(t, manifestHarness.a).Operation != nil {
					t.Fatal("private creator baseline native no-op did not converge")
				}
			}
			if os.Getenv("CATC_SITE_TASK_RECOVERY") == "isolated-acknowledged-tasks" && len(manifestHarness.recovered) != 3 {
				t.Fatal("private original tasks did not exercise Unknown recovery")
			}
			t.Log("three private creator baselines match complete native state across restarted no-ops; this qualification stops at creation")
			return
		}
	}
	if bulk {
		binding, err := newSiteBulkBinding(desired, initial)
		if err != nil {
			t.Fatal("bulk member binding", err)
		}
		resource, err := binding.resource(initial)
		if err != nil {
			t.Fatal("bulk anchor binding", err)
		}
		for _, site := range desired {
			if site.path != root && !strings.HasPrefix(site.path, root+"/") {
				t.Fatal("bulk operation escaped isolated hierarchy")
			}
		}
		settled = false
		var task Task
		var submitted time.Time
		if manifest {
			task, submitted = manifestHarness.run(t, ctx, siteBulkResourcePrefix, "Create")
		} else {
			submit, planErr := c.planGuardedSiteBulkCreate(ctx, desired, func(context.Context) error { return nil })
			if planErr != nil || submit == nil {
				t.Fatal("bulk planning", planErr)
			}
			submitted = time.Now()
			task, err = submit(ctx)
		}
		if err != nil {
			t.Fatal("bulk submission uncertain; retain hierarchy", err)
		}
		t.Logf("bulk create task=%s", task.ID)
		if err = waitTask(ctx, task, submitted); err != nil {
			t.Fatal("bulk task uncertain; retain hierarchy", err)
		}
		if complete, err := c.configurationTaskTreeComplete(ctx, task.ID, submitted, time.Now()); err != nil || !complete {
			t.Fatal("bulk task tree incomplete; retain hierarchy", err)
		}
		actual, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if verified, err := verifySiteBulkBinding(desired, actual, resource); err != nil || !verified {
			t.Fatal("retained bulk member/anchor binding did not verify; retain hierarchy", err)
		}
		if next, err := nextSiteMutation(desired, actual); err != nil || next != nil {
			t.Fatal("bulk hierarchy did not independently converge; retain hierarchy", err)
		}
		for _, site := range actual {
			if site.NameHierarchy == root || strings.HasPrefix(site.NameHierarchy, root+"/") {
				owned[site.ID] = site
			}
		}
		if len(owned) != len(desired) {
			t.Fatal("bulk identity set incomplete; retain hierarchy")
		}
		settled = true
		t.Log("single native batch created all three site identities; retained member/anchor binding and task tree verified")
	}
	for step := 0; step < 4; step++ {
		if (bulk || manifest) && step < 3 {
			continue
		}
		actual, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if step == 3 {
			for i := range desired {
				if desired[i].kind == "floor" {
					desired[i].fields["width"] = 12.654321
				}
			}
		}
		m, err := nextSiteMutation(desired, actual)
		if err != nil || m == nil {
			t.Fatalf("step %d planning: %v", step, err)
		}
		if m.desired.path != root && !strings.HasPrefix(m.desired.path, root+"/") {
			t.Fatal("operation escaped isolated hierarchy")
		}
		settled = false
		submitted := time.Now()
		var task Task
		if manifest {
			manifestHarness.update(t, ctx, desired)
			task, submitted = manifestHarness.run(t, ctx, m.desired.path, m.action)
		} else {
			task, err = c.submitSiteMutation(ctx, m)
		}
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("submitted task=%s at=%s", task.ID, submitted.UTC().Format(time.RFC3339Nano))
		if err = waitTask(ctx, task, submitted); err != nil {
			t.Fatal(err)
		}
		verified, err := c.verifySiteMutation(ctx, m)
		if err != nil || !verified {
			t.Fatalf("step %d read-back verified=%v err=%v", step, verified, err)
		}
		actual, err = c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range actual {
			if s.NameHierarchy == m.desired.path {
				owned[s.ID] = s
			}
		}
		settled = true
		t.Logf("verified %s %s", m.action, m.desired.kind)
	}
	actual, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if next, err := nextSiteMutation(desired, actual); err != nil || next != nil {
		t.Fatalf("final model did not converge: %v", err)
	}
	t.Log("three creates, one floor update, and full hierarchy read-back verified")
	expectedTasks := 4
	if manifest && !bulk && os.Getenv("CATC_SITE_PRIVATE_BASELINE") == "isolated-latest-applied" {
		qualifyLiveSiteAppliedReplacement(t, ctx, manifestHarness, desired, &settled)
		expectedTasks = 6
	}
	if manifest && !bulk && os.Getenv("CATC_SITE_TASK_RECOVERY") == "isolated-acknowledged-tasks" {
		if len(manifestHarness.recovered) != expectedTasks {
			t.Fatal("all four original singular-site tasks did not exercise retained Unknown recovery")
		}
		t.Logf("%d acknowledged Unknown tasks recovered through restarted public reconciliation without resubmission", expectedTasks)
	}
	if faults != nil {
		if faults.pending != "" || len(faults.injected) != expectedTasks || len(faults.retired) != expectedTasks || faults.mutations != expectedTasks {
			t.Fatal("site refusal/replan proof or mutation count incomplete")
		}
		t.Logf("%d injected preflight failures retired without native effects; fresh public plans converged with %d exact mutations and retained owners/history", expectedTasks, expectedTasks)
	}
	if os.Getenv("CATC_SITE_PRIVATE_BASELINE") == "isolated-design" {
		qualifyLivePrivateSiteDesign(t, ctx, manifestHarness, owned, waitTask, &settled)
	}
	if os.Getenv("CATC_FLOOR_IMAGE_EXECUTION") == "isolated-hierarchy" {
		qualifyLiveFloorImageManifest(t, ctx, manifestHarness, &settled)
	}
	if os.Getenv("CATC_PLANNED_AP_EXECUTION") == "isolated-hierarchy" {
		qualifyLivePlannedAPManifest(t, ctx, manifestHarness, owned, waitTask, &settled)
	}
	if os.Getenv("CATC_IPAM_EXECUTION") == "isolated-hierarchy" {
		qualifyLiveIPAM(t, c, ctx, root, waitTask, &settled)
	}
	if os.Getenv("CATC_IPAM_DUALSTACK_MANIFEST") == "isolated-hierarchy" {
		qualifyLiveIPAMDualStackManifest(t, c, ctx, root, waitTask, &settled)
	}
	if os.Getenv("CATC_DNS_OPTIONAL_QUALIFICATION") == "isolated-domain-only" {
		qualifyLiveDNSOptional(t, c, ctx, root, waitTask, &settled)
	}
	if os.Getenv("CATC_NETWORK_EMPTY_PROBE") == "isolated-server-reset" {
		qualifyLiveNetworkEmptyProbe(t, c, ctx, root, waitTask, &settled)
	}
	if os.Getenv("CATC_NETWORK_EMPTY_EXECUTION") == "isolated-public-resets" {
		qualifyLiveNetworkEmptyExecution(t, c, ctx, root, waitTask, &settled)
	}
	if os.Getenv("CATC_TELEMETRY_PARTIAL_PROBE") == "isolated-profile" {
		qualifyLiveTelemetryPartialProbe(t, c, ctx, root, waitTask, &settled)
	}
	if os.Getenv("CATC_TELEMETRY_PARTIAL_EXECUTION") == "isolated-preserving" {
		qualifyLiveTelemetryPartialExecution(t, c, ctx, root, waitTask, &settled)
	}
	if network {
		qualifyLiveNetworkSettings(t, c, ctx, root, waitTask, &settled)
	}
}

func qualifyLiveSiteAppliedReplacement(t *testing.T, ctx context.Context, h *liveSiteManifest, desired []desiredSite, settled *bool) {
	t.Helper()
	state := journalState(t, h.a)
	creators, err := siteCreatedRecords(state)
	if err != nil || len(creators) != 3 {
		t.Fatal("private creator lineage incomplete", err)
	}
	rows, err := siteAppliedRecords(state)
	if err != nil || len(rows) != 1 {
		t.Fatal("first private Update receipt missing", err)
	}
	floorPath, buildingPath := "", ""
	for _, site := range desired {
		if site.kind == "floor" {
			floorPath = site.path
		}
		if site.kind == "building" {
			buildingPath = site.path
		}
	}
	first := rows[sitePathDigest(floorPath)]
	floorCreator, ok := siteCreatorForPath(creators, floorPath)
	if !ok || strings.Split(strings.TrimPrefix(first.Resource, siteAppliedPrefix), ":")[5] == floorCreator.mac {
		t.Fatal("first Update did not replace the latest native baseline")
	}
	*settled = false
	for index := range desired {
		if desired[index].kind == "floor" {
			desired[index].fields["width"] = 10.123456
		}
	}
	h.update(t, ctx, desired)
	task, _ := h.run(t, ctx, floorPath, "Update")
	state = journalState(t, h.a)
	rows, err = siteAppliedRecords(state)
	latest := rows[sitePathDigest(floorPath)]
	if err != nil || len(rows) != 1 || latest.Operation.ID == first.Operation.ID || latest.Operation.TaskID != task.ID {
		t.Fatal("latest replacement did not retain only its new original task", err)
	}
	after, err := siteCreatedRecords(state)
	if err != nil || !reflect.DeepEqual(creators, after) {
		t.Fatal("Update changed original creator lineage", err)
	}
	// The parent's changed coordinates must not rewrite the child's latest row.
	for index := range desired {
		if desired[index].kind == "building" {
			desired[index].fields["latitude"] = 37.33912
		}
	}
	h.update(t, ctx, desired)
	parentTask, _ := h.run(t, ctx, buildingPath, "Update")
	state = journalState(t, h.a)
	rows, err = siteAppliedRecords(state)
	if err != nil || len(rows) != 2 || !reflect.DeepEqual(rows[sitePathDigest(floorPath)], latest) || rows[sitePathDigest(buildingPath)].Operation.TaskID != parentTask.ID {
		t.Fatal("parent update rebased child latest evidence", err)
	}
	after, err = siteCreatedRecords(state)
	if err != nil || !reflect.DeepEqual(creators, after) || len(state.ActionApplications) != 5 {
		t.Fatal("bounded latest rows or creator lineage changed", err)
	}
	*settled = true
	t.Log("two floor Updates replace one latest row; parent coordinate Update preserves child evidence; all three original creator receipts remain unchanged")
}

func TestLiveCatalystCenterYAMLSourceDesign(t *testing.T) {
	if os.Getenv("CATC_SITE_YAML_SOURCE") != "isolated-design" {
		t.Skip("explicit YAML source design qualification disabled")
	}
	t.Setenv("CATC_SITE_MERGED_SOURCE", "isolated-design")
	TestLiveCatalystCenterMergedSiteDesign(t)
}
