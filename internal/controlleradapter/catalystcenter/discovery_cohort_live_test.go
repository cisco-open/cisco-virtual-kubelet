// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type discoveryCohortTrace struct {
	base               http.RoundTripper
	children           []*discoveryOwnershipTrace
	beforeSecondCreate func(context.Context) error
}

func (g *discoveryCohortTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == "GET" || req.URL.Path == authPath {
		return g.base.RoundTrip(req)
	}
	var body map[string]any
	if req.Method == "POST" || req.Method == "PUT" {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		if json.Unmarshal(raw, &body) != nil {
			return nil, errors.New("invalid cohort qualification body")
		}
	}
	for i, child := range g.children {
		selected := req.Method == "POST" && body["name"] == child.name || req.Method == "PUT" && child.id != "" && body["id"] == child.id || req.Method == "DELETE" && child.id != "" && req.URL.Path == discoveryPath+"/"+url.PathEscape(child.id)
		if !selected {
			continue
		}
		if i == 1 && req.Method == "POST" && g.beforeSecondCreate != nil {
			if err := g.beforeSecondCreate(req.Context()); err != nil {
				return nil, err
			}
		}
		return child.RoundTrip(req)
	}
	return nil, errors.New("cohort qualification refused unrelated mutation")
}

func TestLiveDiscoveryCohortProgress(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_COHORT") != "existing-lab-single" {
		t.Skip("explicit isolated discovery cohort qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	c := liveClient(t)
	before, err := c.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seed, ok := before["Switches"]
	if !ok || seed.Status != "Inactive" || seed.Condition != "Complete" {
		t.Fatal("lab seed unavailable")
	}
	credentials, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := []any{}
	for _, id := range seed.CredentialIDs {
		found := ""
		for _, group := range credentials {
			for _, credential := range group {
				if credential.ID == id {
					if found != "" {
						t.Fatal("ambiguous credential")
					}
					found = credential.Name
				}
			}
		}
		if found == "" {
			t.Fatal("credential name unavailable")
		}
		names = append(names, found)
	}
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, devices)
	if err != nil {
		t.Fatal(err)
	}
	nonce := wirelessQualificationName(t, "CVK_COHORT_")
	jobNames := []string{nonce + "_keep", nonce + "_remove"}
	for _, name := range jobNames {
		if _, present := before[name]; present {
			t.Fatal("generated name exists")
		}
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-discovery-cohort-")
	if err != nil {
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
	t.Log("isolated discovery cohort and private evidence", jobNames, dir)
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	children := []*discoveryOwnershipTrace{}
	for _, name := range jobNames {
		children = append(children, &discoveryOwnershipTrace{base: base, name: name, credentialIDs: append([]string(nil), seed.CredentialIDs...), protocol: "SSH", maxPosts: 1, maxDeletes: 1, maxStarts: 2, allowDelete: true})
	}
	// Reuse the singular trace's credential ordering and strict bounded payload gate.
	for _, child := range children {
		slices.Sort(child.credentialIDs)
	}
	trace := &discoveryCohortTrace{base: base, children: children}
	rest.HTTPClient.Transport = trace
	defer func() { rest.HTTPClient.Transport = base }()
	a, config := journalFixture(t)
	a.client = c
	config.Spec.ManagedSections = []string{"inventory"}
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	nc, _ := a.currentController(ctx)
	nc.Spec.Endpoint = c.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	model := func(selected ...string) []byte {
		jobs := []any{}
		for _, name := range selected {
			jobs = append(jobs, map[string]any{"name": name, "type": "SINGLE", "ip_address_list": "198.51.100.101", "protocol_order": "SSH", "global_credential_list": names})
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": jobs}}})
		return raw
	}
	source := model(jobNames[0])
	config.Spec.Source.Inline.Raw = source
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	recovered := map[string]bool{}
	persist := func() {
		t.Helper()
		save("source.json", json.RawMessage(source))
		save("journal.json", journalState(t, a))
		save("identity.json", map[string]any{"names": jobNames, "ids": []string{children[0].id, children[1].id}, "generation": config.Generation})
		save("baseline.json", before)
	}
	baseline := func() {
		t.Helper()
		rows, err := c.discoveries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for i, name := range jobNames {
			if row, present := rows[name]; present {
				if children[i].posts != 1 || children[i].id != "" && children[i].id != row.ID {
					t.Fatal("generated cohort identity changed")
				}
				delete(rows, name)
			}
		}
		if !reflect.DeepEqual(before, rows) {
			t.Fatal("foreign native baseline changed")
		}
	}
	tick := func() {
		t.Helper()
		baseline()
		restart := *a
		if _, err := (&configReconciler{a: &restart}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			persist()
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		current, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if current.Status.Configuration == nil {
			persist()
			return
		}
		if op := current.Status.Configuration.Operation; op != nil && op.Phase == "Accepted" && !recovered[op.ID] {
			save("accepted-"+op.ID+".json", op)
			recovered[op.ID] = true
			op.Phase = "Unknown"
			raw, _ := json.Marshal(current.Status.Configuration)
			_ = json.Unmarshal(raw, &current.Status.Configuration)
			if err := a.statusWriter.Status().Update(ctx, current); err != nil {
				t.Fatal(err)
			}
		}
		for _, r := range current.Status.Configuration.ActionApplications {
			if r.Operation.Action != "Create" || r.Operation.ConfigUID != string(config.UID) || !validOwnedDiscoveryReceipt(r) {
				continue
			}
			for i, name := range jobNames {
				prefix := ownedDiscoveryReceiptKey(name, "")
				if strings.HasPrefix(r.Resource, prefix) {
					id := strings.TrimPrefix(r.Resource, prefix)
					if children[i].id != "" && children[i].id != id {
						t.Fatal("published cohort ID changed")
					}
					children[i].id = id
				}
			}
		}
		persist()
	}
	run := func(done func() bool) {
		t.Helper()
		for range 600 {
			tick()
			if done() {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("cohort did not settle; retain exact evidence", ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
		t.Fatal("cohort iteration budget exhausted")
	}
	inSync := func() bool {
		return config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation && journalState(t, a).Operation == nil
	}
	revise := func(raw []byte) {
		t.Helper()
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		config.Generation++
		source = raw
		config.Spec.Source.Inline.Raw = source
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	observeKept := func(ctx context.Context, file string) (discoveryMetadata, error) {
		rows, err := c.discoveries(ctx)
		if err != nil {
			return discoveryMetadata{}, err
		}
		row, ok := rows[jobNames[0]]
		if !ok || row.ID != children[0].id {
			return row, errors.New("kept job identity changed")
		}
		save(file, row)
		return row, nil
	}
	startKept := func(stage int) {
		t.Helper()
		row, err := observeKept(ctx, fmt.Sprint("before-start-", stage, ".json"))
		if err != nil {
			t.Fatal(err)
		}
		var receipt *cvk.NetworkControllerConfigurationActionApplication
		for _, r := range journalState(t, a).ActionApplications {
			if r.Resource == ownedDiscoveryReceiptKey(jobNames[0], row.ID) && r.Operation.ConfigUID == string(config.UID) && validOwnedDiscoveryReceipt(r) && r.Operation.Action == "Create" {
				copy := r
				receipt = &copy
			}
		}
		if receipt == nil {
			t.Fatal("kept job not owned")
		}
		mask, digest, valid := ownedDiscoveryFingerprintParts(receipt.Operation.Resource)
		effect, err := discoveryNativeEffect(row, mask)
		if !valid || mask == 0 || err != nil || discoveryPublicDigest(effect) != digest {
			t.Fatal("kept footprint drift")
		}
		proof := map[string]any{"id": row.ID, "createTaskId": receipt.Operation.TaskID, "attempted": true, "submittedAt": time.Now().UTC()}
		file := fmt.Sprint("restart-", stage, ".json")
		if err := claimQualificationRecord(filepath.Join(dir, file), proof); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"id": row.ID, "discoveryStatus": "Active"})
		raw, err = c.mutate(ctx, http.MethodPut, discoveryPath, raw)
		if err != nil {
			t.Fatal("restart uncertain; no replay", err)
		}
		var ack struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
			t.Fatal("restart acknowledgement missing")
		}
		proof["taskId"] = ack.Response.ID
		save(file, proof)
		for range 150 {
			row, err = observeKept(ctx, fmt.Sprint("active-ready-", stage, ".json"))
			if err != nil {
				t.Fatal(err)
			}
			if row.Status == "Active" && row.Condition == "In Progress" {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	createWhileActive, pruneWhileActive := 0, 0
	trace.beforeSecondCreate = func(ctx context.Context) error {
		row, err := observeKept(ctx, "before-second-create.json")
		if err == nil && row.Status == "Active" {
			createWhileActive++
		}
		return err
	}
	children[1].beforeDelete = func(ctx context.Context) error {
		row, err := observeKept(ctx, "before-second-prune.json")
		if err == nil && row.Status == "Active" {
			pruneWhileActive++
		}
		return err
	}
	run(inSync)
	startKept(1)
	revise(model(jobNames...))
	run(inSync)
	if children[0].posts != 1 || children[1].posts != 1 || len(journalState(t, a).ActionApplications) != 2 {
		t.Fatal("cohort create did not publish two owned results")
	}
	startKept(2)
	revise(model(jobNames[0]))
	run(func() bool { return children[1].deletes == 1 && journalState(t, a).Operation == nil })
	kept, err := observeKept(ctx, "after-independent-prune.json")
	if err != nil {
		t.Fatal(err)
	}
	if kept.Status == "Active" && config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("active retained peer incorrectly InSync")
	}
	run(inSync)
	revise([]byte(emptyDiscoveryModel))
	run(inSync)
	for range 3 {
		tick()
	}
	rows, err := c.discoveries(ctx)
	if err != nil || !reflect.DeepEqual(before, rows) || children[0].posts != 1 || children[1].posts != 1 || children[0].deletes != 1 || children[1].deletes != 1 || children[0].starts != 2 || children[1].starts != 0 || len(recovered) != 4 || children[0].attempts+children[1].attempts != 6 {
		t.Fatal("cohort did not restore exact baseline without replay", err)
	}
	after, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterTarget, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, after)
	if err != nil || afterTarget.ID != target.ID || afterTarget.Serial != target.Serial || len(after) != len(devices) {
		t.Fatal("target inventory changed", err)
	}
	qualified := createWhileActive == 1 && pruneWhileActive == 1
	save("complete.json", map[string]any{"cleaned": true, "qualified": qualified, "creates": 2, "deletes": 2, "testOnlyStarts": 2, "knownTaskRecoveries": len(recovered), "createWhileActive": createWhileActive, "pruneWhileActive": pruneWhileActive})
	if !qualified {
		t.Skip("active observation window missed; baseline cleaned, cohort remains unqualified")
	}
	t.Log("independent create and prune dispatched while retained job Active; four Unknown task recoveries, no replay, exact baseline restored")
}
