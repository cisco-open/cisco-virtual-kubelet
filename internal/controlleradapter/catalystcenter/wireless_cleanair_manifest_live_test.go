// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controller"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Native service and the public config reconciler; Kubernetes status persistence
// is a fixture here and is separately qualified by the real-API envtest.
func TestLiveWirelessCleanAirManifestBands(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_CLEANAIR_MANIFEST") != "isolated-definition" {
		t.Skip("explicit isolated definition test not enabled")
	}
	timeout := 5 * time.Minute
	if os.Getenv("CATC_WIRELESS_FEATURE_NO_DISPATCH") == "owned-definitions" {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := liveClient(t)
	baseline, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil {
		t.Fatal(err)
	}
	mixed := os.Getenv("CATC_WIRELESS_FEATURE_MIXED") == "isolated-definitions"
	proveCreates := os.Getenv("CATC_WIRELESS_FEATURE_CREATE_PROOF") == "isolated-definitions"
	if proveCreates && !mixed {
		t.Fatal("create-result qualification requires the combined isolated flow")
	}
	publicPrune := os.Getenv("CATC_WIRELESS_FEATURE_PRUNE") == "owned-definitions"
	if publicPrune {
		if !mixed || !proveCreates {
			t.Fatal("public pruning qualification requires isolated combined ownership flow")
		}
		profiles, err := c.wirelessProfileInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, profile := range profiles {
			if _, known := profile.fields["featureTemplates"].([]map[string]any); profile.unmapped || !known {
				t.Fatal("reference metadata incomplete before fixture creation")
			}
		}
	}
	var fraBaseline map[string]wirelessFeatureInstance
	if mixed {
		fraBaseline, err = c.wirelessFeatureInstances(ctx, wirelessFRAKind)
		if err != nil {
			t.Fatal(err)
		}
	}
	name := "CVK_MB_" + time.Now().UTC().Format("02150405")
	fraName := name + "_FRA"
	if mixed {
		if _, exists := fraBaseline[fraName]; exists {
			t.Fatal("FRA fixture already exists")
		}
	}
	if _, exists := baseline[name]; exists {
		t.Fatal("fixture already exists")
	}
	evidence, err := os.MkdirTemp("", "cvk-cleanair-manifest-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("private journal evidence", evidence, "generated definition", name)
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &cleanAirManifestTrace{base: base, name: name}
	rest.HTTPClient.Transport = trace
	var fraTrace *fraManifestTrace
	if mixed {
		fraTrace = &fraManifestTrace{base: base, fallback: trace, name: fraName}
		rest.HTTPClient.Transport = fraTrace
	}
	a, config := journalFixture(t)
	a.client = c
	config.Spec.ManagedSections = []string{"feature_templates"}
	intentWriter := a.statusWriter
	var lifecycleManager *controller.NetworkControllerReconciler
	configDeletion := os.Getenv("CATC_WIRELESS_FEATURE_CONFIG_DELETION") == "owned-definitions"
	if configDeletion {
		if !mixed || !proveCreates || publicPrune {
			t.Fatal("config deletion requires an isolated combined flow without source-prune mode")
		}
		lifecycleManager, intentWriter = featureLifecycleManagerFixture(t, a, config)
	}
	key := ctrlclient.ObjectKeyFromObject(config)
	save := func(file string, value any) {
		t.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(evidence+"/"+file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if os.Getenv("CATC_WIRELESS_FEATURE_NO_DISPATCH") == "owned-definitions" {
		if !mixed || !proveCreates || !publicPrune && !configDeletion {
			t.Fatal("feature no-dispatch qualification requires mixed owned execution and public cleanup")
		}
		faults := installFeatureNoDispatchTrace(t, a, base, save)
		base = faults
		trace.base = faults
		if fraTrace != nil {
			fraTrace.base = faults
		}
	}
	save("fixture.json", map[string]string{"name": name, "fraName": fraName})
	save("baseline.json", baseline)
	if mixed {
		save("fra-baseline.json", fraBaseline)
	}
	id := ""
	var lastFRA wirelessFRA
	recoverUpdates := os.Getenv("CATC_WIRELESS_CLEANAIR_RECOVERY") == "acknowledged-update"
	recoverCreates := os.Getenv("CATC_WIRELESS_FEATURE_CREATOR_RECOVERY") == "acknowledged-create"
	recovered := map[string]bool{}
	createRefs := map[string]wirelessFeatureTaskReference{}
	nativeStatesChecked := 0
	checkNativeState := func(index int, kind, design, id, fingerprint string) {
		t.Helper()
		state := journalState(t, a)
		records, err := featureCreatedRecords(state)
		prefix := featureCreatedPrefix
		if index > 0 {
			records, err = featureAppliedRecords(state)
			prefix = featureAppliedPrefix
		}
		if err != nil {
			t.Fatal(err)
		}
		key := kind + ":" + featureIdentityDigest(design)
		r, exists := records[key]
		gotID, gotHash, err := featureReceiptIdentity(r.Resource, prefix, key)
		if !exists || err != nil || gotID != id || gotHash != fingerprint || !validWirelessFeatureFingerprint(gotHash) {
			t.Fatal("stored state fingerprint differs from native readback", design, err)
		}
		nativeStatesChecked++
	}

	caProbePuts, fraProbePuts := 0, 0
	for index, band := range []string{"2.4GHz", "5GHz", "6GHz", "2.4GHz", "6GHz", "5GHz", "2.4GHz"} {
		trace.maxPuts = index + caProbePuts
		config.Generation++
		caProjection := []byte(fmt.Sprintf(`{"catalyst_center":{"feature_templates":{"wireless":{"cleanair":[{"name":%q,"radio_band":%q,"enable":true,"description":"CVK manifest band qualification"}]}}}}`, name, band))
		config.Spec.Source.Inline.Raw = caProjection
		var fraProjection []byte
		if mixed {
			fraTrace.maxPuts = index + fraProbePuts
			state := wirelessFRAQualificationStates()[[]int{0, 2, 8, 1, 8, 7, 0}[index]]
			pair := "2.4GHz_5GHz"
			if state.Band == "5GHZ_6GHZ" {
				pair = "5GHz_6GHz"
			}
			fra := map[string]any{"name": fraName, "radio_band": pair, "fra_interval": state.Interval, "fra_freeze": state.Freeze, "fra_status": state.Status}
			if state.Sensitivity != "" {
				fra["fra_sensitivity"] = state.Sensitivity
			}
			var root map[string]any
			if err = json.Unmarshal(caProjection, &root); err != nil {
				t.Fatal(err)
			}
			wireless := root["catalyst_center"].(map[string]any)["feature_templates"].(map[string]any)["wireless"].(map[string]any)
			wireless["rrm_fra"] = []any{fra}
			config.Spec.Source.Inline.Raw, err = json.Marshal(root)
			if err != nil {
				t.Fatal(err)
			}
			fraProjection, err = json.Marshal(map[string]any{"catalyst_center": map[string]any{"feature_templates": map[string]any{"wireless": map[string]any{"rrm_fra": []any{fra}}}}})
			if err != nil {
				t.Fatal(err)
			}
		}
		if err = intentWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		converged := false
		for range 80 {
			restarted := *a
			if _, err = (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal("reconcile uncertain; retain generated definition", name, err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if proveCreates && nc.Status.Configuration != nil {
				op := nc.Status.Configuration.Operation
				if op != nil && op.Action == "Create" && op.Phase == "Accepted" {
					key, design := "", ""
					ownedKey, ownedMarker := featureCreateKey(op.Resource)
					if op.Resource == "cleanair/cleanair:"+name || ownedMarker && ownedKey == "ca:"+featureIdentityDigest(name) {
						key, design = "ca", name
					}
					if op.Resource == "rrm_fra/rrm-fra:"+fraName || ownedMarker && ownedKey == "fra:"+featureIdentityDigest(fraName) {
						key, design = "fra", fraName
					}
					if key == "" || op.SubmittedAt == nil || op.TargetID != "" || !validAPIID(op.TaskID) {
						t.Fatal("create task reference malformed; retain joint fixture", op.Resource)
					}
					ref := wirelessFeatureTaskReference{ID: op.TaskID, Name: design, Action: op.Action, Submitted: op.SubmittedAt.Time}
					if old, exists := createRefs[key]; exists && old != ref {
						t.Fatal("create task reference changed", key)
					}
					createRefs[key] = ref
					save("create-reference-"+key+".json", ref)
				}
			}
			if (recoverUpdates || recoverCreates) && nc.Status.Configuration != nil {
				op := nc.Status.Configuration.Operation
				if op != nil && (op.Action == "Update" && recoverUpdates || op.Action == "Create" && recoverCreates) && op.Phase == "Accepted" && !recovered[op.ID] {
					op.Phase = "Unknown"
					raw, err := json.Marshal(nc)
					if err != nil {
						t.Fatal(err)
					}
					restored := &cvk.NetworkController{}
					if err = json.Unmarshal(raw, restored); err != nil {
						t.Fatal(err)
					}
					if err = a.statusWriter.Status().Update(ctx, restored); err != nil {
						t.Fatal("retained-task qualification status uncertain", name, err)
					}
					recovered[op.ID] = true
					nc = restored
					save(fmt.Sprintf("unknown-%d-%s.json", index, op.ID), nc)
					t.Log("serialized acknowledged operation retained Unknown", op.Action, op.TargetID, op.TaskID)
				}
			}
			save("controller.json", nc)
			if err = a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			save("config.json", config)
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
				t.Fatal("manifest failed; retain generated definition", name, config.Status)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				converged = true
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("manifest timed out; retain generated definition", name)
			case <-time.After(100 * time.Millisecond):
			}
		}
		if !converged {
			t.Fatal("manifest did not converge; retain generated definition", name, band, config.Status)
		}
		instances, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
		if err != nil {
			t.Fatal(err)
		}
		instance, exists := instances[name]
		if !exists || !validAPIID(instance.ID) || *instance.System || id != "" && id != instance.ID {
			t.Fatal("manifest UUID changed; retain generated definition", name)
		}
		id = instance.ID
		trace.id = id
		if trace.posts != 1 || trace.puts != index+caProbePuts || trace.attempts != index+1+caProbePuts {
			t.Fatal("manifest dispatched repeated native mutations", name, trace.posts, trace.puts, trace.attempts)
		}
		delete(instances, name)
		if !reflect.DeepEqual(baseline, instances) {
			t.Fatal("unrelated baseline changed; retain generated definition", name, id)
		}
		desired, err := parseWirelessCleanAir(caProjection)
		if err != nil {
			t.Fatal(err)
		}
		detail, fingerprint, err := c.wirelessCleanAirReadState(ctx, instance)
		if err != nil || !wirelessCleanAirMatches(desired[0], detail) {
			t.Fatal("manifest native readback", name, band, err)
		}
		checkNativeState(index, "ca", name, id, fingerprint)
		save(fmt.Sprintf("converged-%d.json", index), detail)
		t.Log("manifest InSync", band, id)
		if mixed {
			current, err := c.wirelessFeatureInstances(ctx, wirelessFRAKind)
			if err != nil {
				t.Fatal(err)
			}
			instance, exists := current[fraName]
			if !exists || !validAPIID(instance.ID) || *instance.System || fraTrace.id != "" && fraTrace.id != instance.ID {
				t.Fatal("FRA UUID changed; retain joint fixture", fraName)
			}
			fraTrace.id = instance.ID
			delete(current, fraName)
			if !reflect.DeepEqual(fraBaseline, current) {
				t.Fatal("FRA baseline changed; retain joint fixture", fraName)
			}
			desired, err := parseWirelessFRA(fraProjection)
			if err != nil {
				t.Fatal(err)
			}
			detail, fingerprint, err := c.wirelessFRAReadState(ctx, instance)
			if err != nil || !wirelessFRAMatches(desired[0], detail) {
				t.Fatal("FRA manifest native readback", fraName, err)
			}
			if fraTrace.posts != 1 || fraTrace.puts != index+fraProbePuts || fraTrace.attempts != index+1+fraProbePuts {
				t.Fatal("repeated FRA mutation", fraName, fraTrace.posts, fraTrace.puts, fraTrace.attempts)
			}
			checkNativeState(index, "fra", fraName, instance.ID, fingerprint)
			lastFRA = detail
			save(fmt.Sprintf("fra-converged-%d.json", index), detail)
			t.Log("joint FRA InSync", detail.Attributes, detail.ID)
		}
		if proveCreates && index == 0 {
			if len(createRefs) != 2 {
				t.Fatal("missing original create receipts", len(createRefs))
			}
			caID, done, err := verifyWirelessFeatureCreatedResult(ctx, c, wirelessCleanAirKind, createRefs["ca"], desired[0], c.wirelessCleanAirRead, wirelessCleanAirMatches, time.Now())
			if err != nil || !done || caID != trace.id {
				t.Fatal("CA create-result identity unproven; retain joint fixture", caID, err)
			}
			fraWant, err := parseWirelessFRA(fraProjection)
			if err != nil {
				t.Fatal(err)
			}
			fraID, done, err := verifyWirelessFeatureCreatedResult(ctx, c, wirelessFRAKind, createRefs["fra"], fraWant[0], c.wirelessFRARead, wirelessFRAMatches, time.Now())
			if err != nil || !done || fraID != fraTrace.id {
				t.Fatal("FRA create-result identity unproven; retain joint fixture", fraID, err)
			}
			save("create-results-proven.json", map[string]any{"caTask": createRefs["ca"].ID, "caID": caID, "fraTask": createRefs["fra"].ID, "fraID": fraID, "durableOwnershipPublished": true})
			t.Log("both task-returned create UUIDs match original receipts, selected kinds and manifest fields", caID, fraID)
		}
		if mixed && index == 0 && os.Getenv("CATC_WIRELESS_FEATURE_COHORT_PROBE") == "isolated-definitions" {
			qualifyWirelessFeatureCompanionFence(t, ctx, c, config.Spec.Source.Inline.Raw, trace, fraTrace, detail, lastFRA, save)
			caProbePuts, fraProbePuts = 2, 2
		}
		// An unchanged reconcile must leave the durable operation boundary released.
		restarted := *a
		if _, err = (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if state := journalState(t, a); state.Operation != nil {
			t.Fatal("manifest no-op acquired another operation", state.Operation)
		}
		if err = a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
	}
	expectedRecoveries := 0
	resources := 1
	if mixed {
		resources = 2
	}
	if recoverUpdates {
		expectedRecoveries += 6 * resources
	}
	if recoverCreates {
		expectedRecoveries += resources
	}
	if (recoverUpdates || recoverCreates) && len(recovered) != expectedRecoveries {
		t.Fatal("missing native known-task recovery", name, len(recovered))
	}
	state := journalState(t, a)
	creatorRecords, err := featureCreatedRecords(state)
	if err != nil || len(creatorRecords) != resources {
		t.Fatal("missing durable creator records", err, len(creatorRecords))
	}
	for _, record := range creatorRecords {
		if proveCreates {
			found := false
			for _, ref := range createRefs {
				if record.Operation.TaskID == ref.ID {
					found = true
				}
			}
			if !found {
				t.Fatal("update replaced original creator task")
			}
		}
		if record.Operation.ConfigUID != string(config.UID) || record.Operation.ConfigName != config.Name {
			t.Fatal("creator record has a foreign manifest owner")
		}
	}
	appliedRecords, err := featureAppliedRecords(state)
	if err != nil || len(appliedRecords) != resources || len(state.ActionApplications) != 2*resources {
		t.Fatal("missing bounded applied state", err, len(appliedRecords))
	}
	for _, record := range appliedRecords {
		if record.Operation.ConfigGeneration != config.Generation || record.Operation.ConfigUID != string(config.UID) || record.Operation.Action != "Update" {
			t.Fatal("stale or foreign applied state")
		}
	}
	save("applied-records.json", appliedRecords)
	save("creator-records.json", state.ActionApplications)
	if configDeletion {
		qualifyLiveFeatureConfigDeletion(t, ctx, a, config, lifecycleManager, intentWriter, trace, fraTrace, baseline, fraBaseline, save, len(recovered))
		return
	}
	if publicPrune {
		trace.allowDelete = true
		fraTrace.allowDelete = true
		desiredBeforePrune := append([]byte(nil), config.Spec.Source.Inline.Raw...)
		tombstones := qualifyLiveFeaturePrune(t, ctx, a, config, save)
		after, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
		if err != nil || !reflect.DeepEqual(after, baseline) {
			t.Fatal("public prune did not restore CleanAir baseline", err)
		}
		fraAfter, err := c.wirelessFeatureInstances(ctx, wirelessFRAKind)
		if err != nil || !reflect.DeepEqual(fraAfter, fraBaseline) {
			t.Fatal("public prune did not restore FRA baseline", err)
		}
		final := journalState(t, a)
		creators, err := featureCreatedRecords(final)
		if err != nil || !reflect.DeepEqual(creators, creatorRecords) {
			t.Fatal("prune changed creators", err)
		}
		applied, err := featureAppliedRecords(final)
		if err != nil || !reflect.DeepEqual(applied, appliedRecords) {
			t.Fatal("prune changed applied records", err)
		}
		if trace.posts != 1 || trace.puts != 6+caProbePuts || trace.deletes != 1 || trace.attempts != 8+caProbePuts || fraTrace.posts != 1 || fraTrace.puts != 6+fraProbePuts || fraTrace.deletes != 1 || fraTrace.attempts != 8+fraProbePuts {
			t.Fatal("public prune replayed native mutation")
		}
		if os.Getenv("CATC_WIRELESS_FEATURE_RECREATE") == "owned-definitions" {
			save("initial-prune-complete.json", final)
			qualifyLiveFeatureRecreateCycles(t, ctx, c, a, config, desiredBeforePrune, name, fraName, baseline, fraBaseline, base, save)
			return
		}
		save("complete.json", map[string]any{"name": name, "id": id, "fraID": fraTrace.id, "cleaned": true, "manifestRevisions": 8, "knownTaskRecoveries": len(recovered) + len(tombstones), "nativeStatesChecked": nativeStatesChecked, "creatorRecords": len(creators), "appliedRecords": len(applied), "tombstoneRecords": len(tombstones), "publicPrune": true, "posts": trace.posts, "puts": trace.puts, "deletes": trace.deletes, "mutationAttempts": trace.attempts, "fraPosts": fraTrace.posts, "fraPuts": fraTrace.puts, "fraDeletes": fraTrace.deletes, "fraMutationAttempts": fraTrace.attempts})
		t.Log("public manifest pruning, retained Delete recovery and tombstones verified; both native baselines restored", id, fraTrace.id)
		return
	}
	// Qualification-only teardown of this never-associated generated definition.
	instances, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil {
		t.Fatal(err)
	}
	instance, exists := instances[name]
	if !exists || instance.ID != id || *instance.System {
		t.Fatal("cleanup identity changed", name, id)
	}
	delete(instances, name)
	if !reflect.DeepEqual(baseline, instances) {
		t.Fatal("cleanup baseline changed", name, id)
	}
	trace.allowDelete = true
	started := time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, wirelessCleanAirPath+"/"+url.PathEscape(id), nil)
	if err != nil {
		t.Fatal("cleanup submission uncertain", name, id, err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("cleanup receipt uncertain", name, id)
	}
	save("cleanup-task.json", receipt.Response)
	if err = waitSiteCredentialTask(ctx, c, receipt.Response, started); err != nil {
		t.Fatal("cleanup task incomplete", name, id, err)
	}
	after, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil || !reflect.DeepEqual(baseline, after) {
		t.Fatal("cleanup baseline not restored", name, id, err)
	}
	if trace.posts != 1 || trace.puts != 6+caProbePuts || trace.deletes != 1 || trace.attempts != 8+caProbePuts {
		t.Fatal("native mutation count mismatch", trace.posts, trace.puts, trace.deletes, trace.attempts)
	}
	if mixed {
		cleanupLiveFRAFeature(t, ctx, c, fraTrace, fraBaseline, lastFRA, save, fraProbePuts)
	}
	complete := map[string]any{"name": name, "id": id, "cleaned": true, "manifestRevisions": 7, "knownTaskRecoveries": len(recovered), "posts": trace.posts, "puts": trace.puts, "deletes": trace.deletes, "mutationAttempts": trace.attempts, "caProbePuts": caProbePuts, "fraProbePuts": fraProbePuts, "createResultsProven": proveCreates, "creatorRecords": len(creatorRecords), "appliedRecords": len(appliedRecords), "nativeStatesChecked": nativeStatesChecked, "durableOwnershipPublished": true}
	if mixed {
		complete["fraID"] = fraTrace.id
		complete["fraPosts"] = fraTrace.posts
		complete["fraPuts"] = fraTrace.puts
		complete["fraDeletes"] = fraTrace.deletes
		complete["fraMutationAttempts"] = fraTrace.attempts
	}
	save("complete.json", complete)
	t.Log("exact generated UUID cleanup and baseline restoration", id, receipt.Response.ID)
}

// Exact evidence-based cleanup after a harness interruption. This never resumes
// the reconciler or adopts a native definition into another configuration.
func TestLiveWirelessCleanAirManifestRetainedCleanup(t *testing.T) {
	evidence := os.Getenv("CATC_WIRELESS_CLEANAIR_RETAINED")
	if evidence == "" {
		t.Skip("no retained qualification evidence selected")
	}
	raw, err := os.ReadFile(evidence + "/converged-0.json")
	if err != nil {
		t.Fatal(err)
	}
	var retained wirelessCleanAir
	if json.Unmarshal(raw, &retained) != nil || validateWirelessCleanAir(retained) != nil || !validAPIID(retained.ID) || len(retained.Name) < 8 || retained.Name[:7] != "CVK_MB_" || retained.Attributes.Description == nil || *retained.Attributes.Description != "CVK manifest band qualification" {
		t.Fatal("retained evidence invalid")
	}
	fixtureRaw, err := os.ReadFile(evidence + "/fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]string
	if json.Unmarshal(fixtureRaw, &fixture) != nil || fixture["name"] != retained.Name {
		t.Fatal("fixture name evidence mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	before, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil {
		t.Fatal(err)
	}
	instance, ok := before[retained.Name]
	if !ok || instance.ID != retained.ID || *instance.System {
		t.Fatal("retained identity changed")
	}
	detail, err := c.wirelessCleanAirRead(ctx, instance)
	// Compare the serialized projection used by the saved evidence. Empty native
	// slices/maps omitted by JSON must not differ solely from their decoded nil form.
	currentJSON, currentErr := json.Marshal(detail)
	retainedJSON, retainedErr := json.Marshal(retained)
	if err != nil || currentErr != nil || retainedErr != nil || string(currentJSON) != string(retainedJSON) {
		t.Fatal("retained fields changed", err)
	}
	delete(before, retained.Name)
	started := time.Now()
	raw, err = c.mutate(ctx, http.MethodDelete, wirelessCleanAirPath+"/"+url.PathEscape(retained.ID), nil)
	if err != nil {
		t.Fatal("retained cleanup uncertain", err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("retained cleanup receipt uncertain")
	}
	t.Log("exact retained cleanup", retained.Name, retained.ID, receipt.Response.ID)
	if err = waitSiteCredentialTask(ctx, c, receipt.Response, started); err != nil {
		t.Fatal(err)
	}
	after, err := c.wirelessFeatureInstances(ctx, wirelessCleanAirKind)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("retained cleanup baseline changed", err)
	}
	if err = os.WriteFile(evidence+"/retained-cleaned.json", []byte(`{"cleaned":true}`), 0600); err != nil {
		t.Fatal(err)
	}
}

// Count and fence qualification mutations without logging headers, bodies or
// credentials. A repeated PUT cannot consume the next manifest's allowance.
type cleanAirManifestTrace struct {
	base                                    http.RoundTripper
	name, id                                string
	maxPuts, posts, puts, deletes, attempts int
	allowDelete                             bool
}

func (g *cleanAirManifestTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet || req.URL.Path == authPath {
		return g.base.RoundTrip(req)
	}
	g.attempts++
	if req.Method == http.MethodDelete {
		if !g.allowDelete || g.id == "" || g.deletes != 0 || req.URL.Path != wirelessCleanAirPath+"/"+url.PathEscape(g.id) {
			return nil, errors.New("qualification refused unrelated or repeated delete")
		}
		g.deletes++
	} else {
		raw, err := io.ReadAll(io.LimitReader(req.Body, 65536))
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		var body wirelessCleanAir
		if json.Unmarshal(raw, &body) != nil || validateWirelessCleanAir(body) != nil || body.Name != g.name || body.ID != "" || !body.Attributes.Enable || body.Attributes.Description == nil || *body.Attributes.Description != "CVK manifest band qualification" {
			return nil, errors.New("qualification refused unrelated definition payload")
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == wirelessCleanAirPath && g.posts == 0 && g.id == "":
			g.posts++
		case req.Method == http.MethodPut && g.id != "" && req.URL.Path == wirelessCleanAirPath+"/"+url.PathEscape(g.id) && g.puts < g.maxPuts:
			g.puts++
		default:
			return nil, errors.New("qualification refused unrelated or repeated mutation")
		}
	}
	return g.base.RoundTrip(req)
}
