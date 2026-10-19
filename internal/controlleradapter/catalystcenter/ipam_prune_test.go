// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const ipamEmptyModel = `{"catalyst_center":{"network_settings":{"ip_pools":[]}}}`
const ipamPruneV6ID = "44444444-4444-4444-8444-444444444444"

type ipamPruneWorld struct {
	pools        []ipPool
	reservations []ipReservation
	sites        []Site
	gateways     []map[string]any
	sessions     []lanSession
	writes       []string
	tasks        map[string]map[string]any
	mode         string
}

func ipamPruneFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig, *ipamPruneWorld, []byte) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	var model map[string]any
	_ = json.Unmarshal([]byte(poolModel), &model)
	settings := model["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)
	settings["ip_pools"] = append(settings["ip_pools"].([]any), map[string]any{"name": "parent6", "type": "Generic", "ip_address_space": "IPv6", "ip_pool_cidr": "2001:db8::/48", "ip_pools_reservations": []any{map[string]any{"name": "access", "type": "LAN", "subnet": "2001:db8:0:1::", "prefix_length": 64}}})
	original, _ := json.Marshal(model)
	intent, err := parseNetworkReportIntent(original)
	if err != nil {
		t.Fatal(err)
	}
	world := &ipamPruneWorld{pools: []ipPool{}, reservations: []ipReservation{}, sites: sampleSites(), gateways: []map[string]any{}, sessions: []lanSession{}, tasks: map[string]map[string]any{}}
	for _, p := range intent.pools {
		p.ID = ipamTestPoolID
		if p.Name == "parent6" {
			p.ID = ipamPruneV6ID
		}
		world.pools = append(world.pools, p)
	}
	r := intent.reservations[0].ipReservation
	r.ID = ipamTestReservationID
	r.SiteID = "area"
	r.V4.GlobalPoolID = ipamTestPoolID
	r.V6.GlobalPoolID = ipamPruneV6ID
	world.reservations = append(world.reservations, r)
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Generation = 2
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(ipamEmptyModel)}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "network_settings", ConfigName: config.Name, ConfigUID: string(config.UID)}}}
	appendReceipt := func(legacy, id, parent string) {
		r := ipamOwnershipReceiptFixture()
		r.Operation.Resource = ipamOwnedResource(legacy)
		r.Operation.ParentID = parent
		r.Operation.ConfigName = config.Name
		r.Operation.ConfigUID = string(config.UID)
		r.Operation.ControllerGeneration = nc.Generation
		r.Resource = ipamResultKey(r.Operation.Resource, id)
		nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, r)
	}
	for _, p := range world.pools {
		appendReceipt(ipamPoolIdentity(p.Name), p.ID, "")
	}
	appendReceipt(ipamReservationIdentity("Global/Lab", "access"), r.ID, r.SiteID)
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a.client = testClient(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		reply := func(rows any) { json.NewEncoder(w).Encode(map[string]any{"response": rows}) }
		if req.Method == "GET" {
			switch req.URL.Path {
			case globalPoolsPath:
				reply(world.pools)
			case sitePoolsPath:
				reply(world.reservations)
			case sitesPath:
				reply(world.sites)
			case anycastGatewaysPath:
				reply(world.gateways)
			case lanSessionStatusPath:
				reply(world.sessions)
			default:
				if task, ok := world.tasks[strings.TrimPrefix(req.URL.Path, taskPath)]; ok {
					reply(task)
				} else {
					t.Error("unexpected GET", req.URL.Path)
					w.WriteHeader(404)
				}
			}
			return
		}
		world.writes = append(world.writes, req.Method+" "+req.URL.Path)
		if world.mode == "backend-in-use" {
			w.WriteHeader(409)
			return
		}
		id := ""
		if req.Method == "DELETE" {
			if strings.HasPrefix(req.URL.Path, sitePoolsPath+"/") {
				id = strings.TrimPrefix(req.URL.Path, sitePoolsPath+"/")
				world.reservations = slices.DeleteFunc(world.reservations, func(r ipReservation) bool { return r.ID == id })
			} else if strings.HasPrefix(req.URL.Path, globalPoolsPath+"/") {
				id = strings.TrimPrefix(req.URL.Path, globalPoolsPath+"/")
				for _, r := range world.reservations {
					if r.V4 != nil && r.V4.GlobalPoolID == id || r.V6 != nil && r.V6.GlobalPoolID == id {
						t.Error("parent deleted before child")
						w.WriteHeader(409)
						return
					}
				}
				world.pools = slices.DeleteFunc(world.pools, func(p ipPool) bool { return p.ID == id })
			}
		} else if req.Method == "POST" {
			switch req.URL.Path {
			case globalPoolsPath:
				var p ipPool
				if err := json.NewDecoder(req.Body).Decode(&p); err != nil {
					t.Error(err)
				}
				p.ID = "33333333-3333-4333-8333-333333333333"
				if p.Name == "parent6" {
					p.ID = "55555555-5555-4555-8555-555555555555"
				}
				world.pools = append(world.pools, p)
				id = p.ID
			case sitePoolsPath:
				var r ipReservation
				if err := json.NewDecoder(req.Body).Decode(&r); err != nil {
					t.Error(err)
				}
				r.ID = "66666666-6666-4666-8666-666666666666"
				if r.V4.GlobalPoolID != "33333333-3333-4333-8333-333333333333" || r.V6.GlobalPoolID != "55555555-5555-4555-8555-555555555555" {
					t.Error("recreated reservation used retired parent UUIDs")
				}
				world.reservations = append(world.reservations, r)
				id = r.ID
			}
		}
		if id == "" {
			t.Error("unexpected mutation", req.Method, req.URL.Path)
			w.WriteHeader(400)
			return
		}
		tid := fmt.Sprintf("task-%d", len(world.writes))
		stamp := time.Now().UnixMilli()
		world.tasks[tid] = map[string]any{"id": tid, "startTime": stamp, "endTime": stamp, "isError": false, "serviceType": "NCIP", "progress": id}
		if world.mode == "lost-ack" {
			reply(map[string]any{})
			return
		}
		reply(Task{ID: tid})
	})
	return a, config, world, original
}
func TestIPAMOwnedPrunePublicCycle(t *testing.T) {
	for _, mode := range []string{"prune", "retain", "lost-ack", "backend-in-use", "source-change", "prune-default-empty", "retain-default-empty", "lost-ack-default-empty", "backend-in-use-default-empty", "source-change-default-empty"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, world, original := ipamPruneFixture(t)
			if strings.HasSuffix(mode, "-default-empty") {
				config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"network_settings":{}}}`)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				mode = strings.TrimSuffix(mode, "-default-empty")
			}
			world.mode = mode
			if mode == "retain" {
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			}
			recovered := map[string]bool{}
			changed := false
			tick := func() {
				t.Helper()
				restart := *a
				if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
					t.Fatal(err)
				}
				if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
					op := nc.Status.Configuration.Operation
					if op.Phase == "Accepted" && !recovered[op.ID] {
						recovered[op.ID] = true
						op.Phase = "Unknown"
						raw, _ := json.Marshal(nc.Status.Configuration)
						_ = json.Unmarshal(raw, &nc.Status.Configuration)
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
						if mode == "source-change" && !changed {
							changed = true
							config.Generation++
							config.Spec.Source.Inline.Raw = original
							if err := a.statusWriter.Update(ctx, config); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
			}
			for range 70 {
				tick()
				if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
					break
				}
			}
			state := journalState(t, a)
			if mode == "retain" {
				if len(world.writes) != 0 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || len(state.ActionApplications) != 3 {
					t.Fatal("Retain changed resources", world.writes, config.Status.Conditions)
				}
				return
			}
			if mode != "prune" {
				if len(world.writes) != 1 || state.Operation == nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
					t.Fatal("uncertain/changed-input cleanup replayed", world.writes, config.Status.Conditions)
				}
				for _, r := range state.ActionApplications {
					if r.Operation.Action == "Delete" {
						t.Fatal("unverified cleanup published")
					}
				}
				return
			}
			if len(world.writes) != 3 || len(world.reservations) != 0 || len(world.pools) != 0 || state.Operation != nil || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("prune did not converge", world.writes, config.Status.Conditions)
			}
			if world.writes[0] != "DELETE "+sitePoolsPath+"/"+ipamTestReservationID || len(recovered) != 3 || len(state.ActionApplications) != 3 {
				t.Fatal("prune ordering/recovery/receipt missing", world.writes)
			}
			for _, r := range state.ActionApplications {
				if r.Operation.Action != "Delete" {
					t.Fatal("deletion tombstone missing")
				}
				if _, err := ipamReceiptTarget(r); err != nil {
					t.Fatal(err)
				}
			}
			for range 3 {
				tick()
			}
			if len(world.writes) != 3 {
				t.Fatal("cleanup replayed")
			}
			// A later equivalent empty representation must keep the verified
			// tombstones without replaying deletion or recreating allocations.
			config.Generation++
			if string(config.Spec.Source.Inline.Raw) == ipamEmptyModel {
				config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"network_settings":{}}}`)
			} else {
				config.Spec.Source.Inline.Raw = []byte(ipamEmptyModel)
			}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			for range 30 {
				tick()
				if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
					break
				}
			}
			if len(world.writes) != 3 || len(recovered) != 3 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || config.Status.ObservedGeneration != config.Generation {
				t.Fatal("equivalent empty revision replayed or failed convergence", world.writes, config.Status.Conditions)
			}
			config.Generation++
			config.Spec.Source.Inline.Raw = original
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			for range 70 {
				tick()
				if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
					break
				}
			}
			state = journalState(t, a)
			if len(world.writes) != 6 || len(recovered) != 6 || len(state.ActionApplications) != 3 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
				t.Fatal("explicit reintroduction did not converge", world.writes, config.Status.Conditions)
			}
			for _, r := range state.ActionApplications {
				id, err := ipamReceiptTarget(r)
				if err != nil || r.Operation.Action != "Create" || r.Operation.ConfigGeneration != config.Generation || id == ipamTestPoolID || id == ipamPruneV6ID || id == ipamTestReservationID {
					t.Fatal("reintroduction adopted retired identity", id, err)
				}
			}
		})
	}
}

