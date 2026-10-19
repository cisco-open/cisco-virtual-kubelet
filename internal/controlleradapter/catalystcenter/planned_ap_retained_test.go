// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestPlannedAPRetainedPrivateBoundary(t *testing.T) {
	for _, mode := range []string{"removed", "declared", "missing-floor", "replacement-floor", "renamed-floor", "missing-AP", "replacement-AP", "renamed-AP", "opaque-drift", "coordinate-drift", "owner", "legacy-anchor", "latest", "latest-drift"} {
		t.Run(mode, func(t *testing.T) {
			e, o, anchor := plannedAPKeyedFixture(t)
			floor := plannedAPTestFloor(o.floorID, o.want.floor)
			sites := []Site{floor}
			e.desired = nil // The default exercises retained state absent from source.
			if mode == "declared" {
				e.desired = []plannedAPIntent{o.want}
			}
			if strings.HasPrefix(mode, "latest") {
				body := map[string]any{"position": map[string]any{"x": 7.0, "y": *o.actual.Position.Y, "z": *o.actual.Position.Z}}
				resource, err := e.privateUpdateResource(o, body)
				if err != nil {
					t.Fatal(err)
				}
				op := anchor.Operation.DeepCopy()
				op.Resource, op.Action, op.TargetID = resource, "Update", o.actual.ID
				e.applied[plannedAPResource(o.want)] = cvk.NetworkControllerConfigurationActionApplication{Resource: plannedAPAppliedResult(op, e.identities[plannedAPResource(o.want)])}
				o.native["position"] = body["position"]
			}
			switch mode {
			case "missing-floor":
				sites = nil
			case "replacement-floor":
				sites[0].ID = plannedAPIdentityTestUUID
			case "renamed-floor":
				sites[0].NameHierarchy += "Renamed"
			case "replacement-AP":
				o.native["id"] = "33333333-3333-4333-8333-333333333333"
			case "renamed-AP":
				o.native["name"] = "Renamed"
			case "opaque-drift", "latest-drift":
				o.native["opaque"].(map[string]any)["counter"] = json.Number("9007199254740993")
			case "coordinate-drift":
				o.native["position"].(map[string]any)["x"] = 9.0
			case "owner":
				e.identityOwner = "other"
			case "legacy-anchor":
				r := e.identities[plannedAPResource(o.want)]
				r.keyID = ""
				e.identities[plannedAPResource(o.want)] = r
			}
			queries := 0
			e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case plannedAPPositionsPath(o.floorID):
					queries++
					rows := []any{o.native}
					if mode == "missing-AP" {
						rows = nil
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				default:
					t.Error("unexpected request", r.Method, r.URL.Path)
					w.WriteHeader(400)
				}
			})
			view, err := e.retainedPrivateView(context.Background(), sites)
			ok := mode == "removed" || mode == "declared" || mode == "latest"
			if (err == nil) != ok || ok && len(view) == 0 {
				t.Fatal("retained boundary result", err)
			}
			if (mode == "missing-floor" || mode == "replacement-floor" || mode == "owner" || mode == "legacy-anchor") && queries != 0 {
				t.Fatal("AP collection queried without floor/owner authority")
			}
		})
	}
}

