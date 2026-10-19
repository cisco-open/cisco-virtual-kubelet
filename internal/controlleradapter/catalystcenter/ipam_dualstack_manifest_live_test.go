// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestLiveIPAMDualStackManifest(t *testing.T) {
	if os.Getenv("CATC_IPAM_DUALSTACK_MANIFEST") != "isolated-hierarchy" {
		t.Skip("explicit isolated dual-stack qualification not enabled")
	}
	t.Setenv("CATC_SITE_EXECUTION", "isolated-hierarchy")
	qualifyLiveSiteExecution(t, false)
}

func TestLiveIPAMOwnedPruneManifest(t *testing.T) {
	if os.Getenv("CATC_IPAM_MANIFEST_PRUNE") != "isolated-hierarchy" {
		t.Skip("explicit owned IPAM prune qualification not enabled")
	}
	t.Setenv("CATC_IPAM_DUALSTACK_MANIFEST", "isolated-hierarchy")
	t.Setenv("CATC_SITE_EXECUTION", "isolated-hierarchy")
	qualifyLiveSiteExecution(t, false)
}

func TestLiveIPAMDefaultEmptyPruneManifest(t *testing.T) {
	if os.Getenv("CATC_IPAM_MANIFEST_DEFAULT_EMPTY") != "isolated-hierarchy" {
		t.Skip("explicit default-empty IPAM prune qualification not enabled")
	}
	t.Setenv("CATC_IPAM_MANIFEST_PRUNE", "isolated-hierarchy")
	t.Setenv("CATC_IPAM_DUALSTACK_MANIFEST", "isolated-hierarchy")
	t.Setenv("CATC_SITE_EXECUTION", "isolated-hierarchy")
	qualifyLiveSiteExecution(t, false)
}

type ipamQualificationTrace struct {
	base                           http.RoundTripper
	names                          map[string]bool
	ids                            map[string]string
	site                           string
	attempts, posts, puts, deletes int
	cleanup                        bool
	maxPosts, maxDeletes           int
}

