// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSitePruneManifestAdmission(t *testing.T) {
	for _, mode := range []string{"private", "default-retain", "no-key", "bulk", "delete-config", "report", "mixed-sections"} {
		t.Run(mode, func(t *testing.T) {
			a, config := journalFixture(t)
			configureSiteNativeComparisonFixture(t, a, config)
			config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
			config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
			switch mode {
			case "default-retain":
				config.Spec.DeletionPolicy = ""
			case "no-key":
				config.Spec.SecretRefs = nil
			case "bulk":
				config.Annotations = map[string]string{siteBulkAnnotation: "true"}
			case "delete-config":
				config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			case "report":
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			case "mixed-sections":
				config.Spec.ManagedSections = append(config.Spec.ManagedSections, "network_settings")
			}
			if supportedConfigurationApply(config) != (mode == "private" || mode == "default-retain") {
				t.Fatal("site prune admission escaped qualified shape")
			}
		})
	}
}

var sitePruneManifestModes = []string{"complete", "consumer", "foreign-child", "missing-owned", "replaced-owned", "reappeared-retired", "lost-ack", "failed-task"}

func TestSitePruneManifestLifecycle(t *testing.T) {
	for _, cm := range []bool{false, true} {
		for _, mode := range sitePruneManifestModes {
			t.Run(fmt.Sprintf("cm-%t/%s", cm, mode), func(t *testing.T) { exerciseSitePruneManifest(t, mode, cm, nil) })
		}
	}
}

func TestSitePruneHierarchyManifestLifecycle(t *testing.T) {
	for _, cm := range []bool{false, true} {
		t.Run(fmt.Sprintf("cm-%t", cm), func(t *testing.T) { exerciseSitePruneManifestKinds(t, "complete", cm, nil, true) })
	}
}

func exerciseSitePruneManifest(t *testing.T, mode string, cm bool, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	exerciseSitePruneManifestKinds(t, mode, cm, install, false)
}

