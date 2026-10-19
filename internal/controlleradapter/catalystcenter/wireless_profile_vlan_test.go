// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"

	"net/http"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestWirelessVLANNameBinding(t *testing.T) {
	refs := wirelessProfileReferences{global: "global", ssids: map[string]wirelessSSIDMetadata{"S": {id: "ssid", name: "S"}}}
	entry := map[string]any{"ssidName": "S", "vlanGroupName": "VG1"}
	body := map[string]any{"ssidDetails": []map[string]any{entry}}
	first, err := wirelessProfileBinding(body, refs)
	if err != nil {
		t.Fatal(err)
	}
	entry["vlanGroupName"] = "VG2"
	next, err := wirelessProfileBinding(body, refs)
	if err != nil || next == first {
		t.Fatal("selector not bound", err)
	}
	entry["vlanGroupName"] = "bad\nname"
	if _, err = wirelessProfileBinding(body, refs); err == nil {
		t.Fatal("invalid literal selector bound")
	}
}

func TestWirelessMissingVLANSelectorNativeRejectionRetainsJournal(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"network_profiles"}
	config.Spec.Source.Inline.Raw = wirelessProfileTestModel(map[string]any{"name": "P", "ssid_details": []any{map[string]any{"name": "S", "vlan_group_name": "Missing"}}})
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	writes := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessProfilesPath && r.Method == "POST":
			writes++
			w.WriteHeader(http.StatusNotAcceptable)
		case r.URL.Path == wirelessProfilesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case r.URL.Path == sitesPath:
			fmt.Fprint(w, `{"response":[{"id":"global","name":"Global","nameHierarchy":"Global","type":"global"}]}`)
		case r.URL.Path == wirelessSSIDPath("global"):
			fmt.Fprint(w, `{"response":[{"id":"ssid","ssid":"S","authType":"OPEN","wlanType":"Enterprise"}]}`)
		default:
			t.Error("selector invented an inventory lookup", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	for range 20 {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if op := journalState(t, a).Operation; op != nil && op.Phase == "Unknown" {
			break
		}
	}
	op := journalState(t, a).Operation
	if op == nil || op.Phase != "Unknown" || op.TaskID != "" || writes != 1 {
		t.Fatal("native rejection did not fence original operation", op, writes)
	}
	retained := op.DeepCopy()
	for range 3 {
		if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(retained, journalState(t, a).Operation) || writes != 1 {
		t.Fatal("rejected selector request was replayed")
	}
}
