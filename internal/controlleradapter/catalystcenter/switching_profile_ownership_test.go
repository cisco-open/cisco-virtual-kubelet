// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var switchingCustodyModes = []string{"two-creates", "created", "capacity", "cohort-capacity", "last-slot", "missing-receipt", "corrupt-receipt", "duplicate-receipt", "foreign-receipt", "foreign-config-name", "replacement", "renamed-owned", "missing-native", "retain-reselect", "owned-update", "import", "wrong-result-uuid", "wrong-service", "taskless", "source-before-publication", "owner-before-publication", "publish-conflict", "publish-lost-response", "publish-pruned"}

func TestSwitchingCreatorPublicCustody(t *testing.T) {
	for _, mode := range switchingCustodyModes {
		t.Run(mode, func(t *testing.T) { exerciseSwitchingCreatorCustody(t, mode, nil) })
	}
}

type switchingReceiptWriter struct {
	ctrlclient.Client
	mode string
	done bool
}

func (w *switchingReceiptWriter) Status() ctrlclient.SubResourceWriter {
	return &switchingReceiptStatus{SubResourceWriter: w.Client.Status(), owner: w}
}

type switchingReceiptStatus struct {
	ctrlclient.SubResourceWriter
	owner *switchingReceiptWriter
}

func (w *switchingReceiptStatus) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok || w.owner.done || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || !switchingCreateSelected(nc.Status.Configuration.Operation.Resource) || nc.Status.Configuration.Operation.Phase != "Verified" {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	w.owner.done = true
	if w.owner.mode == "publish-conflict" {
		return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "networkcontrollers"}, nc.Name, errors.New("qualification status conflict"))
	}
	if w.owner.mode == "publish-pruned" {
		nc.Status.Configuration.ActionApplications = nil
	}
	err := w.SubResourceWriter.Update(ctx, obj, opts...)
	if err == nil && w.owner.mode == "publish-lost-response" {
		return errors.New("qualification lost publication response")
	}
	return err
}
func exerciseSwitchingCreatorCustody(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	model := func(selected, templates, peer bool) []byte {
		rows := []any{}
		if selected {
			p := map[string]any{"name": "A"}
			if templates {
				p["dayn_templates"] = []string{"P#T"}
			}
			rows = append(rows, p)
		}
		if peer {
			rows = append(rows, map[string]any{"name": "B"})
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_profiles": map[string]any{"switching": rows}}})
		return raw
	}
	config.Spec.ManagedSections = []string{"network_profiles"}
	config.Spec.Source.Inline.Raw = model(true, false, mode == "cohort-capacity" || mode == "two-creates")
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	occupied := 0
	if mode == "capacity" {
		occupied = 256
	}
	if mode == "cohort-capacity" || mode == "last-slot" {
		occupied = 255
	}
	if occupied > 0 {
		nc, _ := a.currentController(ctx)
		nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
		submitted := metav1.NewTime(time.Now().Add(-time.Minute))
		verified := metav1.NewTime(time.Now().Add(-time.Second))
		for i := 0; i < occupied; i++ {
			op := cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", i+1), Section: "inventory", Resource: fmt.Sprintf("occupied-%d", i), Action: "Update", Phase: "Verified", ConfigName: "other", ConfigUID: "other", ConfigGeneration: 1, ControllerGeneration: 1, TaskID: fmt.Sprintf("old-task-%d", i), SubmittedAt: &submitted}
			nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: op.Resource, Operation: op, VerifiedAt: &verified})
		}
		if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	var publication *switchingReceiptWriter
	if strings.HasPrefix(mode, "publish-") {
		publication = &switchingReceiptWriter{Client: a.statusWriter, mode: mode}
		a.statusWriter = publication
	}
	actual := map[string]networkProfile{}
	if mode == "import" {
		actual["A"] = networkProfile{ID: switchingResultUUID, Name: "A", Namespace: "switching", Attributes: []networkProfileAttribute{}}
	}
	posts, puts := 0, 0
	taskTimes := map[string]int64{}
	taskResults := map[string]string{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
		switch {
		case r.URL.Path == authPath:
			w.Write([]byte(`{"Token":"test"}`))
		case r.URL.Path == sitesPath:
			reply(sampleSites())
		case r.URL.Path == templateProjectsPath:
			json.NewEncoder(w).Encode([]templateProject{{ID: "project", Name: "P"}})
		case r.URL.Path == templatesPath:
			json.NewEncoder(w).Encode([]templateSummary{{ID: "template", Name: "T", ProjectID: "project"}})
		case r.Method == "GET" && r.URL.Path == networkProfilesPath:
			rows := []networkProfile{}
			for _, p := range actual {
				rows = append(rows, p)
			}
			reply(rows)
		case (r.Method == "POST" && r.URL.Path == networkProfilesPath) || (r.Method == "PUT" && strings.HasPrefix(r.URL.Path, networkProfilesPath+"/")):
			var p networkProfile
			if json.NewDecoder(r.Body).Decode(&p) != nil {
				t.Error("bad profile body")
			}
			if r.Method == "POST" {
				posts++
				if _, ok := actual[p.Name]; ok {
					t.Error("duplicate creation")
				}
			} else {
				puts++
				if old, ok := actual[p.Name]; !ok || r.URL.Path != networkProfilesPath+"/"+old.ID {
					t.Error("update escaped original UUID")
				}
			}
			p.ID = switchingResultUUID
			if p.Name == "B" {
				p.ID = "7ecf67b6-e218-441e-abbe-99603d7de91c"
			}
			actual[p.Name] = p
			tid := fmt.Sprintf("task-%d", posts+puts)
			taskTimes[tid] = time.Now().UnixMilli()
			taskResults[tid] = p.ID
			if mode == "taskless" {
				w.Write([]byte(`{}`))
				return
			}
			reply(map[string]string{"taskId": tid})
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, networkProfilesPath+"/"):
			for _, p := range actual {
				if r.URL.Path == networkProfilesPath+"/"+p.ID {
					reply(p)
					return
				}
			}
			w.WriteHeader(404)
		case strings.HasPrefix(r.URL.Path, taskPath):
			tid := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := taskTimes[tid]
			if !ok {
				t.Error("foreign task read")
				w.WriteHeader(404)
				return
			}
			result := taskResults[tid]
			if mode == "wrong-result-uuid" {
				result = "7ecf67b6-e218-441e-abbe-99603d7de91c"
			}
			service := "Site Profile Service"
			if mode == "wrong-service" {
				service = "OTHER"
			}
			root := map[string]any{"id": tid, "startTime": stamp, "endTime": stamp, "isError": false, "serviceType": service, "progress": "Network Profile [" + result + "] Successfully Created"}
			writeDefinitionTaskFixture(w, r, root)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	a.client = c
	key := ctrlclient.ObjectKeyFromObject(config)
	tick := func() {
		t.Helper()
		restart := *a
		if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
	}
	changed := false
	publishedTampered := false
	for range 24 {
		tick()
		nc, _ := a.currentController(ctx)
		s := nc.Status.Configuration
		if s != nil && s.Operation != nil && s.Operation.Phase == "Accepted" && !changed {
			if mode == "source-before-publication" {
				config.Spec.Source.Inline.Raw = model(false, false, true)
				config.Generation++
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				changed = true
			}
			if mode == "owner-before-publication" {
				s.Owners[0].ConfigUID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				changed = true
			}
		}
		if !publishedTampered && s != nil && s.Operation != nil && s.Operation.Phase == "Verified" && switchingCreateSelected(s.Operation.Resource) {
			switch mode {
			case "missing-receipt":
				s.ActionApplications = nil
				publishedTampered = true
			case "corrupt-receipt":
				s.ActionApplications[0].Resource += "corrupt"
				publishedTampered = true
			case "duplicate-receipt":
				s.ActionApplications = append(s.ActionApplications, s.ActionApplications[0])
				publishedTampered = true
			case "foreign-config-name":
				s.ActionApplications[0].Operation.ConfigName = "foreign-name"
				publishedTampered = true
			case "foreign-receipt":
				s.ActionApplications[0].Operation.ConfigUID = "foreign"
				publishedTampered = true
			}
			if publishedTampered {
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					if mode == "duplicate-receipt" && apierrors.IsInvalid(err) {
						stored, readErr := a.currentController(ctx)
						if readErr != nil {
							t.Fatal(readErr)
						}
						rows, recordErr := switchingCreatorRecords(stored.Status.Configuration, string(config.UID))
						if recordErr != nil || len(rows) != 1 || posts != 1 || puts != 0 {
							t.Fatal("duplicate schema refusal changed original custody")
						}
						t.Log("API schema refused duplicate receipt key and preserved original custody")
						return
					}
					t.Fatal(err)
				}
			}
		}
	}
	nc, _ := a.currentController(ctx)
	s := nc.Status.Configuration
	blocked := mode == "capacity" || mode == "cohort-capacity" || mode == "missing-receipt" || mode == "corrupt-receipt" || mode == "duplicate-receipt" || mode == "foreign-receipt" || mode == "foreign-config-name" || mode == "wrong-result-uuid" || mode == "wrong-service" || mode == "taskless" || mode == "source-before-publication" || mode == "owner-before-publication" || mode == "publish-pruned"
	if mode == "capacity" || mode == "cohort-capacity" {
		if posts != 0 || puts != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("capacity allowed mutation")
		}
		return
	}
	if blocked {
		if posts != 1 || puts != 0 || s == nil || s.Operation == nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("uncertain custody released or replayed", posts, puts, config.Status.Conditions)
		}
		return
	}
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || s == nil || s.Operation != nil {
		t.Fatal("creator lifecycle not InSync", config.Status.Conditions)
	}
	expected := 1
	if mode == "two-creates" {
		expected = 2
	}
	if mode == "import" {
		expected = 0
	}
	rows, err := switchingCreatorRecords(s, string(config.UID))
	if err != nil || len(rows) != expected || posts != expected || puts != 0 {
		t.Fatal("unexpected creator custody", posts, puts, len(rows), err)
	}
	if mode == "last-slot" && len(s.ActionApplications) != 256 {
		t.Fatal("last slot lost unrelated receipts")
	}
	if publication != nil && !publication.done {
		t.Fatal("publication fault not exercised")
	}
	if mode == "replacement" || mode == "renamed-owned" || mode == "missing-native" {
		if mode == "renamed-owned" {
			p := actual["A"]
			delete(actual, "A")
			p.Name = "B"
			actual["B"] = p
			config.Spec.Source.Inline.Raw = model(false, false, true)
			config.Generation++
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		} else if mode == "replacement" {
			p := actual["A"]
			p.ID = "7ecf67b6-e218-441e-abbe-99603d7de91c"
			actual["A"] = p
		} else {
			delete(actual, "A")
		}
		for range 3 {
			tick()
		}
		if posts != 1 || puts != 0 || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("owned UUID silently recreated/adopted")
		}
	}
	if mode == "retain-reselect" {
		for _, selected := range []bool{false, true} {
			config.Spec.Source.Inline.Raw = model(selected, false, false)
			config.Generation++
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			for range 4 {
				tick()
			}
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("Retain selection failed", config.Status.Conditions)
			}
		}
		if posts != 1 || puts != 0 {
			t.Fatal("Retain re-selection replayed")
		}
	}
	if mode == "owned-update" || mode == "import" {
		before, _ := json.Marshal(rows)
		config.Spec.Source.Inline.Raw = model(true, true, false)
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for range 10 {
			tick()
		}
		nc, _ := a.currentController(ctx)
		afterRows, err := switchingCreatorRecords(nc.Status.Configuration, string(config.UID))
		after, _ := json.Marshal(afterRows)
		if err != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || puts != 1 || posts != expected || string(before) != string(after) {
			t.Fatal("Update changed creator custody or adopted import", config.Status.Conditions)
		}
	}
}

func TestSwitchingCreatorMandatoryReceiptRelease(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"network_profiles"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	resource := switchingCreatePrefix + switchingNameDigest("A")
	identity := configurationResourceIdentity{parent: "sp2-" + strings.Repeat("a", 64)}
	op, err := a.prepareConfiguration(ctx, config, nil, "network_profiles", resource, "Create", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.startConfiguration(ctx, config, nil, op.ID, identity); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Accepted", "original-task"); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
		t.Fatal(err)
	}
	if err := a.advanceConfiguration(ctx, op.ID, "Verified", ""); err == nil {
		t.Fatal("generic verification bypassed creator receipt")
	}
	nc, _ := a.currentController(ctx)
	nc.Status.Configuration.Operation.Phase = "Verified"
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err == nil {
		t.Fatal("receipt-less Verified journal released")
	}
	if state := journalState(t, a); state.Operation == nil || state.Operation.ID != op.ID {
		t.Fatal("fence lost")
	}
}
