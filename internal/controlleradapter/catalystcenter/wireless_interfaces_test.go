// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"
)

const wirelessInterfaceModel = `{"catalyst_center":{"wireless":{"interfaces":[{"name":"CVK_LAB","vlan_id":3998}]}}}`

func TestWirelessInterfaceValidation(t *testing.T) {
	for _, v := range []string{"0", "4095", "null", "true", "1.5", `"10"`} {
		if _, err := parseWirelessInterfaces([]byte(strings.Replace(wirelessInterfaceModel, "3998", v, 1))); err == nil {
			t.Fatal("invalid vlan accepted", v)
		}
	}
	for _, v := range []string{`"name":"CVK_LAB","extra":true`, `"name":""`, `"name":"Management"`} {
		if _, err := parseWirelessInterfaces([]byte(strings.Replace(wirelessInterfaceModel, `"name":"CVK_LAB"`, v, 1))); err == nil {
			t.Fatal("invalid model accepted")
		}
	}
}
func TestWirelessInterfaceManifestRestartUpdate(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(wirelessInterfaceModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	rows := []wirelessInterface{}
	writes := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessInterfacesPath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case (r.URL.Path == wirelessInterfacesPath && r.Method == "POST") || (r.URL.Path == wirelessInterfacesPath+"/interface-id" && r.Method == "PUT"):
			var body wirelessInterface
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "" || body.Name != "CVK_LAB" {
				t.Error("invalid payload")
				w.WriteHeader(400)
				return
			}
			writes++
			stamp = time.Now().UnixMilli()
			body.ID = "interface-id"
			rows = []wirelessInterface{body}
			fmt.Fprint(w, `{"response":{"taskId":"interface-task"}}`)
		case r.URL.Path == taskPath+"interface-task" || r.URL.Path == taskPath+"interface-task/tree":
			writeDefinitionTaskFixture(w, r, map[string]any{"id": "interface-task", "isError": false, "startTime": stamp, "endTime": stamp})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	tick := func() {
		t.Helper()
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
	}
	for _, vlan := range []string{"3998", "3999"} {
		config.Generation++
		config.Spec.Source.Inline.Raw = []byte(strings.Replace(wirelessInterfaceModel, "3998", vlan, 1))
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		for range 20 {
			tick()
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				break
			}
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("not converged", config.Status)
		}
		tick()
	}
	if writes != 2 || len(rows) != 1 || rows[0].VLAN != 3999 {
		t.Fatal("incorrect create/update/no-op", writes, rows)
	}
	desired, _ := parseWirelessInterfaces(config.Spec.Source.Inline.Raw)
	e := &wirelessInterfaceExecutor{client: a.client, desired: desired}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: "interface:CVK_LAB", TargetID: "replacement-id"}
	if ok, err := e.verify(ctx, op); err != nil || ok {
		t.Fatal("UUID replacement accepted", err)
	}
	rows[0].VLAN = 3998
	plans, err := a.client.compileConfigReports(config.Spec.Source.Inline.Raw, []string{"wireless"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Change != configapi.NetworkControllerDriftChangeUpdate {
		t.Fatal("missing drift", err)
	}
}

func TestWirelessInterfaceBuiltinManagement(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{`{"id":"builtin","interfaceName":"management","vlanId":0}`, true},
		{`{"id":"builtin","interfaceName":"management"}`, false},
		{`{"id":"other","interfaceName":"custom","vlanId":0}`, false},
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			fmt.Fprint(w, `{"response":[`+tc.body+`]}`)
		})
		_, err := c.wirelessInterfaces(context.Background())
		if (err == nil) != tc.valid {
			t.Fatal("management inventory validation", tc.body, err)
		}
	}
}
