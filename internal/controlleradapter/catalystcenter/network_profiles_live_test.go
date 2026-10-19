// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Public configuration reconciliation with simulated Kubernetes persistence.
// It does not qualify installed actors or physical device provisioning.
func liveSwitchingProfileJournal(t *testing.T, c *client, ctx context.Context) (func([]byte), func()) {
	t.Helper()
	r, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	a := r.a
	a.client = c
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	config.Spec.ManagedSections = []string{"network_profiles"}
	key := ctrlclient.ObjectKeyFromObject(config)
	var recovery *liveNetworkProfileRecovery
	var creatorBaseline *ciskov1.NetworkControllerConfigurationActionApplication
	privateNative := os.Getenv("CATC_SWITCHING_PROFILE_NATIVE_OBSERVATION") == "isolated-public"
	if privateNative {
		configureWirelessTagComparisonFixture(t, a, config)
	}
	var preflight *switchingPreflightQualification
	if os.Getenv("CATC_SWITCHING_PROFILE_NO_DISPATCH") == "isolated-public" {
		preflight = installSwitchingPreflightQualification(a, config, c)
	}
	apply := func(raw []byte) {
		t.Helper()
		if preflight != nil {
			preflight.injected = false
			preflight.retired = false
			preflight.before = nil
			preflight.mutations = 0
		}
		config.Generation++
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("CATC_PROFILE_SITE_RECOVERY") == "isolated-public" {
			executor, err := c.compileNetworkProfileConfiguration(raw)
			if err != nil {
				t.Fatal(err)
			}
			write, err := executor.next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if write != nil {
				kind, _, supported := networkProfileMetadataResource(write.resource)
				if supported && kind == "switching-assignment" {
					h := &liveNetworkProfileRecovery{a: a, config: config, recovered: map[string]bool{}}
					recovery = h
					h.run(t, ctx, c, raw, write)
					h.finish(t, ctx, c, 1)
					t.Log("switching association original task/full-tree recovery and no-dispatch proof passed through the retained profile journal")
					return
				}
			}
		}
		seen := map[string]bool{}
		recoveredDefinitions := map[string]bool{}
		var originalCreate *ciskov1.NetworkControllerConfigurationOperation
		definitionRecovery := os.Getenv("CATC_SWITCHING_PROFILE_RECOVERY") == "isolated-public"
		for {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			if preflight != nil {
				preflight.afterTick(t)
			}
			state := journalState(t, a)
			if state != nil && state.Operation != nil {
				op := state.Operation
				if os.Getenv("CATC_SWITCHING_PROFILE_CREATE_RESULT") == "isolated-public" && op.Action == "Create" && strings.HasPrefix(op.Resource, "profiles/") && op.TaskID != "" && (op.Phase == "Accepted" || op.Phase == "Unknown" || op.Phase == "Verifying") {
					originalCreate = op.DeepCopy()
				}
				if strings.HasPrefix(op.Resource, "profiles/") && !validSwitchingProfileBinding(op.ParentID) {
					t.Fatal("public profile journal lost its project binding")
				}
				if op.TaskID != "" && !seen[op.TaskID] {
					seen[op.TaskID] = true
					t.Log("public profile operation", op.Action, op.Resource, op.TaskID)
				}
				if definitionRecovery && strings.HasPrefix(op.Resource, "profiles/") && op.Phase == "Accepted" && !recoveredDefinitions[op.TaskID] {
					if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
						t.Fatal(err)
					}
					recoveredDefinitions[op.TaskID] = true
					t.Log("retained original switching definition task for read-only Unknown recovery", op.TaskID)
				}
				if op.Phase == "Submitting" || op.Phase == "Unknown" && !(definitionRecovery && recoveredDefinitions[op.TaskID]) {
					t.Fatal("uncertain public profile operation retained", op.ID, op.Phase)
				}
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				if state == nil || state.Operation != nil || len(seen) != 1 {
					t.Fatal("public profile phase did not complete exactly one operation")
				}
				if originalCreate != nil {
					e, err := c.compileNetworkProfileConfiguration(raw)
					if err != nil {
						t.Fatal(err)
					}
					local := originalCreate.DeepCopy()
					if switchingCreateSelected(local.Resource) {
						_, name, ok := e.switchingCreatorSelection(local)
						if !ok {
							t.Fatal("created resource not declared")
						}
						local.Resource = "profiles/" + name
					}
					id, done, err := e.verifySwitchingProfileCreatedResult(ctx, local, time.Now())
					if err != nil || !done || id == "" {
						t.Fatal("original native Create result correlation failed", err)
					}
					t.Log("strict original Create task UUID and stable declared native profile/project/template result verified", id, originalCreate.TaskID)
				}
				if definitionRecovery && len(recoveredDefinitions) != 0 {
					if preflight != nil && (!preflight.injected || !preflight.retired || preflight.mutations != 1) {
						t.Fatal("switching preflight automatic replan missing or mutation replayed")
					}
					if len(recoveredDefinitions) != 1 {
						t.Fatal("switching definition recovery replayed")
					}
					t.Log("switching definition original task recovered, journal released and generation InSync")
				}
				if os.Getenv("CATC_SWITCHING_PROFILE_CUSTODY") == "isolated-public" {
					rows, err := switchingCreatorRecords(state, string(config.UID))
					if err != nil || len(rows) != 1 {
						t.Fatal("original creator custody missing", err)
					}
					for _, row := range rows {
						if creatorBaseline == nil {
							creatorBaseline = row.DeepCopy()
							if _, err := switchingCreatedTarget(row); err != nil {
								t.Fatal(err)
							}
						} else if !reflect.DeepEqual(*creatorBaseline, row) {
							t.Fatal("subsequent work changed original creator receipt")
						}
					}
					t.Log("original switching creator UUID/task/source receipt retained with atomic journal completion")
				}
				if privateNative {
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					source, err := loadControllerSource(ctx, a.statusReader, config)
					if err != nil {
						t.Fatal(err)
					}
					plan, err := a.compileWirelessTagComparison(nc, config, source)
					if err != nil {
						t.Fatal(err)
					}
					e := plan.executor.(*networkProfileConfiguration)
					for _, part := range e.parts {
						profiles, ok := part.executor.(*switchingProfileExecutor)
						if !ok {
							continue
						}
						before, err := profiles.observe(ctx)
						if err != nil {
							t.Fatal(err)
						}
						after, err := profiles.observe(ctx)
						if err != nil || !sameSwitchingProfileObservation(before, after) {
							t.Fatal("complete native observations unstable", err)
						}
						for i, row := range before {
							first, err := switchingProfileNativeMAC(ctx, e.comparison, row.binding, row.native)
							if err != nil {
								t.Fatal(err)
							}
							second, err := switchingProfileNativeMAC(ctx, e.comparison, after[i].binding, after[i].native)
							if err != nil || first != second {
								t.Fatal("protected native comparison unstable", err)
							}
							t.Log("complete private native observation and protected comparison stable through approved projection", row.actual.ID, "field count", len(row.native.native))
						}
					}
				}
				// A rebuilt reconciler with unchanged source must remain a no-op.
				for range 2 {
					fresh := *a
					if _, err := (&configReconciler{a: &fresh}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
						t.Fatal(err)
					}
					if journalState(t, a).Operation != nil {
						t.Fatal("public profile no-op replayed")
					}
				}
				if err := a.statusReader.Get(ctx, key, config); err != nil {
					t.Fatal(err)
				}
				t.Log("public profile source generation converged", config.Generation)
				return
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
				t.Fatal("public profile qualification failed", config.Status.Conditions)
			}
			select {
			case <-ctx.Done():
				t.Fatal("public profile qualification deadline exceeded", config.Status.Conditions)
			case <-time.After(time.Second):
			}
		}
	}
	return apply, func() {
		if recovery == nil {
			t.Fatal("association recovery fixture unavailable")
		}
		recovery.prune(t, ctx, c, 1)
	}
}