func exerciseSitePruneManifestKinds(t *testing.T, mode string, cm bool, install func(*adapter, *configapi.NetworkControllerConfig), hierarchy bool, transforms ...func([]byte) []byte) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	if install != nil {
		install(a, config)
	}
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	configureSiteNativeComparisonFixture(t, a, config)
	var configured cvk.NetworkController
	if err := a.statusReader.Get(ctx, a.key, &configured); err != nil {
		t.Fatal(err)
	}
	a.generation = configured.Generation
	full := `{"catalyst_center":{"sites":{"areas":[{"name":"Probe","parent_name":"Global"},{"name":"Child","parent_name":"Global/Probe"}]}}}`
	count := 2
	if hierarchy {
		count = 3
		full = `{"catalyst_center":{"sites":{"areas":[{"name":"Probe","parent_name":"Global"}],"buildings":[{"name":"Building","parent_name":"Global/Probe","country":"United States","latitude":37.33812,"longitude":-121.88612}],"floors":[{"name":"Floor","parent_name":"Global/Probe/Building","floor_number":1,"rf_model":"Free Space","width":10,"length":15,"height":3,"units_of_measure":"meters"}]}}}`
	}
	const empty = `{"catalyst_center":{"sites":{"areas":[]}}}`
	model := &core.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "prune-model", Namespace: config.Namespace}, Data: map[string]string{"model.json": full}}
	if cm {
		if install == nil {
			model.UID = "prune-source"
		}
		if err := a.statusWriter.Create(ctx, model); err != nil {
			t.Fatal(err)
		}
		config.Spec.Source.Inline = nil
		config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: model.Name, Key: "model.json"}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	selectionOptions := nacmodel.SiteSelectionOptions{}
	if mode == "selection" {
		transforms = append(transforms, func(raw []byte) []byte { return encodeSiteSelectionFixture(t, raw, selectionOptions) })
	}
	update := func(raw string) {
		for _, transform := range transforms {
			raw = string(transform([]byte(raw)))
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if cm {
			model.Data["model.json"] = raw
			if err := a.statusWriter.Update(ctx, model); err != nil {
				t.Fatal(err)
			}
		} else {
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(raw)}
		}
		if len(transforms) > 0 {
			config.Spec.ModelSource.Exporter = SiteDefaultsExporter
		}
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	update(full)
	type nativeTask struct {
		id         string
		start, end time.Time
	}
	tasks := map[string]nativeTask{}
	actual := map[string]Site{"root": {ID: "root", Name: "Global", NameHierarchy: "Global", Type: "global"}}
	posts, deletes, epoch := 0, 0, 0
	firstChild := ""
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodDelete {
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			op := nc.Status.Configuration.Operation
			if op == nil || op.Phase != "Submitting" || op.SubmittedAt == nil {
				t.Error("manifest mutation lacks shared submission permit")
				w.WriteHeader(500)
				return
			}
			id := ""
			if r.Method == http.MethodPost {
				kind := ""
				for _, candidate := range []string{"area", "building", "floor"} {
					if r.URL.Path == siteDesignMutationEndpoint(candidate) {
						kind = candidate
					}
				}
				if kind == "" || !hierarchy && kind != "area" {
					t.Error("wrong native site endpoint")
					w.WriteHeader(500)
					return
				}
				var body map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("bad payload")
					w.WriteHeader(500)
					return
				}
				parentID, valid := body["parentId"].(string)
				parent, ok := actual[parentID]
				if !valid || !ok {
					t.Error("missing actual parent")
					w.WriteHeader(500)
					return
				}
				posts++
				id = fmt.Sprintf("%08x-1111-4111-8111-%012x", posts, posts)
				name, _ := body["name"].(string)
				site := Site{ID: id, Name: name, NameHierarchy: parent.NameHierarchy + "/" + name, ParentID: parent.ID, Type: kind}
				for key, dest := range map[string]**float64{"latitude": &site.Latitude, "longitude": &site.Longitude, "floorNumber": &site.FloorNumber, "width": &site.Width, "length": &site.Length, "height": &site.Height} {
					if value, ok := body[key].(float64); ok {
						copy := value
						*dest = &copy
					}
				}
				for key, dest := range map[string]**string{"country": &site.Country, "rfModel": &site.RFModel, "unitsOfMeasure": &site.UnitsOfMeasure} {
					if value, ok := body[key].(string); ok {
						copy := value
						*dest = &copy
					}
				}
				actual[id] = site
				if name == "Child" && firstChild == "" {
					firstChild = id
				}
			} else {
				for _, kind := range []string{"area", "building", "floor"} {
					prefix := siteDesignMutationEndpoint(kind) + "/"
					if strings.HasPrefix(r.URL.Path, prefix) {
						id = strings.TrimPrefix(r.URL.Path, prefix)
						if site, ok := actual[id]; ok && site.Type != kind {
							t.Error("Delete endpoint kind mismatch")
							w.WriteHeader(500)
							return
						}
					}
				}
				if _, exists := actual[id]; !exists || id == "root" || r.ContentLength > 0 {
					t.Error("unbound native Delete")
					w.WriteHeader(500)
					return
				}
				for _, site := range actual {
					if site.ParentID == id {
						t.Error("parent deleted before child")
						w.WriteHeader(500)
						return
					}
				}
				delete(actual, id)
				deletes++
			}
			task := fmt.Sprintf("manifest-task-%d", posts+deletes)
			tasks[task] = nativeTask{id: id, start: op.SubmittedAt.Time, end: time.Now()}
			if mode == "lost-ack" && posts == 1 {
				fmt.Fprint(w, `{"response":{}}`)
				return
			}
			fmt.Fprintf(w, `{"response":{"taskId":%q}}`, task)
			return
		}
		if r.Method != http.MethodGet {
			t.Error("unexpected native method")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == sitesPath {
			rows := []Site{}
			for _, site := range actual {
				rows = append(rows, site)
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i].NameHierarchy < rows[j].NameHierarchy })
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
			return
		}
		if strings.HasPrefix(r.URL.Path, taskPath) {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			task, exists := tasks[id]
			if !exists {
				t.Error("unknown native task")
				w.WriteHeader(404)
				return
			}
			row := map[string]any{"id": id, "rootId": id, "isError": mode == "failed-task", "startTime": task.start.UnixMilli(), "endTime": task.end.UnixMilli(), "data": task.id}
			if strings.HasSuffix(r.URL.Path, "/tree") {
				json.NewEncoder(w).Encode(map[string]any{"response": []any{row}})
			} else {
				json.NewEncoder(w).Encode(map[string]any{"response": row})
			}
			return
		}
		if mode == "consumer" && epoch == 1 && strings.HasSuffix(r.URL.Path, "/ntpSettings") {
			fmt.Fprint(w, `{"response":{"ntp":{"servers":["192.0.2.1"]}}}`)
			return
		}
		for id, site := range actual {
			if site.Type == "floor" && r.URL.Path == siteDesignMutationEndpoint("floor")+"/"+id+"/plannedAccessPointPositions" {
				fmt.Fprint(w, `{"response":[]}`)
				return
			}
			if serveEmptySiteRetirementConsumers(t, w, r, id) {
				return
			}
		}
		if r.URL.Path == pnpDevicesPath {
			fmt.Fprint(w, `[]`)
			return
		}
		for _, path := range []string{assignedDevicesPath, networkProfilesPath, wirelessProfilesPath, fabricSitesPath, fabricZonesPath, sitePoolsPath} {
			if r.URL.Path == path {
				fmt.Fprint(w, `{"response":[]}`)
				return
			}
		}
		t.Errorf("unexpected lifecycle read %s", r.URL.Path)
		w.WriteHeader(500)
	})
	run := func(converge bool) {
		for i := 0; i < 60; i++ {
			if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.Operation.Phase = "Unknown"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && (state == nil || state.Operation == nil) {
				if !converge {
					t.Fatal("unsafe lifecycle unexpectedly converged")
				}
				return
			}
			if !converge && i >= 5 {
				return
			}
		}
		t.Fatal("manifest lifecycle did not converge", config.Status.Phase, config.Status.Conditions)
	}
	if mode == "lost-ack" || mode == "failed-task" {
		run(false)
		state := journalState(t, a)
		if posts != 1 || deletes != 0 || state.Operation == nil {
			t.Fatal("uncertain manifest create replayed or released")
		}
		return
	}
	run(true)
	if posts != count || deletes != 0 || len(journalState(t, a).ActionApplications) != count {
		t.Fatal("initial manifest creation evidence missing")
	}
	if mode == "selection" {
		original := journalState(t, a).DeepCopy()
		for _, options := range []nacmodel.SiteSelectionOptions{
			{ManageGlobalSettings: true},
			{ManagedSites: []string{"Global/Probe/Building/Floor"}, ManageSpecificSitesOnly: true},
			{ManagedSites: []string{"Global/Probe"}, ManageSpecificSitesOnly: true},
		} {
			selectionOptions = options
			update(full)
			run(true)
			if posts != count || deletes != 0 || !reflect.DeepEqual(original, journalState(t, a)) {
				t.Fatal("selection pruned excluded native sites or lost custody")
			}
		}
		selectionOptions = nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Probe/Building"}}
		var sourceRoot map[string]any
		json.Unmarshal([]byte(full), &sourceRoot)
		sourceRoot["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"] = []any{}
		withoutFloor, _ := json.Marshal(sourceRoot)
		update(string(withoutFloor))
		run(true)
		if deletes != 1 || posts != 3 || len(actual) != 3 {
			t.Fatal("selected omitted floor was not pruned independently")
		}
		update(full)
		run(true)
		state := journalState(t, a)
		archives, err := siteHistoryCohorts(state)
		if err != nil || len(archives) != 1 || posts != 4 || deletes != 1 || len(actual) != 4 || len(state.ActionApplications) != 5 || len(state.Owners) != 1 || state.Operation != nil {
			t.Fatal("selected recreation lost retained parent lineage", err)
		}
		return
	}
	epoch = 1
	update(empty)
	switch mode {
	case "foreign-child":
		actual["foreign"] = Site{ID: "foreign", Name: "Foreign", NameHierarchy: "Global/Probe/Child/Foreign", ParentID: firstChild, Type: "area"}
	case "missing-owned":
		delete(actual, firstChild)
	case "replaced-owned":
		old := actual[firstChild]
		delete(actual, firstChild)
		old.ID = "replacement"
		actual[old.ID] = old
	}
	if mode == "consumer" || mode == "foreign-child" || mode == "missing-owned" || mode == "replaced-owned" {
		run(false)
		if posts != 2 || deletes != 0 || len(journalState(t, a).Owners) != 1 {
			t.Fatal("blocked pruning changed native state or custody")
		}
		return
	}
	run(true)
	if deletes != count || len(actual) != 1 || len(journalState(t, a).ActionApplications) != 2*count {
		t.Fatal("manifest prune did not retain creator/retirement cohort")
	}
	epoch = 2
	update(full)
	if mode == "reappeared-retired" {
		actual[firstChild] = Site{ID: firstChild, Name: "Moved", NameHierarchy: "Global/Moved", ParentID: "root", Type: "area"}
		run(false)
		if posts != 2 || deletes != 2 {
			t.Fatal("reappeared retired UUID gained create authority")
		}
		return
	}
	run(true)
	if posts != 2*count || len(journalState(t, a).ActionApplications) != 3*count {
		t.Fatal("manifest recreation did not archive both predecessors")
	}
	epoch = 3
	update(empty)
	run(true)
	state := journalState(t, a)
	archives, err := siteHistoryCohorts(state)
	if err != nil || len(archives) != count || posts != 2*count || deletes != 2*count || len(actual) != 1 || len(state.ActionApplications) != 4*count || len(state.Owners) != 1 || state.Operation != nil {
		t.Fatal("complete manifest lifecycle lost ownership/history", err)
	}
}
