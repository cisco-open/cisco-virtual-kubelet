// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var credentialCleanupObservationModes = []string{"inline", "configmap", "active", "missing-native", "native-drift", "foreign-uuid", "wrong-name", "source-race", "config-race", "journal-race", "projection-lost", "owner-revoked", "legacy", "missing-pair", "family-conflict", "future-generation", "foreign-config-name", "unknown-receipt"}

func TestDiscoveryCredentialConfigCleanupObservation(t *testing.T) {
	for _, mode := range credentialCleanupObservationModes {
		t.Run(mode, func(t *testing.T) { discoveryCredentialCleanupObservationScenario(t, mode, nil) })
	}
}
func discoveryCredentialCleanupObservationScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	handoffMode := ""
	if strings.HasPrefix(mode, "handoff-observe/") {
		handoffMode = strings.TrimPrefix(mode, "handoff-observe/")
		mode = "complete/handoff-candidate"
	}
	public := strings.HasPrefix(mode, "public/")
	mode = strings.TrimPrefix(mode, "public/")
	completing := public || strings.HasPrefix(mode, "complete/")
	mode = strings.TrimPrefix(mode, "complete/")
	executing := completing || strings.HasPrefix(mode, "execute/")
	mode = strings.TrimPrefix(mode, "execute/")
	preparing := executing || strings.HasPrefix(mode, "prepare/")
	mode = strings.TrimPrefix(mode, "prepare/")
	ctx := context.Background()
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, false)
	a, _, c, _, paths := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	if install != nil {
		install(a, c)
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
	}
	if completing {
		protectCredentialFixtureController(t, a)
	}

	var cm *core.ConfigMap
	if mode == "configmap" || mode == "source-race" || mode == "late-source" || mode == "after-source-change" || mode == "permit-source-race" || mode == "after-source-identity-change" || mode == "completion-source-gone" || mode == "drain-source-return" || strings.Contains(mode, "drain-prepared-configmap") || mode == "drain-prepared-source-loss" || mode == "drain-prepared-source-race" {
		cm = &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: c.Namespace, Name: "cleanup-source"}, Data: map[string]string{"model": string(raw)}}
		if install == nil {
			cm.UID = "cleanup-model"
		}
		if err := a.statusWriter.Create(ctx, cm); err != nil {
			t.Fatal(err)
		}
		c.Spec.Source.Inline = nil
		c.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model"}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	present := false
	armed := false
	gets, posts, writes, deletes := 0, 0, 0, 0
	cleanupExecution := false
	permitChanged := false
	refusedCleanupID := ""
	var deleteStarted int64
	var started int64
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == discoveryPath+"/1/100":
			if armed {
				gets++
			}
			permitPhase := false
			if cleanupExecution && (strings.HasPrefix(mode, "permit-") || mode == "cleanup-preflight-replan") {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
				} else {
					permitPhase = nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting"
				}
				if permitPhase && !permitChanged {
					permitChanged = true
					switch mode {
					case "cleanup-preflight-replan":
						refusedCleanupID = nc.Status.Configuration.Operation.ID
						w.WriteHeader(503)
						return
					case "permit-source-race":
						if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
							t.Error(err)
						}
						cm.Data["model"] += " "
						if err := a.statusWriter.Update(ctx, cm); err != nil {
							t.Error(err)
						}
					case "permit-journal-race":
						nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, pnpCapacityHistory(1, false)...)
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Error(err)
						}
					}
				}
			}
			rows := []discoveryMetadata{}
			if present && !(armed && mode == "missing-native") {
				row := discoveryMetadata{ID: "job-id", Name: "fixture", Type: "SINGLE", Addresses: "192.0.2.1", Protocol: "SSH", Usernames: []string{"operator"}, Condition: "Complete", Status: "Inactive"}
				if armed {
					switch mode {
					case "active":
						row.Condition = "In Progress"
						row.Status = "Active"
					case "native-drift":
						row.Usernames = []string{"other"}
					case "foreign-uuid":
						row.ID = "foreign"
					case "wrong-name":
						row.Name = "other"
					}
				}
				if cleanupExecution && (mode == "late-native-drift" || permitPhase && mode == "permit-native-drift") {
					row.Usernames = []string{"foreign"}
				}
				if cleanupExecution && deletes > 0 && mode == "foreign-after-delete" {
					row.ID = "foreign-job"
				}
				rows = append(rows, row)
			}
			if armed && gets == 1 {
				switch mode {
				case "source-race":
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
						t.Error(err)
					}
					cm.Data["model"] += " "
					if err := a.statusWriter.Update(ctx, cm); err != nil {
						t.Error(err)
					}
				case "config-race":
					var fresh configapi.NetworkControllerConfig
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
						t.Error(err)
					}
					fresh.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
					if err := a.statusWriter.Update(ctx, &fresh); err != nil {
						t.Error(err)
					}
				case "journal-race":
					nc, err := a.currentController(ctx)
					if err != nil {
						t.Error(err)
						break
					}
					nc.Status.Configuration.CredentialApplications = nil
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Error(err)
					}
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == discoveryPath:
			if armed {
				writes++
			}
			posts++
			present = true
			started = time.Now().UnixMilli()
			if mode == "drain-taskless" {
				fmt.Fprint(w, `{"response":{}}`)
			} else {
				fmt.Fprint(w, `{"response":{"taskId":"original-task"}}`)
			}
		case r.Method == "DELETE" && r.URL.Path == discoveryPath+"/job-id" && cleanupExecution:
			deletes++
			deleteStarted = time.Now().UnixMilli()
			present = mode == "retained-native" || mode == "foreign-after-delete"
			if mode == "network-error" {
				w.WriteHeader(503)
				return
			}
			if mode == "ambiguous" {
				fmt.Fprint(w, `{"response":{}}`)
				return
			}
			fmt.Fprint(w, `{"response":{"taskId":"cleanup-task"}}`)
		case r.Method == "GET" && r.URL.Path == taskPath+"cleanup-task":
			end := deleteStarted
			if mode == "pending-task" {
				end = 0
			}
			id := "cleanup-task"
			if mode == "foreign-task" {
				id = "foreign-task"
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "startTime": deleteStarted, "endTime": end, "isError": mode == "failed-task"}})
		case r.Method == "GET" && r.URL.Path == taskPath+"original-task":
			end, id := started, "original-task"
			if armed && mode == "drain-pending" {
				end = 0
			}
			if armed && mode == "drain-foreign-task" {
				id = "foreign-task"
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "startTime": started, "endTime": end, "isError": armed && mode == "drain-failed", "data": `{"discoveryId":"job-id"}`}})
		default:
			writes++
			t.Error("unexpected request", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	})
	if public {
		enrollPublicCredentialFixture(t, a, c, mode, &posts)
	}
	preparedBase := a.statusWriter
	preparedHits := 0
	if public && (strings.HasPrefix(mode, "drain-prepared") || mode == "drain-submitting") {
		a.statusWriter = publicCredentialPreparedFaultClient{preparedBase, &preparedHits}
	}
	interrupted := false
	for i := 0; i < 20; i++ {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
			t.Fatal(err)
		}
		if public && strings.HasPrefix(mode, "drain-") && mode != "drain-retirement" {
			current := journalState(t, a)
			if current == nil {
				continue
			}
			op := current.Operation
			target := "Accepted"
			if strings.HasPrefix(mode, "drain-prepared") || mode == "drain-submitting" {
				target = "Prepared"
			}
			if mode == "drain-taskless" || mode == "drain-unknown" {
				target = "Unknown"
			}
			if mode == "drain-verified" {
				target = "Verified"
			}
			if op != nil && op.Phase == target {
				interrupted = true
				break
			}
		}
		if c.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && journalState(t, a).Operation == nil {
			break
		}
	}
	if public && (strings.HasPrefix(mode, "drain-prepared") || mode == "drain-submitting") {
		a.statusWriter = preparedBase
		if preparedHits != 1 || !interrupted {
			t.Fatal("prepared interruption hook was not exercised")
		}
	}
	if (!public || !interrupted) && (posts != 1 || c.Status.Phase != configapi.NetworkControllerConfigPhaseInSync) {
		t.Fatal("initial creator did not converge", c.Status.Conditions)
	}
	if public && mode == "drain-retirement" {
		raw = discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 2}, false)
		c.Spec.Source.Inline.Raw = raw
		c.Generation++
		if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, 2, `["CVK_SENSITIVE_TEST_VALUE"]`), 0600); err != nil {
			t.Fatal(err)
		}
		if err := a.statusWriter.Update(ctx, c); err != nil {
			t.Fatal(err)
		}
		cleanupExecution = true
		for i := 0; i < 20; i++ {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(c)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
				t.Fatal(err)
			}
			current := journalState(t, a)
			if current != nil && current.Operation != nil && current.Operation.Action == "Delete" && current.Operation.Phase == "Accepted" {
				interrupted = true
				break
			}
		}
		if !interrupted {
			t.Fatal("replacement retirement interruption not exercised")
		}
	}
	if mode == "known-unknown" {
		c.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
	}
	c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	if !public {
		c.Finalizers = append(c.Finalizers, discoveryConfigFinalizer, "example.com/keeper")
	}
	if mode == "family-conflict" {
		c.Finalizers = append(c.Finalizers, featureConfigFinalizer)
	}
	if install == nil && !public {
		c.Generation++
	}
	if err := a.statusWriter.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	if public && mode == "drain-submitting" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		nc.Status.Configuration.Operation.Phase = "Submitting"
		stamp := metav1.Now()
		nc.Status.Configuration.Operation.SubmittedAt = &stamp
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.statusWriter.Delete(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), c); err != nil {
		t.Fatal(err)
	}
	if mode == "projection-lost" {
		if err := os.Remove(paths["passwordList"]); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "owner-revoked" || mode == "legacy" || mode == "missing-pair" || mode == "future-generation" || mode == "foreign-config-name" || mode == "unknown-receipt" {
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		switch mode {
		case "owner-revoked":
			nc.Status.Configuration.Owners = nil
		case "legacy":
			r := &nc.Status.Configuration.ActionApplications[0]
			identity, err := parseDiscoveryCredentialReceipt(r.Resource)
			if err != nil {
				t.Fatal(err)
			}
			r.Resource = discoveryCredentialReceiptPrefix + identity.name + ":" + identity.id
			base, _, ok := discoveryCredentialInputParts(r.Operation.Resource)
			if !ok {
				t.Fatal("invalid legacy fixture")
			}
			r.Operation.Resource = base
			for i := range nc.Status.Configuration.CredentialApplications {
				app := &nc.Status.Configuration.CredentialApplications[i]
				if app.OperationID == r.Operation.ID {
					app.Resource = base
				}
			}

		case "future-generation":
			nc.Status.Configuration.ActionApplications[0].Operation.ConfigGeneration = c.Generation + 1
		case "foreign-config-name":
			nc.Status.Configuration.ActionApplications[0].Operation.ConfigName = "other"
		case "unknown-receipt":
			nc.Status.Configuration.ActionApplications[0].Resource = "unknown-owned-action"
		case "missing-pair":
			nc.Status.Configuration.CredentialApplications = nil
		}
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	before := journalState(t, a).DeepCopy()
	beforeFinalizers := append([]string(nil), c.Finalizers...)
	armed = true
	if public {
		cleanupExecution = true
		exercisePublicCredentialDeletion(t, a, c, mode, cm, paths, &posts, &deletes, &gets, install != nil)
		if mode == "cleanup-preflight-replan" {
			state := journalState(t, a)
			if !permitChanged || refusedCleanupID == "" || state.ActionApplications[1].Operation.ID == refusedCleanupID {
				t.Fatal("cleanup did not replan under a fresh operation after proven refusal")
			}
		}
		if writes != 0 {
			t.Fatal("public lifecycle made an unexpected native request")
		}
		return
	}
	observation, err := a.observeDiscoveryCredentialConfigCleanup(ctx, c)
	success := executing || mode == "inline" || mode == "configmap" || mode == "active"
	if success {
		if err != nil || observation == nil || len(observation.Candidates) != 1 || observation.Candidates[0].ID != "job-id" || gets != 2 {
			t.Fatal("owned cleanup preflight failed", err, observation, gets)
		}
		if observation.ConfigUID != string(c.UID) || observation.SpecDigest != featureIdentityDigest(c.Spec) || observation.DeletionDigest != featureIdentityDigest(c.DeletionTimestamp) || observation.EvidenceDigest != featureIdentityDigest(before) {
			t.Fatal("unbound cleanup observation")
		}
		candidate := observation.Candidates[0]
		source := &configSourceSnapshot{discoveryCleanup: &discoveryCleanupPreparation{observation: *observation, candidate: candidate}}
		if discoveryCleanupPreparationAllowed(c, source, "inventory", source.discoveryCleanup.resource(), "Delete", []configurationResourceIdentity{{target: candidate.ID}}) {
			t.Fatal("read-only credential observation bypassed dispatch enrollment")
		}
	} else if err == nil || observation != nil {
		t.Fatal("unsafe cleanup observation accepted", mode)
	}
	if preparing {
		if mode == "source-race" {
			gets = 0
		} // inject again during the fresh preflight
		prepared, prepareErr := a.prepareDiscoveryCredentialConfigCleanup(ctx, c)
		if success {
			if prepareErr != nil || prepared == nil || prepared.Phase != "Prepared" || prepared.TaskID != "" || prepared.SubmittedAt != nil || len(prepared.CredentialVersions) != 0 || prepared.TargetID != "job-id" {
				t.Fatal("cleanup preparation failed", prepareErr, prepared)
			}
			parts := discoveryFinalizeParts(prepared.Resource)
			if parts == nil || parts[0] != featureIdentityDigest(c.DeletionTimestamp) || parts[1] != featureIdentityDigest(c.Spec) || parts[2] != featureIdentityDigest(before) || parts[3] != observation.Candidates[0].Resource {
				t.Fatal("cleanup journal lost deletion/spec/evidence binding")
			}
			if !discoveryCredentialRetirementSelected(prepared.Resource) || ownedDiscoverySelected(prepared.Resource) || discoveryCredentialRetainedRecoveryOperation(prepared) {
				t.Fatal("cleanup entered the wrong execution family")
			}
			if _, ok := discoveryCredentialRetirementParts(prepared); !ok {
				t.Fatal("typed retirement parser rejected preparation")
			}
			// Even a persisted phase edited to Verifying cannot use generic
			// success or finish without the typed retirement proof.
			injected, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			injected.Status.Configuration.Operation.Phase = "Verifying"
			stamp := metav1.Now()
			injected.Status.Configuration.Operation.SubmittedAt = &stamp
			injected.Status.Configuration.Operation.TaskID = "cleanup-task"
			if err := a.statusWriter.Status().Update(ctx, injected); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(ctx, prepared.ID, "Verified", ""); err == nil {
				t.Fatal("cleanup bypassed typed atomic retirement writer")
			}
			injected, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			injected.Status.Configuration.Operation.Phase = "Verified"
			if err := a.statusWriter.Status().Update(ctx, injected); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, prepared.ID); err == nil {
				t.Fatal("cleanup released without retirement proof")
			}
			injected, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			injected.Status.Configuration.Operation = prepared.DeepCopy()
			if err := a.statusWriter.Status().Update(ctx, injected); err != nil {
				t.Fatal(err)
			}
			before.Operation = prepared.DeepCopy()
			reads := gets
			if again, err := a.prepareDiscoveryCredentialConfigCleanup(ctx, c); err == nil || again != nil {
				t.Fatal("occupied cleanup slot was overwritten")
			}
			if err := a.advanceDiscoveryConfigCleanup(ctx, c); err == nil {
				t.Fatal("ordinary cleanup accepted credential operation")
			}
			if gets != reads {
				t.Fatal("occupied preparation performed native work")
			}
			source, err := loadControllerSource(ctx, a.statusReader, c)
			if err != nil {
				t.Fatal(err)
			}
			source.discoveryCleanup = &discoveryCleanupPreparation{observation: *observation, candidate: observation.Candidates[0], credential: true}
			if discoveryCleanupSubmissionAllowed(c, source, prepared) {
				t.Fatal("preparation forged a deletion submission permit")
			}
		} else if prepareErr == nil || prepared != nil {
			t.Fatal("unsafe cleanup preparation accepted", mode)
		}
	}
	if executing {
		base := a.statusWriter
		hits := 0
		fault := mode
		if mode == "receipt-conflict" {
			fault = "conflict"
		}
		switch fault {
		case "lost-permit-ack", "lost-accepted-ack", "lost-receipt-ack", "conflict", "pruned-retirement":
			a.statusWriter = discoveryRetirementFaultClient{base, fault, &hits}
		}
		defer func() { a.statusWriter = base }()
		cleanupExecution = true
		if mode == "legacy-preparation" || mode == "unsupported-version" {
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			parts := discoveryFinalizeParts(current.Status.Configuration.Operation.Resource)
			if mode == "legacy-preparation" {
				current.Status.Configuration.Operation.Resource = discoveryFinalizePrefix + strings.Join(parts, ":")
			} else {
				current.Status.Configuration.Operation.Resource = "discovery-finalize:v3:" + strings.TrimPrefix(current.Status.Configuration.Operation.Resource, discoveryCredentialFinalizePrefix)
			}
			if err := base.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "late-source" {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
				t.Fatal(err)
			}
			cm.Data["model"] += " "
			if err := base.Update(ctx, cm); err != nil {
				t.Fatal(err)
			}
		}
		blocked := mode == "lost-permit-ack" || mode == "pruned-retirement" || mode == "ambiguous" || mode == "network-error" || mode == "failed-task" || mode == "pending-task" || mode == "foreign-task" || mode == "retained-native" || mode == "foreign-after-delete" || mode == "late-native-drift" || mode == "late-source" || mode == "after-config-change" || mode == "after-source-identity-change" || mode == "legacy-preparation" || mode == "unsupported-version" || strings.HasPrefix(mode, "permit-")
		changed, sawUnknown, restoredProjection := false, false, false
		provenRefusal := false
		for i := 0; i < 18; i++ {
			err := a.advanceDiscoveryCredentialConfigCleanup(ctx, c)
			if strings.HasPrefix(mode, "permit-") && errors.Is(err, errConfigurationNotDispatched) {
				provenRefusal = true
				break
			}
			if deletes == 1 && !changed {
				switch mode {
				case "after-source-loss", "projection-return":
					if err := os.Remove(paths["passwordList"]); err != nil {
						t.Fatal(err)
					}
				case "after-source-change":
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(cm), cm); err != nil {
						t.Fatal(err)
					}
					cm.Data["model"] += " "
					if err := base.Update(ctx, cm); err != nil {
						t.Fatal(err)
					}
				case "after-source-identity-change":
					current, err := a.currentController(ctx)
					if err != nil {
						t.Fatal(err)
					}
					current.Status.Configuration.Operation.SourceUID = "forged-source"
					if err := base.Status().Update(ctx, current); err != nil {
						t.Fatal(err)
					}
				case "after-config-change":
					c.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
					c.Generation++
					if err := base.Update(ctx, c); err != nil {
						t.Fatal(err)
					}
				}
				changed = true
			}
			if mode == "projection-return" && changed && !restoredProjection && journalState(t, a).Operation == nil {
				if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, 1, `["CVK_SENSITIVE_TEST_VALUE"]`), 0600); err != nil {
					t.Fatal(err)
				}
				restoredProjection = true
			}

			state := journalState(t, a)
			if state.Operation != nil && state.Operation.Phase == "Unknown" {
				sawUnknown = true
			}
			if !blocked && state.Operation == nil {
				break
			}
		}
		state := journalState(t, a)
		expectedDeletes := 1
		if mode == "lost-permit-ack" || mode == "late-native-drift" || mode == "late-source" || mode == "legacy-preparation" || mode == "unsupported-version" || strings.HasPrefix(mode, "permit-") {
			expectedDeletes = 0
		}
		if deletes != expectedDeletes || posts != 1 || !reflect.DeepEqual(before.CredentialApplications, state.CredentialApplications) || !reflect.DeepEqual(before.Owners, state.Owners) || !reflect.DeepEqual(before.ActionApplications[0], state.ActionApplications[0]) {
			t.Fatal("cleanup replayed or changed creator/credential ownership", deletes, posts)
		}
		if blocked {
			expectedActions := 1
			if mode == "permit-journal-race" {
				expectedActions = 2
			}
			if (state.Operation == nil) != provenRefusal || len(state.ActionApplications) != expectedActions {
				t.Fatal("uncertain cleanup released or fabricated retirement", state.Operation)
			}
		} else {
			if state.Operation != nil || len(state.ActionApplications) != 2 || !discoveryCredentialRetiredSelected(state.ActionApplications[1].Resource) || present {
				t.Fatal("cleanup failed to converge", state.Operation)
			}
			if _, err := discoveryCredentialLineages(state, string(c.UID)); err != nil {
				t.Fatal(err)
			}
		}
		if strings.HasPrefix(mode, "permit-") && (!permitChanged || !provenRefusal || state.Operation != nil) {
			t.Fatal("proven late preflight refusal did not preserve history and retire only its operation")
		}
		if mode == "unsupported-version" && !ownedDiscoverySelected(state.Operation.Resource) {
			t.Fatal("unknown cleanup version escaped reserved completion guards")
		}
		if mode == "projection-return" && !restoredProjection {
			t.Fatal("projection restoration was not exercised")
		}
		if mode == "known-unknown" && !sawUnknown {
			t.Fatal("timeout recovery was not exercised")
		}
		switch fault {
		case "lost-permit-ack", "lost-accepted-ack", "lost-receipt-ack", "conflict", "pruned-retirement":
			if hits != 1 {
				t.Fatal("journal fault not exercised")
			}
		}
		before = state.DeepCopy()
		for i := 0; i < 3; i++ {
			_ = a.advanceDiscoveryCredentialConfigCleanup(ctx, c)
		}
		if deletes != expectedDeletes || !reflect.DeepEqual(before, journalState(t, a)) {
			t.Fatal("later invocation replayed cleanup")
		}
	}
	if completing {
		exerciseDiscoveryCredentialCompletion(t, a, c, mode, &present, cm, &gets)
		if deletes != 1 {
			t.Fatal("completion replayed native Delete")
		}
		before = journalState(t, a).DeepCopy()
		beforeFinalizers = append([]string(nil), c.Finalizers...)
		if handoffMode != "" {
			exerciseDiscoveryCredentialHandoffObservation(t, a, c, handoffMode, install != nil)
			return
		}
	}
	if mode != "journal-race" && !reflect.DeepEqual(before, journalState(t, a)) {
		t.Fatal("observation changed retained history")
	}
	if writes != 0 || posts != 1 || !reflect.DeepEqual(beforeFinalizers, c.Finalizers) {
		t.Fatal("observation mutated native state or finalizers")
	}
}
