//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
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
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLiveWirelessProfilePublicRecovery(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_PROFILE_RECOVERY") != "isolated-public" {
		t.Skip("isolated public profile/tag recovery disabled")
	}
	qualifyLiveWirelessProfileExecution(t, true, false, "")
}
func TestLiveWirelessPolicyCohortPublicRecovery(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_PROFILE_RECOVERY") != "isolated-public" {
		t.Skip("isolated public profile/tag recovery disabled")
	}
	qualifyLiveWirelessProfileExecution(t, true, true, "")
}

// Reuse the existing generated disabled-SSID/empty-area lifecycle and cleanup.
// Each call observes one expected definition, tag or site-association mutation.
// Physical/device operations do not gain this metadata recovery capability.
type liveNetworkProfileRecovery struct {
	a                *adapter
	config           *configapi.NetworkControllerConfig
	trace            *metadataNoDispatchTrace
	recovered        map[string]bool
	associationTasks map[string]string
	privateTagTasks  map[string]bool
}

func (h *liveNetworkProfileRecovery) finish(t *testing.T, ctx context.Context, c *client, minimum int) {
	t.Helper()
	if h.a == nil || h.trace.pending != "" {
		t.Fatal("public recovery never settled")
	}
	e, err := c.compileNetworkProfileConfiguration(h.config.Spec.Source.Inline.Raw)
	if err != nil {
		t.Fatal(err)
	}
	if next, err := e.next(ctx); err != nil || next != nil {
		t.Fatal("public source still has pending native work", err)
	}
	h.trace.allowMutation = func(*http.Request) bool { return false }
	restore, err := installMetadataSessionTrace(ctx, c, h.trace)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(h.config)}
	for range 3 {
		if _, err := (&configReconciler{a: h.a}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := h.a.statusReader.Get(ctx, req.NamespacedName, h.config); err != nil {
			t.Fatal(err)
		}
	}
	if h.config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(h.recovered) < minimum || len(h.trace.injected) != len(h.recovered) || len(h.trace.retired) != len(h.recovered) || h.trace.mutations != len(h.recovered) {
		t.Fatal("public profile/tag cycle or no-op completion incomplete")
	}
	rows, err := profileSiteApplicationRecords(journalState(t, h.a))
	if err != nil || len(rows) != len(h.associationTasks) {
		t.Fatal("association application receipt count mismatch", err)
	}
	for identity, row := range rows {
		if h.associationTasks[identity] != row.Operation.TaskID {
			t.Fatal("unexpected association application task")
		}
	}
	if os.Getenv("CATC_WIRELESS_TAG_RECEIPTS") == "isolated-private" {
		private, err := wirelessTagRecords(journalState(t, h.a))
		if err != nil {
			t.Fatal(err)
		}
		if len(private) != len(h.privateTagTasks) && ((os.Getenv("CATC_WIRELESS_TAG_MEMBERSHIP") != "isolated-private" && os.Getenv("CATC_WIRELESS_TAG_PRUNE") != "isolated-private") || len(private) > len(h.privateTagTasks)) {
			t.Fatal("private task/receipt count mismatch")
		}
		effects := 0
		for _, row := range private {
			if !h.privateTagTasks[row.Operation.TaskID] {
				t.Fatal("unmatched private task")
			}
			effects += len(row.EffectReceipts)
		}
		t.Logf("%d private tag action receipts with %d member proofs retained; complete producer sets and final InSync/no-op verified", len(private), effects)
	}
	t.Logf("%d association application receipts retained across final no-op reconciliations", len(rows))
	t.Logf("%d public metadata tasks recovered with full trees; %d proven preflight refusals retired, exact mutation counts and final InSync/no-op verified; Kubernetes actors/storage simulated", len(h.recovered), len(h.trace.retired))
}