func (g *ipamQualificationTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == "GET" || req.URL.Path == authPath {
		return g.base.RoundTrip(req)
	}
	g.attempts++
	path := req.URL.Path
	prefix := ""
	for _, p := range []string{globalPoolsPath, sitePoolsPath} {
		if path == p || strings.HasPrefix(path, p+"/") {
			prefix = p
			break
		}
	}
	if prefix == "" {
		return nil, errors.New("IPAM qualification refused unrelated mutation")
	}
	if req.Method == "DELETE" {
		id := strings.TrimPrefix(path, prefix+"/")
		if !g.cleanup || g.deletes >= g.maxDeletes || !validAPIID(id) || g.ids[prefix+"/"+id] == "" {
			return nil, errors.New("IPAM qualification refused unowned cleanup")
		}
		// Consume the exact cleanup target before dispatch, including lost responses.
		delete(g.ids, prefix+"/"+id)
		g.deletes++
		return g.base.RoundTrip(req)
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil {
		return nil, errors.New("IPAM qualification invalid payload")
	}
	name, _ := body["name"].(string)
	if !g.names[name] || g.cleanup || prefix == sitePoolsPath && body["siteId"] != g.site {
		return nil, errors.New("IPAM qualification refused foreign selector")
	}
	switch req.Method {
	case "POST":
		if path != prefix || g.posts >= g.maxPosts {
			return nil, errors.New("IPAM qualification duplicate create")
		}
		g.posts++
	case "PUT":
		if g.ids[path] != name || g.puts >= 3 {
			return nil, errors.New("IPAM qualification refused foreign update")
		}
		g.puts++
	default:
		return nil, errors.New("IPAM qualification refused unsupported method")
	}
	return g.base.RoundTrip(req)
}

func qualifyLiveIPAMDualStackManifest(t *testing.T, c *client, ctx context.Context, root string, wait func(context.Context, Task, time.Time) error, settled *bool) {
	t.Helper()
	// Keep the outer hierarchy cleanup fenced until every IPAM fixture is absent.
	*settled = false
	name := lastSiteName(root)
	hash := sha256.Sum256([]byte(name))
	v4 := fmt.Sprintf("198.18.%d", hash[0])
	v6 := fmt.Sprintf("2001:db8:%x", uint16(hash[1])<<8|uint16(hash[2]))
	subnetName := name + "-dual"
	pool4 := map[string]any{"name": name + "-v4", "type": "Generic", "ip_address_space": "IPv4", "ip_pool_cidr": v4 + ".0/24", "dhcp_servers": []string{}, "dns_servers": []string{v4 + ".53"}, "ip_pools_reservations": []any{map[string]any{"name": subnetName, "type": "LAN", "subnet": v4 + ".128", "prefix_length": 25, "gateway": v4 + ".129"}}}
	child6 := map[string]any{"name": subnetName, "type": "LAN", "subnet": v6 + ":1::", "prefix_length": 64, "gateway": v6 + ":1::1"}
	pool6 := map[string]any{"name": name + "-v6", "type": "Generic", "ip_address_space": "IPv6", "ip_pool_cidr": v6 + "::/48", "dhcp_servers": []string{}, "dns_servers": []string{v6 + "::53"}, "ip_pools_reservations": []any{child6}}
	model := map[string]any{"catalyst_center": map[string]any{"sites": map[string]any{"areas": []any{map[string]any{"name": name, "parent_name": "Global", "ip_pools_reservations": []string{subnetName}}}}, "network_settings": map[string]any{"ip_pools": []any{pool4, pool6}}}}
	source, _ := json.Marshal(model)
	beforePools, err := c.listIPPools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeReservations, err := c.listIPReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	siteID := ""
	for _, s := range sites {
		if s.NameHierarchy == root && s.Type == "area" {
			siteID = s.ID
		}
	}
	if !validAPIID(siteID) {
		t.Fatal("isolated site missing")
	}
	names := map[string]bool{name + "-v4": true, name + "-v6": true, subnetName: true}
	for _, p := range beforePools {
		if names[p.Name] {
			t.Fatal("generated pool exists")
		}
	}
	for _, r := range beforeReservations {
		if names[r.Name] {
			t.Fatal("generated reservation exists")
		}
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &ipamQualificationTrace{base: base, names: names, ids: map[string]string{}, site: siteID, maxPosts: 3, maxDeletes: 3}
	pruneNative := os.Getenv("CATC_IPAM_MANIFEST_PRUNE") == "isolated-hierarchy"
	if pruneNative {
		trace.maxPosts = 6
		trace.maxDeletes = 6
	}
	rest.HTTPClient.Transport = trace
	defer func() { rest.HTTPClient.Transport = base }()
	dir, err := os.MkdirTemp("", "cvk-ipam-dualstack-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	save := func(file string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save("baseline-pools.json", beforePools)
	save("baseline-reservations.json", beforeReservations)
	t.Log("isolated dual-stack public manifest and private evidence", root, dir)
	a, config := journalFixture(t)
	a.client = c
	config.Spec.ManagedSections = []string{"network_settings"}
	if pruneNative {
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	}
	sourceInitial := bytes.Clone(source)
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: source}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	captured := map[string]cvk.NetworkControllerConfigurationOperation{}
	recovered := map[string]bool{}
	persist := func() {
		t.Helper()
		save("source.json", json.RawMessage(source))
		save("journal.json", journalState(t, a))
		save("identity.json", map[string]any{"root": root, "siteId": siteID, "names": names, "ids": trace.ids, "posts": trace.posts, "puts": trace.puts, "deletes": trace.deletes})
		save("accepted.json", captured)
	}
	observe := func() ([]ipPool, []ipReservation) {
		t.Helper()
		pools, err := c.listIPPools(ctx)
		if err != nil {
			persist()
			t.Fatal(err)
		}
		reservations, err := c.listIPReservations(ctx)
		if err != nil {
			persist()
			t.Fatal(err)
		}
		return pools, reservations
	}
	pin := func() {
		t.Helper()
		pools, reservations := observe()
		found := 0
		for _, p := range pools {
			if p.Name == name+"-v4" || p.Name == name+"-v6" {
				key := globalPoolsPath + "/" + p.ID
				for old, n := range trace.ids {
					if n == p.Name && old != key {
						t.Fatal("pool UUID changed")
					}
				}
				trace.ids[key] = p.Name
				found++
			}
		}
		for _, r := range reservations {
			if r.Name == subnetName {
				key := sitePoolsPath + "/" + r.ID
				for old, n := range trace.ids {
					if n == r.Name && old != key {
						t.Fatal("reservation UUID changed")
					}
				}
				if r.SiteID != siteID || r.V4 == nil || r.V6 == nil || r.V6.SLAAC == nil || !*r.V6.SLAAC {
					t.Fatal("dual-stack/site/SLAAC readback missing")
				}
				trace.ids[key] = r.Name
				found++
			}
		}
		if found != 3 {
			t.Fatal("dual-stack allocation missing", found)
		}
		persist()
	}
	run := func(expected int, absent ...bool) {
		wantAbsent := len(absent) > 0 && absent[0]
		t.Helper()
		*settled = false
		persist()
		complete := false
		for range 240 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				persist()
				t.Fatal(err)
			}
			state := journalState(t, a)
			if op := state.Operation; op != nil && op.TaskID != "" {
				if op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation || op.Section != "network_settings" {
					persist()
					t.Fatal("unexpected IPAM journal binding")
				}
				if _, ok := captured[op.ID]; !ok {
					captured[op.ID] = *op.DeepCopy()
					t.Log("retained IPAM task", op.Action, op.Resource, op.TaskID)
				}
				// Round-trip accepted evidence before the next fresh reconciler observes it.
				controller, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if op.Phase == "Accepted" && ipamOwnedSelected(op.Resource) && !recovered[op.ID] {
					controller.Status.Configuration.Operation.Phase = "Unknown"
					recovered[op.ID] = true
					t.Log("restored acknowledged IPAM task as Unknown without resubmission", op.ID)
				}
				raw, _ := json.Marshal(controller.Status.Configuration)
				if err := json.Unmarshal(raw, &controller.Status.Configuration); err != nil {
					t.Fatal(err)
				}
				if err := a.statusWriter.Status().Update(ctx, controller); err != nil {
					t.Fatal(err)
				}
			}
			var current configapi.NetworkControllerConfig
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), &current); err != nil {
				t.Fatal(err)
			}
			persist()
			if current.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && state.Operation == nil {
				complete = true
				break
			}
			if current.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
				t.Fatal("manifest failed; retain evidence", current.Status.Conditions)
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Second):
			}
		}
		if !complete || trace.posts+trace.puts != expected {
			persist()
			t.Fatal("manifest mutation/convergence mismatch", complete, trace.posts, trace.puts)
		}
		if !wantAbsent {
			pin()
		} else {
			currentPools, currentReservations := observe()
			for _, p := range currentPools {
				if names[p.Name] {
					t.Fatal("public prune left a generated pool")
				}
			}
			for _, r := range currentReservations {
				if names[r.Name] {
					t.Fatal("public prune left a generated reservation")
				}
			}
			if len(trace.ids) != 0 {
				t.Fatal("public prune did not consume every bound target")
			}
		}
		receipts := journalState(t, a).ActionApplications
		if len(receipts) != 3 {
			t.Fatal("IPAM UUID result receipts missing", len(receipts))
		}
		for _, r := range receipts {
			id, err := ipamReceiptTarget(r)
			if err != nil || id == "" || wantAbsent && r.Operation.Action != "Delete" || !wantAbsent && trace.ids[globalPoolsPath+"/"+id] == "" && trace.ids[sitePoolsPath+"/"+id] == "" {
				t.Fatal("native result receipt identity invalid", err)
			}
		}
		save("result-receipts.json", receipts)
		for range 2 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
		}
		if trace.posts+trace.puts != expected {
			t.Fatal("converged manifest replayed")
		}
	}
	run(3)
	// Update both IPv6 parent DNS and the reservation gateway, retaining UUIDs.
	pool6["dns_servers"] = []string{v6 + "::54"}
	child6["gateway"] = v6 + ":1::2"
	update := func() {
		t.Helper()
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		source, _ = json.Marshal(model)
		config.Generation++
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: source}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	update()
	run(5)
	// Removing IPv6 reservation intent must preserve the already allocated family.
	delete(pool6, "ip_pools_reservations")
	child4 := pool4["ip_pools_reservations"].([]any)[0].(map[string]any)
	child4["dns_servers"] = []string{v4 + ".54"}
	update()
	run(6)
	pools, reservations := observe()
	var allocated *ipReservation
	for i := range reservations {
		if reservations[i].Name == subnetName {
			allocated = &reservations[i]
		}
	}
	if allocated == nil || allocated.V6 == nil || allocated.V6.Gateway == nil || *allocated.V6.Gateway != v6+":1::2" || allocated.V6.Subnet != v6+":1::" || allocated.V6.PrefixLength == nil || *allocated.V6.PrefixLength != 64 {
		t.Fatal("unselected IPv6 family was not preserved")
	}
	poolIDs := map[string]string{}
	for _, p := range pools {
		poolIDs[p.Name] = p.ID
	}
	if allocated.V4.GlobalPoolID != poolIDs[name+"-v4"] || allocated.V6.GlobalPoolID != poolIDs[name+"-v6"] {
		t.Fatal("dual-stack parent UUID binding lost")
	}
	save("verified-reservation.json", allocated)
	if len(captured) != 6 || len(recovered) != 6 {
		t.Fatal("missing persisted operation evidence", len(captured))
	}
	t.Log("public dual-stack create, IPv6 updates, omitted-family preservation, restart and no-op verified", poolIDs[name+"-v4"], poolIDs[name+"-v6"], allocated.ID)
	if pruneNative {
		save("before-prune-receipts.json", journalState(t, a).ActionApplications)
		emptyModel := map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"ip_pools": []any{}}}}
		defaultEmpty := os.Getenv("CATC_IPAM_MANIFEST_DEFAULT_EMPTY") == "isolated-hierarchy"
		if defaultEmpty {
			emptyModel = map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{}}}
		}
		model = emptyModel
		trace.cleanup = true
		update()
		run(6, true)
		if trace.deletes != 3 {
			t.Fatal("first public prune mutation count")
		}
		save("first-prune-receipts.json", journalState(t, a).ActionApplications)
		t.Log("manifest-driven owned prune reached verified tombstones", config.Generation)
		// A later explicit revision may create the same logical allocations again.
		// Its task proof establishes the new creation, even on an appliance that reuses IDs.
		model = nil
		if err := json.Unmarshal(sourceInitial, &model); err != nil {
			t.Fatal(err)
		}
		trace.cleanup = false
		update()
		run(9)
		save("reintroduced-receipts.json", journalState(t, a).ActionApplications)
		for _, r := range journalState(t, a).ActionApplications {
			if r.Operation.Action != "Create" || r.Operation.ConfigGeneration != config.Generation {
				t.Fatal("reintroduction lacks fresh creation proof")
			}
		}
		t.Log("explicit reintroduction retained fresh task-correlated native UUID receipts", config.Generation)
		model = emptyModel
		trace.cleanup = true
		update()
		run(9, true)
		pools, reservations = observe()
		if !reflect.DeepEqual(beforePools, pools) || !reflect.DeepEqual(beforeReservations, reservations) || trace.posts != 6 || trace.puts != 3 || trace.deletes != 6 || trace.attempts != 15 || len(captured) != 15 || len(recovered) != 15 {
			t.Fatal("public prune/reintroduction baseline, task or mutation count mismatch")
		}
		*settled = true
		save("complete.json", map[string]any{"cleaned": true, "publicPrune": true, "defaultEmpty": defaultEmpty, "posts": trace.posts, "puts": trace.puts, "deletes": trace.deletes, "knownTaskRecoveries": len(recovered)})
		t.Log("public owned prune/reintroduction/prune completed; all 15 acknowledged tasks recovered without replay and original IPAM baseline restored")
		return
	}
	// Only exact generated UUIDs can be removed, reservation before parent pools.
	trace.cleanup = true
	for _, key := range []string{sitePoolsPath + "/" + allocated.ID, globalPoolsPath + "/" + poolIDs[name+"-v6"], globalPoolsPath + "/" + poolIDs[name+"-v4"]} {
		pools, reservations = observe()
		found := false
		for _, p := range pools {
			if key == globalPoolsPath+"/"+p.ID {
				if p.Name != trace.ids[key] {
					t.Fatal("pool cleanup name changed")
				}
				found = true
			}
		}
		for _, r := range reservations {
			if key == sitePoolsPath+"/"+r.ID {
				if r.Name != trace.ids[key] || r.SiteID != siteID {
					t.Fatal("reservation cleanup identity changed")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("cleanup target absent before first attempt")
		}
		if strings.HasPrefix(key, globalPoolsPath+"/") {
			id := strings.TrimPrefix(key, globalPoolsPath+"/")
			for _, r := range reservations {
				if r.V4 != nil && r.V4.GlobalPoolID == id || r.V6 != nil && r.V6.GlobalPoolID == id {
					t.Fatal("parent still referenced")
				}
			}
		}
		*settled = false
		// This private write-ahead marker retains an uncertain cleanup for inspection.
		save("cleanup-pending.json", map[string]any{"path": key, "submittedAt": time.Now().UTC()})
		submitted := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, key, nil)
		if err != nil {
			persist()
			t.Fatal("cleanup uncertain; no replay", err)
		}
		var result struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &result) != nil || !validAPIID(result.Response.ID) {
			persist()
			t.Fatal("cleanup task unavailable")
		}
		save("cleanup-pending.json", map[string]any{"path": key, "taskId": result.Response.ID, "submittedAt": submitted})
		if err := wait(ctx, result.Response, submitted); err != nil {
			persist()
			t.Fatal(err)
		}
		pools, reservations = observe()
		for _, p := range pools {
			if key == globalPoolsPath+"/"+p.ID {
				t.Fatal("pool remains")
			}
		}
		for _, r := range reservations {
			if key == sitePoolsPath+"/"+r.ID {
				t.Fatal("reservation remains")
			}
		}
		persist()
		t.Log("exact generated IPAM cleanup verified", key, result.Response.ID)
	}
	pools, reservations = observe()
	if !reflect.DeepEqual(beforePools, pools) || !reflect.DeepEqual(beforeReservations, reservations) || trace.posts != 3 || trace.puts != 3 || trace.deletes != 3 || trace.attempts != 9 {
		t.Fatal("IPAM baseline or mutation count mismatch")
	}
	*settled = true
	save("complete.json", map[string]any{"cleaned": true, "posts": trace.posts, "puts": trace.puts, "deletes": trace.deletes})
	t.Log("original IPAM baseline restored; no device assignment or provisioning requested")
}
