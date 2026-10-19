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
	"sync"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWirelessTagBindingReadbackReplacement(t *testing.T) {
	scenarios := []string{"rf", "ssid", "unselected-rf", "unselected-ssid", "missing-rf", "missing-ssid", "order", "prune-rf", "prune-ssid", "prune-unselected-rf", "prune-unselected-ssid", "prune-order", "delete-rf", "delete-ssid", "delete-unselected-rf", "delete-unselected-ssid", "delete-order"}
	for _, scenario := range append([]string{}, scenarios...) {
		scenarios = append(scenarios, "lineage-"+scenario)
	}
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			mode, lineaged := strings.CutPrefix(scenario, "lineage-")
			stage := "write"
			if strings.HasPrefix(mode, "prune-") {
				stage, mode = "prune", strings.TrimPrefix(mode, "prune-")
			}
			if strings.HasPrefix(mode, "delete-") {
				stage, mode = "delete", strings.TrimPrefix(mode, "delete-")
			}
			ctx := context.Background()
			var mu sync.Mutex
			changed, absent := false, false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != "GET" && r.URL.Path != authPath {
					t.Error("readback mutated native state")
					w.WriteHeader(500)
					return
				}
				var rows []any
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				case wirelessProfilesPath:
					zones := []any{map[string]any{"apZoneName": "Z1", "rfProfileName": "RF1", "ssids": []string{"S"}}, map[string]any{"apZoneName": "Z2", "rfProfileName": "RF2", "ssids": []string{"T"}}}
					if changed && mode == "order" {
						zones[0], zones[1] = zones[1], zones[0]
					}
					rows = []any{map[string]any{"id": "profile", "wirelessProfileName": "P", "apZones": zones, "ssidDetails": []any{map[string]any{"ssidName": "S"}, map[string]any{"ssidName": "T"}}}}
				case sitesPath:
					_ = json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}, {ID: "site", Name: "Lab", NameHierarchy: "Global/Lab", ParentID: "global", Type: "area"}}})
					return
				case profileSiteAssignmentsPath("profile"):
					rows = []any{map[string]any{"id": "site"}}
				case wirelessProfileTagPath("profile", true):
					rows = []any{map[string]any{"policyTagId": "tag", "policyTagName": "PT", "siteIds": []string{"site"}, "apZones": []string{"Z1"}}}
					if absent {
						rows = []any{}
					}
				case pnpDevicesPath:
					fmt.Fprint(w, `[]`)
					return
				case assignedDevicesPath:
					rows = []any{}
				case wirelessRFPath:
					for _, name := range []string{"RF1", "RF2"} {
						if changed && mode == "missing-rf" && name == "RF1" {
							continue
						}
						id := name + "-original"
						if changed && (mode == "rf" && name == "RF1" || mode == "unselected-rf" && name == "RF2") {
							id = name + "-replacement"
						}
						rows = append(rows, map[string]any{"id": id, "rfProfileName": name, "defaultRfProfile": false, "enableRadioTypeA": true, "enableRadioTypeB": false, "enableRadioType6GHz": false})
					}
				case wirelessSSIDPath("global"):
					for _, name := range []string{"S", "T"} {
						if changed && mode == "missing-ssid" && name == "S" {
							continue
						}
						id := name + "-original"
						if changed && (mode == "ssid" && name == "S" || mode == "unselected-ssid" && name == "T") {
							id = name + "-replacement"
						}
						rows = append(rows, map[string]any{"id": id, "ssid": name, "authType": "WPA2_PERSONAL", "wlanType": "Enterprise", "passphrase": "secret-sentinel"})
					}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			raw := wirelessProfileTestModel(map[string]any{"name": "P", "sites": []string{"Global/Lab"}, "ssid_details": []any{map[string]any{"name": "S"}, map[string]any{"name": "T"}}, "ap_zones": []any{map[string]any{"name": "Z1", "rf_profile_name": "RF1", "ssids": []string{"S"}}, map[string]any{"name": "Z2", "rf_profile_name": "RF2", "ssids": []string{"T"}}}, "policy_tags": []any{map[string]any{"name": "PT", "sites": []string{"Global/Lab"}, "ap_zones": []string{"Z1"}}}})
			e, err := c.compileNetworkProfileConfiguration(raw)
			if err != nil {
				t.Fatal(err)
			}
			e.comparison = &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("c", 64)}
			var rows []wirelessProfileTagObservation
			for _, tags := range e.tagExecutors {
				tags.comparison = e.comparison
				if !tags.policy {
					continue
				}
				rows, err = tags.observe(ctx)
				if err != nil {
					t.Fatal(err)
				}
			}
			bound, err := bindWirelessTagRows(ctx, c, rows, true)
			if err != nil || len(bound) != 1 {
				t.Fatal("initial bindings", err)
			}
			key, _ := wirelessTagLogical(bound[0].resource)
			op := boundTagEnvelopeFixture("write", "Update")
			op.Resource = "wireless-policy-tags/" + wirelessTagBoundPrefix + "write:" + bound[0].resource + ":" + wirelessTagGroupKey([]string{key + ":"}) + ":" + strings.Repeat("c", 64) + ":" + strings.Repeat("d", 64)
			if lineaged {
				op.Resource, err = wirelessTagLineageResource(op.Resource, "write", strings.Repeat("e", 64))
				if err != nil {
					t.Fatal(err)
				}
			}
			for _, tags := range e.tagExecutors {
				if !tags.policy {
					continue
				}
				local := op.DeepCopy()
				local.Resource = strings.TrimPrefix(local.Resource, "wireless-policy-tags/")
				part := &privateWirelessTagExecutor{base: tags, namespace: "wireless-policy-tags/"}
				if done, err := part.verify(ctx, local); err != nil || !done {
					t.Fatal("modular private executor readback", err)
				}
			}
			before, done, err := e.privateTagMembers(ctx, op)
			if err != nil || !done {
				t.Fatal("initial v4 readback", err)
			}
			encoded, _ := json.Marshal(before)
			if strings.Contains(string(encoded), "secret-sentinel") {
				t.Fatal("secret in keyed effects")
			}
			if lineaged && stage == "write" && mode == "order" {
				for _, tags := range e.tagExecutors {
					if !tags.policy {
						continue
					}
					base := *tags
					base.desired = append([]wirelessProfileIntent{}, tags.desired...)
					base.desired[0].policyTags = append([]wirelessProfileTag{}, base.desired[0].policyTags...)
					base.desired[0].policyTags[0].zones = []string{"Z2"}
					base.removal = func(ctx context.Context, row wirelessProfileTagObservation) error {
						ids, err := wirelessTagRemovedScopes(row)
						if err != nil {
							return err
						}
						return c.wirelessTagUnusedScopes(ctx, ids)
					}
					deny := false
					wrapper := &privateWirelessTagExecutor{base: &base, namespace: "wireless-policy-tags/", plan: func(context.Context) (string, error) { return strings.Repeat("d", 64), nil }, proof: func(context.Context, string) error {
						if deny {
							return errConfigurationOwned
						}
						return nil
					}, lineage: func([]string) (string, error) { return strings.Repeat("e", 64), nil }}
					write, err := wrapper.next(ctx)
					if err != nil || write == nil {
						t.Fatal("lineage membership write not planned", err)
					}
					resource := "wireless-policy-tags/" + write.resource
					digest, ok := wirelessTagLineageDigest(resource)
					ids, scopeErr := decodeWirelessTagMembershipScope(resource)
					if !ok || digest != strings.Repeat("e", 64) || wirelessTagLineageMode(resource) != "members" || scopeErr != nil || len(ids) != 1 || ids[0] != "site" {
						t.Fatal("planned reduction lost lineage or affected scope", scopeErr)
					}
					deny = true
					if _, err := write.submit(ctx); err == nil || write.noDispatch(err) == nil {
						t.Fatal("lineage write ignored final custody proof or lost no-dispatch capability")
					}
				}
			}
			var prune *wirelessTagPruneExecutor
			var deletion *cvk.NetworkControllerConfigurationOperation
			var prior wirelessTagMemberProof
			var recreation *wirelessTagPruneExecutor
			var recreationCohort wirelessTagRecreationCohort
			if stage != "write" {
				old := op.DeepCopy()
				old.Phase = "Verified"
				at := metav1.NewTime(time.Now().Add(-time.Minute).UTC().Truncate(time.Second))
				mac := strings.Split(before.([]string)[0], ":")[2]
				prior = wirelessTagMemberProof{row: cvk.NetworkControllerConfigurationActionApplication{Operation: *old, VerifiedAt: &at}, id: "tag", mac: mac}
				prune = &wirelessTagPruneExecutor{c: c, comparison: e.comparison, enabled: true, latest: map[string]wirelessTagMemberProof{"wireless-policy-tags/" + key: prior}, wanted: map[string]bool{}, predecessor: strings.Repeat("d", 64), proof: func(context.Context, string) error { return nil }}
				frozen, err := prune.live(ctx, "wireless-policy-tags/"+key, prior)
				if err != nil {
					t.Fatal("initial v4 prune proof", err)
				}
				deletion = boundTagEnvelopeFixture("delete", "Delete")
				deletion.Resource = frozen.Resource
				if stage == "delete" {
					mu.Lock()
					absent = true
					mu.Unlock()
					if done, err := prune.verify(ctx, deletion); err != nil || !done {
						t.Fatal("original absence binding", err)
					}
					settled := *prune
					deleted := deletion.DeepCopy()
					deleted.Phase = "Verified"
					proof := wirelessTagMemberProof{row: cvk.NetworkControllerConfigurationActionApplication{Operation: *deleted, VerifiedAt: &at}, id: "tag", mac: strings.Split(frozen.Effect, ":")[2]}
					settled.latest = map[string]wirelessTagMemberProof{"wireless-policy-tags/" + key: proof}
					recreation = &settled
					recreationCohort = wirelessTagRecreationCohort{Namespace: "wireless-policy-tags/", ParentID: "profile", ComparisonKeyID: e.comparison.keyID, Keys: []string{key}, NativeIDs: map[string]string{key: "tag"}}
					if err := recreation.recreationAbsent(ctx, recreationCohort); err != nil {
						t.Fatal("settled recreation absence", err)
					}
					for _, fault := range []string{"key", "uuid", "journal", "mac"} {
						probe, candidate := *recreation, recreationCohort
						switch fault {
						case "key":
							candidate.ComparisonKeyID = strings.Repeat("f", 64)
						case "uuid":
							candidate.NativeIDs = map[string]string{key: "replacement"}
						case "journal":
							probe.proof = func(context.Context, string) error { return errConfigurationOwned }
						case "mac":
							bad := proof
							bad.mac = strings.Repeat("f", 64)
							probe.latest = map[string]wirelessTagMemberProof{"wireless-policy-tags/" + key: bad}
						}
						if err := probe.recreationAbsent(ctx, candidate); err == nil {
							t.Fatal("recreation absence ignored changed", fault)
						}
					}
				}
			}
			mu.Lock()
			changed = true
			mu.Unlock()
			if recreation != nil {
				err := recreation.recreationAbsent(ctx, recreationCohort)
				if (err == nil) != (mode == "order") {
					t.Fatal("recreation absence lost transitive deletion identity", mode, err)
				}
			}
			if stage != "write" {
				var err error
				done := false
				if stage == "prune" {
					_, err = prune.live(ctx, "wireless-policy-tags/"+key, prior)
					done = err == nil
				} else {
					done, err = prune.verify(ctx, deletion)
				}
				if mode == "order" {
					if err != nil || !done {
						t.Fatal("ordering changed deletion binding", err)
					}
				} else if err == nil || done {
					t.Fatal("dependency replacement passed prune/absence proof", scenario)
				}
				return
			}
			after, done, err := e.privateTagMembers(ctx, op)
			if mode == "order" {
				if err != nil || !done || !reflect.DeepEqual(before, after) {
					t.Fatal("ordering changed versioned proof", err)
				}
			} else if err == nil || done {
				t.Fatal("dependency replacement/removal passed original selector", mode)
			}
		})
	}
}
