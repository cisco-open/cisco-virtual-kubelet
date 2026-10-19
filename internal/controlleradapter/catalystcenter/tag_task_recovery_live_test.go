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
	"io"
	"net/http"
	"os"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
	"time"
)

// Isolated real Kubernetes API plus native controller; no installed actors or
// creator/deletion ownership claim. Cleanup is supervised by original task UUID.
func TestLiveTagKnownTaskPublicRestart(t *testing.T) {
	if os.Getenv("CATC_TAG_PUBLIC_RECOVERY") != "isolated-generated-tag" {
		t.Skip("native tag recovery qualification disabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := liveClient(t)
	before, err := c.listTags(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("CVK-Q-Tag-Recovery-%d", time.Now().UnixNano())
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
	config.Spec.ManagedSections = []string{"inventory", "templates"}
	raw := func(description string) []byte {
		b, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"tags": []any{map[string]any{"name": name, "description": description, "system_tag": false}}}, "templates": map[string]any{}}})
		return b
	}
	config.Spec.Source.Inline.Raw = raw("CVK tag recovery create")
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	installWirelessFeatureQualificationAPI(t, api, "native-tag-recovery")(a, config)
	a.client = c
	var immutableCreator *cvk.NetworkControllerConfigurationActionApplication
	var created *cvk.NetworkControllerConfigurationOperation
	nativeID := ""
	settled := false

	session, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := session.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	posts, puts, deletes := 0, 0, 0
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
			expected := map[string]any{"name": name, "description": "CVK tag recovery create", "systemTag": false}
			if req.Method == http.MethodPost {
				if posts != 0 {
					return nil, errors.New("Create replay refused")
				}
				posts++
			} else {
				if puts != 0 || posts != 1 || nativeID == "" {
					return nil, errors.New("Update replay/identity refused")
				}
				puts++
				expected["id"] = nativeID
				expected["description"] = "CVK tag recovery update"
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
		if req.Method == http.MethodDelete && settled && nativeID != "" && deletes == 0 && req.URL.Path == tagsPath+"/"+nativeID {
			deletes++
			return base.RoundTrip(req)
		}
		return nil, errors.New("unrelated native mutation refused")
	})
	t.Cleanup(func() {
		if !settled || created == nil || nativeID == "" {
			t.Log("uncertain generated tag retained", name, nativeID)
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		causal, done, err := c.tagCreateTaskResult(cleanup, created, time.Now())
		if err != nil || !done || causal != nativeID {
			t.Error("cleanup lacks original causal UUID", err)
			return
		}
		rows, err := c.listTags(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		row, exists := rows[name]
		if !exists || row.ID != nativeID || row.SystemTag == nil || *row.SystemTag || row.Description == nil || *row.Description != "CVK tag recovery update" {
			t.Error("cleanup identity/effect changed")
			return
		}
		delete(rows, name)
		if !reflect.DeepEqual(rows, before) {
			t.Error("unrelated tag catalog changed; retain")
			return
		}
		for _, kind := range []string{"networkdevice", "template"} {
			members, err := c.tagMembers(cleanup, nativeID, kind)
			if err != nil || len(members) != 0 {
				t.Error("tag has consumers; retain", err)
				return
			}
		}
		at := time.Now()
		receipt, err := c.mutate(cleanup, http.MethodDelete, tagsPath+"/"+nativeID, nil)
		if err != nil {
			t.Error(err)
			return
		}
		var task struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(receipt, &task) != nil || !validAPIID(task.Response.ID) {
			t.Error("cleanup task missing")
			return
		}
		for {
			done, err := c.configurationTaskComplete(cleanup, task.Response.ID, at, time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			if done {
				break
			}
			select {
			case <-cleanup.Done():
				t.Error(cleanup.Err())
				return
			case <-time.After(time.Second):
			}
		}
		after, err := c.listTags(cleanup)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Error("tag baseline not restored", err)
			return
		}
		t.Log("supervised exact original-task-bound cleanup and baseline restoration", nativeID)
	})
	for _, description := range []string{"CVK tag recovery create", "CVK tag recovery update"} {
		if description == "CVK tag recovery update" {
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			config.Spec.Source.Inline.Raw = raw(description)
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
				if !validAPIID(op.TaskID) {
					t.Fatal("original task missing")
				}
				if op.Action == "Create" {
					created = op.DeepCopy()
				}
				t.Log("retained original task", op.Action, op.TaskID)
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
			_, _, fingerprint, err := tagReceiptIdentity(applied)
			if err != nil || fingerprint != featureIdentityDigest(tagPayload(row)) {
				t.Fatal("latest-state readback not retained", err)
			}
		}
		t.Log("durable immutable creator and current latest-state rows", len(records.created), len(records.applied))
		t.Log("recovered native manifest revision without replay", config.Generation, nativeID)
	}
	if posts != 1 || puts != 1 {
		t.Fatal("unexpected native mutation count", posts, puts)
	}
	settled = true
	t.Log("exact native mutation counts", posts, puts)
}
