// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
)

func TestWirelessProfileZoneCombinations(t *testing.T) {
	for _, mode := range []string{"duplicate", "different-rf", "different-ssid", "retained-duplicate"} {
		t.Run(mode, func(t *testing.T) {
			want, refs, actual := wirelessProfilePayloadFixture(t)
			want.zones = append(want.zones, wirelessProfileZone{name: "Second", fields: map[string]any{"apZoneName": "Second", "rfProfileName": "RF"}, ssids: []string{"SSID"}})
			switch mode {
			case "different-rf":
				want.zones[1].fields["rfProfileName"] = "RF2"
				refs.rf["RF2"] = "rf2-id"
			case "different-ssid":
				want.zones[1].ssids = []string{"SSID2"}
				want.ssids = append(want.ssids, wirelessProfileSSID{name: "SSID2", fields: map[string]any{"ssidName": "SSID2"}})
				refs.ssids["SSID2"] = wirelessSSIDMetadata{id: "ssid2", name: "SSID2"}
			case "retained-duplicate":
				actual.fields["apZones"] = []map[string]any{{"apZoneName": "Z", "rfProfileName": "RF", "ssids": []string{"SSID"}}, {"apZoneName": "Second", "rfProfileName": "RF", "ssids": []string{"SSID"}}}
				want.zones = nil
			}
			before, _ := json.Marshal(actual.fields)
			_, err := want.payload(refs, actual)
			bad := mode == "duplicate" || mode == "retained-duplicate"
			if bad && !errors.Is(err, errWirelessAPZoneCombination) || !bad && err != nil {
				t.Fatal("wrong combination result", err)
			}
			after, _ := json.Marshal(actual.fields)
			if string(before) != string(after) {
				t.Fatal("observation mutated")
			}
		})
	}
}

func TestWirelessProfileDuplicateZoneManifestBeforeWrites(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"network_profiles"}
	config.Spec.Source.Inline.Raw = wirelessProfileTestModel(map[string]any{"name": "P", "ssid_details": []any{map[string]any{"name": "S"}}, "ap_zones": []any{map[string]any{"name": "Z1", "rf_profile_name": "RF", "ssids": []string{"S"}}, map[string]any{"name": "Z2", "rf_profile_name": "RF", "ssids": []string{"S"}}}})
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
		case r.URL.Path == wirelessRFPath:
			fmt.Fprint(w, `{"response":[{"id":"rf","rfProfileName":"RF","defaultRfProfile":false,"enableRadioTypeA":true,"enableRadioTypeB":false,"enableRadioType6GHz":false}]}`)
		case r.URL.Path == wirelessInterfacesPath:
			fmt.Fprint(w, `{"response":[{"id":"management","interfaceName":"management","vlanId":0}]}`)
		default:
			t.Error("unexpected read", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	for range 2 {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, condition := range config.Status.Conditions {
		if condition.Reason == "DuplicateAPZoneCombination" && strings.Contains(condition.Message, "unique") {
			found = true
		}
	}
	if !found || config.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || writes != 0 || journalState(t, a).Operation != nil {
		t.Fatal("invalid zones did not stop before journaling or writes", config.Status)
	}
}
