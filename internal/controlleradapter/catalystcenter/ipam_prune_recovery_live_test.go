// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
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

type ipamRecoverySiteProof struct {
	Kind        string    `json:"kind"`
	Path        string    `json:"path"`
	ID          string    `json:"id"`
	TaskID      string    `json:"taskID"`
	SubmittedAt time.Time `json:"submittedAt"`
}
type ipamRecoverySiteTrace struct {
	base     http.RoundTripper
	allowed  map[string]bool
	attempts int
}

func (g *ipamRecoverySiteTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != "GET" && req.URL.Path != authPath {
		g.attempts++
		if req.Method != "DELETE" || !g.allowed[req.URL.Path] {
			return nil, errors.New("retained site cleanup refused unbound mutation")
		}
		delete(g.allowed, req.URL.Path)
	}
	return g.base.RoundTrip(req)
}

// Recover only generated qualification data with verified creation receipts.
// This uses the public prune reconciler; private fake-Kubernetes artifacts are
// not an installed cluster journal, and an uncertain recovery never replays.
func TestLiveRetainedIPAMPublicPruneCleanup(t *testing.T) {
	dir := os.Getenv("CATC_IPAM_PRUNE_RETAINED")
	if dir == "" {
		t.Skip("no retained qualification supplied")
	}
	read := func(name string, value any) {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || json.Unmarshal(raw, value) != nil {
			t.Fatal("retained evidence unavailable", name, err)
		}
	}
	save := func(name string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var identity struct {
		Root   string          `json:"root"`
		SiteID string          `json:"siteId"`
		Names  map[string]bool `json:"names"`
	}
	var state cvk.NetworkControllerConfigurationStatus
	var proofs []ipamRecoverySiteProof
	var beforePools []ipPool
	var beforeReservations []ipReservation
	read("identity.json", &identity)
	read("journal.json", &state)
	read("recovery-site-proof.json", &proofs)
	read("baseline-pools.json", &beforePools)
	read("baseline-reservations.json", &beforeReservations)
	raw, err := os.ReadFile(filepath.Join(dir, "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := parseNetworkReportIntent(raw)
	if err != nil || !strings.HasPrefix(identity.Root, "Global/CVK-Q-") || !canonicalIPAMUUID(identity.SiteID) || len(proofs) != 3 || len(state.ActionApplications) != 3 || state.Operation != nil || len(intent.pools) != 2 || len(intent.reservations) != 1 || intent.reservations[0].site != identity.Root {
		t.Fatal("retained qualification scope refused", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c := liveClient(t)
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &ipamQualificationTrace{base: base, names: identity.Names, ids: map[string]string{}, site: identity.SiteID, cleanup: true, maxPosts: 0, maxDeletes: 3}
	rest.HTTPClient.Transport = trace
	defer func() { rest.HTTPClient.Transport = base }()
	assertAbsent := func() {
		t.Helper()
		pools, err := c.listIPPools(ctx)
		if err != nil {
			t.Fatal(err)
		}
		reservations, err := c.listIPReservations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pools, beforePools) || !reflect.DeepEqual(reservations, beforeReservations) {
			t.Fatal("original IPAM baseline not restored")
		}
		sites, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, site := range sites {
			if site.NameHierarchy == identity.Root || strings.HasPrefix(site.NameHierarchy, identity.Root+"/") {
				t.Fatal("generated hierarchy still present")
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "recovery-complete.json")); err == nil {
		var complete struct {
			Cleaned bool `json:"cleaned"`
		}
		read("recovery-complete.json", &complete)
		if !complete.Cleaned {
			t.Fatal("uncertain completion")
		}
		assertAbsent()
		if trace.attempts != 0 {
			t.Fatal("completed recovery mutated")
		}
		t.Log("read-only retained cleanup resume verified; zero mutations")
		return
	}
	if _, err := os.Stat(filepath.Join(dir, "recovery-start.json")); err == nil || !os.IsNotExist(err) {
		t.Fatal("prior recovery uncertain; no replay")
	}
	// Reverify the exact native creation tasks and fields before claiming cleanup.
	e := &ipamConfigurationExecutor{client: c, owner: "config", intent: intent, receipts: state.ActionApplications}
	for _, receipt := range state.ActionApplications {
		id, err := ipamReceiptTarget(receipt)
		if err != nil || receipt.Operation.Action != "Create" || receipt.Operation.ConfigUID != "config" || receipt.Operation.ConfigName != "sites" || receipt.Operation.ConfigGeneration != 5 {
			t.Fatal("retained creation proof refused", err)
		}
		observed, done, err := e.verifyOwned(ctx, &receipt.Operation)
		if err != nil || !done || observed != id {
			t.Fatal("native creation UUID/fields not verified", err)
		}
		path := globalPoolsPath
		if receipt.Operation.ParentID != "" {
			path = sitePoolsPath
			if receipt.Operation.ParentID != identity.SiteID {
				t.Fatal("foreign reservation site")
			}
		}
		trace.ids[path+"/"+id] = lastSiteName(receipt.Resource) // A nonempty bound value authorizes only this exact UUID.
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectedPaths := []string{identity.Root, identity.Root + "/TestBuilding", identity.Root + "/TestBuilding/TestFloor"}
	for i, proof := range proofs {
		if proof.Kind != []string{"area", "building", "floor"}[i] || proof.Path != expectedPaths[i] || !canonicalIPAMUUID(proof.ID) || !validAPIID(proof.TaskID) || proof.SubmittedAt.IsZero() {
			t.Fatal("site origin proof malformed")
		}
		taskRaw, err := c.GetTask(ctx, proof.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		done, err := decodeConfigurationTaskComplete(taskRaw, proof.TaskID, proof.SubmittedAt, time.Now())
		if err != nil || !done {
			t.Fatal("site creation task not complete", err)
		}
		var task struct {
			Response struct {
				Data string `json:"data"`
			} `json:"response"`
		}
		if json.Unmarshal(taskRaw, &task) != nil || task.Response.Data != proof.ID {
			t.Fatal("site UUID not task-correlated")
		}
		found := false
		for _, site := range sites {
			if site.ID == proof.ID {
				if site.NameHierarchy != proof.Path || site.Type != proof.Kind {
					t.Fatal("site identity changed")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("created site missing before recovery")
		}
	}
	if proofs[0].ID != identity.SiteID {
		t.Fatal("root site receipt changed")
	}
	if err := claimQualificationRecord(filepath.Join(dir, "recovery-start.json"), map[string]any{"root": identity.Root, "siteId": identity.SiteID, "startedAt": time.Now().UTC(), "configGeneration": 7}); err != nil {
		t.Fatal(err)
	}
	a, config := journalFixture(t)
	a.client = c
	config.Generation = 7
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: []byte(ipamEmptyModel)}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = state.DeepCopy()
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	recovered := map[string]bool{}
	complete := false
	for range 180 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if op := current.Status.Configuration.Operation; op != nil && op.Phase == "Accepted" && !recovered[op.ID] {
			op.Phase = "Unknown"
			recovered[op.ID] = true
			if err := a.statusWriter.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
		}
		save("recovery-journal.json", journalState(t, a))
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && journalState(t, a).Operation == nil {
			complete = true
			break
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
			t.Fatal("public retained prune failed", config.Status.Conditions)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if !complete || trace.deletes != 3 || trace.attempts != 3 || len(recovered) != 3 {
		t.Fatal("retained public prune did not converge", trace.deletes, trace.attempts, len(recovered))
	}
	for _, receipt := range journalState(t, a).ActionApplications {
		if receipt.Operation.Action != "Delete" || receipt.Operation.ConfigGeneration != 7 {
			t.Fatal("retained deletion receipt missing")
		}
	}
	save("recovery-ipam-complete.json", map[string]any{"cleaned": true, "deletes": trace.deletes, "knownTaskRecoveries": len(recovered)})
	// Task-bound hierarchy cleanup follows only after every IPAM allocation is gone.
	siteTrace := &ipamRecoverySiteTrace{base: base, allowed: map[string]bool{}}
	rest.HTTPClient.Transport = siteTrace
	for i := len(proofs) - 1; i >= 0; i-- {
		proof := proofs[i]
		path := map[string]string{"area": "/dna/intent/api/v1/areas/", "building": "/dna/intent/api/v2/buildings/", "floor": "/dna/intent/api/v2/floors/"}[proof.Kind] + proof.ID
		sites, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, site := range sites {
			if site.ID == proof.ID {
				if site.NameHierarchy != proof.Path || site.Type != proof.Kind {
					t.Fatal("hierarchy replaced")
				}
				found = true
			}
			if strings.HasPrefix(site.NameHierarchy, proof.Path+"/") {
				t.Fatal("foreign or remaining child prevents cleanup")
			}
		}
		if !found {
			continue
		}
		submitted := time.Now()
		if err := claimQualificationRecord(filepath.Join(dir, "recovery-site-delete-"+proof.ID+".json"), map[string]any{"id": proof.ID, "path": proof.Path, "submittedAt": submitted}); err != nil {
			t.Fatal("site deletion claimed; no replay", err)
		}
		siteTrace.allowed[path] = true
		response, err := c.mutate(ctx, http.MethodDelete, path, nil)
		if err != nil {
			t.Fatal("site cleanup uncertain", err)
		}
		var task struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(response, &task) != nil || !validAPIID(task.Response.ID) {
			t.Fatal("site cleanup task absent")
		}
		save("recovery-site-delete-"+proof.ID+".json", map[string]any{"id": proof.ID, "path": proof.Path, "taskID": task.Response.ID, "submittedAt": submitted})
		if err := waitSiteCredentialTask(ctx, c, task.Response, submitted); err != nil {
			t.Fatal(err)
		}
		sites, err = c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, site := range sites {
			if site.ID == proof.ID {
				t.Fatal("site deletion not absent")
			}
		}
		t.Log("verified exact task-correlated site cleanup", proof.Kind, proof.ID, task.Response.ID)
	}
	assertAbsent()
	if siteTrace.attempts != 3 {
		t.Fatal("unexpected hierarchy cleanup mutations", siteTrace.attempts)
	}
	save("recovery-complete.json", map[string]any{"cleaned": true, "ipamDeletes": trace.deletes, "siteDeletes": siteTrace.attempts})
	t.Log("retained public prune and exact hierarchy cleanup completed; original IPAM baseline restored", identity.Root)
}
