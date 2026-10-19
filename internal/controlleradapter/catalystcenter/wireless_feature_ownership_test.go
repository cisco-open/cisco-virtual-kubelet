// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"net/http"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func featureOwnedTestResource() string {
	return featureCreatePrefix + "fra:" + featureIdentityDigest("FRA") + ":" + featureIdentityDigest("effect")
}
func TestFeatureCreatorReceiptRequiredForRelease(t *testing.T) {
	for _, mode := range []string{"known", "missing", "duplicate", "pruned"} {
		t.Run(mode, func(t *testing.T) { exerciseFeatureCreatorRelease(t, mode, nil) })
	}
}
func exerciseFeatureCreatorRelease(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()

	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"feature_templates"}
	config.Spec.Source.Inline.Raw = []byte(wirelessFRAModel)
	if install != nil {
		install(a, config)
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareConfiguration(ctx, config, nil, "feature_templates", featureOwnedTestResource(), "Create")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.startConfiguration(ctx, config, nil, op.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Accepted", "creator-task"); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Verified", ""); err != errConfigurationTransition {
		t.Fatal("generic verified bypassed creator proof", err)
	}
	current, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current.Status.Configuration.Operation.Phase = "Verified"
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	verified := current.Status.Configuration.Operation.DeepCopy()
	key, _ := featureCreateKey(verified.Resource)
	receipt := cvk.NetworkControllerConfigurationActionApplication{Section: verified.Section, Resource: featureCreatedPrefix + key + ":" + featureTestFRAUUID, Operation: *verified, VerifiedAt: &now}
	current.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{receipt}
	if mode == "pruned" {
		a.statusWriter = pruningActionReceiptClient{Client: a.statusWriter}
	}
	err = a.persistConfigurationActionReceipt(ctx, current)
	if mode == "pruned" {
		if err == nil {
			t.Fatal("pruned publication reported success")
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if mode == "missing" || mode == "duplicate" {
		current, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if mode == "missing" {
			current.Status.Configuration.ActionApplications = nil
		} else {
			duplicate := receipt
			duplicate.Operation = *receipt.Operation.DeepCopy()
			duplicate.Operation.ID = strings.Repeat("d", 32)
			duplicate.Resource = featureCreatedPrefix + key + ":" + featureTestCAUUID
			current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, duplicate)
		}
		if err = a.statusWriter.Status().Update(ctx, current); err != nil {
			t.Fatal(err)
		}
	}
	err = a.finishConfiguration(ctx, op.ID)
	state := journalState(t, a)
	if mode == "known" {
		if err != nil || state.Operation != nil || len(state.ActionApplications) != 1 {
			t.Fatal("verified creator receipt not retained", err, state)
		}
	} else if err == nil || state.Operation == nil || state.Operation.Phase != "Verified" {
		t.Fatal("unproven ownership released", mode, err, state)
	}
}

func TestFeatureCreatorCapacityAndMalformedRecords(t *testing.T) {
	op := &cvk.NetworkControllerConfigurationOperation{Section: "feature_templates", Resource: featureOwnedTestResource(), Action: "Create"}
	state := &cvk.NetworkControllerConfigurationStatus{}
	if err := featureCreateCapacity(state, op); err != nil {
		t.Fatal(err)
	}
	for index := range 256 {
		state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: fmt.Sprint("other-", index)})
	}
	before := state.DeepCopy()
	if err := featureCreateCapacity(state, op); err != errConfigurationBusy || !reflect.DeepEqual(state, before) {
		t.Fatal("full capacity not fenced", err)
	}
	for _, resource := range []string{featureCreatePrefix, featureCreatePrefix + "fra:bad:bad", featureCreatePrefix + "other:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)} {
		copy := *op
		copy.Resource = resource
		if err := featureCreateCapacity(&cvk.NetworkControllerConfigurationStatus{}, &copy); err == nil {
			t.Fatal("malformed marker admitted", resource)
		}
	}
	state.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: "feature_templates", Resource: featureCreatedPrefix + "invalid", Operation: *op}}
	if _, err := featureCreatedRecords(state); err == nil {
		t.Fatal("malformed result became ownership")
	}
}

