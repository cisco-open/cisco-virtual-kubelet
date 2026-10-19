// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var pnpCapacityModes = []string{"full", "cohort", "bytes", "edge", "late-count", "late-bytes", "publication-count", "publication-bytes"}

func TestPnPRegistrationCapacity(t *testing.T) {
	for _, mode := range pnpCapacityModes {
		t.Run(mode, func(t *testing.T) { exercisePnPRegistrationCapacity(t, mode, nil) })
	}
}

func pnpCapacityHistory(count int, large bool) []cvk.NetworkControllerConfigurationActionApplication {
	now := metav1.NewTime(time.Now().Add(-time.Second).UTC().Truncate(time.Second))
	out := make([]cvk.NetworkControllerConfigurationActionApplication, count)
	for i := range out {
		key := fmt.Sprintf("unrelated-%d", i)
		out[i] = cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: key, Operation: cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", i+1), ConfigName: "retained", ConfigUID: "retained-owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "templates", Resource: key, Action: "Update", Phase: "Verified", TaskID: "old-task", SubmittedAt: &now}, VerifiedAt: &now}
		if large {
			out[i].EffectReceipts = make([]string, 256)
			for j := range out[i].EffectReceipts {
				out[i].EffectReceipts[j] = fmt.Sprintf("%03d", j) + strings.Repeat("x", 509)
			}
		}
	}
	return out
}

func exercisePnPRegistrationCapacity(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	exercisePnPRegistrationCapacityEntry(t, mode, false, install)
}

func TestPnPPrivateRegistrationCapacity(t *testing.T) {
	for _, mode := range pnpCapacityModes {
		t.Run(mode, func(t *testing.T) { exercisePnPRegistrationCapacityEntry(t, mode, true, nil) })
	}
}

// Only the initial plan/submission uses the private entry. The same public
// journal reconciler resumes it, proving that the planners do not fork recovery.
func exercisePnPRegistrationCapacityEntry(t *testing.T, mode string, privateFirst bool, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
	if err != nil {
		t.Fatal(err)
	}
	if mode == "cohort" {
		second := wants[0]
		second.Name = "second"
		second.Serial = "FOC12345679"
		wants = append(wants, second)
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": wants}}})
	config.Spec.Source.Inline.Raw = raw
	config.Spec.ManagedSections = []string{"inventory"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	posts := 0
	rows := []map[string]any{}
	late := strings.HasPrefix(mode, "late-")
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method == http.MethodGet && r.URL.Path == pnpDevicesPath:
			if late {
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Error(err)
					w.WriteHeader(500)
					return
				}
				if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.Phase == "Submitting" {
					late = false
					if mode == "late-count" {
						nc.Status.Configuration.ActionApplications = pnpCapacityHistory(256, false)
					} else {
						nc.Status.Configuration.ActionApplications = pnpCapacityHistory(2, true)
					}
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Error(err)
					}
				}
			}
			json.NewEncoder(w).Encode(rows)
		case r.Method == http.MethodGet && r.URL.Path == devicesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case r.Method == http.MethodPost && r.URL.Path == pnpDevicesPath:
			posts++
			row := pnpFixture("new-registration", wants[0].Serial)
			row["deviceInfo"].(map[string]any)["hostname"] = wants[0].Name
			rows = append(rows, row)
			json.NewEncoder(w).Encode(row)
		default:
			t.Error("unexpected native request", r.Method, r.URL.Path)
			w.WriteHeader(400)
		}
	})
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = a.client.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 255
	if mode == "full" {
		count = 256
	}
	if mode == "bytes" || mode == "publication-bytes" || mode == "late-bytes" {
		count = 2
	}
	history := pnpCapacityHistory(count, mode == "bytes")
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: config.Name, ConfigUID: string(config.UID)}}, ActionApplications: history}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	// Reload actual API representation for equality, including serialized timestamps.
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := nc.Status.Configuration.DeepCopy()
	tick := func() {
		t.Helper()
		if privateFirst {
			privateFirst = false
			write, id, err := a.planPnPRegistrationStage(ctx, config, nil, wants[0])
			if errors.Is(err, errPnPRegistrationCapacity) {
				return
			}
			if err != nil || id != "" || write == nil {
				t.Fatal("private registration planning failed", err)
			}
			err = a.submitConfigurationWrite(ctx, config, nil, "inventory", write)
			if err != nil && !errors.Is(err, errConfigurationNotDispatched) {
				t.Fatal("private registration submission failed", err)
			}
			return
		}
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
	}
	tick()
	if strings.HasPrefix(mode, "publication-") {
		if posts != 1 || journalState(t, a).Operation == nil || journalState(t, a).Operation.Phase != "Verifying" {
			t.Fatal("publication fixture lacks acknowledged native UUID")
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "publication-count" {
			current.Status.Configuration.ActionApplications = pnpCapacityHistory(256, false)
		} else {
			current.Status.Configuration.ActionApplications = pnpCapacityHistory(2, true)
		}
		if err := a.statusWriter.Status().Update(ctx, current); err != nil {
			t.Fatal(err)
		}
	}
	if strings.HasPrefix(mode, "late-") {
		if posts != 0 || journalState(t, a).Operation != nil {
			t.Fatal("late capacity did not retire proven zero-write operation")
		}
	}
	retained := journalState(t, a).DeepCopy()
	for range 8 {
		tick()
	}
	state := journalState(t, a)
	if mode == "edge" {
		if posts != 1 || state.Operation != nil || len(state.ActionApplications) != 256 || !reflect.DeepEqual(before.ActionApplications, state.ActionApplications[:255]) {
			t.Fatal("exact capacity/owned no-op failed", posts)
		}
		return
	}
	expected := 0
	if strings.HasPrefix(mode, "publication-") {
		expected = 1
	}
	if posts != expected || !reflect.DeepEqual(retained, state) {
		t.Fatal("capacity refusal lost ownership, changed work or dispatched", posts)
	}
	capacity := false
	for _, c := range config.Status.Conditions {
		if c.Type == configapi.NetworkControllerConfigConditionReady && c.Status == metav1.ConditionFalse && c.Reason == "PnPRegistrationCapacityUnavailable" {
			capacity = true
		}
	}
	if !capacity {
		t.Fatal("capacity limit not reported", config.Status)
	}
	if expected == 0 && state.Operation != nil {
		t.Fatal("capacity exhaustion left a new journal operation")
	}
	if expected == 1 && (state.Operation == nil || state.Operation.CreatedResourceID != "new-registration" || state.Operation.Phase != "Verifying") {
		t.Fatal("acknowledged ownership evidence lost")
	}
}

