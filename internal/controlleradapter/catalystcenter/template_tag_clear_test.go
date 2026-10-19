// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestTagEmptyRuleComparisonIsSelectionScoped(t *testing.T) {
	rules := []any{map[string]any{"memberType": "networkdevice", "rules": map[string]any{"operation": "EQ", "name": "hostname", "value": "isolated"}}}
	for _, test := range []struct {
		name    string
		want    map[string]any
		got     []any
		matches bool
	}{
		{"clear-native-null", map[string]any{"name": "tag", "dynamicRules": []any{}}, nil, true},
		{"clear-native-empty", map[string]any{"name": "tag", "dynamicRules": []any{}}, []any{}, true},
		{"clear-native-present", map[string]any{"name": "tag", "dynamicRules": []any{}}, rules, false},
		{"omitted-native-present", map[string]any{"name": "tag"}, rules, true},
		{"nonempty-native-null", map[string]any{"name": "tag", "dynamicRules": rules}, nil, false},
		{"nonempty-native-matching", map[string]any{"name": "tag", "dynamicRules": rules}, rules, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			before, _ := json.Marshal(test.want)
			if tagPayloadMatches(test.want, observedTag{Name: "tag", DynamicRules: test.got}) != test.matches {
				t.Fatal("empty comparison widened selected rule ownership")
			}
			after, _ := json.Marshal(test.want)
			if string(before) != string(after) {
				t.Fatal("comparison changed desired payload")
			}
		})
	}
}

func TestTagUnmanagedRulesArePreserved(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing-rules-%t", existing), func(t *testing.T) {
			falseValue, oldDescription := false, "old"
			tag := observedTag{ID: "tag", Name: "tag", Description: &oldDescription, SystemTag: &falseValue}
			if existing {
				tag.DynamicRules = []any{map[string]any{"memberType": "networkdevice", "rules": map[string]any{"operation": "EQ", "name": "hostname", "value": "isolated"}}}
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "GET" {
					json.NewEncoder(w).Encode(map[string]any{"response": []observedTag{tag}})
					return
				}
				var body map[string]any
				if r.Method != "PUT" || json.NewDecoder(r.Body).Decode(&body) != nil {
					t.Error("incorrect update")
				}
				rules, present := body["dynamicRules"]
				if present != existing || existing && !reflect.DeepEqual(rules, tag.DynamicRules) {
					t.Error("unmanaged rules were cleared or a clearing field was invented")
				}
				writes++
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
			})
			model := []byte(`{"catalyst_center":{"templates":{"tags":[{"name":"tag","description":"new"}]}}}`)
			e, err := c.compileConfigurationExecutor(model, "templates")
			if err != nil {
				t.Fatal(err)
			}
			write, err := e.next(context.Background())
			if err != nil || write == nil {
				t.Fatal(err)
			}
			if _, err := write.submit(context.Background()); err != nil || writes != 1 {
				t.Fatal("unmanaged-rule update failed", err)
			}
		})
	}
}

func TestDynamicTagClearRestorePublicRestartCycle(t *testing.T) {
	qualifyDynamicTagClearRestorePublicRestartCycle(t, false)
}

func TestUnaryTagClearRestorePublicRestartCycle(t *testing.T) {
	qualifyDynamicTagClearRestorePublicRestartCycle(t, true)
}

func qualifyDynamicTagClearRestorePublicRestartCycle(t *testing.T, unary bool) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	var actual *observedTag
	writes := 0
	tasks := map[string]int64{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if strings.HasPrefix(r.URL.Path, taskPath) {
			id := strings.TrimPrefix(r.URL.Path, taskPath)
			stamp := tasks[id]
			if stamp == 0 {
				t.Error("foreign task observation")
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false, "serviceType": "Grouping Service", "data": "22f33fa0-e275-41c9-a50d-d7e5135adb8c"}})
			return
		}
		if r.URL.Path != tagsPath {
			t.Error("unexpected endpoint")
		}
		if r.Method == "GET" {
			rows := []observedTag{}
			if actual != nil {
				rows = append(rows, *actual)
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
			return
		}
		var tag observedTag
		if json.NewDecoder(r.Body).Decode(&tag) != nil || writes == 0 && r.Method != "POST" || writes > 0 && (r.Method != "PUT" || tag.ID != "22f33fa0-e275-41c9-a50d-d7e5135adb8c") {
			t.Error("mutation replaced native tag identity")
		}
		if len(tag.DynamicRules) == 0 {
			tag.DynamicRules = nil // Native clear may return null/omit the rule list.
		}
		tag.ID, actual = "22f33fa0-e275-41c9-a50d-d7e5135adb8c", &tag
		writes++
		id := fmt.Sprintf("task-%d", writes)
		tasks[id] = time.Now().UnixMilli()
		json.NewEncoder(w).Encode(map[string]any{"response": map[string]string{"taskId": id}})
	})
	for cycle := 0; cycle < 5; cycle++ {
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		config.Spec.ManagedSections = []string{"inventory"}
		rules := []any{}
		if cycle != 2 {
			value := "first"
			if cycle == 1 {
				value = "second"
			}
			rule := map[string]any{"member_type": "networkdevice", "operation": "AND", "items": []any{map[string]any{"operation": "EQ", "name": "hostname", "value": value}, map[string]any{"operation": "EQ", "name": "family", "value": "Switches and Hubs"}}}
			if unary {
				rule["items"] = rule["items"].([]any)[:1]
				if cycle == 1 || cycle == 4 {
					rule["operation"] = "OR"
				}
			}
			rules = append(rules, rule)
		}
		config.Spec.Source.Inline.Raw, _ = json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"tags": []any{map[string]any{"name": "tag", "system_tag": false, "dynamic_rules": rules}}}}})
		config.Generation = int64(cycle + 1)
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for range 12 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				break
			}
		}
		expected := cycle + 1
		if cycle == 4 {
			expected = 4
		}
		if writes != expected || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || config.Status.ObservedGeneration != config.Generation || journalState(t, a).Operation != nil || (len(actual.DynamicRules) == 0) != (cycle == 2) {
			t.Fatal("clear/restore cycle failed or replayed", cycle, writes, config.Status)
		}
	}
}
