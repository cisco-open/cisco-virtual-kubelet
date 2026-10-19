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
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Actual appliance APIs through public reconciliation, simulated Kubernetes
// persistence/worker restart. No device deployment or installed-actor claim.
func TestLiveCatalystCenterCompositeTemplatePacing(t *testing.T) {
	if os.Getenv("CATC_TEMPLATE_PACING_EXECUTION") != "isolated-project" {
		t.Skip("isolated public template pacing qualification not enabled")
	}
	c := liveClient(t)
	updateQualification := os.Getenv("CATC_TEMPLATE_NO_DISPATCH_UPDATE") == "isolated-source-revision"
	recoveryQualification := os.Getenv("CATC_TEMPLATE_TASK_RECOVERY") == "acknowledged-metadata"
	baselineQualification := os.Getenv("CATC_TEMPLATE_NATIVE_BASELINE") == "projected-creation-state"
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	name := "CVK-Q-Pacing-" + time.Now().UTC().Format("20060102-150405")
	projects, err := c.listTemplateProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projects[name]; exists {
		t.Fatal("isolated project already exists")
	}
	common := func(name string) map[string]any {
		return map[string]any{"name": name, "language": "JINJA", "software_type": "IOS-XE", "device_types": []any{map[string]any{"product_family": "Switches and Hubs"}}}
	}
	base, composite := common("base"), common("combined")
	composite["composite"] = true
	composite["containing_templates"] = []string{"base"}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"templates": map[string]any{"projects": []any{map[string]any{"name": name, "dayn_templates": []any{composite, base}}}}}})
	r, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	a := r.a
	a.client = c
	var faults *templateNoDispatchTrace
	if os.Getenv("CATC_TEMPLATE_NO_DISPATCH") == "isolated-preflight-reads" {
		faults = &templateNoDispatchTrace{a: a, injected: map[string]bool{}, retired: map[string]bool{}}
		restore, err := installMetadataSessionTrace(ctx, c, faults)
		if err != nil {
			t.Fatal(err)
		}
		defer restore()
	}
	model := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "template-model", UID: "template-source"}, Data: map[string]string{"model.yaml": string(raw), nacmodel.TemplateAssetsKey: `{"base.j2":"! CVK isolated pacing qualification\n"}`}}
	if err := a.statusWriter.Create(ctx, model); err != nil {
		t.Fatal(err)
	}
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	config.Spec.ManagedSections = []string{"templates"}
	if baselineQualification {
		configureTemplateNativeComparisonFixture(t, a, config)
	}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: model.Name, Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	settled := false
	projectID := ""
	owned := map[string]string{}
	wait := func(ctx context.Context, task Task, submitted time.Time) error {
		for {
			done, err := c.configurationTaskComplete(ctx, task.ID, submitted, time.Now())
			if err != nil || done {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	t.Cleanup(func() {
		if !settled {
			t.Log("retained unsettled isolated project for inspection", name)
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Minute)
		defer stop()
		remove := func(path string) bool {
			submitted := time.Now()
			raw, err := c.mutate(cleanup, http.MethodDelete, path, nil)
			var response struct {
				Response Task `json:"response"`
			}
			if err != nil || json.Unmarshal(raw, &response) != nil || !validAPIID(response.Response.ID) {
				t.Error("cleanup receipt not accepted; no replay", err)
				return false
			}
			if err := wait(cleanup, response.Response, submitted); err != nil {
				t.Error(err)
				return false
			}
			return true
		}
		for _, child := range []string{"combined", "base"} {
			definitions, err := c.listTemplateDefinitions(cleanup)
			if err != nil {
				t.Error(err)
				return
			}
			current, exists := definitions[projectID+"#"+child]
			if !exists {
				continue
			}
			if current.ID != owned[child] {
				t.Error("cleanup child identity changed")
				return
			}
			if !remove(templatesPath + "/" + current.ID) {
				return
			}
		}
		raw, err := c.get(cleanup, templateProjectsPath+"/"+projectID, nil, 200)
		var detail struct {
			ID, Name  string
			Templates []json.RawMessage `json:"templates"`
		}
		if err != nil || json.Unmarshal(raw, &detail) != nil || detail.ID != projectID || detail.Name != name || len(detail.Templates) != 0 {
			t.Error("cleanup project changed or still populated", err)
			return
		}
		if !remove(templateProjectsPath + "/" + projectID) {
			return
		}
		projects, err := c.listTemplateProjects(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		if _, exists := projects[name]; exists {
			t.Error("isolated project remains")
			return
		}
		t.Log("isolated template/project cleanup verified", name)
	})
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	accepted := map[string]string{}
	recovered := map[string]string{}
	prepared := map[string]time.Time{}
	verifiedBaselines := map[string]bool{}
	verifiedApplied := map[string]bool{}
	converged := false
	var creatorSnapshot map[string]cvk.NetworkControllerConfigurationActionApplication
	originalVersionReceipts := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	verifiedVersionReceipts := map[string]bool{}
	sourceUpdated := false
	for step := 0; step < 320; step++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, a)
		if op := state.Operation; op != nil {
			if baselineQualification && op.Phase == "Verified" && templateCreatorComparisonKey(op.Resource) != "" && !verifiedBaselines[op.ID] {
				var current configapi.NetworkControllerConfig
				if err := a.statusReader.Get(ctx, req.NamespacedName, &current); err != nil {
					t.Fatal(err)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				source, err := loadControllerSource(ctx, a.statusReader, &current)
				if err != nil {
					t.Fatal(err)
				}
				plan, err := a.compilePrivateTemplateDefinitions(nc, &current, source)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.bindTemplateOwnership(nc, &current, source, &plan); err != nil {
					t.Fatal(err)
				}
				owned, ok := plan.executor.(*templateOwnedExecutor)
				if !ok {
					t.Fatal("private native creator executor missing")
				}
				records, err := templateCreatedRecords(state)
				if err != nil {
					t.Fatal(err)
				}
				key, _ := templateCreateKey(op.Resource)
				receipt := records[key]
				id, err := templateCreatedTarget(receipt)
				if err != nil {
					t.Fatal(err)
				}
				_, expected, err := templateCreatorBaseline(receipt)
				if err != nil || expected == "" {
					t.Fatal("native creator baseline missing", err)
				}
				actual, err := owned.captureCreatorBaseline(ctx, op, id)
				if err != nil || actual != expected {
					t.Fatal("native creation footprint did not match its atomic private baseline", err)
				}
				verifiedBaselines[op.ID] = true
				t.Log("keyed native creation footprint verified against the original receipt before subsequent mutations", len(verifiedBaselines))
			}
			if baselineQualification && op.Phase == "Verified" && strings.HasPrefix(op.Resource, templateApplyPrefix) && !verifiedApplied[op.ID] {
				var current configapi.NetworkControllerConfig
				if err := a.statusReader.Get(ctx, req.NamespacedName, &current); err != nil {
					t.Fatal(err)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				source, err := loadControllerSource(ctx, a.statusReader, &current)
				if err != nil {
					t.Fatal(err)
				}
				plan, err := a.compilePrivateTemplateDefinitions(nc, &current, source)
				if err != nil {
					t.Fatal(err)
				}
				if err := a.bindTemplateOwnership(nc, &current, source, &plan); err != nil {
					t.Fatal(err)
				}
				owned, ok := plan.executor.(*templateOwnedExecutor)
				if !ok {
					t.Fatal("private applied executor missing")
				}
				records, err := templateAppliedRecords(state)
				if err != nil {
					t.Fatal(err)
				}
				key, _, _, ok := templateApplyIdentity(op)
				if !ok {
					t.Fatal("invalid applied identity")
				}
				r, exists := records[key]
				if !exists || !reflect.DeepEqual(r.Operation, *op) {
					t.Fatal("atomic applied receipt missing")
				}
				expected := strings.Split(r.Resource, ":")[7]
				actual, err := owned.captureAppliedBaseline(ctx, op)
				if err != nil || actual != expected {
					t.Fatal("latest native footprint disagrees with applied receipt", err)
				}
				if templateVersionOperation(op) {
					version, bodyMAC, err := owned.captureCreatedVersion(ctx, op)
					if err != nil {
						t.Fatal(err)
					}
					fields := strings.Split(r.Resource, ":")
					if version != fields[len(fields)-2] || bodyMAC != fields[len(fields)-1] {
						t.Fatal("native nonce-bound version receipt disagrees with original proof")
					}
					b, _, ok := owned.findApplied(op)
					if !ok {
						t.Fatal("native version binding missing")
					}
					footprint, err := c.templateVersionFootprint(ctx, templateSummary{ID: op.TargetID, Name: b.name, ProjectID: op.ParentID})
					if err != nil {
						t.Fatal(err)
					}
					_, creatorResource, _, _ := templateApplyIdentity(op)
					matched, err := templateVersionCohortMatches(owned.comparison, state, creatorResource, footprint)
					if err != nil || !matched {
						t.Fatal("native complete version cohort was not owned", err)
					}
					if !sourceUpdated {
						originalVersionReceipts[op.ID] = *r.DeepCopy()
					}
					verifiedVersionReceipts[op.ID] = true
					t.Log("original nonce-bound version UUID/body and complete owned cohort verified", len(verifiedVersionReceipts))
				}
				verifiedApplied[op.ID] = true
				t.Log("private latest-applied native state and version history verified", len(verifiedApplied))
			}
			if op.Phase == "Unknown" && (!recoveryQualification || recovered[op.ID] != op.TaskID) {
				t.Fatal("uncertain native outcome retained", op.Resource)
			}
			if op.Phase == "Prepared" && op.PreparedAt != nil {
				prepared[op.ID] = op.PreparedAt.Time
			}
			if op.Phase == "Accepted" && op.TaskID != "" && op.SubmittedAt != nil {
				if strings.HasSuffix(op.Resource, "Lab#combined") {
					t.Fatal("escaped isolated project")
				}
				if op.PreparedAt != nil {
					// Prefer correlated regular commit verification; fallback is this exact
					// operation's preparation timestamp. Never use this process's start.
					floor := prepared[op.ID]
					for _, receipt := range state.ActionApplications {
						applyKey, _, stage, _ := templateApplyIdentity(&receipt.Operation)
						typedBase := !strings.HasPrefix(receipt.Resource, templateAppliedHistoryPrefix) && applyKey == strings.TrimPrefix(templateCreatorResource("r", name, "base"), templateCreatePrefix) && stage == "commit"
						if (receipt.Resource == "commit:"+name+"#base" || typedBase) && receipt.VerifiedAt != nil {
							floor = receipt.VerifiedAt.Time
						}
					}
					if floor.IsZero() || op.SubmittedAt.Before(&metav1.Time{Time: floor.Add(templateRegularSettleDelay + time.Second)}) {
						t.Fatal("composite submitted before durable settling floor")
					}
				}
				if _, seen := accepted[op.ID]; !seen {
					accepted[op.ID] = op.TaskID
					t.Log("public native task accepted", op.Resource, op.TaskID)
				}
				if recoveryQualification && recovered[op.ID] == "" {
					current, err := a.currentController(ctx)
					if err != nil || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
						t.Fatal("acknowledged recovery injection lost original operation binding", err)
					}
					current.Status.Configuration.Operation.Phase = "Unknown"
					if err := a.statusWriter.Status().Update(ctx, current); err != nil {
						t.Fatal(err)
					}
					recovered[op.ID] = op.TaskID
				}
			}
		}
		var current configapi.NetworkControllerConfig
		if err := a.statusReader.Get(ctx, req.NamespacedName, &current); err != nil {
			t.Fatal(err)
		}
		if current.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
			t.Fatal("public manifest failed; retained isolated project")
		}
		if current.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			wantActions, wantPrepared := 5, 2
			if sourceUpdated {
				wantActions, wantPrepared = 9, 3
			}
			if state.Operation != nil || len(accepted) != wantActions {
				t.Fatal("unexpected convergence/task count", len(accepted))
			}
			if recoveryQualification {
				if len(recovered) != wantActions || !reflect.DeepEqual(accepted, recovered) {
					t.Fatal("acknowledged metadata recovery coverage incomplete")
				}
				if faults != nil && len(faults.recoveryTrees) != wantActions {
					t.Fatal("original task-tree observation coverage incomplete", len(faults.recoveryTrees))
				}
				t.Log("original acknowledged metadata tasks recovered from Unknown without replay", len(recovered))
			}
			if faults != nil {
				if faults.pending != "" || len(faults.injected) != wantActions || len(faults.retired) != wantActions || faults.prepared != wantPrepared || faults.mutations != wantActions {
					t.Fatal("incomplete template refusal/replan qualification", len(faults.injected), len(faults.retired), faults.prepared, faults.mutations)
				}
				t.Log("public template preflight refusals retired with owners/history intact", len(faults.retired), "prepared composite refusals", faults.prepared, "native mutations after fresh replanning", faults.mutations)
			}
			projects, err := c.listTemplateProjects(ctx)
			if err != nil {
				t.Fatal(err)
			}
			projectID = projects[name].ID
			definitions, err := c.listTemplateDefinitions(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, child := range []string{"base", "combined"} {
				id := definitions[projectID+"#"+child].ID
				if previous := owned[child]; previous != "" && previous != id {
					t.Fatal("source revision replaced a template UUID")
				}
				owned[child] = id
				if !validAPIID(owned[child]) {
					t.Fatal("created template identity absent")
				}
			}
			creators, err := templateCreatedRecords(state)
			if err != nil || len(creators) != 3 {
				t.Fatal("three task-bound creator receipts were not retained", err, len(creators))
			}
			for key, receipt := range creators {
				id, err := templateCreatedTarget(receipt)
				want := projectID
				switch strings.Split(key, ":")[0] {
				case "r":
					want = owned["base"]
				case "c":
					want = owned["combined"]
				}
				if err != nil || id != want || receipt.Operation.ConfigUID != string(config.UID) || receipt.Operation.ConfigName != config.Name || receipt.Operation.SourceUID != string(model.UID) || receipt.Operation.SourceVersion != model.ResourceVersion || accepted[receipt.Operation.ID] != receipt.Operation.TaskID {
					t.Fatal("creator receipt lost task/native/owner/source identity")
				}
			}
			if sourceUpdated && !reflect.DeepEqual(creatorSnapshot, creators) {
				t.Fatal("source update replaced immutable creator receipts")
			}
			creatorSnapshot = creators
			if baselineQualification && len(verifiedBaselines) != 3 {
				t.Fatal("private creation footprint qualification incomplete", len(verifiedBaselines))
			}
			t.Log("three task-bound project/regular/composite creator receipts verified; original receipts preserved across source revisions")
			if baselineQualification {
				want := 2
				if sourceUpdated {
					want = 6
				}
				applied, err := templateAppliedRecords(state)
				wantRows := 2
				if sourceUpdated {
					wantRows = 3
				}
				if err != nil || len(verifiedApplied) != want || len(applied) != wantRows {
					t.Fatal("latest-applied native qualification incomplete", err, len(verifiedApplied), len(applied))
				}
				history, err := templateAppliedHistoryRecords(state)
				wantHistory, wantVersions := 0, 2
				if sourceUpdated {
					wantHistory, wantVersions = 2, 4
				}
				if err != nil || len(history) != wantHistory || len(verifiedVersionReceipts) != wantVersions {
					t.Fatal("native version lineage/archive qualification incomplete", err, len(history), len(verifiedVersionReceipts))
				}
				for _, archived := range history {
					normalized := *archived.DeepCopy()
					normalized.Resource = templateAppliedPrefix + strings.TrimPrefix(archived.Resource, templateAppliedHistoryPrefix)
					if !reflect.DeepEqual(originalVersionReceipts[archived.Operation.ID], normalized) {
						t.Fatal("new source changed an archived native version receipt")
					}
				}
			}
			settled = true
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil || journalState(t, a).Operation != nil {
				t.Fatal("converged input replayed", err)
			}
			t.Log("public template model reached InSync with durable settling, simulated restarts and no-replay", name)
			if updateQualification && !sourceUpdated {
				// Edit the original source through its API, keeping the same owner,
				// project and template UUIDs. This exercises public Update recovery.
				var fresh core.ConfigMap
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(model), &fresh); err != nil {
					t.Fatal(err)
				}
				var updated map[string]any
				if err := json.Unmarshal(raw, &updated); err != nil {
					t.Fatal(err)
				}
				projects := updated["catalyst_center"].(map[string]any)["templates"].(map[string]any)["projects"].([]any)
				projects[0].(map[string]any)["description"] = "CVK isolated updated project"
				updatedRaw, err := json.Marshal(updated)
				if err != nil {
					t.Fatal(err)
				}
				fresh.Data["model.yaml"] = string(updatedRaw)
				fresh.Data[nacmodel.TemplateAssetsKey] = `{"base.j2":"! CVK isolated pacing qualification updated\n"}`
				if err := a.statusWriter.Update(ctx, &fresh); err != nil {
					t.Fatal(err)
				}
				sourceUpdated, settled = true, false
				t.Log("isolated source revision updated; qualifying project/draft updates and member-version recommit")
				continue
			}
			converged = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if !converged {
		t.Fatal("public template model did not converge")
	}
}
