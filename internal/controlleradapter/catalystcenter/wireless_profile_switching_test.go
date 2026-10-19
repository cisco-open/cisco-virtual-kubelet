// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestWirelessProfileSwitchingModesMergedPayload(t *testing.T) {
	for _, mode := range []string{"same-ssid", "different-ssids", "retained-flex", "retained-collection", "explicit-disable", "central-fabric", "central-flex"} {
		t.Run(mode, func(t *testing.T) {
			want, refs, actual := wirelessProfilePayloadFixture(t)
			want.zones, want.features = nil, nil
			want.ssids = []wirelessProfileSSID{{name: "SSID", fields: map[string]any{"ssidName": "SSID", "enableFabric": true}}}
			bad := true
			switch mode {
			case "same-ssid":
				actual = nil
				want.ssids[0].fields["flexConnect"] = map[string]any{"enableFlexConnect": true}
			case "different-ssids":
				actual = nil
				want.ssids = append(want.ssids, wirelessProfileSSID{name: "Second", fields: map[string]any{"ssidName": "Second", "flexConnect": map[string]any{"enableFlexConnect": true}}})
				refs.ssids["Second"] = wirelessSSIDMetadata{id: "second-id", name: "Second"}
			case "retained-flex":
				// The explicit Fabric flag conflicts with a retained Flex flag.
			case "retained-collection":
				want.ssids = nil
				actual.fields["ssidDetails"].([]map[string]any)[0]["enableFabric"] = true
			case "explicit-disable":
				want.ssids[0].fields["flexConnect"] = map[string]any{"enableFlexConnect": false}
				bad = false
			case "central-fabric", "central-flex":
				actual = nil
				bad = false
				if mode == "central-flex" {
					want.ssids[0].fields = map[string]any{"ssidName": "SSID", "flexConnect": map[string]any{"enableFlexConnect": true}}
				}
				want.ssids = append(want.ssids, wirelessProfileSSID{name: "Second", fields: map[string]any{"ssidName": "Second"}})
				refs.ssids["Second"] = wirelessSSIDMetadata{id: "second-id", name: "Second"}
			}
			var before []byte
			if actual != nil {
				before, _ = json.Marshal(actual.fields)
			}
			out, err := want.payload(refs, actual)
			if bad && !errors.Is(err, errWirelessProfileSwitchingModes) || !bad && err != nil {
				t.Fatal("incorrect switching-mode validation", err)
			}
			if actual != nil {
				after, _ := json.Marshal(actual.fields)
				if string(before) != string(after) {
					t.Fatal("native observation mutated")
				}
			}
			if mode == "explicit-disable" {
				flex := out["ssidDetails"].([]map[string]any)[0]["flexConnect"].(map[string]any)
				if _, exists := flex["localToVlan"]; exists {
					t.Fatal("disabled Flex retained its local VLAN")
				}
			}
		})
	}
}

func TestWirelessProfileLateMixedModeManifestBeforeWrites(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"network_profiles"}
	config.Spec.Source.Inline.Raw, _ = json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_profiles": map[string]any{"wireless": []any{
		map[string]any{"name": "A-valid", "ssid_details": []any{map[string]any{"name": "S"}}},
		map[string]any{"name": "Z-invalid", "ssid_details": []any{map[string]any{"name": "S", "enable_fabric": true, "enable_flex_connect": true}}},
	}}}})
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	writes := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.Method != "GET":
			writes++
			w.WriteHeader(500)
		case r.URL.Path == wirelessProfilesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
		case r.URL.Path == wirelessSSIDPath("global"):
			fmt.Fprint(w, `{"response":[{"id":"ssid","ssid":"S","authType":"OPEN","wlanType":"Enterprise"}]}`)
		case r.URL.Path == wirelessInterfacesPath:
			fmt.Fprint(w, `{"response":[{"id":"management","interfaceName":"management","vlanId":0}]}`)
		default:
			t.Error("unexpected read", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	for range 3 {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, condition := range config.Status.Conditions {
		found = found || condition.Reason == "MixedWirelessSwitchingModes"
	}
	if !found || config.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || writes != 0 || journalState(t, a).Operation != nil {
		t.Fatal("later invalid profile did not fence all writes and journal preparation", config.Status, writes)
	}
}
