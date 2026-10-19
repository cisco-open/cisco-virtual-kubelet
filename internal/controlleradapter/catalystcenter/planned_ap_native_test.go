// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func plannedAPNativeTestObject(t *testing.T) map[string]any {
	t.Helper()
	value, err := decodeExactNativeJSON([]byte(plannedAPObservedFixture))
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)
}
func TestPlannedAPCompleteNativeDispatchView(t *testing.T) {
	for _, mode := range []string{"stable", "unknown-added", "unknown-number", "unknown-removed", "null-to-absent", "array-order", "sensor-flag", "nested-radio", "nested-position", "nested-antenna", "equivalent-key-order"} {
		t.Run(mode, func(t *testing.T) {
			_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			native := plannedAPNativeTestObject(t)
			native["vendorState"] = map[string]any{"counter": json.Number("9007199254740992"), "items": []any{"a", "b"}, "nullable": nil}
			// The selected write creates AP2; AP1 is an unchanged sibling with
			// unknown private state. Freeze both, not just the mutation target.
			_, creates, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
			if err != nil {
				t.Fatal(err)
			}
			creates[0].payload["name"] = "AP2"
			creates[0].payload["macAddress"] = "02:00:00:00:00:02"
			desired = append(creates, desired...)
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case plannedAPCatalogPath:
					fmt.Fprint(w, plannedAPCatalogFixture)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": append(sampleSites(), plannedAPTestFloor("floor", desired[0].floor))})
				case plannedAPPositionsPath("floor"):
					json.NewEncoder(w).Encode(map[string]any{"response": []any{native}})
				case plannedAPPositionsPath("floor") + "/bulk":
					writes++
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				default:
					t.Error("unexpected API")
					w.WriteHeader(400)
				}
			})
			write, err := (&plannedAPExecutor{client: c, desired: desired}).next(context.Background())
			if err != nil || write == nil {
				t.Fatal(err)
			}
			antenna := native["radios"].([]any)[0].(map[string]any)["antenna"].(map[string]any)
			switch mode {
			case "unknown-added":
				native["newPrivateField"] = "not-for-status"
			case "unknown-number":
				native["vendorState"].(map[string]any)["counter"] = json.Number("9007199254740993")
			case "unknown-removed":
				delete(native, "vendorState")
			case "null-to-absent":
				delete(native["vendorState"].(map[string]any), "nullable")
			case "array-order":
				native["vendorState"].(map[string]any)["items"] = []any{"b", "a"}
			case "sensor-flag":
				antenna["elevationAccelerometer"] = true
			case "nested-radio":
				native["radios"].([]any)[0].(map[string]any)["vendorField"] = true
			case "nested-position":
				native["position"].(map[string]any)["vendorField"] = true
			case "nested-antenna":
				antenna["vendorField"] = true
			case "equivalent-key-order":
				raw, _ := json.Marshal(native)
				value, err := decodeExactNativeJSON(raw)
				if err != nil {
					t.Fatal(err)
				}
				native = value.(map[string]any)
			}
			_, err = write.submit(context.Background())
			proof := write.noDispatch(err)
			valid := mode == "stable" || mode == "equivalent-key-order"
			if valid {
				if err != nil || writes != 1 || proof != nil {
					t.Fatal("stable native state refused", err)
				}
			} else if err == nil || writes != 0 || proof == nil {
				t.Fatal("late raw native change escaped preflight")
			}
		})
	}
}
func TestPlannedAPNativeSnapshotPrecision(t *testing.T) {
	values := []string{`{"counter":9007199254740992}`, `{"counter":9007199254740993}`, `{"counter":null}`, `{}`, `{"array":[1,2]}`, `{"array":[2,1]}`}
	seen := map[string]bool{}
	for _, raw := range values {
		value, err := decodeExactNativeJSON([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		view, err := plannedAPDispatchView([]plannedAPState{{native: value.(map[string]any)}})
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(view)] {
			t.Fatal("distinct complete native states collapsed")
		}
		seen[string(view)] = true
	}
	raw := []byte(`{"customOrder":false,"templateParams":[{"parameterName":"z"},{"parameterName":"a"}]}`)
	value, err := decodeExactNativeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(value)
	if bytes.Index(encoded, []byte(`"z"`)) > bytes.Index(encoded, []byte(`"a"`)) {
		t.Fatal("template-only normalization altered opaque AP arrays")
	}
	for _, invalid := range []string{`{} {}`, `{"x":`, ``} {
		if _, err := decodeExactNativeJSON([]byte(invalid)); err == nil {
			t.Fatal("malformed/trailing native JSON accepted")
		}
	}
}
func TestPlannedAPUnmappedUpdateFields(t *testing.T) {
	for _, mode := range []string{"known", "sensor", "bad-sensor", "top", "position", "radio", "antenna"} {
		t.Run(mode, func(t *testing.T) {
			native := plannedAPNativeTestObject(t)
			antenna := native["radios"].([]any)[0].(map[string]any)["antenna"].(map[string]any)
			switch mode {
			case "sensor":
				antenna["elevationAccelerometer"] = false
			case "bad-sensor":
				antenna["elevationAccelerometer"] = "false"
			case "top":
				native["vendorState"] = true
			case "position":
				native["position"].(map[string]any)["vendorState"] = true
			case "radio":
				native["radios"].([]any)[0].(map[string]any)["vendorState"] = true
			case "antenna":
				antenna["vendorState"] = true
			}
			err := validatePlannedAPUpdateFootprint(native)
			if (err == nil) != (mode == "known" || mode == "sensor") {
				t.Fatal("unmapped fields were dropped or sensor type ignored", err)
			}
			if mode != "known" && mode != "sensor" && mode != "bad-sensor" && !errors.Is(err, errPlannedAPUnmappedUpdate) {
				t.Fatal(err)
			}
		})
	}
	// An unknown private attribute can be observed, but is never copied into a
	// native request or persisted as an unkeyed hash by these helpers.
	value, err := decodeExactNativeJSON([]byte(strings.Replace(plannedAPObservedFixture, `"name":"AP1"`, `"name":"AP1","privateValue":"keep-private"`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(validatePlannedAPUpdateFootprint(value.(map[string]any)), errPlannedAPUnmappedUpdate) {
		t.Fatal("unknown native field was silently omitted")
	}
}
