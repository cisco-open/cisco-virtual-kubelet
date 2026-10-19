// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Real appliance requests through the public reconciler, with simulated
// Kubernetes persistence and worker restarts. This is not installed-actor or
// API-server admission qualification.
type liveSiteManifest struct {
	a             *adapter
	config        *configapi.NetworkControllerConfig
	singular      bool
	recovered     map[string]bool
	resolveSource func([]byte) []byte
}

// Simulate a worker retaining an acknowledged task as Unknown. The appliance
// task remains untouched; the restarted public reconciler must observe it.
func (h *liveSiteManifest) injectSiteTaskUncertainty(t *testing.T, ctx context.Context) {
	t.Helper()
	if !h.singular || os.Getenv("CATC_SITE_TASK_RECOVERY") != "isolated-acknowledged-tasks" {
		return
	}
	state := journalState(t, h.a)
	if state == nil || state.Operation == nil || state.Operation.Phase != "Accepted" {
		return
	}
	op := state.Operation
	if h.recovered[op.ID] {
		return
	}
	if !(siteSingularOperation(op) || siteDesignOperation(op) || siteRetirementKnownTask(op)) || !validAPIID(op.TaskID) || op.SubmittedAt == nil {
		t.Fatal("cannot inject site uncertainty without acknowledged singular task")
	}
	if err := h.a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
		t.Fatal(err)
	}
	if h.recovered == nil {
		h.recovered = map[string]bool{}
	}
	h.recovered[op.ID] = true
	t.Log("retained acknowledged singular-site task as Unknown for restarted observation")
}

type liveSiteCreateReceipt struct {
	task      Task
	submitted time.Time
}

// One complete singular hierarchy source, with the expected parent-first task
// sequence checked across worker restarts. Does not synthesize native receipts.
func (h *liveSiteManifest) runCreates(t *testing.T, ctx context.Context, paths []string) []liveSiteCreateReceipt {
	actions := make([]string, len(paths))
	for i := range actions {
		actions[i] = "Create"
	}
	return h.runSiteActions(t, ctx, paths, actions)
}

// Drive public reconciliation without submitting or settling a task directly.
// The expected sequence is checked against every retained operation.
func (h *liveSiteManifest) runSiteActions(t *testing.T, ctx context.Context, paths, actions []string) []liveSiteCreateReceipt {
	t.Helper()
	if len(paths) != len(actions) {
		t.Fatal("invalid expected site action sequence")
	}
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(h.config)}
	seen := map[string]bool{}
	receipts := []liveSiteCreateReceipt{}
	for step := 0; step < 160; step++ {
		restarted := *h.a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal("singular hierarchy reconcile failed", err)
		}
		state := journalState(t, h.a)
		if state.Operation != nil {
			op := state.Operation
			index := len(receipts)
			if seen[op.ID] {
				index--
			}
			pathKey := siteResourcePathDigest(op.Resource)
			if siteRetirementOperation(op) {
				pathKey = strings.Split(strings.TrimPrefix(op.Resource, siteRetirementPrefix), ":")[1]
			}
			resourceMatches := index >= 0 && index < len(paths) && pathKey == sitePathDigest(paths[index])
			if !resourceMatches || op.Action != actions[index] || op.Section != "sites" || op.ConfigUID != string(h.config.UID) || op.ConfigGeneration != h.config.Generation {
				t.Fatal("singular hierarchy escaped its retained operation sequence")
			}
			if op.TaskID != "" && !seen[op.ID] {
				if op.SubmittedAt == nil {
					t.Fatal("singular hierarchy task timestamp missing")
				}
				seen[op.ID] = true
				receipts = append(receipts, liveSiteCreateReceipt{Task{ID: op.TaskID}, op.SubmittedAt.Time})
				t.Log("singular manifest create task", op.TaskID)
			}
		}
		h.injectSiteTaskUncertainty(t, ctx)
		if err := h.a.statusReader.Get(ctx, req.NamespacedName, h.config); err != nil {
			t.Fatal(err)
		}
		if h.config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			if len(receipts) != len(paths) || state.Operation != nil || len(state.Owners) != 1 || state.Owners[0].ConfigUID != string(h.config.UID) {
				t.Fatal("singular hierarchy completed without all task/owner bindings")
			}
			return receipts
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("singular hierarchy did not converge within bounded reconciliation")
	return nil
}

func newLiveSiteManifest(t *testing.T, c *client, desired []desiredSite) *liveSiteManifest {
	t.Helper()
	r, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	a := r.a
	a.client = c
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	bulkSiteManifest(t, a, config, desired)
	return &liveSiteManifest{a: a, config: config}
}