func TestIPAMDefaultEmptyNeverAdoptsExistingAllocations(t *testing.T) {
	ctx := context.Background()
	a, config, world, _ := ipamPruneFixture(t)
	config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"network_settings":{}}}`)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.ActionApplications = nil
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	for range 12 {
		restart := *a
		if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
	}
	state := journalState(t, a)
	if len(world.writes) != 0 || len(world.pools) != 2 || len(world.reservations) != 1 || state.Operation != nil || len(state.ActionApplications) != 0 || config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || config.Status.ObservedGeneration != config.Generation {
		t.Fatal("empty source adopted or deleted unowned native allocations", world.writes, config.Status.Conditions)
	}
}

func TestIPAMPruneReferenceAndIdentityPreflight(t *testing.T) {
	for _, mode := range []string{"gateway-primary", "gateway-secondary", "gateway-unknown", "gateway-incomplete", "lan-reference", "unowned-child", "site-replaced", "owned-missing", "foreign-config-unrelated"} {
		t.Run(mode, func(t *testing.T) {
			a, config, world, _ := ipamPruneFixture(t)
			gateway := map[string]any{"id": "gateway", "fabricId": "fabric", "virtualNetworkName": "Corp", "ipPoolName": "other", "vlanName": "Corp", "vlanId": 150, "trafficType": "DATA", "additionalIpPools": []any{}}
			switch mode {
			case "gateway-primary":
				gateway["ipPoolName"] = "access"
				world.gateways = append(world.gateways, gateway)
			case "gateway-secondary":
				gateway["additionalIpPools"] = []any{map[string]any{"name": "access", "order": 2}}
				world.gateways = append(world.gateways, gateway)
			case "gateway-unknown":
				gateway["newPoolSelector"] = "unknown"
				world.gateways = append(world.gateways, gateway)
			case "gateway-incomplete":
				delete(gateway, "additionalIpPools")
				world.gateways = append(world.gateways, gateway)
			case "lan-reference":
				session := lanSessionFixture()
				session.Pools[0].Name = "access"
				world.sessions = append(world.sessions, session)
			case "unowned-child":
				bits := 26
				world.reservations = append(world.reservations, ipReservation{ID: "77777777-7777-4777-8777-777777777777", Name: "foreign", SiteID: "area", Type: "LAN", V4: &poolAddressSpace{Subnet: "192.0.2.0", PrefixLength: &bits, GlobalPoolID: ipamTestPoolID}})
			case "site-replaced":
				for i := range world.sites {
					if world.sites[i].ID == "area" {
						world.sites[i].ID = "replacement-area"
					}
					if world.sites[i].ParentID == "area" {
						world.sites[i].ParentID = "replacement-area"
					}
				}
				world.reservations[0].SiteID = "replacement-area"
			case "owned-missing":
				world.reservations = []ipReservation{}
			case "foreign-config-unrelated":
				nc, err := a.currentController(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "wireless", Resource: "foreign", Operation: cvk.NetworkControllerConfigurationOperation{ConfigUID: "another-config"}})
				if err := a.statusWriter.Status().Update(context.Background(), nc); err != nil {
					t.Fatal(err)
				}
			}
			for range 3 {
				noAuthCredentialTick(t, a, config)
			}
			expected := 0
			if mode == "foreign-config-unrelated" {
				expected = 1
			}
			if len(world.writes) != expected {
				t.Fatal("unsafe preflight or unrelated owner blocked", mode, world.writes, config.Status.Conditions)
			}
		})
	}
}

func TestIPAMPruneSubmissionRepeatsCohortAndAuthorityChecks(t *testing.T) {
	for _, mode := range []string{"stable", "gateway-added", "lan-reference-added", "pool-replaced", "pool-fields-changed", "site-replaced", "receipt-pruned", "unrelated-site", "unrelated-gateway", "equivalent-empty-source-revision"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, world, _ := ipamPruneFixture(t)
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := a.compileNetworkSettingsApply(nc, config, source)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.bindIPAMOwnership(nc, config, source, &plan); err != nil {
				t.Fatal(err)
			}
			write, err := plan.executor.next(ctx)
			if err != nil || write == nil || write.action != "Delete" {
				t.Fatal("prune plan", err)
			}
			op, err := a.prepareConfiguration(ctx, config, source, plan.section, write.resource, write.action, write.identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, source, op.ID, write.identity); err != nil {
				t.Fatal(err)
			}
			gateway := map[string]any{"id": "gateway", "fabricId": "fabric", "virtualNetworkName": "Corp", "ipPoolName": "access", "vlanName": "Corp", "vlanId": 150, "trafficType": "DATA", "additionalIpPools": []any{}}
			switch mode {
			case "equivalent-empty-source-revision":
				// Semantic equivalence does not authorize a prepared operation to
				// cross the existing immutable source-revision fence.
				config.Generation++
				config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"network_settings":{}}}`)
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
			case "gateway-added":
				world.gateways = append(world.gateways, gateway)
			case "lan-reference-added":
				session := lanSessionFixture()
				session.Pools[0].Name = "access"
				world.sessions = append(world.sessions, session)
			case "pool-replaced":
				world.pools[0].ID = "77777777-7777-4777-8777-777777777777"
				world.reservations[0].V4.GlobalPoolID = world.pools[0].ID
			case "pool-fields-changed":
				dns := []string{"192.0.2.54"}
				world.pools[0].Space.DNS = &dns
			case "site-replaced":
				for i := range world.sites {
					if world.sites[i].ID == "area" {
						world.sites[i].ID = "replacement-area"
					}
					if world.sites[i].ParentID == "area" {
						world.sites[i].ParentID = "replacement-area"
					}
				}
				world.reservations[0].SiteID = "replacement-area"
			case "receipt-pruned":
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Status.Configuration.ActionApplications = nil
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "unrelated-site":
				world.sites = append(world.sites, Site{ID: "other-area", Name: "Other", NameHierarchy: "Global/Other", ParentID: "root", Type: "area"})
			case "unrelated-gateway":
				gateway["ipPoolName"] = "Other"
				world.gateways = append(world.gateways, gateway)
			}
			_, err = write.submit(ctx)
			allowed := mode == "stable" || strings.HasPrefix(mode, "unrelated-")
			expected := 0
			if allowed {
				expected = 1
			}
			if (err == nil) != allowed || len(world.writes) != expected {
				t.Fatal("stale/foreign plan reached native mutation or unrelated churn blocked it", world.writes, err)
			}
			if _, err := write.submit(ctx); err == nil || len(world.writes) != expected {
				t.Fatal("prune plan replayed")
			}
		})
	}
}