func TestFeatureCreatorCapacityImportsAndIdentity(t *testing.T) {
	for _, mode := range []string{"projected-capacity", "import-noop", "import-update", "owned-missing", "owned-replaced", "foreign-owned"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"feature_templates"}
			config.Spec.Source.Inline.Raw = []byte(wirelessFeatureGroupModel)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{}
			fra := wirelessFRA{ID: featureTestFRAUUID, Name: "FRA", Attributes: wirelessFRAAttributes{Band: "2_4GHZ_5GHZ", Interval: 3, Sensitivity: "Medium"}}
			ca := wirelessCleanAir{ID: featureTestCAUUID, Name: "CA", Attributes: wirelessCleanAirAttributes{Band: "2_4GHZ"}}
			missing := mode == "projected-capacity" || mode == "owned-missing"
			if mode == "projected-capacity" {
				for index := range 255 {
					nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: fmt.Sprint("other-", index), Operation: cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", index+1), ConfigUID: "other", ConfigName: "other", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "other", Action: "Create", Phase: "Verified"}})
				}
			}
			if strings.HasPrefix(mode, "owned-") || mode == "foreign-owned" {
				at := metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-time.Second))
				verified := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
				op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: string(config.UID), ConfigName: config.Name, ConfigGeneration: 1, ControllerGeneration: 1, Section: "feature_templates", Resource: featureOwnedTestResource(), Action: "Create", Phase: "Verified", TaskID: "creator-task", SubmittedAt: &at}
				if mode == "foreign-owned" {
					op.ConfigUID = "foreign"
				}
				key, _ := featureCreateKey(op.Resource)
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: op.Section, Resource: featureCreatedPrefix + key + ":" + featureTestFRAUUID, Operation: op, VerifiedAt: &verified}}
			}
			if mode == "owned-replaced" {
				fra.ID = "11111111-1111-4111-8111-111111111111"
			}
			if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if mode == "import-update" {
				fra.Attributes.Interval = 6
			}
			writes := 0
			stamp := int64(0)
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == wirelessFeatureSummaryPath:
					kind := r.URL.Query().Get("type")
					rows := []wirelessFeatureInstance{}
					flag := false
					if !missing {
						if kind == wirelessFRAKind {
							rows = append(rows, wirelessFeatureInstance{ID: fra.ID, Name: fra.Name, System: &flag})
						} else {
							rows = append(rows, wirelessFeatureInstance{ID: ca.ID, Name: ca.Name, System: &flag})
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": kind, "count": len(rows), "instances": rows}}})
				case r.Method == http.MethodGet && r.URL.Path == wirelessFRAPath+"/"+fra.ID:
					json.NewEncoder(w).Encode(map[string]any{"response": fra})
				case r.Method == http.MethodGet && r.URL.Path == wirelessCleanAirPath+"/"+ca.ID:
					json.NewEncoder(w).Encode(map[string]any{"response": ca})
				case mode == "import-update" && r.Method == http.MethodPut && r.URL.Path == wirelessFRAPath+"/"+fra.ID:
					var body wirelessFRA
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("body")
					}
					body.ID = fra.ID
					fra = body
					writes++
					stamp = time.Now().UnixMilli()
					fmt.Fprint(w, `{"response":{"taskId":"update-task"}}`)
				case r.URL.Path == taskPath+"update-task":
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "update-task", "startTime": stamp, "endTime": stamp, "isError": false}})
				default:
					if r.Method != http.MethodGet {
						writes++
					}
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			})
			for range 16 {
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
			}
			state := journalState(t, a)
			if mode == "import-noop" || mode == "import-update" {
				expected := 0
				if mode == "import-update" {
					expected = 1
				}
				if writes != expected || len(state.ActionApplications) != 0 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("import acquired ownership or failed", mode, writes, state, config.Status)
				}
			} else if writes != 0 || state.Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("capacity or provenance failed before dispatch", mode, writes, state, config.Status)
			}
		})
	}
}

func TestFeatureOwnedExecutorVerifiesItsVersionedResource(t *testing.T) {
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-time.Second))
	original := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: "owner", ConfigName: "config", ConfigGeneration: 1, ControllerGeneration: 1, Section: "feature_templates", Resource: featureOwnedTestResource(), Action: "Create", Phase: "Verifying", TaskID: "create-task", SubmittedAt: &at}
	calls := 0
	e := &featureOwnedCreateExecutor{owner: "owner", ownerName: "config", bindings: map[string]featureCreateBinding{"rrm-fra:FRA": {name: "FRA", resource: original.Resource, verify: func(_ context.Context, ref wirelessFeatureTaskReference) (string, bool, error) {
		calls++
		if ref.ID != original.TaskID || ref.Name != "FRA" || ref.Action != "Create" || !ref.Submitted.Equal(at.Time) {
			t.Error("result reference changed")
		}
		return featureTestFRAUUID, true, nil
	}}}}
	before := original.DeepCopy()
	if ok, err := e.verify(context.Background(), original); err != nil || !ok || calls != 1 || !reflect.DeepEqual(original, before) || len(e.receipts) != 0 {
		t.Fatal("owned readback failed or published authority", ok, err, calls)
	}
	foreign := original.DeepCopy()
	foreign.ConfigUID = "foreign"
	if ok, err := e.verify(context.Background(), foreign); err == nil || ok || calls != 1 {
		t.Fatal("foreign verification allowed", ok, err, calls)
	}
}
