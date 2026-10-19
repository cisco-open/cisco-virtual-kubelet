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

const wirelessAnchorModel = `{"catalyst_center":{"wireless":{"anchor_groups":[{"name":"CVK_LAB","mobility_anchors":[{"device_name":"external-wlc","ip_address":"192.0.2.200","anchor_priority":"PRIMARY","managed_anchor_wlc":false,"peer_device_type":"IOS-XE","mac_address":"02:00:00:00:00:01","mobility_group_name":"CVKLAB"}]}]}}}`

func TestWirelessAnchorManifestRestartUpdate(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"wireless"}
	rows := []wirelessAnchorGroup{}
	writes := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == devicesPath:
			fmt.Fprint(w, `{"response":[]}`)
		case r.URL.Path == wirelessAnchorPath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.URL.Path == wirelessAnchorPath && r.Method == "POST" || r.URL.Path == wirelessAnchorPath+"/anchor-id" && r.Method == "PUT":
			var body wirelessAnchorGroup
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.ID != "" || body.Name != "CVK_LAB" {
				t.Error("invalid payload")
				w.WriteHeader(400)
				return
			}
			writes++
			stamp = time.Now().UnixMilli()
			body.ID = "anchor-id"
			rows = []wirelessAnchorGroup{body}
			fmt.Fprint(w, `{"response":{"taskId":"anchor-task"}}`)
		case r.URL.Path == taskPath+"anchor-task" || r.URL.Path == taskPath+"anchor-task/tree":
			writeDefinitionTaskFixture(w, r, map[string]any{"id": "anchor-task", "isError": false, "startTime": stamp, "endTime": stamp})
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
	models := []string{wirelessAnchorModel, strings.Replace(wirelessAnchorModel, "PRIMARY", "SECONDARY", 1)}
	desired, err := parseWirelessAnchors([]byte(models[1]))
	if err != nil {
		t.Fatal(err)
	}
	appendModel := func() {
		t.Helper()
		peers := []map[string]any{}
		for _, p := range desired[0].Peers {
			peers = append(peers, map[string]any{"device_name": p.DeviceName, "ip_address": p.IP, "anchor_priority": p.Priority, "managed_anchor_wlc": *p.Managed, "peer_device_type": *p.Type, "mac_address": *p.MAC, "mobility_group_name": *p.Group})
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"wireless": map[string]any{"anchor_groups": []any{map[string]any{"name": "CVK_LAB", "mobility_anchors": peers}}}}})
		if err != nil {
			t.Fatal(err)
		}
		models = append(models, string(raw))
	}
	peer := desired[0].Peers[0]
	peer.DeviceName = "external-two"
	peer.IP = "192.0.2.201"
	peer.Priority = "PRIMARY"
	mac := "02:00:00:00:00:02"
	peer.MAC = &mac
	desired[0].Peers = append(desired[0].Peers, peer)
	appendModel()
	desired[0].Peers[0].Priority = "PRIMARY"
	desired[0].Peers[1].Priority = "SECONDARY"
	appendModel()
	desired[0].Peers = desired[0].Peers[1:]
	appendModel()
	for _, model := range models {
		config.Generation++
		config.Spec.Source.Inline.Raw = []byte(model)
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
	if writes != 5 || len(rows) != 1 || len(rows[0].Peers) != 1 || rows[0].Peers[0].DeviceName != "external-two" || rows[0].Peers[0].Priority != "SECONDARY" {
		t.Fatal("incorrect create/update/no-op", writes, rows)
	}
	desired, _ = parseWirelessAnchors(config.Spec.Source.Inline.Raw)
	e := &wirelessAnchorExecutor{client: a.client, desired: desired}
	if ok, err := e.verify(ctx, &ciskov1.NetworkControllerConfigurationOperation{Action: "Update", Resource: "anchor-group:CVK_LAB", TargetID: "replacement"}); err != nil || ok {
		t.Fatal("UUID replacement accepted", err)
	}
	rows[0].Peers[0].Priority = "PRIMARY"
	plans, err := a.client.compileConfigReports(config.Spec.Source.Inline.Raw, []string{"wireless"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Change != configapi.NetworkControllerDriftChangeUpdate {
		t.Fatal("missing drift", err)
	}
}

func TestWirelessAnchorInputAndNormalization(t *testing.T) {
	for _, pair := range [][2]string{{`"managed_anchor_wlc":false`, `"managed_anchor_wlc":null`}, {`"managed_anchor_wlc":false,`, ``}, {`"PRIMARY"`, `"bad"`}, {`"192.0.2.200"`, `"not-an-ip"`}, {`"peer_device_type":"IOS-XE"`, `"peer_device_type":"other"`}, {`"name":"CVK_LAB"`, `"name":"CVK_LAB","unknown":true`}, {`"mac_address":"02:00:00:00:00:01"`, `"mac_address":"invalid"`}} {
		if _, err := parseWirelessAnchors([]byte(strings.Replace(wirelessAnchorModel, pair[0], pair[1], 1))); err == nil {
			t.Fatal("invalid anchor accepted", pair)
		}
	}
	desired, err := parseWirelessAnchors([]byte(strings.Replace(wirelessAnchorModel, "02:00:00:00:00:01", "0200.0000.0001", 1)))
	if err != nil || *desired[0].Peers[0].MAC != "02:00:00:00:00:01" {
		t.Fatal("MAC normalization", err)
	}
	group := desired[0]
	group.Peers = append(group.Peers, group.Peers[0])
	if normalizeWirelessAnchor(&group) == nil {
		t.Fatal("duplicate peer accepted")
	}
}

func TestWirelessAnchorResolutionAndOptionalOwnership(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		desired, _ := parseWirelessAnchors([]byte(wirelessAnchorModel))
		desired[0].Peers[0].DeviceName = "wlc"
		yes := true
		desired[0].Peers[0].Managed = &yes
		actual := desired[0]
		actual.ID = "anchor-id"
		actual.Peers = append([]wirelessAnchorPeer(nil), actual.Peers...)
		actual.Peers[0].DeviceName = "wlc.lab"
		actual.Peers[0].Priority = "SECONDARY"
		private := "192.0.2.201"
		actual.Peers[0].PrivateIP = &private
		writes := 0
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == authPath:
				fmt.Fprint(w, `{"Token":"test"}`)
			case r.URL.Path == devicesPath:
				rows := []Device{{ID: "wlc-id", Hostname: "wlc.lab"}}
				if ambiguous {
					rows = append(rows, Device{ID: "other", Hostname: "wlc.other"})
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			case r.URL.Path == wirelessAnchorPath && r.Method == "GET":
				json.NewEncoder(w).Encode(map[string]any{"response": []wirelessAnchorGroup{actual}})
			case r.URL.Path == wirelessAnchorPath+"/anchor-id" && r.Method == "PUT":
				writes++
				var body wirelessAnchorGroup
				json.NewDecoder(r.Body).Decode(&body)
				if body.ID != "" || body.Peers[0].DeviceName != "wlc.lab" || body.Peers[0].PrivateIP == nil || *body.Peers[0].PrivateIP != private {
					t.Error("peer resolution or optional ownership lost")
				}
				fmt.Fprint(w, `{"response":{"taskId":"anchor-task"}}`)
			default:
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				w.WriteHeader(404)
			}
		})
		e := &wirelessAnchorExecutor{client: c, desired: desired}
		write, err := e.next(context.Background())
		if ambiguous {
			if err == nil || write != nil || writes != 0 {
				t.Fatal("ambiguous hostname accepted")
			}
			continue
		}
		if err != nil || write == nil {
			t.Fatal(err)
		}
		if _, err = write.submit(context.Background()); err != nil {
			t.Fatal(err)
		}
		if writes != 1 || e.desired[0].Peers[0].DeviceName != "wlc" || e.desired[0].Peers[0].PrivateIP != nil {
			t.Fatal("compiler intent mutated")
		}
	}
}

func TestWirelessAnchorCombinedPreflight(t *testing.T) {
	raw := strings.Replace(wirelessAnchorModel, `"wireless":{`, `"wireless":{"interfaces":[{"name":"new","vlan_id":123}],`, 1)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("mutation before complete preflight")
		}
		if r.URL.Path == wirelessInterfacesPath {
			fmt.Fprint(w, `{"response":[]}`)
			return
		}
		fmt.Fprint(w, `{"response":[{"id":"bad","anchorGroupName":"broken","mobilityAnchors":[{"deviceName":"wlc","ipAddress":"192.0.2.1","anchorPriority":"PRIMARY"}]}]}`)
	})
	e, err := c.compileWirelessConfiguration([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("write released despite malformed anchor inventory")
	}
}