func (h *liveNetworkProfileRecovery) run(t *testing.T, ctx context.Context, c *client, raw []byte, expected *configurationWrite) *cvk.NetworkControllerConfigurationOperation {
	t.Helper()
	if h.a == nil {
		r, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
		h.a, h.config = r.a, config
		h.a.client = c
		h.config.Spec.Mode = configapi.NetworkControllerApplyModeApply
		h.config.Spec.ManagedSections = []string{"network_profiles"}
		h.config.Spec.Source.ConfigMapRef = nil
		if os.Getenv("CATC_WIRELESS_TAG_RECEIPTS") == "isolated-private" {
			configureWirelessTagComparisonFixture(t, h.a, h.config)
		}
		h.recovered = map[string]bool{}
	}
	if !bytes.Equal(h.config.Spec.Source.Inline.Raw, raw) {
		h.config.Spec.Source.Inline = &runtime.RawExtension{Raw: append([]byte{}, raw...)}
		h.config.Generation++
		if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
			t.Fatal(err)
		}
	}
	if wirelessTagComparisonConfig(h.config) && !wirelessTagPrivateSelected(expected.resource) {
		kind, key, valid := networkProfileMetadataResource(expected.resource)
		if valid && (kind == "site-tag" || kind == "policy-tag" || kind == "policy-cohort") {
			// Freeze the actual private selector before dispatch. A legacy
			// attribute-only plan cannot stand in for v4's dependency digest.
			plan := h.privatePrunePlan(t, ctx, c)
			planned, err := plan.executor.next(ctx)
			if err != nil || planned == nil {
				t.Fatal("private native metadata plan unavailable", err)
			}
			plannedKind, plannedKey, ok := networkProfileMetadataResource(planned.resource)
			if !ok || plannedKind != kind || plannedKey != key || planned.action != expected.action || !reflect.DeepEqual(planned.identity, expected.identity) || !wirelessTagPrivateSelected(planned.resource) {
				t.Fatal("private qualification plan changed the isolated action or native target")
			}
			expected = planned
		}
	}
	if h.trace == nil {
		h.trace = &metadataNoDispatchTrace{a: h.a, section: "network_profiles", preflightPath: wirelessProfilesPath, injected: map[string]bool{}, retired: map[string]bool{}}
	}
	trace := h.trace
	kind, _, _ := networkProfileMetadataResource(expected.resource)
	trace.preflightPath = wirelessProfilesPath
	if kind == "switching-assignment" {
		trace.preflightPath = networkProfilesPath
	}
	trace.allowMutation = func(req *http.Request) bool {
		nc, err := h.a.currentController(req.Context())
		if err != nil || nc.Status.Configuration == nil {
			return false
		}
		op := nc.Status.Configuration.Operation
		if op == nil || !profileSiteSubmissionMatches(op.Resource, expected.resource) || op.Action != expected.action || op.ParentID != expected.identity.parent || op.TargetID != expected.identity.target {
			return false
		}
		if kind == "wireless-assignment" || kind == "switching-assignment" {
			if expected.action == "Delete" {
				return req.Method == http.MethodDelete && req.URL.Path == profileSiteAssignmentsPath(expected.identity.parent)+"/"+expected.identity.target
			}
			return req.Method == http.MethodPost && req.URL.Path == profileSiteAssignmentsPath(expected.identity.parent)
		}
		if req.Method == http.MethodDelete && expected.action == "Delete" && wirelessTagPruneSelected(expected.resource) {
			ns, _, _, valid := wirelessTagPruneParts(expected.resource)
			return valid && req.URL.Path == wirelessProfileTagPath(expected.identity.parent, ns == "wireless-policy-tags/")+"/"+expected.identity.target
		}
		if req.Method != http.MethodPost && req.Method != http.MethodPut {
			return false
		}
		if expected.action == "Create" && req.URL.Path == wirelessProfilesPath {
			return true
		}
		if expected.action == "Update" && req.URL.Path == wirelessProfilesPath+"/"+expected.identity.target {
			return true
		}
		for _, policy := range []bool{false, true} {
			path := wirelessProfileTagPath(expected.identity.parent, policy)
			if expected.action == "Create" && req.URL.Path == path+"/bulk" || expected.action == "Update" && req.URL.Path == path+"/"+expected.identity.target {
				return true
			}
		}
		return false
	}
	restore, err := installMetadataSessionTrace(ctx, c, trace)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	beforeMutations := trace.mutations
	var accepted *cvk.NetworkControllerConfigurationOperation
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(h.config)}
	for step := 0; step < 90; step++ {
		restarted := *h.a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := h.a.statusReader.Get(ctx, req.NamespacedName, h.config); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, h.a)
		if state != nil && state.Operation != nil {
			op := state.Operation
			if !profileSiteSubmissionMatches(op.Resource, expected.resource) || op.Action != expected.action || op.ParentID != expected.identity.parent || op.TargetID != expected.identity.target {
				t.Fatal("public execution escaped the expected isolated metadata action")
			}
			if op.Phase == "Accepted" && accepted == nil {
				accepted = op.DeepCopy()
				if err := h.a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				t.Log("original acknowledged metadata task retained as Unknown", op.Resource, op.TaskID)
			}
			if op.Phase == "Unknown" && op.TaskID == "" {
				t.Fatal("missing acknowledgement; retain exact fixture, no replay")
			}
		}
		if accepted != nil && state.Operation == nil {
			if trace.mutations != beforeMutations+1 || trace.recoveryTrees[accepted.TaskID] < 1 || trace.pending != "" || len(trace.injected) != len(trace.retired) {
				t.Fatalf("native mutation/recovery/refusal proof incomplete: mutation delta=%d tree reads=%d pending refusal=%t injected=%d retired=%d", trace.mutations-beforeMutations, trace.recoveryTrees[accepted.TaskID], trace.pending != "", len(trace.injected), len(trace.retired))
			}
			if profileSiteApplicationSelected(accepted) {
				rows, err := profileSiteApplicationRecords(state)
				if err != nil {
					t.Fatal("invalid association application receipts", err)
				}
				ns, key, _, _ := profileSiteApplicationParts(accepted.Resource)
				row, found := rows[ns+key]
				if !found || row.Operation.ID != accepted.ID || row.Operation.TaskID != accepted.TaskID || row.Operation.ParentID != accepted.ParentID || row.Operation.TargetID != accepted.TargetID {
					t.Fatal("settled association lacks its exact application receipt")
				}
				if h.associationTasks == nil {
					h.associationTasks = map[string]string{}
				}
				h.associationTasks[ns+key] = accepted.TaskID
			}
			if wirelessTagPrivateSelected(accepted.Resource) {
				rows, err := wirelessTagRecords(state)
				if err != nil {
					t.Fatal("invalid private tag receipts", err)
				}
				found := false
				for _, row := range rows {
					if row.Operation.ID == accepted.ID && row.Operation.TaskID == accepted.TaskID {
						found = true
					}
				}
				if !found {
					t.Fatal("original tag task lacks its atomic private receipt")
				}
				if h.privateTagTasks == nil {
					h.privateTagTasks = map[string]bool{}
				}
				h.privateTagTasks[accepted.TaskID] = true
			}
			h.recovered[accepted.TaskID] = true
			t.Log("public refusal retirement, original full-tree recovery and readback verified", accepted.Resource, accepted.TaskID)
			return accepted
		}
		select {
		case <-ctx.Done():
			h.captureUnsettledProfile(t)
			t.Fatal("profile metadata recovery timed out; retain fixtures", ctx.Err())
		case <-time.After(time.Second):
		}
	}
	h.captureUnsettledProfile(t)
	t.Fatal("public metadata operation did not settle; retain fixtures")
	return nil
}

