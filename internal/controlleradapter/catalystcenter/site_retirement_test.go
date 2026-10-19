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
)

func TestSiteRetirementDependencies(t *testing.T) {
	modes := []string{"stable", "legacy-wireless-alias", "unrelated-reference", "late-unrelated", "global", "invalid-id", "missing", "replacement", "child", "device", "pnp", "pnp-unbound", "planned-ap", "switching", "wireless", "site-tag", "policy-tag", "tag-unknown", "profile-unknown", "profile-collision", "other-profile", "fabric", "zone", "reservation", "dangling-profile", "dangling-fabric", "dangling-zone", "dangling-reservation", "late-device", "late-profile", "late-native", "late-parent", "late-replacement", "late-local-shape"}
	for _, path := range []string{sitesPath, assignedDevicesPath, pnpDevicesPath, plannedAPPositionsPath(siteOwnedTestUUID), networkProfilesPath, wirelessProfilesPath, profileSiteAssignmentsPath("switch-profile"), wirelessProfileTagPath("wireless-profile", false), fabricSitesPath, fabricZonesPath, sitePoolsPath} {
		modes = append(modes, "error:"+path)
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			target := Site{ID: siteOwnedTestUUID, Name: "Floor", NameHierarchy: "Global/Lab/Building/Floor", ParentID: "building", Type: "floor"}
			base := append(sampleSites(), target)
			reads, writes := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(500)
					return
				}
				if strings.TrimPrefix(mode, "error:") == r.URL.Path && strings.HasPrefix(mode, "error:") {
					w.WriteHeader(503)
					return
				}
				if mode == "late-local-shape" && reads >= 3 && r.URL.Path == sitesPath+"/"+target.ID+"/ntpSettings" {
					fmt.Fprint(w, `{"response":{"ntp":{}}}`)
					return
				}
				if serveEmptySiteRetirementConsumers(t, w, r, target.ID) {
					return
				}
				rows := []any{}
				late := reads >= 3
				targetRef := target.ID
				if mode == "unrelated-reference" || mode == "late-unrelated" || strings.HasPrefix(mode, "dangling-") {
					targetRef = "building"
				}
				if strings.HasPrefix(mode, "dangling-") {
					targetRef = "unknown-site"
				}
				switch r.URL.Path {
				case sitesPath:
					reads++
					for _, s := range base {
						if mode == "missing" && s.ID == target.ID {
							continue
						}
						if (mode == "replacement" || mode == "late-replacement" && reads >= 3) && s.ID == target.ID {
							s.ID = "replacement-floor"
						}
						raw, _ := json.Marshal(s)
						var row map[string]any
						json.Unmarshal(raw, &row)
						if mode == "late-native" && reads >= 4 && s.ID == target.ID {
							row["futureNativeAttribute"] = "private drift"
						}
						if mode == "late-parent" && reads >= 4 && s.ID == "building" {
							row["futureNativeAttribute"] = "private parent drift"
						}
						rows = append(rows, row)
					}
					if mode == "child" {
						rows = append(rows, Site{ID: "child", Name: "Child", NameHierarchy: target.NameHierarchy + "/Child", ParentID: target.ID, Type: "floor"})
					}
				case assignedDevicesPath:
					if mode == "device" || mode == "late-device" && late {
						rows = append(rows, map[string]string{"deviceId": "consumer", "siteId": target.ID})
					}
				case pnpDevicesPath:
					if mode == "pnp" {
						rows = append(rows, map[string]any{"id": "pnp", "deviceInfo": map[string]string{"siteId": target.ID, "pid": "C9300", "serialNumber": "FOC12345678"}})
					}
					if mode == "pnp-unbound" {
						rows = append(rows, map[string]any{"id": "pnp", "deviceInfo": map[string]string{"state": "Unclaimed", "onbState": "Not Contacted", "pid": "C9300", "serialNumber": "FOC12345678"}})
					}
					json.NewEncoder(w).Encode(rows)
					return
				case plannedAPPositionsPath(target.ID):
					if mode == "planned-ap" {
						var ap map[string]any
						json.Unmarshal([]byte(plannedAPObservedFixture), &ap)
						rows = append(rows, ap)
					}
				case networkProfilesPath:
					id := "switch-profile"
					if mode == "profile-collision" || mode == "legacy-wireless-alias" {
						id = "wireless-profile"
					}
					namespace := "switching"
					if mode == "other-profile" {
						namespace = "routing"
					}
					if mode == "legacy-wireless-alias" {
						namespace = "wireless"
					}
					rows = append(rows, networkProfile{ID: id, Name: "switching", Namespace: namespace})
				case wirelessProfilesPath:
					row := map[string]any{"id": "wireless-profile", "wirelessProfileName": "wireless"}
					if mode == "profile-unknown" {
						row["futureReference"] = "unqualified"
					}
					rows = append(rows, row)
				case profileSiteAssignmentsPath("switch-profile"):
					if mode == "switching" || mode == "other-profile" || mode == "dangling-profile" || mode == "late-profile" && late || mode == "unrelated-reference" || mode == "late-unrelated" && late {
						rows = append(rows, map[string]string{"id": targetRef})
					}
				case profileSiteAssignmentsPath("wireless-profile"):
					if mode == "wireless" {
						rows = append(rows, map[string]string{"id": target.ID})
					}
				case wirelessProfileTagPath("wireless-profile", false), wirelessProfileTagPath("wireless-profile", true):
					policy := strings.HasSuffix(r.URL.Path, "policyTags")
					if mode == "site-tag" && !policy || mode == "policy-tag" && policy || mode == "tag-unknown" && !policy {
						idKey, nameKey := "siteTagId", "siteTagName"
						if policy {
							idKey, nameKey = "policyTagId", "policyTagName"
						}
						row := map[string]any{idKey: "tag", nameKey: "tag", "siteIds": []string{target.ID}}
						if policy {
							row["apZones"] = []string{}
						}
						if mode == "tag-unknown" {
							row["siteIds"] = []string{}
							row["futureReference"] = true
						}
						rows = append(rows, row)
					}
				case fabricSitesPath:
					if mode == "fabric" || mode == "dangling-fabric" || mode == "unrelated-reference" {
						rows = append(rows, map[string]any{"id": "fabric", "siteId": targetRef, "authenticationProfileName": "Closed Authentication", "isPubSubEnabled": false})
					}
				case fabricZonesPath:
					if mode == "zone" || mode == "dangling-zone" || mode == "unrelated-reference" {
						rows = append(rows, map[string]any{"id": "zone", "siteId": targetRef, "authenticationProfileName": "Closed Authentication"})
					}
				case sitePoolsPath:
					if mode == "reservation" || mode == "dangling-reservation" || mode == "unrelated-reference" {
						rows = append(rows, map[string]any{"id": "pool", "name": "pool", "poolType": "LAN", "siteId": targetRef, "ipV4AddressSpace": map[string]any{"subnet": "10.0.0.0", "prefixLength": 24, "globalPoolId": "global-pool"}})
					}
				default:
					t.Errorf("unexpected retirement read %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			expected := target
			if mode == "global" {
				expected = base[0]
			}
			if mode == "invalid-id" {
				expected.ID = "not-a-native-uuid"
			}
			err := c.stableUnusedSiteLeaf(context.Background(), expected)
			success := mode == "stable" || mode == "legacy-wireless-alias" || mode == "unrelated-reference" || mode == "late-unrelated" || mode == "pnp-unbound"
			if (err == nil) != success {
				t.Fatalf("retirement observation mismatch: %v", err)
			}
			if writes != 0 {
				t.Fatal("dependency observation mutated native state")
			}
			if success && reads != 4 {
				t.Fatal("did not take two independent complete native observations", reads)
			}
		})
	}
}

func TestSiteRetirementObservationRedactsNative(t *testing.T) {
	observation := &siteRetirementObservation{target: Site{NameHierarchy: "Global/private-location"}, native: []byte("private-native-setting")}
	for _, rendered := range []string{fmt.Sprint(observation), fmt.Sprintf("%+v", observation), fmt.Sprintf("%#v", observation)} {
		if strings.Contains(rendered, "private-") {
			t.Fatal("native retirement observation leaked")
		}
	}
}
