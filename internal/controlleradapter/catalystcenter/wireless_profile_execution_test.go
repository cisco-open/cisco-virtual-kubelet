// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestWirelessProfileNativeIdentityCycle(t *testing.T) {
	qualifyWirelessProfileIdentityCycle(t, false)
}
func TestWirelessProfileNamedVLANCycle(t *testing.T) { qualifyWirelessProfileIdentityCycle(t, true) }
func qualifyWirelessProfileIdentityCycle(t *testing.T, namedVLAN bool) {
	ctx := context.Background()
	ssid := map[string]any{"name": "S"}
	vlanName := "VG1"
	if namedVLAN {
		ssid["vlan_group_name"] = vlanName
	}
	desired, err := parseWirelessProfiles(wirelessProfileTestModel(map[string]any{"name": "P", "ssid_details": []any{ssid}}))
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}

	ssidID := "ssid-id"
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
		case r.URL.Path == wirelessSSIDPath("global"):
			json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": ssidID, "ssid": "S", "authType": "OPEN", "wlanType": "Enterprise"}}})
		case r.URL.Path == wirelessInterfacesPath:
			fmt.Fprint(w, `{"response":[{"id":"interface-id","interfaceName":"management","vlanId":0}]}`)
		case r.Method == "GET" && r.URL.Path == wirelessProfilesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.Method == "POST" && r.URL.Path == wirelessProfilesPath || r.Method == "PUT" && r.URL.Path == wirelessProfilesPath+"/profile-id":
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil {
				t.Error("invalid payload")
			}
			entry := body["ssidDetails"].([]any)[0].(map[string]any)
			if namedVLAN && (entry["vlanGroupName"] != vlanName || entry["interfaceName"] != nil) {
				t.Error("named selector lost or an interface was invented")
			}

			if writes > 0 && entry["policyProfileName"] != "native-policy" {
				t.Error("native policy association lost during full-object update")
			}
			// The actual API supplies this public attribute after creation.
			entry["policyProfileName"] = "native-policy"
			body["id"] = "profile-id"
			rows = []map[string]any{body}
			writes++
			fmt.Fprint(w, `{"response":{"taskId":"task-id"}}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	e := &wirelessProfileExecutor{client: c, desired: desired}
	run := func(action string) *ciskov1.NetworkControllerConfigurationOperation {
		t.Helper()
		write, err := e.next(ctx)
		if err != nil || write == nil || write.action != action {
			t.Fatal("plan", err)
		}
		if _, err = write.submit(ctx); err != nil {
			t.Fatal(err)
		}
		op := &ciskov1.NetworkControllerConfigurationOperation{Action: action, Resource: write.resource, ParentID: write.identity.parent, TargetID: write.identity.target}
		restarted := *e
		if verified, err := restarted.verify(ctx, op); err != nil || !verified {
			t.Fatal("verification", err)
		}
		if write, err = e.next(ctx); err != nil || write != nil {
			t.Fatal("no-op failed", err)
		}
		return op
	}
	created := run("Create")
	if namedVLAN {
		vlanName = "VG2"
		e.desired[0].ssids[0].fields["vlanGroupName"] = vlanName
	}
	e.desired[0].ssids[0].fields["wlanProfileName"] = "new-wlan"
	op := run("Update")
	if namedVLAN && created.Resource == op.Resource {
		t.Fatal("changed selector did not change dependency binding")
	}

	if writes != 2 {
		t.Fatal("unexpected replay")
	}
	ssidID = "replacement-ssid"
	if ok, err := e.verify(ctx, op); err == nil || ok {
		t.Fatal("replaced dependency verified")
	}
	ssidID = "ssid-id"
	e.desired[0].ssids[0].fields["wlanProfileName"] = "later-wlan"
	write, err := e.next(ctx)
	if err != nil || write == nil {
		t.Fatal(err)
	}
	if namedVLAN {
		e.desired[0].ssids[0].fields["vlanGroupName"] = "changed-after-plan"
		if _, err = write.submit(ctx); err == nil || writes != 2 {
			t.Fatal("changed selector submitted")
		}
		e.desired[0].ssids[0].fields["vlanGroupName"] = vlanName
	}
	ssidID = "replacement-ssid"
	if _, err = write.submit(ctx); err == nil || writes != 2 {
		t.Fatal("stale dependency submitted")
	}
	ssidID = "ssid-id"
	rows[0]["id"] = "replacement-profile"
	if ok, err := e.verify(ctx, op); err == nil || ok {
		t.Fatal("replaced profile verified")
	}
}
func TestWirelessProfileBindingIgnoresUnrelatedInventory(t *testing.T) {
	want, refs, actual := wirelessProfilePayloadFixture(t)
	body, err := want.payload(refs, actual)
	if err != nil {
		t.Fatal(err)
	}
	first, err := wirelessProfileBinding(body, refs)
	if err != nil {
		t.Fatal(err)
	}
	refs.interfaces["unrelated"] = "new-id"
	next, err := wirelessProfileBinding(body, refs)
	if err != nil || next != first {
		t.Fatal("unrelated reference changed binding")
	}
	refs.interfaces["management"] = "replaced-id"
	next, err = wirelessProfileBinding(body, refs)
	if err != nil || next == first {
		t.Fatal("used reference replacement not bound")
	}
}