func TestIPAMPruneCompilerRefusesUnimplementedCleanupScope(t *testing.T) {
	for _, mode := range []string{"credentials", "settings", "foreign-section", "own-other-receipt", "own-credential-receipt", "null-pools"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, _ := ipamPruneFixture(t)
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "credentials":
				source.raw = []byte(`{"catalyst_center":{"network_settings":{"ip_pools":[],"device_credentials":{}}}}`)
			case "settings":
				source.raw = []byte(`{"catalyst_center":{"network_settings":{"ip_pools":[],"ntp":{}}}}`)
			case "foreign-section":
				config.Spec.ManagedSections = []string{"network_settings", "fabric"}
			case "own-other-receipt":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Resource: "other", Operation: cvk.NetworkControllerConfigurationOperation{ConfigUID: string(config.UID)}})
			case "own-credential-receipt":
				nc.Status.Configuration.CredentialApplications = []cvk.NetworkControllerConfigurationCredentialApplication{{ConfigUID: string(config.UID)}}
			case "null-pools":
				source.raw = []byte(`{"catalyst_center":{"network_settings":{"ip_pools":null}}}`)
			}
			if _, err := a.compileIPAMPruneApply(nc, config, source); err == nil {
				t.Fatal("unimplemented cleanup scope accepted")
			}
		})
	}
}
