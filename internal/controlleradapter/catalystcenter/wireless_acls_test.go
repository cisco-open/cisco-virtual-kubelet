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

const wirelessACLModel = `{"catalyst_center":{"wireless":{"security_settings":{"pre_auth_acls":[{"name":"CVK_LAB","rules":[{"source_address":"192.0.2.1","destination_address":"198.51.100.1","protocol":"TCP","destination_ports":"443"}]}]}}}}`

func TestWirelessACLManifestRestartUpdate(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	config.Spec.Source.Inline.Raw = []byte(wirelessACLModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	rows := []wirelessACL{}
	writes := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == wirelessACLsPath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case (r.URL.Path == wirelessACLsPath && r.Method == "POST") || (r.URL.Path == wirelessACLsPath+"/acl-id" && r.Method == "PUT"):
			var body wirelessACL
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "" || body.Name != "CVK_LAB" {
				t.Error("invalid payload")
				w.WriteHeader(400)
				return
			}
			writes++
			stamp = time.Now().UnixMilli()
			body.ID = "acl-id"
			rows = []wirelessACL{body}
			fmt.Fprint(w, `{"response":{"taskId":"acl-task"}}`)
		case r.URL.Path == taskPath+"acl-task" || r.URL.Path == taskPath+"acl-task/tree":
			writeDefinitionTaskFixture(w, r, map[string]any{"id": "acl-task", "isError": false, "startTime": stamp, "endTime": stamp})
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
	for _, vlan := range []string{"443", "8443"} {
		config.Generation++
		config.Spec.Source.Inline.Raw = []byte(strings.Replace(wirelessACLModel, "443", vlan, 1))
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
	if writes != 2 || len(rows) != 1 || rows[0].Rules[0].DestinationPorts != "8443" {
		t.Fatal("incorrect create/update/no-op", writes, rows)
	}
	desired, _ := parseWirelessACLs(config.Spec.Source.Inline.Raw)
	e := &wirelessACLExecutor{client: a.client, desired: desired}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: "pre-auth-acl:CVK_LAB", TargetID: "replacement-id"}
	if ok, err := e.verify(ctx, op); err != nil || ok {
		t.Fatal("UUID replacement accepted", err)
	}
	rows[0].Rules[0].DestinationPorts = "443"
	plans, err := a.client.compileConfigReports(config.Spec.Source.Inline.Raw, []string{"wireless"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Change != configapi.NetworkControllerDriftChangeUpdate {
		t.Fatal("missing drift", err)
	}
}

func TestWirelessACLValidationAndOwnership(t *testing.T) {
	desired, err := parseWirelessACLs([]byte(wirelessACLModel))
	if err != nil || len(desired) != 1 {
		t.Fatal(err)
	}
	a := desired[0]
	if a.ACLName != a.Name || a.IPv6 || !a.AutoRules || a.Rules[0].SourcePrefix != 32 || a.Rules[0].SourcePorts != "0-65535" {
		t.Fatal("pinned defaults", a)
	}
	for _, pair := range [][2]string{{`"TCP"`, `"ICMPV6"`}, {`"443"`, `"65536"`}, {`"443"`, `"100-1"`}, {`"192.0.2.1"`, `"2001:db8::1"`}, {`"name":"CVK_LAB"`, `"name":"CVK_LAB","unknown":true`}, {`"protocol":"TCP"`, `"protocol":"TCP","source_prefix":33`}} {
		if _, err := parseWirelessACLs([]byte(strings.Replace(wirelessACLModel, pair[0], pair[1], 1))); err == nil {
			t.Fatal("invalid ACL accepted", pair)
		}
	}
	got := a
	got.URLs = []string{"example.com"}
	if !wirelessACLMatches(a, got) {
		t.Fatal("omitted URLs acquired ownership")
	}
	a.URLs = []string{}
	if wirelessACLMatches(a, got) {
		t.Fatal("explicit URL removal ignored")
	}
	a.URLs = []string{"example.com"}
	if !wirelessACLMatches(a, got) {
		t.Fatal("URL comparison")
	}
}

func TestWirelessCombinedPreflight(t *testing.T) {
	raw := strings.Replace(wirelessACLModel, `"wireless":{`, `"wireless":{"interfaces":[{"name":"new","vlan_id":123}],`, 1)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("write before complete preflight")
		}
		if r.URL.Path == wirelessInterfacesPath {
			fmt.Fprint(w, `{"response":[]}`)
			return
		}
		if r.URL.Path != wirelessACLsPath {
			t.Error("unexpected endpoint")
		}
		fmt.Fprint(w, `{"response":[{"id":"bad","aclListName":"CVK_LAB"}]}`)
	})
	e, err := c.compileWirelessConfiguration([]byte(raw))
	if err != nil || e.count() != 2 {
		t.Fatal(err)
	}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("interface write released with malformed ACL inventory")
	}
}

func TestWirelessACLURLUpdatePayload(t *testing.T) {
	for _, clear := range []bool{false, true} {
		desired, _ := parseWirelessACLs([]byte(wirelessACLModel))
		actual := desired[0]
		actual.ID = "acl-id"
		actual.URLs = []string{"example.com"}
		actual.ACLName = "older"
		if clear {
			desired[0].URLs = []string{}
		}
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			if r.Method == "GET" {
				json.NewEncoder(w).Encode(map[string]any{"response": []wirelessACL{actual}})
				return
			}
			if r.Method != "PUT" || r.URL.Path != wirelessACLsPath+"/acl-id" {
				t.Error("unexpected mutation")
			}
			var payload map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&payload) != nil {
				t.Fatal("payload")
			}
			var urls []string
			if json.Unmarshal(payload["walledGardenUrls"], &urls) != nil {
				t.Error("missing explicit URL payload")
			}
			if clear && len(urls) != 0 || !clear && (len(urls) != 1 || urls[0] != "example.com") {
				t.Error("URL ownership changed", urls)
			}
			fmt.Fprint(w, `{"response":{"taskId":"acl-task"}}`)
		})
		e := &wirelessACLExecutor{client: c, desired: desired}
		write, err := e.next(context.Background())
		if err != nil || write == nil {
			t.Fatal(err)
		}
		if _, err = write.submit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWirelessACLIPv6(t *testing.T) {
	raw := strings.Replace(wirelessACLModel, `"name":"CVK_LAB"`, `"name":"CVK_LAB","ipv6_enabled":true`, 1)
	raw = strings.ReplaceAll(raw, "192.0.2.1", "2001:db8::1")
	raw = strings.ReplaceAll(raw, "198.51.100.1", "2001:db8::2")
	raw = strings.Replace(raw, `"protocol":"TCP"`, `"protocol":"ICMPV6","source_prefix":128,"destination_prefix":64`, 1)
	if _, err := parseWirelessACLs([]byte(raw)); err != nil {
		t.Fatal("valid IPv6 model rejected", err)
	}
	if _, err := parseWirelessACLs([]byte(strings.Replace(raw, "128", "129", 1))); err == nil {
		t.Fatal("invalid IPv6 prefix")
	}
}
