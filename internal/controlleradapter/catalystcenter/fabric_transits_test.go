// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const transitModel = `{"catalyst_center":{"fabric":{"transits":[{"name":"CVK_IP","type":"IP_BASED_TRANSIT","routing_protocol_name":"BGP","autonomous_system_number":65001}]}}}`

func TestTransitASNAndModel(t *testing.T) {
	for _, v := range []any{1, 4294967295, "65001", json.Number("65001")} {
		if _, err := transitASN(v); err != nil {
			t.Fatal(v, err)
		}
	}
	for _, v := range []any{nil, true, 0, -1, 4294967296, 1.1, "1.10", " 65001", "1e3"} {
		if _, err := transitASN(v); err == nil {
			t.Fatal("invalid ASN accepted", v)
		}
	}
	for _, replace := range [][2]string{
		{`"IP_BASED_TRANSIT"`, `"SDA_LISP_PUB_SUB_TRANSIT"`},
		{`"BGP"`, `"OSPF"`},
		{`"name":"CVK_IP"`, `"name":"CVK_IP","control_plane_devices":[]`},
		{`"name":"CVK_IP"`, `"name":"CVK_IP","create_per_site":true`},
		{`"name":"CVK_IP"`, `"name":"CVK_IP","multicast_over_sda_transit":null`},
	} {
		if _, err := parseFabricTransits([]byte(strings.Replace(transitModel, replace[0], replace[1], 1))); err == nil {
			t.Fatal("unsupported transit accepted")
		}
	}
}

func TestTransitManifestRestartReplacementAndReport(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"fabric"}
	config.Spec.Source.Inline.Raw = []byte(transitModel)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	rows := []fabricTransit{}
	writes := 0
	stamp := int64(0)
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == fabricTransitsPath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.URL.Path == fabricTransitsPath && r.Method == "POST":
			var body []fabricTransit
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || body[0].ID != "" || body[0].IP == nil || body[0].IP.ASN != "65001" {
				t.Error("invalid transit body")
				w.WriteHeader(400)
				return
			}
			writes++
			stamp = time.Now().UnixMilli()
			body[0].ID = "transit-id"
			rows = body
			fmt.Fprint(w, `{"response":{"taskId":"transit-task"}}`)
		case r.URL.Path == taskPath+"transit-task":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "transit-task", "isError": false, "startTime": stamp, "endTime": stamp}})
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
	for range 20 {
		tick()
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	if writes != 1 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("transit did not converge", config.Status)
	}
	tick()
	if writes != 1 {
		t.Fatal("transit replayed")
	}
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(transitModel, "65001", "65002", 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	tick()
	if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || writes != 1 {
		t.Fatal("implicit transit replacement")
	}
	plans, err := a.client.compileConfigReports(config.Spec.Source.Inline.Raw, []string{"fabric"})
	if err != nil {
		t.Fatal(err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 1 || drift[0].Path != "/transits" {
		t.Fatal("missing transit drift", err)
	}
}

func TestCombinedFabricPreflight(t *testing.T) {
	raw := strings.Replace(transitModel, `"fabric":{`, `"fabric":{"l3_virtual_networks":[{"name":"new"}],`, 1)
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("mutation during preflight")
		}
		if r.URL.Path == fabricL3NetworksPath {
			fmt.Fprint(w, `{"response":[]}`)
			return
		}
		if r.URL.Path != fabricTransitsPath {
			t.Error("unexpected endpoint")
		}
		fmt.Fprint(w, `{"response":[{"id":"existing","name":"CVK_IP","type":"IP_BASED_TRANSIT","ipTransitSettings":{"routingProtocolName":"BGP","autonomousSystemNumber":"65002"}}]}`)
	})
	e, err := c.compileFabricConfiguration([]byte(raw))
	if err != nil || e.count() != 2 {
		t.Fatal(err)
	}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("earlier VN write released before conflicting transit resolved")
	}
}

func TestTransitInventoryValidation(t *testing.T) {
	for _, body := range []string{
		`{"response":[{"id":"one","name":"A","type":"IP_BASED_TRANSIT"}]}`,
		`{"response":[{"id":"one","name":"A","type":"IP_BASED_TRANSIT","ipTransitSettings":{"routingProtocolName":"BGP","autonomousSystemNumber":"0"}}]}`,
		`{"response":[{"id":"one","name":"A","type":"unknown"}]}`,
		`{"response":[{"id":"one","name":"A","type":"SDA_LISP_BGP_TRANSIT"},{"id":"two","name":"A","type":"SDA_LISP_BGP_TRANSIT"}]}`,
		`{"response":[{"id":"one","name":"A","type":"SDA_LISP_BGP_TRANSIT"},{"id":"one","name":"B","type":"SDA_LISP_BGP_TRANSIT"}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				fmt.Fprint(w, body)
			})
			if _, err := c.fabricTransits(context.Background()); err == nil {
				t.Fatal("malformed inventory accepted")
			}
		})
	}
}

func TestTransitInventoryPagination(t *testing.T) {
	pages := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path != fabricTransitsPath || r.URL.Query().Get("limit") != "500" {
			t.Error("unexpected list request")
		}
		pages++
		rows := []fabricTransit{}
		switch r.URL.Query().Get("offset") {
		case "1":
			for i := range 500 {
				rows = append(rows, fabricTransit{ID: fmt.Sprintf("id-%d", i), Name: fmt.Sprintf("name-%d", i), Type: "SDA_LISP_BGP_TRANSIT"})
			}
		case "501":
			rows = append(rows, fabricTransit{ID: "last", Name: "last", Type: "IP_BASED_TRANSIT", IP: &ipTransitSettings{Protocol: "BGP", ASN: "4294967295"}})
		default:
			t.Error("unexpected offset")
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	rows, err := c.fabricTransits(context.Background())
	if err != nil || len(rows) != 501 || pages != 2 {
		t.Fatal("pagination failed", len(rows), pages, err)
	}
}
