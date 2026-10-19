// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

func TestCombinedTagApplyAcrossReconcilerRestarts(t *testing.T) {
	qualifyCombinedTagApplyAcrossReconcilerRestarts(t, false)
}

func TestCombinedDynamicTagApplyAcrossReconcilerRestarts(t *testing.T) {
	qualifyCombinedTagApplyAcrossReconcilerRestarts(t, true)
}

func qualifyCombinedTagApplyAcrossReconcilerRestarts(t *testing.T, dynamic bool) {
	t.Helper()
	for _, sections := range [][]string{{"templates", "inventory"}, {"inventory", "templates"}} {
		ctx := context.Background()
		a, config := journalFixture(t)
		config.Spec.ManagedSections = sections
		model := combinedTagsModel
		if dynamic {
			model = strings.Replace(model, `"description":"inventory"`, `"description":"inventory","dynamic_rules":[{"member_type":"networkdevice","operation":"AND","items":[{"operation":"EQ","name":"hostname","value":"isolated"},{"operation":"EQ","name":"family","value":"Switches and Hubs"}]}]`, 1)
		}
		config.Spec.Source.Inline.Raw = []byte(model)
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		actual := []observedTag{}
		writes := 0
		stamp := int64(0)
		a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			if r.Method == "GET" && strings.HasPrefix(r.URL.Path, taskPath) {
				fmt.Fprintf(w, `{"response":{"id":"tag-task","serviceType":"Grouping Service","data":"22f33fa0-e275-41c9-a50d-d7e5135adb8c","startTime":%d,"endTime":%d,"isError":false}}`, stamp, stamp)
				return
			}
			if r.URL.Path != tagsPath {
				t.Error("unexpected API")
				w.WriteHeader(400)
				return
			}
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(map[string]any{"response": actual})
				return
			}
			if r.Method != "POST" {
				t.Error("unexpected mutation")
				w.WriteHeader(400)
				return
			}
			var tag observedTag
			if json.NewDecoder(r.Body).Decode(&tag) != nil || tag.Description == nil || *tag.Description != "inventory" {
				t.Error("inventory precedence lost")
			}
			if dynamic != (len(tag.DynamicRules) == 1) {
				t.Error("effective dynamic rules were dropped or shadowed")
			}
			tag.ID = "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
			system := false
			tag.SystemTag = &system
			actual = []observedTag{tag}
			writes++
			stamp = time.Now().UnixMilli()
			fmt.Fprint(w, `{"response":{"taskId":"tag-task"}}`)
		})
		for step := 0; step < 10; step++ {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				break
			}
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || writes != 1 {
			t.Fatal("combined manifest did not converge once", config.Status.Phase, writes)
		}
		if len(config.Status.SectionStatus) != 2 {
			t.Fatal("missing section status")
		}
		total := int64(0)
		for _, s := range config.Status.SectionStatus {
			total += s.ResourceCount
			if s.State != configapi.NetworkControllerSectionStateInSync {
				t.Fatal("section not in sync")
			}
		}
		if total != 1 {
			t.Fatal("shared tag counted twice")
		}
		state := journalState(t, a)
		if len(state.Owners) != 2 || state.Operation != nil {
			t.Fatal("combined ownership or terminal journal incorrect")
		}
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if writes != 1 || journalState(t, a).Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || config.Status.ObservedGeneration != config.Generation {
			t.Fatal("unchanged rule source generation replayed mutation or failed to converge")
		}
	}
}
func TestCombinedApplyRejectsDeviceActionsAndUnsupportedCombinations(t *testing.T) {
	c := &client{}
	raw := strings.Replace(combinedTagsModel, `"inventory":{"tags"`, `"inventory":{"devices":[{"name":"switch","state":"ASSIGN","site":"Global/Lab"}],"tags"`, 1)
	if _, err := c.compileConfigurationApply([]byte(raw), []string{"inventory", "templates"}, nil); err == nil {
		t.Fatal("device action passed combined tag gate")
	}
	if _, err := c.compileConfigurationApply([]byte(combinedTagsModel), []string{"templates", "templates"}, nil); err == nil {
		t.Fatal("duplicate sections accepted")
	}
}