// Qualification and callers with a legacy planned spelling may match a new
// public submission by logical pair. Two versioned predecessors are never
// interchangeable; a supplied versioned plan must match exactly.
func profileSiteSubmissionMatches(current, planned string) bool {
	if current == planned {
		return true
	}
	if wirelessTagPrivateSelected(current) {
		canonical, _, _, valid := wirelessTagPrivateParts(current)
		return valid && !wirelessTagPrivateSelected(planned) && canonical == planned
	}
	ns, _, _, ok := profileSiteApplicationParts(current)
	return ok && strings.HasPrefix(planned, ns) && !profileSiteApplicationSelected(&cvk.NetworkControllerConfigurationOperation{Resource: planned}) && profileSiteResourceMatches(strings.TrimPrefix(current, ns), strings.TrimPrefix(planned, ns))
}

// Retain the complete profile definition while removing only association/tag
// selections. Tags are independently cleaned by the existing exact-ID fixture;
// CVK must refuse unassignment until those consumers have disappeared.
func (h *liveNetworkProfileRecovery) preparePrune(t *testing.T, ctx context.Context) {
	t.Helper()
	var model map[string]any
	dec := json.NewDecoder(bytes.NewReader(h.config.Spec.Source.Inline.Raw))
	dec.UseNumber()
	if dec.Decode(&model) != nil {
		t.Fatal("invalid native profile source")
	}
	root, ok := model["catalyst_center"].(map[string]any)
	if !ok {
		t.Fatal("missing Catalyst Center source")
	}
	profiles, ok := root["network_profiles"].(map[string]any)
	if !ok {
		t.Fatal("missing network profile source")
	}
	for _, kind := range []string{"switching", "wireless"} {
		if items, exists := profiles[kind]; exists {
			rows, ok := items.([]any)
			if !ok {
				t.Fatal("invalid profile collection")
			}
			for _, item := range rows {
				p, ok := item.(map[string]any)
				if !ok {
					t.Fatal("invalid profile")
				}
				for _, key := range []string{"sites", "site_tags", "policy_tags"} {
					delete(p, key)
				}
			}
		}
	}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(raw, h.config.Spec.Source.Inline.Raw) && h.config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return
	}
	h.config.Spec.Source.Inline.Raw = raw
	h.config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	h.config.Generation++
	if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
		t.Fatal(err)
	}
}
func (h *liveNetworkProfileRecovery) assertPruneReferenced(t *testing.T, ctx context.Context, c *client) {
	t.Helper()
	h.preparePrune(t, ctx)
	if wirelessTagComparisonConfig(h.config) {
		plan := h.privatePrunePlan(t, ctx, c)
		next, err := plan.executor.next(ctx)
		if err != nil || next == nil || next.action != "Delete" || !wirelessTagPruneSelected(next.resource) {
			t.Fatal("owned tag references must be pruned before unassignment", err)
		}
		t.Log("owned tag DELETE is eligible first; no prospective association DELETE admitted")
		return
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	h.trace.base = base
	h.trace.allowMutation = func(*http.Request) bool { return false }
	rest.HTTPClient.Transport = h.trace
	defer func() { rest.HTTPClient.Transport = base }()
	before := h.trace.mutations
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(h.config)}
	for range 3 {
		if _, err := (&configReconciler{a: h.a}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		if err := h.a.statusReader.Get(ctx, req.NamespacedName, h.config); err != nil {
			t.Fatal(err)
		}
	}
	found := false
	for _, condition := range h.config.Status.Conditions {
		if condition.Reason == "ProfileSitePruneReferenced" {
			found = true
		}
	}
	if !found || h.trace.mutations != before || journalState(t, h.a).Operation != nil {
		t.Fatal("referenced native association was not refused without mutation")
	}
	t.Log("public native unassignment refused while actual site/policy tags still reference its sites; no mutation submitted")
}
func (h *liveNetworkProfileRecovery) prune(t *testing.T, ctx context.Context, c *client, expected int) {
	t.Helper()
	h.preparePrune(t, ctx)
	removed := 0
	for step := 0; step <= expected; step++ {
		nc, err := h.a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		source, err := loadControllerSource(ctx, h.a.statusReader, h.config)
		if err != nil {
			t.Fatal(err)
		}
		var plan configurationApplyPlan
		if wirelessTagComparisonConfig(h.config) {
			plan, err = h.a.compileWirelessTagComparison(nc, h.config, source)
		} else {
			plan, err = c.compileConfigurationApply(source.raw, []string{"network_profiles"}, source.templateFiles)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := h.a.bindProfileSiteApplications(nc, h.config, source, &plan); err != nil {
			t.Fatal(err)
		}
		if err := h.a.bindWirelessTagReceipts(nc, h.config, source, &plan); err != nil {
			t.Fatal(err)
		}
		h.a.bindNetworkProfileTaskRecovery(h.config, source, &plan)
		if err := h.a.bindProfileSitePrune(nc, h.config, source, &plan); err != nil {
			t.Fatal(err)
		}
		write, err := plan.executor.next(ctx)
		if err != nil {
			t.Fatal("native unassignment planning refused; retain fixtures", err)
		}
		if write == nil {
			break
		}
		if write.action != "Delete" || !profileSiteRemovalSelected(&cvk.NetworkControllerConfigurationOperation{Resource: write.resource}) || removed >= expected {
			t.Fatal("unassignment escaped generated receipt scope")
		}
		h.run(t, ctx, c, h.config.Spec.Source.Inline.Raw, write)
		removed++
	}
	if removed != expected {
		t.Fatal("native unassignment count mismatch")
	}
	rows, err := profileSiteApplicationRecords(journalState(t, h.a))
	if err != nil || len(rows) != len(h.associationTasks) {
		t.Fatal("native tombstone count mismatch", err)
	}
	for _, row := range rows {
		if row.Operation.Action != "Delete" {
			t.Fatal("latest association evidence is not a removal tombstone")
		}
	}
	h.finish(t, ctx, c, len(h.recovered))
	t.Logf("%d manifest-driven native unassignments completed with atomic tombstones, original task trees and final InSync/no-op", removed)
}
