// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestAnycastBulkStableTwentyMemberChunks(t *testing.T) {
	for _, mode := range []string{"complete", "partial", "late-cohort-conflict", "late-pool", "tail-source-change", "legacy-large-recovery", "public-manifest", "bad-offset"} {
		t.Run(mode, func(t *testing.T) { exerciseAnycastBulkChunks(t, mode, nil) })
	}
}

func exerciseAnycastBulkChunks(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	wants := []fabricAnycastIntent{}
	reservations := []ipReservation{}
	for i := 44; i >= 0; i-- {
		name := fmt.Sprintf("pool-%02d", i)
		model := map[string]any{"ip_pool_name": name, "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true, "critical_pool": false, "layer2_flooding": false, "wireless_pool": false, "ip_directed_broadcast": false, "intra_subnet_routing_enabled": false, "multiple_ip_to_mac_addresses": false}
		if mode == "late-cohort-conflict" && (i == 0 || i == 44) {
			model["vlan_id"] = 150
		}
		intent, err := parseAnycastGateway(model)
		if err != nil {
			t.Fatal(err)
		}
		wants = append(wants, fabricAnycastIntent{path: "Global/Lab", anycastGatewayIntent: intent})
		bits := 30
		gateway := fmt.Sprintf("192.0.2.%d", i*4+1)
		reservations = append(reservations, ipReservation{ID: fmt.Sprintf("reservation-%02d", i), Name: name, Type: "LAN", SiteID: "area", V4: &poolAddressSpace{Subnet: fmt.Sprintf("192.0.2.%d", i*4), PrefixLength: &bits, Gateway: &gateway, GlobalPoolID: "pool"}})
	}
	gateways := []map[string]any{}
	var latestBody []map[string]any
	taskReads := 0
	fabricID := "fabric"
	posts := []int{}
	flag := false
	appendGateway := func(body map[string]any) {
		row := map[string]any{}
		for key, value := range body {
			if key != "autoGenerateVlanName" {
				row[key] = value
			}
		}
		pool := row["ipPoolName"].(string)
		var index int
		fmt.Sscanf(pool, "pool-%02d", &index)
		row["id"] = "gateway-" + pool
		row["vlanId"] = 100 + index
		row["vlanName"] = fmt.Sprintf("192.0.2.%d/30-Corp", index*4)
		gateways = append(gateways, row)
	}
	if mode == "public-existing-replaced" {
		for _, want := range wants {
			if want.pool == "pool-00" {
				body := map[string]any{}
				for k, v := range want.payload {
					body[k] = v
				}
				body["fabricId"] = "fabric"
				appendGateway(body)
			}
		}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		reply := func(value any) { json.NewEncoder(w).Encode(map[string]any{"response": value}) }
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case sitesPath:
			reply(sampleSites())
		case fabricSitesPath:
			reply([]fabricSite{{ID: fabricID, SiteID: "area", Authentication: "Closed Authentication", PubSub: &flag}})
		case fabricZonesPath:
			reply([]fabricZone{})
		case fabricL3NetworksPath:
			reply([]fabricL3Network{{ID: "network", Name: "Corp", Fabrics: []string{"fabric"}}})
		case globalPoolsPath:
			reply(fixturePools())
		case sitePoolsPath:
			reply(reservations)
		case fabricL2NetworksPath:
			reply([]fabricL2Network{})
		case anycastGatewaysPath:
			if r.Method == "GET" {
				reply(gateways)
				return
			}
			if r.Method != "POST" {
				t.Error("unexpected mutation")
				w.WriteHeader(500)
				return
			}
			var body []map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) > 20 {
				t.Error("oversized/invalid native request")
			}
			posts = append(posts, len(body))
			latestBody = body
			for i, row := range body {
				if mode != "partial" && mode != "public-partial" || i < 10 {
					appendGateway(row)
				}
			}
			reply(map[string]any{"taskId": fmt.Sprintf("task-%d", len(posts))})
		default:
			if strings.HasPrefix(r.URL.Path, taskPath) {
				now := time.Now().UnixMilli()
				taskReads++
				id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
				if mode == "public-tree-unavailable" && strings.HasSuffix(r.URL.Path, "/tree") {
					w.WriteHeader(404)
					return
				}
				root := map[string]any{"id": id, "isError": false, "startTime": now, "endTime": now}
				var children []map[string]any
				if mode == "public-child-failed" || mode == "public-child-pending" || strings.HasPrefix(mode, "public-accepted-child-") {
					child := map[string]any{"id": "child-" + id, "rootId": id, "parentId": id, "isError": strings.HasSuffix(mode, "child-failed"), "startTime": now, "endTime": now}
					if strings.HasSuffix(mode, "child-pending") {
						child["endTime"] = 0
					}
					children = append(children, child)
				}
				writeDefinitionTaskFixture(w, r, root, children...)
				return
			}
			t.Errorf("unexpected API %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	if strings.HasPrefix(mode, "public-") {
		a, config := journalFixture(t)
		a.client = c
		rows := []any{}
		for _, want := range wants {
			rows = append(rows, map[string]any{"ip_pool_name": want.pool, "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true, "critical_pool": false, "layer2_flooding": false, "wireless_pool": false, "ip_directed_broadcast": false, "intra_subnet_routing_enabled": false, "multiple_ip_to_mac_addresses": false})
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"fabric_sites": []any{map[string]any{"name": "Global/Lab", "authentication_template": map[string]any{"name": "Closed Authentication"}, "pub_sub_enabled": false, "l3_virtual_networks": []string{"Corp"}, "anycast_gateways": rows}}}}})
		if err != nil {
			t.Fatal(err)
		}
		config.Spec.ManagedSections = []string{"fabric"}
		config.Spec.Source.Inline.Raw = raw
		config.Annotations = map[string]string{anycastBulkAnnotation: "true"}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		if install != nil {
			install(a, config)
			// The real-API installer deliberately replaces ObjectMeta. Restore
			// the public opt-in on the persisted config before the first reconcile.
			config.Annotations = map[string]string{anycastBulkAnnotation: "true"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
		}
		if mode == "public-legacy-known" {
			before, err := c.observeAnycastBulk(ctx, wants)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range wants {
				body := map[string]any{}
				for k, v := range want.payload {
					body[k] = v
				}
				body["fabricId"] = "fabric"
				appendGateway(body)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "fabric", ConfigName: config.Name, ConfigUID: string(config.UID)}}, Operation: &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "fabric", Resource: before.resource(before.mask), Action: "Create", Phase: "Unknown", ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, TaskID: "legacy-task", ParentID: "fabric", SubmittedAt: &metav1.Time{Time: time.Now().UTC()}}}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}
		recovered := map[string]bool{}
		partialReleased := false
		acceptedHeld := strings.HasPrefix(mode, "public-accepted-child-")
		frozen := acceptedHeld || mode == "public-child-failed" || mode == "public-child-pending" || mode == "public-tree-unavailable" || mode == "public-source-changed" || mode == "public-parent-replaced" || mode == "public-taskless" || mode == "public-binding-changed" || mode == "public-existing-replaced"
		key := ctrlclient.ObjectKeyFromObject(config)
		for range 60 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if state := nc.Status.Configuration; state != nil && state.Operation != nil {
				op := state.Operation
				if op.Phase == "Accepted" && mode != "public-manifest" && !acceptedHeld && !recovered[op.ID] {
					if !strings.Contains(op.Resource, "anycast-gateway-bulk:") {
						t.Fatal("expected original bulk operation", op.Resource)
					}
					recovered[op.ID] = true
					if err := a.advanceConfiguration(ctx, op.ID, "Unknown", ""); err != nil {
						t.Fatal(err)
					}
					if err := a.statusReader.Get(ctx, key, config); err != nil {
						t.Fatal(err)
					}
					if mode == "public-option-removed" && len(posts) == 3 {
						config.Annotations = nil
						if err := a.statusWriter.Update(ctx, config); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "public-source-changed" {
						config.Generation++
						config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(raw), "pool-44", "pool-tail-replaced", 1))
						if err := a.statusWriter.Update(ctx, config); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "public-existing-replaced" {
						gateways[0]["id"] = "replaced-original-existing"
					}
					if mode == "public-parent-replaced" {
						fabricID = "replacement"
					}
					if mode == "public-taskless" || mode == "public-binding-changed" {
						nc, _ = a.currentController(ctx)
						if mode == "public-taskless" {
							nc.Status.Configuration.Operation.TaskID = ""
						} else {
							parts := strings.Split(nc.Status.Configuration.Operation.Resource, ":")
							parts[len(parts)-2] = strings.Repeat("f", 64)
							nc.Status.Configuration.Operation.Resource = strings.Join(parts, ":")
						}
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
					}
				}
				if mode == "public-partial" && op.Phase == "Verifying" {
					seen := map[string]bool{}
					for _, row := range gateways {
						seen[row["ipPoolName"].(string)] = true
					}
					for _, row := range latestBody {
						if !seen[row["ipPoolName"].(string)] {
							appendGateway(row)
						}
					}
					partialReleased = true
				}
			}
			// Kubernetes JSON may omit empty collections; recover from the
			// persisted representation rather than a retained Go object.
			nc, _ = a.currentController(ctx)
			encoded, _ := json.Marshal(nc)
			var restored cvk.NetworkController
			if json.Unmarshal(encoded, &restored) != nil {
				t.Fatal("invalid journal encoding")
			}
			if err := a.statusWriter.Status().Update(ctx, &restored); err != nil {
				t.Fatal(err)
			}

		}
		if err := a.statusReader.Get(ctx, key, config); err != nil {
			t.Fatal(err)
		}
		nc, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if frozen {
			phase, recoveredCount := "Unknown", 1
			if acceptedHeld {
				phase, recoveredCount = "Accepted", 0
				if taskReads == 0 {
					t.Fatal("accepted bulk task was not observed")
				}
			}
			if len(posts) != 1 || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != phase || len(recovered) != recoveredCount {
				t.Fatal("unsafe bulk recovery/replay", posts, config.Status.Conditions)
			}
			if mode == "public-binding-changed" || mode == "public-parent-replaced" || mode == "public-source-changed" || mode == "public-taskless" || mode == "public-existing-replaced" {
				if taskReads != 0 {
					t.Fatal("unbound original task observed")
				}
			}
			return
		}
		if mode == "public-legacy-known" {
			if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || nc.Status.Configuration.Operation != nil || len(posts) != 0 || taskReads == 0 {
				t.Fatal("legacy acknowledged cohort replayed or not recovered", posts, config.Status.Conditions)
			}
			return
		}
		if mode != "public-manifest" && len(recovered) != 3 {
			t.Fatal("not every original chunk recovered", len(recovered))
		}
		if mode == "public-partial" && !partialReleased {
			t.Fatal("partial original footprint did not wait for readback")
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || nc.Status.Configuration.Operation != nil || len(posts) != 3 || posts[0] != 20 || posts[1] != 20 || posts[2] != 5 {
			t.Fatal("public chunk lifecycle failed", posts, config.Status.Conditions)
		}
		return
	}
	guardCalls := 0
	executor := &anycastGatewayExecutor{client: c, desired: wants, bulkGuard: func(context.Context, string, string) error { guardCalls++; return nil }}
	if mode == "legacy-large-recovery" {
		before, err := c.observeAnycastBulk(ctx, wants)
		if err != nil {
			t.Fatal(err)
		}
		op := &cvk.NetworkControllerConfigurationOperation{Action: "Create", ParentID: "fabric", Resource: before.resource(before.mask)}
		for _, want := range wants {
			body := map[string]any{}
			for k, v := range want.payload {
				body[k] = v
			}
			body["fabricId"] = "fabric"
			appendGateway(body)
		}
		done, err := (&anycastGatewayExecutor{client: c, desired: wants}).verify(ctx, op)
		if err != nil || !done || len(posts) != 0 {
			t.Fatal("acknowledged legacy footprint lost", err)
		}
		if _, err := c.planAnycastGatewayBulk(ctx, wants, func(context.Context) error { return nil }); err == nil {
			t.Fatal("new oversized legacy request allowed")
		}
		return
	}
	for batch := 0; batch < 3; batch++ {
		write, err := executor.next(ctx)
		if mode == "late-cohort-conflict" {
			if err == nil || write != nil || len(posts) != 0 {
				t.Fatal("later chunk conflict hid behind first write", err)
			}
			return
		}
		if err != nil || write == nil || !strings.HasPrefix(write.resource, anycastBulkChunkPrefix) {
			t.Fatal("chunk planning", err)
		}
		if mode == "late-pool" {
			reservations[0].ID = "replaced-tail-pool"
		}
		_, err = write.submit(ctx)
		if mode == "late-pool" {
			if err == nil || len(posts) != 0 || guardCalls != 0 {
				t.Fatal("later dependency changed before dispatch", err)
			}
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		op := &cvk.NetworkControllerConfigurationOperation{Action: "Create", ParentID: write.identity.parent, Resource: write.resource}
		if mode == "bad-offset" {
			parts := strings.Split(op.Resource, ":")
			parts[2] = "1"
			op.Resource = strings.Join(parts, ":")
		}
		if mode == "tail-source-change" {
			wants[0].payload["isWirelessPool"] = true
		}
		done, err := (&anycastGatewayExecutor{client: c, desired: wants}).verify(ctx, op)
		if mode == "partial" {
			if err != nil || done || len(posts) != 1 || posts[0] != 20 {
				t.Fatal("partial batch shrank original selection", err)
			}
			return
		}
		if mode == "tail-source-change" || mode == "bad-offset" {
			if err == nil || done {
				t.Fatal("same-sized changed cohort verified")
			}
			return
		}
		if err != nil || !done {
			t.Fatal("original chunk recovery", err)
		}
		if _, err := write.submit(ctx); err == nil {
			t.Fatal("chunk closure replayed")
		}
	}
	if len(posts) != 3 || posts[0] != 20 || posts[1] != 20 || posts[2] != 5 || guardCalls != 3 {
		t.Fatal("wrong chunk sequence", posts)
	}
	next, err := executor.next(ctx)
	if err != nil || next != nil {
		t.Fatal("settled chunks replayed", err)
	}
}

func TestAnycastBulkAcknowledgedRecovery(t *testing.T) {
	for _, mode := range []string{"public-unknown", "public-partial", "public-option-removed", "public-child-failed", "public-child-pending", "public-tree-unavailable", "public-source-changed", "public-parent-replaced", "public-taskless", "public-binding-changed", "public-existing-replaced", "public-legacy-known"} {
		t.Run(mode, func(t *testing.T) { exerciseAnycastBulkChunks(t, mode, nil) })
	}
}