func TestPnPRegistrationPrivatePlannerScope(t *testing.T) {
	for _, mode := range []string{"report", "prune-delete", "deletion-delete", "secrets", "sections", "version", "format", "unresolved"} {
		t.Run(mode, func(t *testing.T) {
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Spec.Source.Inline.Raw = []byte(pnpIntentFixture())
			switch mode {
			case "report":
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			case "prune-delete":
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
			case "deletion-delete":
				config.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			case "secrets":
				config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "inventory", Path: "unused", Source: "unused"}}
			case "sections":
				config.Spec.ManagedSections = []string{"inventory", "sites"}
			case "version":
				config.Spec.ModelSource.ModelVersion = "0.6.0"
			case "format":
				config.Spec.ModelSource.Format = "other"
			case "unresolved":
				config.Spec.ModelSource.Resolved = false
			}
			wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
			if err != nil {
				t.Fatal(err)
			}
			// No native client is installed: unsupported scopes must fail before access.
			w, id, err := a.planPnPRegistrationStage(context.Background(), config, nil, wants[0])
			if err == nil || w != nil || id != "" || journalState(t, a) != nil {
				t.Fatal("unsupported private planner scope admitted", mode, err)
			}
		})
	}
}

func TestPnPPrivatePlannerFrozenSource(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	config.Spec.Source.Inline.Raw = []byte(pnpIntentFixture())
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	posts := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method == "GET" && r.URL.Path == pnpDevicesPath:
			fmt.Fprint(w, `[]`)
		case r.Method == "GET" && r.URL.Path == devicesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case r.Method == "POST":
			posts++
			fmt.Fprint(w, `{"id":"unexpected"}`)
		default:
			t.Error("unexpected API")
			w.WriteHeader(400)
		}
	})
	wants, err := parsePnPInventory(config.Spec.Source.Inline.Raw)
	if err != nil {
		t.Fatal(err)
	}
	write, id, err := a.planPnPRegistrationStage(ctx, config, nil, wants[0])
	if err != nil || write == nil || id != "" {
		t.Fatal("private plan failed", err)
	}
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(pnpIntentFixture(), "edge", "changed", 1))
	config.Generation++
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	err = a.submitConfigurationWrite(ctx, config, nil, "inventory", write)
	if !errors.Is(err, errConfigurationNotDispatched) || posts != 0 || journalState(t, a).Operation != nil {
		t.Fatal("borrowed source object changed the prepared payload revision", err, posts)
	}
}