func TestCombinedApplyTransitionRetainsUncertainOldSection(t *testing.T) {
	for _, phase := range []string{"Prepared", "Accepted", "Verified"} {
		a, config := journalFixture(t)
		ctx := context.Background()
		config.Spec.ManagedSections = []string{"inventory"}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		op, err := a.prepareConfiguration(ctx, config, nil, "inventory", "tag", "Create")
		if err != nil {
			t.Fatal(err)
		}
		if phase != "Prepared" {
			if err := a.startConfiguration(ctx, config, nil, op.ID); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(ctx, op.ID, "Accepted", "task"); err != nil {
				t.Fatal(err)
			}
		}
		if phase == "Verified" {
			if err := a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err != nil {
				t.Fatal(err)
			}
		}
		config.Spec.ManagedSections = []string{"templates", "inventory"}
		config.Spec.Source.Inline.Raw = []byte(combinedTagsModel)
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		a.client = &client{}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		status := &configapi.NetworkControllerConfigStatus{}
		_, _, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
		pending := journalState(t, a).Operation
		if phase == "Accepted" {
			if pending == nil || reason != "OperationRecoveryRequired" {
				t.Fatal("accepted old operation released", reason)
			}
		} else if pending != nil || reason != "ApplyProgressing" {
			t.Fatal("safe old boundary stranded", phase, reason)
		}
	}
}

func TestCombinedApplyOwnershipConflictDoesNotPartiallyClaim(t *testing.T) {
	a, config := journalFixture(t)
	ctx := context.Background()
	config.Spec.ManagedSections = []string{"templates", "inventory"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = &ciskov1.NetworkControllerConfigurationStatus{Owners: []ciskov1.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: "foreign", ConfigUID: "foreign"}}}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.claimConfigurationSection(ctx, config, nil); !errors.Is(err, errConfigurationOwned) {
		t.Fatal("combined claim ignored foreign owner", err)
	}
	state := journalState(t, a)
	if len(state.Owners) != 1 || state.Owners[0].ConfigUID != "foreign" {
		t.Fatal("partial claim persisted")
	}
}

func TestCombinedDiscoveryPreflightsBeforeTagWrites(t *testing.T) {
	raw := strings.Replace(string(discoveryModel()), `"inventory":`, `"templates":{"tags":[{"name":"new-tag"}]},"inventory":`, 1)
	reads, writes := 0, 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			writes++
			t.Error("mutation before credential resolution")
			w.WriteHeader(400)
			return
		}
		reads++
		switch r.URL.Path {
		case tagsPath, discoveryPath + "/1/100":
			fmt.Fprint(w, `{"response":[]}`)
		case globalCredentialsPath:
			fmt.Fprint(w, `{"response":{}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(400)
		}
	})
	for _, sections := range [][]string{{"templates", "inventory"}, {"inventory", "templates"}} {
		plan, err := c.compileConfigurationApply([]byte(raw), sections, nil)
		if err != nil {
			t.Fatal(err)
		}
		write, err := plan.executor.next(context.Background())
		if err == nil || write != nil || writes != 0 {
			t.Fatal("unresolved discovery failed to fence earlier tag writes", err)
		}
	}
	if reads == 0 {
		t.Fatal("did not check dependencies")
	}
}

func TestCombinedDiscoveryDoesNotRequireInventoryTags(t *testing.T) {
	raw := strings.Replace(string(discoveryModel()), `"inventory":`, `"templates":{"projects":[{"name":"project"}]},"inventory":`, 1)
	plan, err := (&client{}).compileConfigurationApply([]byte(raw), []string{"templates", "inventory"}, nil)
	if err != nil || plan.executor.count() != 2 {
		t.Fatal("project/discovery composition failed", err)
	}
	for _, section := range plan.sections {
		if section.ResourceCount != 1 {
			t.Fatal("wrong attribution", section)
		}
	}
}