func TestPlannedAPRetainedDispatchBoundary(t *testing.T) {
	for _, mode := range []string{"same", "native-before", "native-during-CAS", "journal", "synchronous", "native-response-error"} {
		t.Run(mode, func(t *testing.T) {
			e, o, _ := plannedAPKeyedFixture(t)
			e.desired = nil
			floor := plannedAPTestFloor(o.floorID, o.want.floor)
			floor.ParentID = "building"
			// ListSites validates the entire hierarchy, including parent identities.
			sites := append(sampleSites(), floor)
			e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case plannedAPPositionsPath(o.floorID):
					json.NewEncoder(w).Encode(map[string]any{"response": []any{o.native}})
				default:
					t.Error("unexpected request", r.URL.Path)
					w.WriteHeader(400)
				}
			})
			ctx := context.Background()
			before, err := e.retainedPrivateView(ctx, sites)
			if err != nil {
				t.Fatal(err)
			}
			writes, guards := 0, 0
			x := &sitePlannedAPExecutor{sites: &siteConfigurationExecutor{client: e.client}, placements: e,
				retainedGuard: func(context.Context, string, string, configurationResourceIdentity) error {
					guards++
					if mode == "journal" {
						return errConfigurationStale
					}
					if mode == "native-during-CAS" {
						o.native["opaque"].(map[string]any)["counter"] = json.Number("9007199254740993")
					}
					return nil
				}}
			write := &configurationWrite{resource: "test", action: "Update", submit: func(context.Context) (Task, error) {
				writes++
				if mode == "native-response-error" {
					return Task{}, errInvalidResponse
				}
				return Task{}, nil
			}}
			if mode == "synchronous" {
				write.submit = nil
				write.submitCompleted = func(context.Context) error { writes++; return nil }
			}
			write, err = x.guardRetainedPrivate(ctx, before, write)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "native-before" {
				o.native["opaque"].(map[string]any)["counter"] = json.Number("9007199254740993")
			}
			call := func() error {
				if write.submitCompleted != nil {
					return write.submitCompleted(ctx)
				}
				_, err := write.submit(ctx)
				return err
			}
			err = call()
			refused := mode == "native-before" || mode == "native-during-CAS" || mode == "journal"
			proof := write.noDispatch(err)
			if refused {
				if err == nil || writes != 0 || proof == nil || write.noDispatch(err) != nil {
					t.Fatal("pre-submit refusal lost its one-use proof", err)
				}
			} else if writes != 1 || guards != 1 || proof != nil || (err != nil) != (mode == "native-response-error") {
				t.Fatal("native sender or uncertain outcome mishandled", err)
			}
			if call() == nil || writes > 1 {
				t.Fatal("retained guard allowed replay")
			}
		})
	}
}

func TestPlannedAPRetainedCollectionBatch(t *testing.T) {
	e, o, anchor := plannedAPKeyedFixture(t)
	e.desired = nil
	raw, _ := json.Marshal(o.native)
	value, err := decodeExactNativeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	second := o
	second.native = value.(map[string]any)
	second.native["id"] = "33333333-3333-4333-8333-333333333333"
	second.native["name"] = "AP2"
	raw, _ = json.Marshal(second.native)
	if json.Unmarshal(raw, &second.actual) != nil || validatePlannedAPObservation(&second.actual) != nil {
		t.Fatal("invalid second AP")
	}
	second.want = plannedAPIntent{floor: o.want.floor, payload: map[string]any{"name": second.actual.Name}}
	r := anchor
	r.Operation = *anchor.Operation.DeepCopy()
	r.Operation.Resource = plannedAPPrivateObservePrefix + strings.TrimPrefix(plannedAPResource(second.want), "planned-ap:") + ":i:" + e.comparison.keyID
	view, err := plannedAPBaselineView(second)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := plannedAPNativeMAC(e.comparison, r.Operation.Resource, view)
	if err != nil {
		t.Fatal(err)
	}
	e.identities[plannedAPResource(second.want)] = plannedAPIdentityRecord{id: second.actual.ID, keyID: e.comparison.keyID, mac: mac, receipt: r}
	queries := 0
	e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case plannedAPPositionsPath(o.floorID):
			queries++
			json.NewEncoder(w).Encode(map[string]any{"response": []any{o.native, second.native}})
		default:
			t.Error("unexpected request", r.URL.Path)
			w.WriteHeader(400)
		}
	})
	sites := []Site{plannedAPTestFloor(o.floorID, o.want.floor)}
	first, err := e.retainedPrivateView(context.Background(), sites)
	if err != nil || queries != 1 {
		t.Fatal("retained APs did not share a complete floor read", err)
	}
	again, err := e.retainedPrivateView(context.Background(), sites)
	if err != nil || string(first) != string(again) || queries != 2 {
		t.Fatal("retained cohort ordering is unstable", err)
	}
	second.native["opaque"].(map[string]any)["nullable"] = "changed"
	if _, err := e.retainedPrivateView(context.Background(), sites); err == nil {
		t.Fatal("second retained member drift was ignored")
	}
}
