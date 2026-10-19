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

const fabricL3Model = `{"catalyst_center":{"fabric":{"l3_virtual_networks":[{"name":"CVK-Lab"}]}}}`

func TestFabricL3ManifestRestartAndNoOp(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint("failed=", failed), func(t *testing.T) { fabricL3ManifestCycle(t, failed) })
	}
}
func fabricL3ManifestCycle(t *testing.T, failed bool) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"fabric"}
	config.Spec.Source.Inline.Raw = []byte(fabricL3Model)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	writes := 0
	stamp := int64(0)
	rows := []fabricL3Network{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == fabricL3NetworksPath && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case r.URL.Path == fabricL3NetworksPath && r.Method == "POST":
			var body []map[string]string
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || len(body[0]) != 1 || body[0]["virtualNetworkName"] != "CVK-Lab" {
				t.Error("unexpected create payload")
			}
			writes++
			stamp = time.Now().UnixMilli()
			rows = []fabricL3Network{{ID: "vn-id", Name: "CVK-Lab"}}
			fmt.Fprint(w, `{"response":{"taskId":"vn-task"}}`)
		case r.URL.Path == taskPath+"vn-task":
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "vn-task", "isError": failed, "errorCode": "NCSP11000", "failureReason": "SECRET-MARKER", "startTime": stamp, "endTime": stamp}})
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
	if failed {
		op := journalState(t, a).Operation
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseFailed || writes != 1 || op == nil || op.Phase != "Accepted" || op.TaskID != "vn-task" {
			t.Fatal("failed task lost fence or replayed", config.Status, writes)
		}
		found := false
		for _, c := range config.Status.Conditions {
			if strings.Contains(c.Message, "SECRET") {
				t.Fatal("raw task details published")
			}
			if c.Reason == "ControllerTaskFailed" && strings.Contains(c.Message, "NCSP11000") {
				found = true
			}
		}
		if !found {
			t.Fatal("missing diagnostic task failure", config.Status.Conditions)
		}
		return
	}
	if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || writes != 1 || journalState(t, a).Operation != nil {
		t.Fatal("virtual network did not converge", config.Status, writes)
	}
	tick()
	if writes != 1 {
		t.Fatal("virtual network replayed")
	}
	// Membership belongs to a different resource and must survive global no-op.
	rows[0].Fabrics = []string{"fabric-id"}
	tick()
	if writes != 1 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("global definition changed membership")
	}
}

func TestFabricL3ModelValidation(t *testing.T) {
	for _, replacement := range [][2]string{
		{`{"name":"CVK-Lab"}`, `"CVK-Lab"`},
		{`"name":"CVK-Lab"`, `"name":"CVK-Lab","anchor_site":"Global/Lab"`},
		{`"name":"CVK-Lab"`, `"name":"CVK-Lab","anchor_site":null`},
		{`{"name":"CVK-Lab"}`, `{"name":"CVK-Lab"},{"name":"CVK-Lab"}`},
		{`"l3_virtual_networks":`, `"fabric_sites":`},
	} {
		if _, err := parseFabricL3Networks([]byte(strings.Replace(fabricL3Model, replacement[0], replacement[1], 1))); err == nil {
			t.Fatal("unsupported fabric intent accepted")
		}
	}
}

func TestFabricL3InventoryAndPreflight(t *testing.T) {
	for _, mode := range []string{"anchor", "duplicate-name", "duplicate-id", "missing-system", "null", "invalid-anchor"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("mutation during preflight")
				}
				rows := []fabricL3Network{{ID: "vn", Name: "existing"}}
				switch mode {
				case "anchor":
					rows[0].Anchor = "fabric"
					rows[0].Fabrics = []string{"fabric"}
				case "duplicate-name":
					rows = append(rows, fabricL3Network{ID: "other", Name: "existing"})
				case "duplicate-id":
					rows = append(rows, fabricL3Network{ID: "vn", Name: "other"})
				case "null":
					fmt.Fprint(w, `{"response":null}`)
					return
				case "invalid-anchor":
					rows[0].Anchor = "fabric"
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			names := []string{"new", "existing"}
			if mode == "missing-system" {
				names = append(names, "INFRA_VN")
			}
			if write, err := (&fabricL3Executor{client: c, names: names}).next(context.Background()); err == nil || write != nil {
				t.Fatal("partial write allowed")
			}
		})
	}
}