// Reuse the isolated project/template fixture; no profile is assigned to a
// real device site, and no template deployment is performed.
func qualifyLiveNetworkProfile(t *testing.T, c *client, ctx context.Context, projectName, templateName, templateID string, settled *bool, submit func(context.Context, string, string, any)) {
	t.Helper()
	name := "CVK-Q-Profile-" + time.Now().UTC().Format("20060102-150405")
	siteName := name + "-Site"
	sitePath := "Global/" + siteName
	profileID, siteID := "", ""
	var publicApply func([]byte)
	var publicPrune func()
	if os.Getenv("CATC_NETWORK_PROFILE_JOURNAL") == "isolated-profile" {
		publicApply, publicPrune = liveSwitchingProfileJournal(t, c, ctx)
	}
	initial, err := c.listSwitchingProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := initial[name]; exists {
		t.Fatal("profile fixture exists")
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.NameHierarchy == sitePath {
			t.Fatal("site fixture exists")
		}
	}
	wait := func(task Task, submitted time.Time) {
		t.Helper()
		for {
			done, err := c.configurationTaskComplete(ctx, task.ID, submitted, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			if done {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
	}
	t.Cleanup(func() {
		if !*settled {
			t.Log("retained uncertain profile/site fixtures", name, profileID, siteID)
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		profiles, err := c.listSwitchingProfiles(cleanup)
		if err != nil {
			*settled = false
			t.Error(err)
			return
		}
		if p, exists := profiles[name]; exists {
			if p.ID != profileID {
				*settled = false
				t.Error("profile fixture replaced")
				return
			}
			assignments, err := c.profileAssignedSites(cleanup, profileID)
			if err != nil {
				*settled = false
				t.Error(err)
				return
			}
			for id := range assignments {
				if id != siteID {
					*settled = false
					t.Error("foreign profile site assignment; retained")
					return
				}
			}
			if assignments[siteID] {
				submit(cleanup, http.MethodDelete, profileSiteAssignmentsPath(profileID)+"/"+siteID, nil)
				assignments, err = c.profileAssignedSites(cleanup, profileID)
				if err != nil || len(assignments) != 0 {
					t.Error("assignment cleanup failed", err)
					return
				}
			}
			submit(cleanup, http.MethodDelete, networkProfilesPath+"/"+profileID, nil)
			profiles, err = c.listSwitchingProfiles(cleanup)
			if err != nil {
				t.Error(err)
				return
			}
			if _, exists := profiles[name]; exists {
				t.Error("profile remains")
				return
			}
		}
		if siteID != "" {
			sites, err := c.ListSites(cleanup)
			if err != nil {
				*settled = false
				t.Error(err)
				return
			}
			found := false
			for _, s := range sites {
				if s.NameHierarchy == sitePath && s.ID != siteID {
					*settled = false
					t.Error("site fixture replaced")
					return
				}
				if s.ParentID == siteID {
					*settled = false
					t.Error("site fixture has foreign child")
					return
				}
				if s.ID == siteID {
					if s.NameHierarchy != sitePath {
						*settled = false
						t.Error("site fixture moved")
						return
					}
					found = true
				}
			}
			if found {
				submit(cleanup, http.MethodDelete, "/dna/intent/api/v1/areas/"+siteID, nil)
			}
			sites, err = c.ListSites(cleanup)
			if err != nil {
				t.Error(err)
				return
			}
			for _, s := range sites {
				if s.ID == siteID {
					t.Error("site remains")
					return
				}
			}
		}
		*settled = true
		t.Log("profile, site assignment and isolated site cleanup verified", profileID, siteID)
	})
	execute := func(raw []byte, section string) {
		t.Helper()
		if section == "network_profiles" && publicApply != nil {
			*settled = false
			publicApply(raw)
			*settled = true
			return
		}
		e, err := c.compileConfigurationExecutor(raw, section)
		if err != nil {
			t.Fatal(err)
		}
		w, err := e.next(ctx)
		if err != nil || w == nil {
			t.Fatal("missing profile qualification plan", err)
		}
		*settled = false
		submitted := time.Now()
		task, err := w.submit(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("profile qualification operation", section, w.action, w.resource, task.ID)
		wait(task, submitted)
		e, err = c.compileConfigurationExecutor(raw, section)
		if err != nil {
			t.Fatal(err)
		}
		op := &ciskov1.NetworkControllerConfigurationOperation{Resource: w.resource, Action: w.action, TargetID: w.identity.target, ParentID: w.identity.parent}
		if ok, err := e.verify(ctx, op); err != nil || !ok {
			t.Fatal("profile qualification readback failed", err)
		}
		if next, err := e.next(ctx); err != nil || next != nil {
			t.Fatal("profile qualification did not converge", err)
		}
		*settled = true
	}
	model := func(templates, assign bool) []byte {
		p := map[string]any{"name": name}
		if templates {
			p["dayn_templates"] = []string{projectName + "#" + templateName}
		}
		if assign {
			p["sites"] = []string{sitePath}
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_profiles": map[string]any{"switching": []any{p}}}})
		return raw
	}
	referenceQualification := os.Getenv("CATC_TEMPLATE_PROFILE_REFERENCES") == "isolated-lifecycle"
	assertReferences := func(kind string) {
		if !referenceQualification {
			return
		}
		refs, err := c.stableTemplateProfileReferences(ctx, templateID)
		if err != nil {
			*settled = false
			t.Fatal("native profile reference observation failed", err)
		}
		if kind == "" {
			if len(refs) != 0 {
				*settled = false
				t.Fatal("unexpected template profile reference remains")
			}
			t.Log("stable empty profile reference set verified")
			return
		}
		found, modern := false, false
		for _, ref := range refs {
			if ref.ProfileID != profileID || ref.Kind != kind && ref.Kind != "attached" {
				*settled = false
				t.Fatal("foreign or stale native profile reference")
			}
			found = found || ref.Kind == kind
			modern = modern || ref.Kind == "attached"
		}
		if !found || kind == "cli.templates" && !modern {
			*settled = false
			t.Fatal("native template association missing from qualified views")
		}
		t.Log("stable native profile reference verified", kind, "reverse association present", modern)
	}
	assertReferences("")
	createWithTemplates := os.Getenv("CATC_SWITCHING_PROFILE_CREATE_RESULT") == "isolated-public"
	execute(model(createWithTemplates, false), "network_profiles")
	profiles, err := c.listSwitchingProfiles(ctx)
	if err != nil {
		*settled = false
		t.Fatal(err)
	}
	profileID = profiles[name].ID
	if !createWithTemplates {
		assertReferences("")
		execute(model(true, false), "network_profiles")
	}
	assertReferences("cli.templates")
	if os.Getenv("CATC_TEMPLATE_PROFILE_REFERENCE_PROBE") == "isolated-readback" {
		probeLiveTemplateProfileReferences(t, c, ctx, projectName, templateName, profileID)
	}
	siteModel, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": map[string]any{"areas": []any{map[string]string{"name": siteName, "parent_name": "Global"}}}}})
	execute(siteModel, "sites")
	sites, err = c.ListSites(ctx)
	if err != nil {
		*settled = false
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.NameHierarchy == sitePath {
			siteID = s.ID
		}
	}
	if siteID == "" {
		*settled = false
		t.Fatal("site fixture identity missing")
	}
	execute(model(true, true), "network_profiles")
	assertReferences("cli.templates")
	// Updates after assignment must preserve server-materialized site settings.
	execute(model(false, true), "network_profiles")
	assertReferences("")
	if referenceQualification {
		var onboarding map[string]any
		if json.Unmarshal(model(false, true), &onboarding) != nil {
			t.Fatal("invalid onboarding reference fixture")
		}
		profile := onboarding["catalyst_center"].(map[string]any)["network_profiles"].(map[string]any)["switching"].([]any)[0].(map[string]any)
		profile["onboarding_templates"] = []string{projectName + "#" + templateName}
		encoded, _ := json.Marshal(onboarding)
		execute(encoded, "network_profiles")
		assertReferences("day0.templates")
		execute(model(false, true), "network_profiles")
		assertReferences("")
	}
	execute(model(true, true), "network_profiles")
	assertReferences("cli.templates")
	plans, err := c.compileConfigReports(model(true, true), []string{"network_profiles"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].count != 2 {
		t.Fatal("wrong profile resource count")
	}
	if drift, err := plans[0].observe(ctx); err != nil || len(drift) != 0 {
		t.Fatal("profile report drift", err)
	}
	t.Log("native switching profile create, template update, isolated site assignment and restart readback verified", profileID, siteID)
	if os.Getenv("CATC_PROFILE_SITE_PRUNE") == "isolated-public" {
		if publicPrune == nil {
			t.Fatal("public prune fixture requires public journal")
		}
		*settled = false
		publicPrune()
		*settled = true
	}
}
