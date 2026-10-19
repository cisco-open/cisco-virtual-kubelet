// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const ipamTestPoolID = "11111111-1111-4111-8111-111111111111"
const ipamTestReservationID = "22222222-2222-4222-8222-222222222222"

func TestIPAMOwnedPublicCycleAndLegacyRecovery(t *testing.T) {
	for _, mode := range []string{"owned", "legacy", "mixed-metadata", "mixed-fabric"} {
		t.Run(mode, func(t *testing.T) {
			legacy, mixed := mode == "legacy", mode == "mixed-metadata"
			fabric := mode == "mixed-fabric"
			ctx := context.Background()
			a, config := journalFixture(t)
			if mixed {
				a, config = noAuthCredentialConfigurationFixture(t)
			}
			config.Spec.ManagedSections = []string{"network_settings"}
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(poolModel)}
			if mixed {
				var root, credential map[string]any
				_ = json.Unmarshal(config.Spec.Source.Inline.Raw, &root)
				_ = json.Unmarshal(noAuthCredentialModel("fixture", "tester"), &credential)
				root["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["device_credentials"] = credential["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["device_credentials"]
				config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
			}
			if fabric {
				var root map[string]any
				_ = json.Unmarshal(config.Spec.Source.Inline.Raw, &root)
				root["catalyst_center"].(map[string]any)["fabric"] = map[string]any{"l3_virtual_networks": []any{map[string]any{"name": "ExistingVN"}}}
				config.Spec.ManagedSections = []string{"fabric", "network_settings"}
				config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			pools := []ipPool{}
			reservations := []ipReservation{}
			writes := 0
			tasks := map[string]map[string]any{}
			actualCredentials := []globalCredentialMetadata{}
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method == "GET" {
					var rows any
					switch r.URL.Path {
					case globalPoolsPath:
						rows = pools
					case sitePoolsPath:
						rows = reservations
					case sitesPath:
						rows = sampleSites()
					case globalCredentialsPath:
						rows = map[string]any{"snmpV3": actualCredentials}
					case fabricL3NetworksPath:
						rows = []fabricL3Network{{ID: "existing-vn", Name: "ExistingVN"}}
					default:
						if task, ok := tasks[strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")]; ok {
							var response any = task
							if strings.HasSuffix(r.URL.Path, "/tree") {
								response = []any{task}
							}
							json.NewEncoder(w).Encode(map[string]any{"response": response})
							return
						}
						t.Error("unexpected GET", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				}
				writes++
				id := ""
				path := r.URL.Path
				switch {
				case path == globalCredentialsPath && mixed:
					var payload map[string][]globalCredentialMetadata
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload["snmpV3"]) != 1 || len(actualCredentials) != 0 {
						t.Error("invalid/duplicate metadata credential create", err)
						w.WriteHeader(400)
						return
					}
					actualCredentials = payload["snmpV3"]
					actualCredentials[0].ID = "metadata-credential"
					id = "metadata-credential"
				case path == globalPoolsPath || path == globalPoolsPath+"/"+ipamTestPoolID:
					var p ipPool
					if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
						t.Error(err)
					}
					p.ID = ipamTestPoolID
					pools = []ipPool{p}
					id = p.ID
				case path == sitePoolsPath || path == sitePoolsPath+"/"+ipamTestReservationID:
					var p ipReservation
					if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
						t.Error(err)
					}
					p.ID = ipamTestReservationID
					reservations = []ipReservation{p}
					id = p.ID
				default:
					t.Error("unexpected mutation", r.Method, path)
					w.WriteHeader(400)
					return
				}
				tid := fmt.Sprintf("task-%d", writes)
				now := time.Now().UnixMilli()
				tasks[tid] = map[string]any{"id": tid, "startTime": now, "endTime": now, "isError": false, "serviceType": "NCIP", "progress": id}
				json.NewEncoder(w).Encode(map[string]any{"response": Task{ID: tid}})
			})
			recovered := map[string]bool{}
			legacyRestored := false
			releaseChecked := false
			tick := func() {
				t.Helper()
				restarted := *a
				if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
					return
				}
				op := nc.Status.Configuration.Operation
				if op.Phase == "Accepted" && !recovered[op.ID] {
					recovered[op.ID] = true
					if legacy && !legacyRestored {
						op.Resource = ipamPoolIdentity("parent")
						legacyRestored = true
					} else if ipamOwnedSelected(op.Resource) {
						op.Phase = "Unknown"
					}
					raw, _ := json.Marshal(nc.Status.Configuration)
					if err := json.Unmarshal(raw, &nc.Status.Configuration); err != nil {
						t.Fatal(err)
					}
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
				}
				if op.Phase == "Verified" && ipamOwnedSelected(op.Resource) && !releaseChecked {
					original := nc.DeepCopy()
					nc.Status.Configuration.ActionApplications = nil
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
					if err := a.finishConfiguration(ctx, op.ID); err == nil {
						t.Fatal("schema-pruned result allowed release")
					}
					if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), nc); err != nil {
						t.Fatal(err)
					}
					nc.Status.Configuration = original.Status.Configuration
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						t.Fatal(err)
					}
					releaseChecked = true
				}
			}
			settle := func(expected int) {
				t.Helper()
				for range 60 {
					tick()
					if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
						break
					}
				}
				if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || writes != expected {
					t.Fatal("public IPAM did not converge", writes, config.Status.Conditions)
				}
				for range 3 {
					tick()
				}
				if writes != expected || journalState(t, a).Operation != nil {
					t.Fatal("IPAM replayed")
				}
			}
			initialWrites := 2
			if mixed {
				initialWrites++
			}
			settle(initialWrites)
			state := journalState(t, a)
			wantReceipts := 2
			if legacy {
				wantReceipts = 1
			}
			if mixed {
				wantReceipts++
			}
			if len(state.ActionApplications) != wantReceipts || len(recovered) != initialWrites || !releaseChecked {
				t.Fatal("missing atomic ownership/recovery", len(state.ActionApplications), len(recovered), releaseChecked)
			}
			for _, r := range state.ActionApplications {
				if !ipamOwnedSelected(r.Operation.Resource) {
					continue
				}
				if _, err := ipamReceiptTarget(r); err != nil {
					t.Fatal(err)
				}
			}
			// An owned reservation update replaces its receipt without accumulating history.
			old := state.DeepCopy()
			config.Generation++
			config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"gateway":"192.0.2.129"`, `"gateway":"192.0.2.130"`, 1))
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			settle(initialWrites + 1)
			state = journalState(t, a)
			if len(state.ActionApplications) != wantReceipts || reflect.DeepEqual(old.ActionApplications, state.ActionApplications) {
				t.Fatal("update did not replace result receipt")
			}
			if mixed {
				before, after := 0, 0
				for _, r := range old.ActionApplications {
					if !ipamOwnedSelected(r.Operation.Resource) {
						before++
						for _, current := range state.ActionApplications {
							if reflect.DeepEqual(r, current) {
								after++
							}
						}
					}
				}
				if before != 1 || after != 1 {
					t.Fatal("IPAM callback altered metadata credential receipt")
				}
			}
			// Same-name native replacement is refused before another mutation, even no-op.
			reservations[0].ID = "33333333-3333-4333-8333-333333333333"
			for range 3 {
				tick()
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync || writes != initialWrites+1 {
				t.Fatal("owned UUID replacement adopted")
			}
		})
	}
}

func ipamOwnershipReceiptFixture() cvk.NetworkControllerConfigurationActionApplication {
	submitted := metav1.NewTime(time.Now().Add(-time.Minute))
	verified := metav1.NewTime(time.Now().Add(-time.Second))
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "sites", ConfigUID: "config", ConfigGeneration: 1, ControllerGeneration: 1, Section: "network_settings", Resource: ipamOwnedResource(ipamPoolIdentity("parent")), Action: "Create", Phase: "Verified", TaskID: "task", SubmittedAt: &submitted}
	return cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: ipamResultKey(op.Resource, ipamTestPoolID), Operation: op, VerifiedAt: &verified}
}
func TestIPAMOwnedReceiptAndCapacity(t *testing.T) {
	r := ipamOwnershipReceiptFixture()
	if id, err := ipamReceiptTarget(r); err != nil || id != ipamTestPoolID {
		t.Fatal("fixture", id, err)
	}
	for _, mode := range []string{"foreign-owner", "duplicate", "replaced", "missing", "renamed", "pruned-result", "pruned-protocol"} {
		t.Run(mode, func(t *testing.T) {
			intent, err := parseNetworkReportIntent([]byte(poolModel))
			if err != nil {
				t.Fatal(err)
			}
			receipt := r.DeepCopy()
			pools := fixturePools()
			pools[0].ID = ipamTestPoolID
			e := &ipamConfigurationExecutor{owner: "config", intent: intent, receipts: []cvk.NetworkControllerConfigurationActionApplication{*receipt}}
			switch mode {
			case "foreign-owner":
				e.owner = "foreign"
			case "duplicate":
				e.receipts = append(e.receipts, *receipt)
			case "replaced":
				pools[0].ID = ipamTestReservationID
			case "missing":
				pools = nil
			case "renamed":
				pools[0].Name = "other"
			case "pruned-result":
				e.receipts[0].Resource = ipamResultKey(r.Operation.Resource, "")
			case "pruned-protocol":
				e.receipts[0].Operation.Resource = ipamPoolIdentity("parent")
			}
			if err := e.checkOwnership(pools, nil); err == nil {
				t.Fatal("invalid resource ownership accepted")
			}
		})
	}
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
	op := r.Operation.DeepCopy()
	op.Phase = "Prepared"
	op.Action = "Update"
	op.TargetID = ipamTestPoolID
	if err := ipamOwnedCapacity(state, op); err != nil {
		t.Fatal("owned update capacity", err)
	}
	empty := &cvk.NetworkControllerConfigurationStatus{}
	if err := ipamOwnedCapacity(empty, op); err == nil {
		t.Fatal("update without lineage accepted")
	}
	op.Action = "Create"
	op.TargetID = ""
	if err := ipamOwnedCapacity(state, op); err == nil {
		t.Fatal("repeat Create accepted")
	}
	if err := ipamOwnedCapacity(empty, op); err != nil {
		t.Fatal(err)
	}
	full := &cvk.NetworkControllerConfigurationStatus{}
	for range 256 {
		full.ActionApplications = append(full.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: "unrelated"})
	}
	if err := ipamOwnedCapacity(full, op); err == nil {
		t.Fatal("unreserved receipt capacity")
	}
	bytesFull := &cvk.NetworkControllerConfigurationStatus{}
	for range 180 {
		other := *r.DeepCopy()
		other.Section = "sites"
		other.Resource = strings.Repeat("s", 512)
		other.Operation.Resource = strings.Repeat("r", 512)
		other.Operation.ConfigName = strings.Repeat("n", 253)
		other.Operation.ConfigUID = strings.Repeat("u", 128)
		other.Operation.SourceVersion = strings.Repeat("v", 128)
		other.Operation.SourceUID = strings.Repeat("i", 128)
		bytesFull.ActionApplications = append(bytesFull.ActionApplications, other)
	}
	if err := ipamOwnedCapacity(bytesFull, op); err == nil {
		t.Fatal("unreserved serialized receipt byte capacity")
	}

}

func TestIPAMTaskUUIDBindingRefusesOpaqueOrForeignProgress(t *testing.T) {
	now := time.Now()
	r := ipamOwnershipReceiptFixture()
	op := r.Operation.DeepCopy()
	op.SubmittedAt = &metav1.Time{Time: now.Add(-time.Second)}
	for _, mode := range []string{"valid", "prose", "json", "wrong-service", "wrong-task", "failed", "running", "stale", "future", "bad-uuid"} {
		t.Run(mode, func(t *testing.T) {
			body := map[string]any{"id": op.TaskID, "startTime": now.UnixMilli(), "endTime": now.UnixMilli(), "isError": false, "serviceType": "NCIP", "progress": ipamTestPoolID}
			switch mode {
			case "prose":
				body["progress"] = "created " + ipamTestPoolID
			case "json":
				body["progress"] = `{"id":"` + ipamTestPoolID + `"}`
			case "wrong-service":
				body["serviceType"] = "SWIM"
			case "wrong-task":
				body["id"] = "foreign"
			case "failed":
				body["isError"] = true
			case "running":
				delete(body, "endTime")
			case "stale":
				body["startTime"] = now.Add(-time.Hour).UnixMilli()
			case "future":
				body["endTime"] = now.Add(time.Hour).UnixMilli()
			case "bad-uuid":
				body["progress"] = "not-a-uuid"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": body})
			})
			id, done, err := c.ipamTaskResult(context.Background(), op)
			if mode == "valid" {
				if err != nil || !done || id != ipamTestPoolID {
					t.Fatal(id, done, err)
				}
			} else if mode == "running" {
				if err != nil || done || id != "" {
					t.Fatal(id, done, err)
				}
			} else if err == nil || done || id != "" {
				t.Fatal("untrusted progress accepted", id, done, err)
			}
		})
	}
}

func TestIPAMOwnedCreateRequiresTaskUUIDAndBothFamilyBindings(t *testing.T) {
	for _, mode := range []string{"valid", "same-name-other-id", "wrong-task-target", "wrong-site", "wrong-parent", "missing-v6"} {
		t.Run(mode, func(t *testing.T) {
			var model map[string]any
			if err := json.Unmarshal([]byte(poolModel), &model); err != nil {
				t.Fatal(err)
			}
			root := model["catalyst_center"].(map[string]any)
			network := root["network_settings"].(map[string]any)
			network["ip_pools"] = append(network["ip_pools"].([]any), map[string]any{"name": "parent6", "type": "Generic", "ip_address_space": "IPv6", "ip_pool_cidr": "2001:db8::/48", "ip_pools_reservations": []any{map[string]any{"name": "access", "type": "LAN", "subnet": "2001:db8:0:1::", "prefix_length": 64}}})
			raw, _ := json.Marshal(model)
			intent, err := parseNetworkReportIntent(raw)
			if err != nil {
				t.Fatal(err)
			}
			pools := fixturePools()
			pools[0].ID = ipamTestPoolID
			v6 := *intent.pools[1].Space
			pool6ID := "44444444-4444-4444-8444-444444444444"
			pools = append(pools, ipPool{ID: pool6ID, Name: "parent6", Type: "Generic", Space: &v6})
			reservation := intent.reservations[0].ipReservation
			reservation.ID = ipamTestReservationID
			reservation.SiteID = "area"
			v4 := *reservation.V4
			v4.GlobalPoolID = ipamTestPoolID
			reservation.V4 = &v4
			v6child := *reservation.V6
			v6child.GlobalPoolID = pool6ID
			reservation.V6 = &v6child
			resultID := ipamTestReservationID
			switch mode {
			case "same-name-other-id":
				reservation.ID = "55555555-5555-4555-8555-555555555555"
			case "wrong-task-target":
				resultID = ipamTestPoolID
			case "wrong-site":
				reservation.SiteID = "elsewhere"
			case "wrong-parent":
				reservation.V6.GlobalPoolID = ipamTestPoolID
			case "missing-v6":
				reservation.V6 = nil
			}
			r := ipamOwnershipReceiptFixture()
			op := r.Operation.DeepCopy()
			op.Resource = ipamOwnedResource(ipamReservationIdentity("Global/Lab", "access"))
			op.ParentID = "area"
			op.Phase = "Verifying"
			stamp := op.SubmittedAt.UnixMilli()
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				var rows any
				switch r.URL.Path {
				case globalPoolsPath:
					rows = pools
				case sitePoolsPath:
					rows = []ipReservation{reservation}
				case sitesPath:
					rows = sampleSites()
				case taskPath + op.TaskID:
					rows = map[string]any{"id": op.TaskID, "startTime": stamp, "endTime": stamp, "isError": false, "serviceType": "NCIP", "progress": resultID}
				default:
					t.Error("unexpected read", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			e := &ipamConfigurationExecutor{client: c, owner: "config", intent: intent}
			id, done, err := e.verifyOwned(context.Background(), op)
			if mode == "valid" {
				if err != nil || !done || id != ipamTestReservationID {
					t.Fatal(id, done, err)
				}
			} else if done {
				t.Fatal("unbound native result accepted", id, err)
			}
		})
	}
}

func TestIPAMOwnedVerifiedReleaseRejectsAmbiguousReceipts(t *testing.T) {
	r := ipamOwnershipReceiptFixture()
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
	if !ipamOwnedReceiptPresent(state, &r.Operation) {
		t.Fatal("valid result missing")
	}
	for _, mode := range []string{"missing", "same-id-other-key", "duplicate-key-other-op"} {
		t.Run(mode, func(t *testing.T) {
			current := state.DeepCopy()
			switch mode {
			case "missing":
				current.ActionApplications = nil
			case "same-id-other-key":
				other := *r.DeepCopy()
				other.Resource = "another-result"
				current.ActionApplications = append(current.ActionApplications, other)
			case "duplicate-key-other-op":
				other := *r.DeepCopy()
				other.Operation.ID = strings.Repeat("b", 32)
				current.ActionApplications = append(current.ActionApplications, other)
			}
			if ipamOwnedReceiptPresent(current, &r.Operation) {
				t.Fatal("ambiguous/pruned receipt released")
			}
		})
	}
}

func TestIPAMOwnedReservationRetainsOriginalSiteUUID(t *testing.T) {
	r := ipamOwnershipReceiptFixture()
	r.Operation.Resource = ipamOwnedResource(ipamReservationIdentity("Global/Lab", "access"))
	r.Operation.ParentID = "area"
	r.Resource = ipamResultKey(r.Operation.Resource, ipamTestReservationID)
	intent, err := parseNetworkReportIntent([]byte(poolModel))
	if err != nil {
		t.Fatal(err)
	}
	e := &ipamConfigurationExecutor{owner: "config", intent: intent, receipts: []cvk.NetworkControllerConfigurationActionApplication{r}}
	rows := fixtureReservations()
	rows[0].ID = ipamTestReservationID
	if err := e.checkOwnership(nil, rows); err != nil {
		t.Fatal("stable site", err)
	}
	rows[0].SiteID = "replacement-area"
	if err := e.checkOwnership(nil, rows); err == nil {
		t.Fatal("same-name site replacement adopted")
	}
	op := r.Operation.DeepCopy()
	op.Action = "Update"
	op.Phase = "Prepared"
	op.TargetID = ipamTestReservationID
	op.ParentID = "replacement-area"
	if err := ipamOwnedCapacity(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, op); err == nil {
		t.Fatal("site-changed update reserved capacity")
	}
}