func (h *liveSiteManifest) update(t *testing.T, ctx context.Context, desired []desiredSite) {
	t.Helper()
	if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), h.config); err != nil {
		t.Fatal(err)
	}
	h.config.Generation++
	if ref := h.config.Spec.Source.ConfigMapRef; ref != nil {
		var cm corev1.ConfigMap
		if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKey{Namespace: h.config.Namespace, Name: ref.Name}, &cm); err != nil {
			t.Fatal(err)
		}
		raw, err := siteManifestModel(desired)
		if err != nil {
			t.Fatal(err)
		}
		if h.resolveSource != nil {
			raw = h.resolveSource(raw)
		}
		cm.Data[ref.Key] = string(raw)
		if err := h.a.statusWriter.Update(ctx, &cm); err != nil {
			t.Fatal(err)
		}
		if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
			t.Fatal(err)
		}
	} else {
		bulkSiteManifest(t, h.a, h.config, desired)
		if h.resolveSource != nil {
			h.config.Spec.Source.Inline.Raw = h.resolveSource(h.config.Spec.Source.Inline.Raw)
		}
	}
	if h.resolveSource != nil {
		h.config.Spec.ModelSource.Exporter = SiteDefaultsExporter
	}
	if h.singular {
		h.config.Annotations[siteBulkAnnotation] = "false"
		if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
			t.Fatal(err)
		}
	}
}

func (h *liveSiteManifest) run(t *testing.T, ctx context.Context, expectedResource string, action string) (Task, time.Time) {
	t.Helper()
	a, config := h.a, h.config
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	var accepted Task
	var submitted time.Time
	synchronousImage := strings.HasPrefix(expectedResource, "floor-image-sync:")
	for step := 0; step < 120; step++ {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal("public manifest reconcile", err)
		}
		state := journalState(t, a)
		if state.Operation != nil {
			op := state.Operation
			resourceMatches := op.Resource == expectedResource
			if plannedAPResourceKey(expectedResource) != "" {
				resourceMatches = plannedAPResourceKey(op.Resource) == plannedAPResourceKey(expectedResource)
			}
			if validSitePath(expectedResource) {
				resourceMatches = siteResourcePathDigest(op.Resource) == sitePathDigest(expectedResource)
			}
			if expectedResource == siteBulkResourcePrefix {
				resourceMatches = strings.HasPrefix(op.Resource, siteBulkResourcePrefix)
			}
			if !resourceMatches || op.Action != action || op.Section != "sites" || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation {
				t.Fatal("unexpected public journal binding; retain fixture")
			}
			if op.TaskID != "" && op.SubmittedAt != nil {
				accepted.ID, submitted = op.TaskID, op.SubmittedAt.Time
			}
			if synchronousImage && op.SubmittedAt != nil {
				submitted = op.SubmittedAt.Time
			}
		}
		h.injectSiteTaskUncertainty(t, ctx)
		var current configapi.NetworkControllerConfig
		if err := a.statusReader.Get(ctx, req.NamespacedName, &current); err != nil {
			t.Fatal(err)
		}
		if current.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			if (!synchronousImage && accepted.ID == "") || submitted.IsZero() || state.Operation != nil || len(state.Owners) != 1 || state.Owners[0].ConfigUID != string(config.UID) {
				t.Fatal("manifest completed without retained owner/accepted task evidence")
			}
			if synchronousImage {
				found := false
				for _, receipt := range state.ActionApplications {
					if receipt.Operation.Resource == expectedResource && receipt.Operation.Phase == "Verified" && receipt.Operation.TaskID == "" && receipt.VerifiedAt != nil {
						found = true
					}
				}
				if !found {
					t.Fatal("synchronous image completed without exact retained response receipt")
				}
			}
			// A second pass proves converged input keeps its owner and does
			// not create a new operation after the completed slot is cleared.
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
			if journalState(t, a).Operation != nil {
				t.Fatal("converged manifest created another operation")
			}
			t.Logf("public manifest %s reached InSync across simulated worker restarts; journal cleared, owner retained, no-op verified", action)
			return accepted, submitted
		}
		if current.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
			t.Fatal("public manifest failed; retain fixture")
		}
		if (isFloorImageOperation("sites", expectedResource) || strings.HasPrefix(expectedResource, "planned-ap:")) && state.Operation != nil && state.Operation.Phase == "Unknown" {
			t.Fatalf("floor design outcome retained without replay: operation=%s target=%s task=%s", state.Operation.ID, state.Operation.TargetID, state.Operation.TaskID)
		}
		if step%20 == 0 {
			phase, task := "none", ""
			if state.Operation != nil {
				phase, task = state.Operation.Phase, state.Operation.TaskID
			}
			reasons := []string{}
			for _, condition := range current.Status.Conditions {
				reasons = append(reasons, condition.Reason)
			}
			t.Logf("public manifest step=%d status=%s operation=%s task=%s reasons=%v", step, current.Status.Phase, phase, task, reasons)
		}
		select {
		case <-ctx.Done():
			t.Fatal("public manifest timed out; retain fixture", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatal("public manifest did not converge; retain fixture")
	return Task{}, time.Time{}
}
