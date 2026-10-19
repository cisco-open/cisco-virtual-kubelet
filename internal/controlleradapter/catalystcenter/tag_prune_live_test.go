//go:build live && envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"io"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"os"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"slices"
	"strings"
	"testing"
	"time"
)

// Isolated real Kubernetes API plus native controller. Every effect uses the
// public manifest journal; no installed-actor or physical-device claim is made.
func TestLiveTagPrunePublicCycle(t *testing.T) {
	if os.Getenv("CATC_TAG_PUBLIC_PRUNE") != "isolated-owned-tag-cleanup" {
		t.Skip("native tag prune qualification disabled")
	}
	projected := os.Getenv("CATC_TAG_PROJECTED_SOURCE") == "mutable-recreated-configmap"
	finalizing := os.Getenv("CATC_TAG_CONFIG_DELETE") == "manifest-delete"
	handoffCycles := 0
	if os.Getenv("CATC_TAG_HANDOFF") == "two-successors" {
		handoffCycles = 2
		if !finalizing || os.Getenv("CATC_TAG_RETIREMENT") != "durable-proof" {
			t.Fatal("native handoff requires durable CR deletion qualifier")
		}
	}
	sectionSequence := strings.Split(os.Getenv("CATC_TAG_HANDOFF_SCOPES"), ",")
	if os.Getenv("CATC_TAG_HANDOFF_SCOPES") != "" {
		if handoffCycles == 0 || len(sectionSequence) != handoffCycles {
			t.Fatal("handoff scopes must select exactly the configured successors")
		}
		for _, scope := range sectionSequence {
			if scope != "inventory" && scope != "templates" && scope != "combined" {
				t.Fatal("invalid successor scope")
			}
		}
	}
	recreateCycles := 0
	if os.Getenv("CATC_TAG_RECREATE") == "two-cycles" {
		recreateCycles = 2
	}
	evidenceDir := os.Getenv("CATC_TAG_RECREATE_EVIDENCE")
	if finalizing && recreateCycles > 0 {
		t.Fatal("CR deletion cannot recreate a deleted owner")
	}
	if recreateCycles > 0 || finalizing {
		if evidenceDir == "" {
			t.Fatal("private recreation evidence directory required")
		}
		if err := os.MkdirAll(evidenceDir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := liveClient(t)
	before, err := c.listTags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("CVK-Q-Tag-Prune-%d", time.Now().UnixNano())
	if _, exists := before[name]; exists {
		t.Fatal("fixture exists")
	}
	a, config := journalFixture(t)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = c.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	inventoryOnly := os.Getenv("CATC_TAG_PRUNE_SCOPE") == "inventory"
	templatesOnly := os.Getenv("CATC_TAG_PRUNE_SCOPE") == "templates"
	config.Spec.ManagedSections = []string{"inventory", "templates"}
	if inventoryOnly {
		config.Spec.ManagedSections = []string{"inventory"}
	}
	if finalizing {
		config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
	}
	if templatesOnly {
		config.Spec.ManagedSections = []string{"templates"}
	}
	legacyBaseline := os.Getenv("CATC_TAG_PRUNE_BASELINE") == "retained-creator"
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	if legacyBaseline && finalizing {
		t.Fatal("CR cleanup requires enrollment before baseline creation")
	}
	if finalizing {
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	if legacyBaseline {
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	}
	raw := func(description string) []byte {
		primary := "inventory"
		if templatesOnly {
			primary = "templates"
		}
		root := map[string]any{primary: map[string]any{"tags": []any{map[string]any{"name": name, "description": description, "system_tag": false}}}}
		if !inventoryOnly && !templatesOnly {
			root["templates"] = map[string]any{}
		}
		b, _ := json.Marshal(map[string]any{"catalyst_center": root})
		return b
	}
	config.Spec.Source.Inline.Raw = raw("CVK tag recovery create")
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	installWirelessFeatureQualificationAPI(t, api, "native-tag-prune")(a, config)
	if finalizing {
		config.Finalizers = append(config.Finalizers, tagCleanupFinalizer(config), "example.com/other")
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	intentWriter := a.statusWriter
	var projectedEpochs []map[string]string
	staleRefusals := 0
	setModel := func(target *configapi.NetworkControllerConfig, data []byte) {
		if !projected {
			target.Spec.Source.Inline.Raw = append([]byte(nil), data...)
			return
		}
		var stored configapi.NetworkControllerConfig
		var oldSource *configSourceSnapshot
		if target.UID != "" && intentWriter.Get(ctx, ctrlclient.ObjectKeyFromObject(target), &stored) == nil && stored.Spec.Source.ConfigMapRef != nil {
			if previous, err := loadControllerSource(ctx, a.statusReader, &stored); err == nil {
				oldSource = previous
			}
		}
		target.Spec.Source = configapi.NetworkControllerConfigurationSource{ConfigMapRef: &configapi.ConfigMapKeyRef{Name: "tag-source", Key: "model"}}
		key := ctrlclient.ObjectKey{Namespace: target.Namespace, Name: "tag-source"}
		var cm corev1.ConfigMap
		err := intentWriter.Get(ctx, key, &cm)
		if apierrors.IsNotFound(err) {
			cm = corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace}, Data: map[string]string{"model": string(data)}}
			err = intentWriter.Create(ctx, &cm)
		} else if err == nil {
			cm.Data["model"] = string(data)
			err = intentWriter.Update(ctx, &cm)
		}
		if err != nil {
			t.Fatal(err)
		}
		if oldSource != nil {
			if err := a.checkConfigurationSnapshot(ctx, &stored, oldSource); err == nil {
				t.Fatal("old ConfigMap revision passed dispatch snapshot gate")
			}
			staleRefusals++
		}
		projectedEpochs = append(projectedEpochs, map[string]string{"uid": string(cm.UID), "version": cm.ResourceVersion, "digest": featureIdentityDigest(data)})
	}
	removeModel := func() {
		if !projected {
			return
		}
		if err := intentWriter.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "tag-source", Namespace: config.Namespace}}); err != nil {
			t.Fatal(err)
		}
	}
	assertSourceBinding := func(op *cvk.NetworkControllerConfigurationOperation) {
		if !projected {
			return
		}
		if tagFinalizeSelected(op.Resource) {
			if op.SourceUID != "" || op.SourceVersion != "" {
				t.Fatal("synthetic finalization retained mutable source binding")
			}
			return
		}
		var cm corev1.ConfigMap
		if err := intentWriter.Get(ctx, ctrlclient.ObjectKey{Namespace: config.Namespace, Name: "tag-source"}, &cm); err != nil {
			t.Fatal(err)
		}
		if op.SourceUID != string(cm.UID) || op.SourceVersion != cm.ResourceVersion {
			t.Fatal("native task lost exact ConfigMap snapshot")
		}
	}
	if projected {
		setModel(config, raw("CVK tag recovery create"))
		if err := intentWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	a.client = c
	var immutableCreator *cvk.NetworkControllerConfigurationActionApplication
	var created *cvk.NetworkControllerConfigurationOperation
	nativeID := ""
	settled := false
	configGone := false

	session, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := session.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	posts, puts, deletes := 0, 0, 0
	expectedCreate, expectedUpdate := "CVK tag recovery create", "CVK tag recovery update"
	expectedTotal := 1 + recreateCycles + handoffCycles
	session.HTTPClient.Transport = discoveryCleanupRoundTrip(func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet || req.Method == http.MethodPost && req.URL.Path == authPath {
			return base.RoundTrip(req)
		}
		if req.URL.Path == tagsPath && (req.Method == http.MethodPost || req.Method == http.MethodPut) {
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			if json.Unmarshal(raw, &body) != nil {
				return nil, errors.New("invalid tag payload")
			}
			expected := map[string]any{"name": name, "description": expectedCreate, "systemTag": false}
			if req.Method == http.MethodPost {
				if posts >= expectedTotal || posts != deletes || puts != posts {
					return nil, errors.New("Create replay refused")
				}
				posts++
			} else {
				if puts >= expectedTotal || puts+1 != posts || deletes != puts || nativeID == "" {
					return nil, errors.New("Update replay/identity refused")
				}
				puts++
				expected["id"] = nativeID
				expected["description"] = expectedUpdate
				// Native empty rules are preserved by the common selected-field writer.
				if rules, present := body["dynamicRules"]; present {
					list, ok := rules.([]any)
					if !ok || len(list) != 0 {
						return nil, errors.New("unexpected rules")
					}
					expected["dynamicRules"] = []any{}
				}
			}
			if !reflect.DeepEqual(body, expected) {
				return nil, errors.New("unrelated tag fields refused")
			}
			return base.RoundTrip(req)
		}
		if req.Method == http.MethodDelete && nativeID != "" && deletes < expectedTotal && req.URL.Path == tagsPath+"/"+nativeID {
			nc, err := a.currentController(ctx)
			if err != nil {
				return nil, err
			}
			if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
				return nil, errors.New("Delete without durable journal refused")
			}
			op := nc.Status.Configuration.Operation
			if !tagDeleteSelected(op.Resource) || op.Phase != "Submitting" || op.TargetID != nativeID || op.ConfigUID != string(config.UID) || posts != puts || deletes+1 != posts {
				return nil, errors.New("unrelated Delete refused")
			}

			deletes++
			return base.RoundTrip(req)
		}
		return nil, errors.New("unrelated native mutation refused")
	})
	checkpoint := func(phase string) {
		if evidenceDir == "" {
			return
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		writeLiveDiscoveryPublicRecord(t, evidenceDir, phase+".json", map[string]any{"phase": phase, "tagName": name, "nativeID": nativeID, "configUID": config.UID, "generation": config.Generation, "posts": posts, "puts": puts, "deletes": deletes, "baselineDigest": featureIdentityDigest(before), "configuration": current.Status.Configuration, "controllerConditions": current.Status.Conditions, "configGone": configGone, "projectedSource": projected, "projectedEpochs": projectedEpochs, "staleSourceRefusals": staleRefusals, "configStatus": config.Status, "configFinalizers": config.Finalizers, "configDeletionTimestamp": config.DeletionTimestamp})
	}
	checkpoint("before-create")
	t.Cleanup(func() {
		if !settled {
			t.Log("unsettled generated tag retained for exact recovery", name, nativeID)
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		after, err := c.listTags(cleanup)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Error("catalog baseline not restored", err)
		}
	})
	for _, description := range []string{"CVK tag recovery create", "CVK tag recovery update"} {
		if description == "CVK tag recovery update" {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if !finalizing {
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
			}
			setModel(config, raw(description))
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
		interrupted := false
		for i := 0; i < 90; i++ {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !interrupted && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Accepted" {
				op := nc.Status.Configuration.Operation
				assertSourceBinding(op)
				if !validAPIID(op.TaskID) {
					t.Fatal("original task missing")
				}
				if op.Action == "Create" {
					created = op.DeepCopy()
				}
				t.Log("retained original task", op.Action, op.TaskID)
				checkpoint("initial-" + strings.ToLower(op.Action) + "-accepted")
				op.Phase = "Unknown"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				interrupted = true
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if interrupted && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(300 * time.Millisecond):
			}
		}
		if !interrupted || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("original native task did not recover", config.Status.Phase)
		}
		rows, err := c.listTags(ctx)
		if err != nil {
			t.Fatal(err)
		}
		row, exists := rows[name]
		if !exists || nativeID != "" && nativeID != row.ID {
			t.Fatal("native tag identity lost")
		}
		nativeID = row.ID
		if row.Description == nil || *row.Description != description {
			t.Fatal("tag effect did not match recovered manifest")
		}
		controller, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		records, err := tagOwnedRecords(controller.Status.Configuration)
		if err != nil {
			t.Fatal(err)
		}
		key := featureIdentityDigest(name)
		creator, exists := records.created[key]
		if !exists || len(records.created) != 1 || created == nil || creator.Operation.ID != created.ID || creator.Operation.TaskID != created.TaskID {
			t.Fatal("durable original creator missing or changed")
		}
		_, receiptID, _, err := tagReceiptIdentity(creator)
		if err != nil || receiptID != nativeID {
			t.Fatal("creator UUID binding missing", err)
		}
		if immutableCreator == nil {
			immutableCreator = creator.DeepCopy()
			_, fullErr := tagFullReceiptState(creator)
			if legacyBaseline && (fullErr == nil) || !legacyBaseline && fullErr != nil {
				t.Fatal("initial complete-state baseline mode mismatch", fullErr)
			}
			if len(records.applied) != 0 {
				t.Fatal("Create fabricated an Update receipt")
			}
		} else {
			if !reflect.DeepEqual(*immutableCreator, creator) || len(records.applied) != 1 {
				t.Fatal("creator changed or applied row missing")
			}
			applied, exists := records.applied[key]
			if !exists || applied.Operation.ConfigGeneration != config.Generation || applied.Operation.TargetID != nativeID {
				t.Fatal("latest-state evidence is not current")
			}
			if _, err := tagFullReceiptState(applied); err != nil {
				t.Fatal("explicit update did not retain complete baseline", err)
			}
			_, _, fingerprint, err := tagReceiptIdentity(applied)
			if err != nil || fingerprint != featureIdentityDigest(tagPayload(row)) {
				t.Fatal("latest-state readback not retained", err)
			}
		}
		t.Log("durable immutable creator and current latest-state rows", len(records.created), len(records.applied))
		t.Log("recovered native manifest revision without replay", config.Generation, nativeID)
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	admin := a.statusWriter
	if finalizing {
		removeModel()
		if err := admin.Delete(ctx, config); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		a.statusWriter = statusOnlyFeatureClient{Client: admin}
	} else {
		setModel(config, tagDeletionEmptySource(config))
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	deleteRecovered := false
	for range 90 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !deleteRecovered && nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Accepted" && tagDeleteSelected(nc.Status.Configuration.Operation.Resource) {
			op := nc.Status.Configuration.Operation
			if op.TargetID != nativeID || !validAPIID(op.TaskID) {
				t.Fatal("original Delete task/UUID missing")
			}
			t.Log("retained original Delete task", op.TaskID, op.TargetID)
			checkpoint("initial-delete-accepted")
			op.Phase = "Unknown"
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			deleteRecovered = true
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if deleteRecovered && (finalizing && tagDeletionCompleted(nc, config) || !finalizing && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
	currentAfterDelete, readErr := a.currentController(ctx)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !deleteRecovered || (!finalizing && config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || finalizing && !tagDeletionCompleted(currentAfterDelete, config)) || deletes != 1 {
		t.Fatal("native manifest prune did not settle once", deleteRecovered, deletes, config.Status.Conditions)
	}
	controller, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	records, err := tagOwnedRecords(controller.Status.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := tagDeletedRecords(controller.Status.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	key := featureIdentityDigest(name)
	if len(deleted) != 1 || deleted[key].Operation.TargetID != nativeID || !reflect.DeepEqual(*immutableCreator, records.created[key]) || len(records.applied) != 1 || controller.Status.Configuration.Operation != nil {
		t.Fatal("prune lost creator/latest/tombstone or journal release")
	}
	t.Log("manifest-driven prune and original-task recovery retained immutable creator, latest-state and absence tombstone", nativeID)
	checkpoint("initial-pruned")
	if finalizing {
		if !tagFinalizeSelected(deleted[key].Operation.Resource) {
			t.Fatal("CR cleanup lacked deletion/spec binding")
		}
		accepted := false
		for _, policy := range controlleradapter.ConfigurationLifecyclesFor(TypeName) {
			if policy.Finalizer == tagCleanupFinalizer(config) {
				accepted = policy.Completed(controller, config)
			}
		}
		if !accepted {
			t.Fatal("manager contract refused cleanup proof")
		}
		before := config.DeepCopy()
		config.Finalizers = slices.DeleteFunc(config.Finalizers, func(f string) bool { return f == tagCleanupFinalizer(before) })
		if err := admin.Patch(ctx, config, ctrlclient.MergeFromWithOptions(before, ctrlclient.MergeFromWithOptimisticLock{})); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(config.Finalizers, "example.com/other") || slices.Contains(config.Finalizers, tagCleanupFinalizer(config)) {
			t.Fatal("manager metadata release altered unrelated finalizer")
		}
		checkpoint("manager-contract-released")
		if os.Getenv("CATC_TAG_RETIREMENT") == "durable-proof" {
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			beforeRecord, _, err := tagRetirementRecord(current)
			if err != nil {
				t.Fatal("retirement witness unavailable before config removal", err)
			}
			before := config.DeepCopy()
			config.Finalizers = slices.DeleteFunc(config.Finalizers, func(f string) bool { return f == "example.com/other" })
			if err := admin.Patch(ctx, config, ctrlclient.MergeFromWithOptions(before, ctrlclient.MergeFromWithOptimisticLock{})); err != nil {
				t.Fatal(err)
			}
			var gone configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &gone); !apierrors.IsNotFound(err) {
				t.Fatal("old config did not disappear", err)
			}
			configGone = true
			current, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			afterRecord, _, err := tagRetirementRecord(current)
			if err != nil || !reflect.DeepEqual(beforeRecord, afterRecord) {
				t.Fatal("retirement proof lost after old CR removal", err)
			}
			checkpoint("retirement-proof-survives")
			t.Log("persisted tag retirement survives old config removal without changing ownership or receipts", config.UID)
			priorIDs := map[string]bool{nativeID: true}
			for owner := 0; owner < handoffCycles; owner++ {
				expectedCreate = fmt.Sprintf("CVK successor tag %d", owner)
				expectedUpdate = fmt.Sprintf("CVK successor tag updated %d", owner)
				next := config.DeepCopy()
				if os.Getenv("CATC_TAG_HANDOFF_SCOPES") != "" {
					inventoryOnly, templatesOnly = sectionSequence[owner] == "inventory", sectionSequence[owner] == "templates"
					next.Spec.ManagedSections = []string{"inventory", "templates"}
					if inventoryOnly {
						next.Spec.ManagedSections = []string{"inventory"}
					} else if templatesOnly {
						next.Spec.ManagedSections = []string{"templates"}
					}
				}
				next.ObjectMeta = metav1.ObjectMeta{Name: config.Name, Namespace: config.Namespace, Finalizers: []string{tagCleanupFinalizer(next), "example.com/other"}}
				next.Status = configapi.NetworkControllerConfigStatus{}
				setModel(next, raw(expectedCreate))
				if err := admin.Create(ctx, next); err != nil {
					t.Fatal(err)
				}
				config = next
				configGone = false
				transferred := false
				advanceOwner := func(label, action string) {
					recovered := false
					for range 90 {
						restarted := *a
						if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
							t.Fatal(err)
						}
						current, err := a.currentController(ctx)
						if err != nil {
							t.Fatal(err)
						}
						mine := true
						for _, o := range current.Status.Configuration.Owners {
							if o.ConfigUID != string(config.UID) {
								mine = false
							}
						}
						if mine && !transferred {
							transferred = true
							if len(current.Status.Configuration.Owners) != len(config.Spec.ManagedSections) || configurationOwnersMatch(current.Status.Configuration, config) != nil {
								t.Fatal("scope handoff retained stale section claims")
							}
							if posts != owner+1 || puts != owner+1 || deletes != owner+1 {
								t.Fatal("handoff bypassed the native journal")
							}
							if _, err := tagOwnerArchiveGraph(current.Status.Configuration); err != nil {
								t.Fatal(err)
							}
							checkpoint(fmt.Sprintf("handoff-%d-claims-transferred", owner))
						}
						if op := current.Status.Configuration.Operation; op != nil && !recovered && op.Phase == "Accepted" && op.ConfigUID == string(config.UID) && op.Action == action {
							assertSourceBinding(op)
							if action == "Create" && !tagSuccessorSelected(op.Resource) {
								t.Fatal("successor did not retain prior-owner history binding")
							}
							if action != "Create" && op.TargetID != nativeID {
								t.Fatal("successor mutation lost exact UUID")
							}
							checkpoint(label + "-accepted")
							t.Log("recovering original successor task", label, op.TaskID)
							op.Phase = "Unknown"
							if err := a.statusWriter.Status().Update(ctx, current); err != nil {
								t.Fatal(err)
							}
							recovered = true
						}
						if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
							t.Fatal(err)
						}
						done := config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation
						if action == "Delete" {
							done = tagDeletionCompleted(current, config)
						}
						if recovered && done {
							checkpoint(label + "-verified")
							return
						}
						select {
						case <-ctx.Done():
							t.Fatal(ctx.Err())
						case <-time.After(300 * time.Millisecond):
						}
					}
					t.Fatal("native successor did not converge", label, config.Status.Conditions)
				}
				advanceOwner(fmt.Sprintf("handoff-%d-create", owner), "Create")
				observed, err := c.listTags(ctx)
				if err != nil {
					t.Fatal(err)
				}
				got, ok := observed[name]
				if !ok || priorIDs[got.ID] {
					t.Fatal("successor reused an old native identity")
				}
				nativeID = got.ID
				priorIDs[nativeID] = true
				setModel(config, raw(expectedUpdate))
				if err := admin.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				advanceOwner(fmt.Sprintf("handoff-%d-update", owner), "Update")
				removeModel()
				if err := admin.Delete(ctx, config); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				advanceOwner(fmt.Sprintf("handoff-%d-delete", owner), "Delete")
				current, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				record, _, err := tagRetirementRecord(current)
				if err != nil || record.Completion.ConfigUID != string(config.UID) {
					t.Fatal("successor retirement lost predecessor audit", err)
				}
				accepted := false
				for _, policy := range controlleradapter.ConfigurationLifecyclesFor(TypeName) {
					if policy.Finalizer == tagCleanupFinalizer(config) {
						accepted = policy.Completed(current, config)
					}
				}
				if !accepted {
					t.Fatal("manager rejected successor closure")
				}
				old := config.DeepCopy()
				config.Finalizers = slices.DeleteFunc(config.Finalizers, func(f string) bool { return f == tagCleanupFinalizer(old) })
				if err := admin.Patch(ctx, config, ctrlclient.MergeFromWithOptions(old, ctrlclient.MergeFromWithOptimisticLock{})); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				if !slices.Contains(config.Finalizers, "example.com/other") {
					t.Fatal("successor manager boundary removed unrelated finalizer")
				}
				old = config.DeepCopy()
				config.Finalizers = nil
				if err := admin.Patch(ctx, config, ctrlclient.MergeFromWithOptions(old, ctrlclient.MergeFromWithOptimisticLock{})); err != nil {
					t.Fatal(err)
				}
				var absent configapi.NetworkControllerConfig
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &absent); !apierrors.IsNotFound(err) {
					t.Fatal("successor CR not removed", err)
				}
				configGone = true
				checkpoint(fmt.Sprintf("handoff-%d-closed-and-gone", owner))
				t.Log("complete native successor epoch", owner, nativeID)
			}
			if handoffCycles > 0 && (posts != expectedTotal || puts != expectedTotal || deletes != expectedTotal || len(priorIDs) != expectedTotal) {
				t.Fatal("native owner cycles repeated writes or identities", posts, puts, deletes, len(priorIDs))
			}

		}

		if projected {
			if staleRefusals < 1 || len(projectedEpochs) != 2*(handoffCycles+1) {
				t.Fatal("projected native source epochs/refusal checks missing", staleRefusals, len(projectedEpochs))
			}
			seen := map[string]bool{}
			for i := 0; i < len(projectedEpochs); i += 2 {
				uid := projectedEpochs[i]["uid"]
				if seen[uid] || projectedEpochs[i+1]["uid"] != uid || projectedEpochs[i+1]["version"] == projectedEpochs[i]["version"] {
					t.Fatal("ConfigMap UID/revision epoch was not independently exercised")
				}
				seen[uid] = true
			}
			t.Log("qualified projected source epochs", len(seen), "model revisions", len(projectedEpochs), "stale refusals", staleRefusals)
		}
		settled = true
		t.Log("native CR deletion recovered original task; bound manager proof accepted; unrelated finalizer retained", nativeID)
		return
	}
	priorIDs := map[string]bool{nativeID: true}
	advance := func(label, action string, intent []byte) {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		setModel(config, intent)
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		recoveredTask := ""
		for range 120 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			checkpoint(label + "-state")
			if current.Status.Configuration != nil && current.Status.Configuration.Operation != nil {
				op := current.Status.Configuration.Operation
				if op.Phase == "Accepted" {
					if op.Action != action || !validAPIID(op.TaskID) || recoveredTask != "" && recoveredTask != op.TaskID {
						t.Fatal("recreation sequence received unrelated or repeated task")
					}
					assertSourceBinding(op)
					if action == "Create" && !tagRecreateSelected(op.Resource) {
						t.Fatal("recreation lost predecessor binding")
					}
					if action != "Create" && op.TargetID != nativeID {
						t.Fatal("recreated action lost native UUID")
					}
					recoveredTask = op.TaskID
					checkpoint(label + "-accepted")
					t.Log("recovering original lifecycle task", label, op.Action, op.TaskID)
					op.Phase = "Unknown"
					if err := a.statusWriter.Status().Update(ctx, current); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if recoveredTask != "" && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				checkpoint(label + "-verified")
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(300 * time.Millisecond):
			}
		}
		t.Fatal("recreated lifecycle failed to converge", label, config.Status.Conditions)
	}
	for cycle := 0; cycle < recreateCycles; cycle++ {
		expectedCreate = fmt.Sprintf("CVK recreated tag %d", cycle)
		advance(fmt.Sprintf("cycle-%d-create", cycle), "Create", raw(expectedCreate))
		rows, err := c.listTags(ctx)
		if err != nil {
			t.Fatal(err)
		}
		row, exists := rows[name]
		if !exists || priorIDs[row.ID] {
			t.Fatal("recreated tag lacks a fresh UUID")
		}
		nativeID = row.ID
		priorIDs[nativeID] = true
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cohorts, err := tagRetiredCohorts(current.Status.Configuration)
		if err != nil || len(cohorts) != cycle+1 {
			t.Fatal("native recreation ancestry incomplete", err)
		}
		originalFound := false
		for _, cohort := range cohorts {
			if reflect.DeepEqual(cohort.Creator, *immutableCreator) {
				originalFound = true
			}
		}
		if !originalFound {
			t.Fatal("native recreation overwrote original creator")
		}
		own, err := tagOwnedRecords(current.Status.Configuration)
		if err != nil {
			t.Fatal(err)
		}
		creator := own.created[key]
		checkpoint(fmt.Sprintf("cycle-%d-created-uuid", cycle))
		expectedUpdate = fmt.Sprintf("CVK recreated tag updated %d", cycle)
		advance(fmt.Sprintf("cycle-%d-update", cycle), "Update", raw(expectedUpdate))
		current, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		own, err = tagOwnedRecords(current.Status.Configuration)
		if err != nil || !reflect.DeepEqual(creator, own.created[key]) || len(own.applied) != 1 {
			t.Fatal("recreated Update changed creator or lost latest state", err)
		}
		empty := []byte(`{"catalyst_center":{"inventory":{"tags":[]},"templates":{}}}`)
		if inventoryOnly {
			empty = []byte(`{"catalyst_center":{"inventory":{"tags":[]}}}`)
		}
		advance(fmt.Sprintf("cycle-%d-delete", cycle), "Delete", empty)
		current, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cohorts, err = tagRetiredCohorts(current.Status.Configuration)
		if err != nil || len(cohorts) != cycle+1 {
			t.Fatal("recreated cleanup lost archives", err)
		}
		tombstones, err := tagDeletedRecords(current.Status.Configuration)
		if err != nil || len(tombstones) != 1 || tombstones[key].Operation.TargetID != nativeID {
			t.Fatal("recreated cleanup lost exact tombstone", err)
		}
		t.Log("complete native recreation/update/prune lifecycle", cycle, nativeID, "archivedCohorts", len(cohorts))
	}
	if posts != expectedTotal || puts != expectedTotal || deletes != expectedTotal {
		t.Fatal("unexpected native mutation count", posts, puts)
	}
	settled = true
	t.Log("exact native mutation counts", posts, puts, deletes, "legacyBaseline", legacyBaseline, "inventoryOnly", inventoryOnly, "recreateCycles", recreateCycles)
}
