// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
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
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type discoveryOwnershipTrace struct {
	protocol             string
	maxPosts, maxDeletes int
	starts, maxStarts    int

	base                     http.RoundTripper
	name, id                 string
	credentialIDs            []string
	posts, deletes, attempts int
	allowDelete              bool
	beforeDelete             func(context.Context) error
}

func (g *discoveryOwnershipTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.URL.Path != authPath {
		g.attempts++
		if req.Method == http.MethodDelete && g.allowDelete && g.id != "" && g.deletes < g.maxDeletes && req.URL.Path == discoveryPath+"/"+url.PathEscape(g.id) {
			if g.beforeDelete != nil {
				if err := g.beforeDelete(req.Context()); err != nil {
					return nil, err
				}
			}
			g.deletes++
		} else if req.Method == http.MethodPut && req.URL.Path == discoveryPath && g.id != "" && g.starts < g.maxStarts {
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(raw))
			var body map[string]any
			if json.Unmarshal(raw, &body) != nil || len(body) != 2 || body["id"] != g.id || body["discoveryStatus"] != "Active" {
				return nil, errors.New("qualification refused unrelated discovery restart")
			}
			g.starts++
		} else {
			if req.Method != http.MethodPost || req.URL.Path != discoveryPath || g.posts >= g.maxPosts {
				return nil, errors.New("discovery ownership qualification refused unrelated/repeated mutation")
			}
			raw, err := io.ReadAll(req.Body)
			if err != nil {
				return nil, err
			}
			req.Body = io.NopCloser(bytes.NewReader(raw))
			var payload map[string]any
			if json.Unmarshal(raw, &payload) != nil || len(payload) != 5 || payload["name"] != g.name || payload["discoveryType"] != "SINGLE" || payload["protocolOrder"] != g.protocol || payload["ipAddressList"] != "198.51.100.101" {
				return nil, errors.New("discovery ownership qualification refused unexpected target or fields")
			}
			rows, ok := payload["globalCredentialIdList"].([]any)
			if !ok {
				return nil, errors.New("missing global credential selection")
			}
			ids := []string{}
			for _, v := range rows {
				id, ok := v.(string)
				if !ok {
					return nil, errors.New("invalid credential selection")
				}
				ids = append(ids, id)
			}
			slices.Sort(ids)
			if !slices.Equal(ids, g.credentialIDs) {
				return nil, errors.New("discovery ownership qualification refused changed credentials")
			}
			g.posts++
		}
	}
	return g.base.RoundTrip(req)
}

func TestLiveDiscoveryOwnedManifestReceipt(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_OWNED") != "existing-lab-single" {
		t.Skip("explicit existing .101 discovery ownership qualification not enabled")
	}
	qualifyLiveDiscoveryOwned(t, false)
}
func TestLiveDiscoveryOwnedReplacement(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_REPLACEMENT") != "existing-lab-single" {
		t.Skip("explicit owned discovery replacement qualification not enabled")
	}
	qualifyLiveDiscoveryOwned(t, true)
}
func TestLiveDiscoveryActiveOwnershipLifecycle(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_ACTIVE_OWNERSHIP") != "existing-lab-single" {
		t.Skip("explicit active discovery lifecycle qualification not enabled")
	}
	qualifyLiveDiscoveryOwned(t, true)
}
func TestLiveDiscoveryInProgressOwnershipLifecycle(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_IN_PROGRESS") != "existing-lab-single" {
		t.Skip("explicit in-progress discovery lifecycle qualification not enabled")
	}
	t.Setenv("CATC_DISCOVERY_ACTIVE_OWNERSHIP", "existing-lab-single")
	t.Setenv("CATC_DISCOVERY_ACTIVE_CONDITION", "In Progress")
	qualifyLiveDiscoveryOwned(t, true)
}
func TestLiveDiscoveryMixedOwnership(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_MIXED_OWNERSHIP") != "existing-lab-single" {
		t.Skip("explicit mixed discovery/tag ownership qualification not enabled")
	}
	qualifyLiveDiscoveryOwned(t, true)
}
func TestLiveDiscoveryOwnedPruning(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_PRUNE") != "existing-lab-single" {
		t.Skip("explicit owned discovery source-prune qualification not enabled")
	}
	qualifyLiveDiscoveryOwned(t, false, true)
}
func TestLiveDiscoverySelectedOwnershipLifecycle(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_SITE_SELECTION") != "existing-lab-single" {
		t.Skip("explicit selected discovery qualification not enabled")
	}
	qualifyLiveDiscoveryOwned(t, false, true)
}
func TestLiveDiscoveryDefaultsOwnershipLifecycle(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_DEFAULTS_EXECUTION") != "existing-lab-single" {
		t.Skip("explicit defaults discovery qualification disabled")
	}
	qualifyLiveDiscoveryOwned(t, true)
}

func TestLiveDiscoveryConfigCleanupObservation(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_CLEANUP_OBSERVATION") != "existing-lab-single" {
		t.Skip("explicit discovery cleanup observation disabled")
	}
	qualifyLiveDiscoveryOwned(t, false)
}

func TestLiveDiscoveryConfigCleanupExecution(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_CLEANUP_EXECUTION") != "existing-lab-single" {
		t.Skip("explicit discovery cleanup execution disabled")
	}
	t.Setenv("CATC_DISCOVERY_CLEANUP_OBSERVATION", "existing-lab-single")
	t.Setenv("CATC_DISCOVERY_CLEANUP_PREPARATION", "existing-lab-single")
	qualifyLiveDiscoveryOwned(t, false)
}

func TestLiveDiscoveryConfigDeletionDrain(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_DELETION_DRAIN") != "existing-lab-single" {
		t.Skip("explicit pre-deletion discovery drain disabled")
	}
	t.Setenv("CATC_DISCOVERY_CLEANUP_OBSERVATION", "existing-lab-single")
	t.Setenv("CATC_DISCOVERY_CLEANUP_PREPARATION", "existing-lab-single")
	t.Setenv("CATC_DISCOVERY_CLEANUP_EXECUTION", "existing-lab-single")
	qualifyLiveDiscoveryOwned(t, false)
}

func TestLiveDiscoveryConfigCleanupCompletion(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_CLEANUP_COMPLETION") != "existing-lab-single" {
		t.Skip("explicit discovery completion qualification disabled")
	}
	t.Setenv("CATC_DISCOVERY_DELETION_DRAIN", "existing-lab-single")
	t.Setenv("CATC_DISCOVERY_CLEANUP_OBSERVATION", "existing-lab-single")
	t.Setenv("CATC_DISCOVERY_CLEANUP_PREPARATION", "existing-lab-single")
	t.Setenv("CATC_DISCOVERY_CLEANUP_EXECUTION", "existing-lab-single")
	qualifyLiveDiscoveryOwned(t, false)
}

func TestLiveDiscoveryPublicConfigDeletion(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_PUBLIC_DELETION") != "existing-lab-single" {
		t.Skip("explicit public discovery deletion qualification disabled")
	}
	qualifyLiveDiscoveryOwned(t, false)
}

func TestLiveDiscoveryRetainedSourceRecovery(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_PUBLIC_SOURCE_RECOVERY") != "existing-lab-single" {
		t.Skip("explicit retained-source recovery disabled")
	}
	t.Setenv("CATC_DISCOVERY_PUBLIC_DELETION", "existing-lab-single")
	qualifyLiveDiscoveryOwned(t, false)
}

func TestLiveDiscoveryOwnershipHandoff(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_OWNERSHIP_HANDOFF") != "existing-lab-single" {
		t.Skip("explicit same-name discovery handoff qualification disabled")
	}
	t.Setenv("CATC_DISCOVERY_PUBLIC_DELETION", "existing-lab-single")
	qualifyLiveDiscoveryOwned(t, false)
}

func TestLiveDiscoveryOptionalNullManifest(t *testing.T) {
	if os.Getenv("CATC_DISCOVERY_OPTIONAL_NULLS") != "existing-lab-single" {
		t.Skip("explicit optional-null discovery qualification disabled")
	}
	t.Setenv("CATC_DISCOVERY_PUBLIC_DELETION", "existing-lab-single")
	qualifyLiveDiscoveryOwned(t, false)
}

func qualifyLiveDiscoveryOwned(t *testing.T, replace bool, pruning ...bool) {
	defaults := os.Getenv("CATC_DISCOVERY_DEFAULTS_EXECUTION") == "existing-lab-single"
	selection := os.Getenv("CATC_DISCOVERY_SITE_SELECTION") == "existing-lab-single"
	prune := len(pruning) == 1 && pruning[0]
	active := os.Getenv("CATC_DISCOVERY_ACTIVE_OWNERSHIP") == "existing-lab-single"
	mixed := os.Getenv("CATC_DISCOVERY_MIXED_OWNERSHIP") == "existing-lab-single"
	activeCondition := os.Getenv("CATC_DISCOVERY_ACTIVE_CONDITION")
	activeMatches := func(row discoveryMetadata) bool {
		return row.Status == "Active" && (activeCondition == "" || row.Condition == activeCondition)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	c := liveClient(t)
	before, err := c.discoveries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seed, ok := before["Switches"]
	if !ok || seed.Condition != "Complete" || seed.Status != "Inactive" {
		t.Fatal("existing lab discovery unavailable")
	}
	credentials, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := []any{}
	for _, id := range seed.CredentialIDs {
		name := ""
		for _, group := range credentials {
			for _, credential := range group {
				if credential.ID == id {
					if name != "" {
						t.Fatal("ambiguous lab credential identity")
					}
					name = credential.Name
				}
			}
		}
		if name == "" {
			t.Fatal("lab credential name unavailable")
		}
		names = append(names, name)
	}
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	target, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, devices)
	if err != nil {
		t.Fatal(err)
	}
	name := wirelessQualificationName(t, "CVK_DISC_")
	if _, exists := before[name]; exists {
		t.Fatal("qualification name already exists")
	}
	var beforeTags map[string]observedTag
	tagName := ""
	if mixed {
		beforeTags, err = c.listTags(ctx)
		if err != nil {
			t.Fatal(err)
		}
		keys := []string{}
		for name := range beforeTags {
			keys = append(keys, name)
		}
		slices.Sort(keys)
		for _, candidate := range keys {
			row := beforeTags[candidate]
			if row.SystemTag == nil || *row.SystemTag {
				continue
			}
			desired, err := parseTemplateTags([]any{map[string]any{"name": candidate}})
			if err != nil {
				continue
			}
			mutation, err := planTagMutation(desired, beforeTags)
			if err == nil && mutation == nil {
				tagName = candidate
				break
			}
		}
		if tagName == "" {
			t.Skip("no matching ordinary imported tag for read-only mixed composition")
		}
	}
	dir, err := os.MkdirTemp("/private/tmp", "cvk-discovery-owned-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log("isolated discovery name and private recovery evidence", name, dir)
	a, config := journalFixture(t)
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = c.cfg.Endpoint
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.ControllerRef.Name = nc.Name
	config.Spec.ManagedSections = []string{"inventory"}
	if mixed {
		config.Spec.ManagedSections = []string{"templates", "inventory"}
	}
	if prune {
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
	}
	source, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"discovery": []any{map[string]any{"name": name, "type": "SINGLE", "ip_address_list": "198.51.100.101", "protocol_order": "SSH", "global_credential_list": names}}}}})
	if os.Getenv("CATC_DISCOVERY_OPTIONAL_NULLS") == "existing-lab-single" {
		source = discoveryNullFixture(t, source, false)
	}
	var defaultsMeta DiscoverySelectionProvenance
	if defaults {
		source, defaultsMeta = discoveryDefaultsFixture(t, source, "SSH")
		config.Spec.ModelSource.Exporter = DiscoveryDefaultsExporter
	}
	var selectionMeta DiscoverySelectionProvenance
	if selection {
		var err error
		source, selectionMeta, err = ExportSelectedDiscovery(discoverySelectionSource(t, source), nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}})
		if err != nil || selectionMeta.SelectedJobs != 1 || selectionMeta.DeclaredJobs != 2 {
			t.Fatal("native discovery selection failed", err)
		}
	}
	if mixed {
		var document map[string]any
		if err := json.Unmarshal(source, &document); err != nil {
			t.Fatal(err)
		}
		document["catalyst_center"].(map[string]any)["templates"] = map[string]any{"tags": []any{map[string]any{"name": tagName}}}
		source, _ = json.Marshal(document)
	}
	config.Spec.Source.Inline.Raw = source
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	a.client = c
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	ids := slices.Clone(seed.CredentialIDs)
	slices.Sort(ids)
	trace := &discoveryOwnershipTrace{base: base, name: name, credentialIDs: ids, protocol: "SSH", maxPosts: 1, maxDeletes: 1}
	if active {
		trace.maxStarts = 2
	}
	if replace || prune {
		trace.maxPosts = 2
		trace.maxDeletes = 2
	}
	rest.HTTPClient.Transport = trace
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
	var publicLifecycle *liveDiscoveryPublicLifecycle
	if os.Getenv("CATC_DISCOVERY_PUBLIC_DELETION") == "existing-lab-single" {
		if replace || prune || mixed || active || defaults || selection {
			t.Fatal("public lifecycle requires independent single discovery qualification")
		}
		publicLifecycle = prepareLiveDiscoveryPublicLifecycle(t, a, config, dir)
	}
	save("baseline.json", before)
	if defaults {
		save("defaults-initial-provenance.json", defaultsMeta)
	}
	if selection {
		save("site-selection-provenance.json", selectionMeta)
	}
	if mixed {
		save("tag-baseline.json", beforeTags[tagName])
	}
	activeDeletes, ownedActive := 0, 0
	activeReady := 0
	ensureActive := func() {
		t.Helper()
		rows, err := c.discoveries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		row, present := rows[name]
		state := journalState(t, a)
		if !present || row.ID != trace.id || state.Operation != nil || len(state.ActionApplications) != 1 {
			t.Fatal("restart requires the original owned native job")
		}
		r := state.ActionApplications[0]
		mask, digest, valid := ownedDiscoveryFingerprintParts(r.Operation.Resource)
		effect, effectErr := discoveryNativeEffect(row, mask)
		if !valid || mask == 0 || effectErr != nil || discoveryPublicDigest(effect) != digest || !validOwnedDiscoveryReceipt(r) || r.Operation.Action != "Create" || r.Operation.ConfigUID != string(config.UID) || r.Resource != ownedDiscoveryReceiptKey(name, trace.id) {
			t.Fatal("restart ownership/footprint mismatch")
		}
		if row.Status != "Active" {
			// Test-only precondition: exactly one start request, durably claimed
			// before dispatch. It is never part of CVK's production execution.
			file := "restart-" + fmt.Sprint(trace.posts) + ".json"
			started := time.Now().UTC()
			proof := map[string]any{"name": name, "id": trace.id, "createTaskId": r.Operation.TaskID, "attempted": true, "submittedAt": started}
			if err := claimQualificationRecord(filepath.Join(dir, file), proof); err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(map[string]any{"id": trace.id, "discoveryStatus": "Active"})
			raw, err := c.mutate(ctx, http.MethodPut, discoveryPath, body)
			if err != nil {
				t.Fatal("restart uncertain; retain exact proof without replay", err)
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				t.Fatal("restart acknowledgement missing; no replay")
			}
			proof["taskId"] = receipt.Response.ID
			save(file, proof)
			for range 20 {
				rows, err = c.discoveries(ctx)
				if err != nil {
					t.Fatal(err)
				}
				row, present = rows[name]
				if !present || row.ID != trace.id {
					t.Fatal("restart replaced native job")
				}
				if row.Status == "Active" {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		// Observe the requested runtime state without another start request.
		// A missed window is reported only after public cleanup restores baseline.
		for range 100 {
			if activeMatches(row) || row.Status == "Inactive" && row.Condition == "Complete" {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
			rows, err = c.discoveries(ctx)
			if err != nil {
				t.Fatal(err)
			}
			row, present = rows[name]
			if !present || row.ID != trace.id {
				t.Fatal("active state observation lost original identity")
			}
		}
		save("active-ready-"+fmt.Sprint(trace.posts)+".json", row)
		if activeMatches(row) {
			activeReady++
		}
	}
	if active {
		trace.beforeDelete = func(ctx context.Context) error {
			rows, err := c.discoveries(ctx)
			if err != nil {
				return err
			}
			row, present := rows[name]
			if !present || row.ID != trace.id {
				return errors.New("active qualification refused changed native ID/name")
			}
			save("before-delete-"+fmt.Sprint(trace.deletes+1)+".json", row)
			if activeMatches(row) {
				activeDeletes++
			}
			return nil
		}
	}
	persist := func() {
		t.Helper()
		state := journalState(t, a)
		save("journal.json", state)
		save("source.json", json.RawMessage(source))
		save("identity.json", map[string]any{"name": name, "id": trace.id, "posts": trace.posts, "deletes": trace.deletes, "ip": "198.51.100.101"})
		raw, _ := json.Marshal(state)
		controller, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &controller.Status.Configuration); err != nil {
			t.Fatal(err)
		}
		if err := a.statusWriter.Status().Update(ctx, controller); err != nil {
			t.Fatal(err)
		}
	}
	baseline := func() {
		t.Helper()
		current, err := c.discoveries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if row, present := current[name]; present {
			pendingCreate := false
			if (replace || prune) && trace.posts == 2 && trace.deletes == 1 {
				op := journalState(t, a).Operation
				pendingCreate = op != nil && op.Action == "Create" && (op.Phase == "Accepted" || op.Phase == "Verifying" || op.Phase == "Unknown") && validAPIID(op.TaskID) && op.ConfigUID == string(config.UID)
				for _, receipt := range journalState(t, a).ActionApplications {
					if validOwnedDiscoveryReceipt(receipt) && receipt.Operation.Action == "Create" && receipt.Operation.ConfigUID == string(config.UID) && receipt.Operation.ConfigGeneration == config.Generation && receipt.Resource == ownedDiscoveryReceiptKey(name, row.ID) {
						pendingCreate = true
					}
				}
			}
			if trace.posts < 1 || trace.id != "" && row.ID != trace.id && !pendingCreate {
				t.Fatal("generated discovery identity changed")
			}
			delete(current, name)
		}
		if !reflect.DeepEqual(before, current) {
			t.Fatal("existing discovery baseline changed")
		}
	}
	accepted := false
	acceptedActions := map[string]string{}
	recovered := map[string]bool{}
	settle := func() {
		settled := false
		persist()
		for range 360 {
			baseline()
			rebuilt := *a
			if _, err := (&configReconciler{a: &rebuilt}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				persist()
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state == nil {
				state = &cvk.NetworkControllerConfigurationStatus{}
			}
			if state.Operation != nil && state.Operation.Phase == "Accepted" {
				accepted = true
				acceptedActions[state.Operation.ID] = state.Operation.Action
				save("accepted-"+state.Operation.ID+".json", state.Operation)
			}
			if (replace || prune) && state.Operation != nil && state.Operation.Phase == "Accepted" && !recovered[state.Operation.ID] {
				controller, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				controller.Status.Configuration.Operation.Phase = "Unknown"
				if err := a.statusWriter.Status().Update(ctx, controller); err != nil {
					t.Fatal(err)
				}
				recovered[state.Operation.ID] = true
				t.Log("retained known task restored as uncertain without resubmission", state.Operation.Action, state.Operation.TaskID)
			}
			persist()
			if active && state.Operation == nil && len(state.ActionApplications) == 1 {
				r := state.ActionApplications[0]
				if r.Operation.Action == "Create" && r.Operation.ConfigGeneration == config.Generation {
					rows, err := c.discoveries(ctx)
					if err != nil {
						t.Fatal(err)
					}
					row, present := rows[name]
					if !present || r.Resource != ownedDiscoveryReceiptKey(name, row.ID) {
						t.Fatal("creation receipt does not bind native job")
					}
					save("owned-state-"+fmt.Sprint(config.Generation)+".json", row)
					if row.Status == "Active" {
						ownedActive++
						if config.Status.Phase == "InSync" {
							t.Fatal("active discovery incorrectly reported InSync")
						}
					}
					settled = true
					break
				}
			}
			if state.Operation == nil && len(state.ActionApplications) == 1 && config.Status.Phase == "InSync" && config.Status.ObservedGeneration == config.Generation {
				settled = true
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("discovery did not converge; retain private task evidence", ctx.Err())
			case <-time.After(time.Second):
			}
		}
		if !settled || !accepted || trace.posts < 1 {
			t.Fatal("discovery ownership did not converge; retain evidence")
		}
	}
	settle()
	state := journalState(t, a)
	receipt := state.ActionApplications[0]
	prefix := ownedDiscoveryReceiptKey(name, "")
	trace.id = strings.TrimPrefix(receipt.Resource, prefix)
	if !validOwnedDiscoveryReceipt(receipt) || !strings.HasPrefix(receipt.Resource, prefix) || !validAPIID(trace.id) || receipt.Operation.ConfigUID != string(config.UID) {
		t.Fatal("native UUID ownership receipt missing")
	}
	persist()
	for range 2 {
		rebuilt := *a
		if _, err := (&configReconciler{a: &rebuilt}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
			t.Fatal(err)
		}
		baseline()
	}
	if trace.posts != 1 || trace.attempts != 1 {
		t.Fatal("completed discovery replayed")
	}
	if active {
		ensureActive()
	}
	if prune {
		initialSource := bytes.Clone(source)
		revise := func(raw []byte) {
			t.Helper()
			if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
				t.Fatal(err)
			}
			config.Generation++
			source = bytes.Clone(raw)
			config.Spec.Source.Inline.Raw = source
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			settle()
		}
		save("created-receipt.json", receipt)
		trace.allowDelete = true
		revise([]byte(emptyDiscoveryModel))
		tombstone := journalState(t, a).ActionApplications[0]
		if !validOwnedDiscoveryReceipt(tombstone) || tombstone.Operation.Action != "Delete" || !strings.HasSuffix(tombstone.Operation.Resource, discoveryPruneSuffix) || trace.posts != 1 || trace.deletes != 1 || len(recovered) != 2 {
			t.Fatal("source pruning did not establish a verified tombstone")
		}
		save("first-prune-receipt.json", tombstone)
		t.Log("public discovery source removal verified with task/UUID absence and retained tombstone", tombstone.Operation.TaskID)
		revise(initialSource)
		receipt = journalState(t, a).ActionApplications[0]
		if !validOwnedDiscoveryReceipt(receipt) || receipt.Operation.Action != "Create" || receipt.Operation.ConfigGeneration != config.Generation || trace.posts != 2 || trace.deletes != 1 || len(recovered) != 3 {
			t.Fatal("explicit reintroduction lacks fresh task/UUID proof")
		}
		trace.id = strings.TrimPrefix(receipt.Resource, ownedDiscoveryReceiptKey(name, ""))
		if !validAPIID(trace.id) {
			t.Fatal("reintroduced job UUID missing")
		}
		save("reintroduced-receipt.json", receipt)
		revise([]byte(omittedDiscoveryModel))
		receipt = journalState(t, a).ActionApplications[0]
		if !validOwnedDiscoveryReceipt(receipt) || receipt.Operation.Action != "Delete" || !strings.HasSuffix(receipt.Operation.Resource, discoveryPruneSuffix) || trace.posts != 2 || trace.deletes != 2 || trace.attempts != 4 || len(recovered) != 4 {
			t.Fatal("omitted-key final pruning/recovery did not converge")
		}
		for range 2 {
			rebuilt := *a
			if _, err := (&configReconciler{a: &rebuilt}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			baseline()
		}
		current, err := c.discoveries(ctx)
		if err != nil || !reflect.DeepEqual(before, current) || trace.attempts != 4 {
			t.Fatal("public pruning did not restore native baseline", err)
		}
		afterDevices, err := c.configurationDevices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		afterTarget, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, afterDevices)
		if err != nil || afterTarget.ID != target.ID || afterTarget.Serial != target.Serial || len(afterDevices) != len(devices) {
			t.Fatal("lab inventory identity changed", err)
		}
		persist()
		save("complete.json", map[string]any{"cleaned": true, "publicPrune": true, "siteSelection": selection, "posts": trace.posts, "deletes": trace.deletes, "knownTaskRecoveries": len(recovered)})
		t.Log("owned discovery prune/reintroduction/prune passed four Unknown task recoveries without replay; original native job baseline restored")
		return
	}
	if replace {
		save("created-receipt.json", receipt)
		initial := receipt
		trace.allowDelete = true
		trace.protocol = "SSH,TELNET"
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		var root map[string]any
		if err := json.Unmarshal(source, &root); err != nil {
			t.Fatal(err)
		}
		model := root["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["discovery"].([]any)[0].(map[string]any)
		model["protocol_order"] = "SSH,TELNET"
		source, _ = json.Marshal(root)
		if defaults {
			source, defaultsMeta = discoveryDefaultsFixture(t, source, "SSH,TELNET")
			save("defaults-replacement-provenance.json", defaultsMeta)
		}
		config.Spec.Source.Inline.Raw = source
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		settle()
		final := journalState(t, a)
		if len(final.ActionApplications) != 1 || !validOwnedDiscoveryReceipt(final.ActionApplications[0]) {
			t.Fatal("replacement receipt unavailable")
		}
		receipt = final.ActionApplications[0]
		if receipt.Operation.Action != "Create" || receipt.Operation.ID == initial.Operation.ID || receipt.Operation.Resource == initial.Operation.Resource || receipt.Operation.ConfigGeneration != config.Generation || trace.posts != 2 || trace.deletes != 1 {
			t.Fatal("replacement did not create a distinct source/task result")
		}
		trace.id = strings.TrimPrefix(receipt.Resource, ownedDiscoveryReceiptKey(name, ""))
		if !validAPIID(trace.id) {
			t.Fatal("replacement native UUID missing")
		}
		save("replacement-receipt.json", receipt)
		persist()
		creates, deletes := 0, 0
		for _, action := range acceptedActions {
			if action == "Create" {
				creates++
			}
			if action == "Delete" {
				deletes++
			}
		}
		if creates != 2 || deletes != 1 || len(recovered) != 3 {
			t.Fatal("accepted replacement sequence missing", creates, deletes)
		}
		for range 2 {
			rebuilt := *a
			if _, err := (&configReconciler{a: &rebuilt}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
				t.Fatal(err)
			}
			baseline()
		}
		if trace.attempts != 3+trace.starts {
			t.Fatal("replacement replayed")
		}
		t.Log("owned delete/task/absence/new-create/UUID receipt and no-op verified", initial.Resource, receipt.Resource)
	}
	afterDevices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterTarget, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, afterDevices)
	if err != nil || target.ID != afterTarget.ID || target.Serial != afterTarget.Serial || len(afterDevices) != len(devices) {
		t.Fatal("lab inventory identity changed", err)
	}
	t.Log("public manifest task/UUID receipt, JSON restart and no-op verified", name, trace.id, receipt.Operation.TaskID)
	for range 2 {
		baseline()
	}
	if publicLifecycle != nil {
		if os.Getenv("CATC_DISCOVERY_OPTIONAL_NULLS") == "existing-lab-single" {
			before := journalState(t, a).DeepCopy().ActionApplications
			for _, omit := range []bool{true, false} {
				if err := publicLifecycle.admin.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
					t.Fatal(err)
				}
				config.Generation++
				config.Spec.Source.Inline.Raw = discoveryNullFixture(t, config.Spec.Source.Inline.Raw, omit)
				if err := publicLifecycle.admin.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				settled := false
				for range 12 {
					if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}); err != nil {
						t.Fatal(err)
					}
					if err := publicLifecycle.admin.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
						t.Fatal(err)
					}
					if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
						settled = true
						break
					}
				}
				if !settled || trace.posts != 1 || trace.deletes != 0 || trace.attempts != 1 || !reflect.DeepEqual(before, journalState(t, a).ActionApplications) {
					t.Fatal("nullable transition replayed or changed the creation receipt")
				}
			}
			save("null-omission-transitions.json", map[string]any{"fields": discoveryNullableFields, "transitions": []string{"null-to-omitted", "omitted-to-null"}, "mutationAttempts": trace.attempts, "creatorReceiptPreserved": true, "installedClusterMutated": false})
		}
		beforeAttempts := trace.attempts
		trace.allowDelete = true
		publicLifecycle.finish(t, a, config, trace.id, dir)
		rows, err := c.discoveries(ctx)
		if err != nil || !reflect.DeepEqual(before, rows) || trace.attempts != beforeAttempts+1 || trace.posts != 1 || trace.deletes != 1 {
			t.Fatal("public cleanup did not restore exact native baseline", err)
		}
		finalDevices, err := c.configurationDevices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		finalTarget, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, finalDevices)
		if err != nil || finalTarget.ID != target.ID || finalTarget.Serial != target.Serial || len(finalDevices) != len(devices) {
			t.Fatal("public cleanup changed lab inventory", err)
		}
		persist()
		save("complete.json", map[string]any{"cleaned": true, "publicLifecycle": true, "managerEnrolled": true, "managerReleased": true, "posts": trace.posts, "deletes": trace.deletes, "inventoryIdentityPreserved": true, "installedClusterMutated": false})
		if os.Getenv("CATC_DISCOVERY_OWNERSHIP_HANDOFF") == "existing-lab-single" {
			publicLifecycle.qualifyHandoff(t, a, config, trace, before, dir)
		}
		return
	}
	if os.Getenv("CATC_DISCOVERY_CLEANUP_OBSERVATION") == "existing-lab-single" {
		beforeAttempts := trace.attempts
		executeCleanup := os.Getenv("CATC_DISCOVERY_CLEANUP_EXECUTION") == "existing-lab-single"
		if executeCleanup {
			trace.allowDelete = true
		}
		qualifyLiveDiscoveryCleanupObservation(t, a, config, trace.id, dir)
		if executeCleanup {
			rows, err := c.discoveries(ctx)
			if err != nil || !reflect.DeepEqual(before, rows) || trace.attempts != beforeAttempts+1 || trace.deletes != 1 {
				t.Fatal("private CR cleanup did not restore exact baseline with one delete", err)
			}
			baseline()
			finalDevices, err := c.configurationDevices(ctx)
			if err != nil {
				t.Fatal(err)
			}
			finalTarget, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, finalDevices)
			if err != nil || finalTarget.ID != target.ID || finalTarget.Serial != target.Serial || len(finalDevices) != len(devices) {
				t.Fatal("private cleanup changed lab inventory identity", err)
			}
			save("complete.json", map[string]any{"cleaned": true, "privateCleanupExecution": true, "posts": trace.posts, "deletes": trace.deletes, "installedClusterMutated": false, "inventoryIdentityPreserved": true})
			return
		}
		if trace.attempts != beforeAttempts {
			t.Fatal("cleanup prerequisite performed a native mutation")
		}

	}
	trace.allowDelete = true
	if active {
		ensureActive()
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		config.Generation++
		config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyDelete
		source = []byte(emptyDiscoveryModel)
		config.Spec.Source.Inline.Raw = source
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		settle()
		state := journalState(t, a)
		rows, err := c.discoveries(ctx)
		if err != nil || !reflect.DeepEqual(before, rows) || state.Operation != nil || len(state.ActionApplications) != 1 || state.ActionApplications[0].Operation.Action != "Delete" || !strings.HasSuffix(state.ActionApplications[0].Operation.Resource, discoveryPruneSuffix) || trace.posts != 2 || trace.deletes != 2 || trace.attempts != 4+trace.starts || len(recovered) != 4 {
			t.Fatal("active public cleanup did not restore baseline", err)
		}
		finalDevices, err := c.configurationDevices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		finalTarget, err := resolveConfigurationDevice(configurationDeviceReference{IP: "198.51.100.101"}, finalDevices)
		if err != nil || finalTarget.ID != target.ID || finalTarget.Serial != target.Serial || len(finalDevices) != len(devices) {
			t.Fatal("final lab inventory identity changed", err)
		}
		persist()
		save("complete.json", map[string]any{"cleaned": true, "posts": trace.posts, "deletes": trace.deletes, "starts": trace.starts, "knownTaskRecoveries": len(recovered), "ownedWhileActive": ownedActive, "activeReady": activeReady, "activeDeletes": activeDeletes, "activeCondition": activeCondition, "activeQualified": activeReady == 2 && activeDeletes == 2})
		if activeReady != 2 || activeDeletes != 2 {
			t.Skip("native jobs completed before active-state observation; all generated jobs cleaned, active lifecycle remains unqualified")
		}
		t.Log("native owned active replacement and active source-prune verified; test-only restarts", trace.starts, "creation receipts observed active", ownedActive, "all four Unknown tasks recovered without replay and original job baseline restored")
		return
	}
	submitted := time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, discoveryPath+"/"+url.PathEscape(trace.id), nil)
	if err != nil {
		t.Fatal("exact cleanup uncertain; retain", err)
	}
	var cleanup struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &cleanup) != nil || !validAPIID(cleanup.Response.ID) {
		t.Fatal("cleanup task unavailable")
	}
	save("cleanup.json", map[string]any{"name": name, "id": trace.id, "deleteTaskId": cleanup.Response.ID, "deleted": false})
	if err := waitSiteCredentialTask(ctx, c, cleanup.Response, submitted); err != nil {
		t.Fatal(err)
	}
	absent := false
	for range 30 {
		current, err := c.discoveries(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := current[name]; !exists {
			if !reflect.DeepEqual(before, current) {
				t.Fatal("discovery baseline changed")
			}
			absent = true
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	if !absent {
		t.Fatal("delete task did not establish absence; no replay")
	}
	baseline()
	persist()
	save("cleanup.json", map[string]any{"name": name, "id": trace.id, "deleteTaskId": cleanup.Response.ID, "deleted": true})
	if mixed {
		finalTags, err := c.listTags(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(beforeTags[tagName], finalTags[tagName]) || receipt.Section != "templates" || !strings.HasPrefix(receipt.Operation.Resource, "discoveries/"+ownedDiscoveryPrefix) || len(recovered) != 3 {
			t.Fatal("mixed canonical ownership or imported tag preservation failed")
		}
		save("mixed-complete.json", map[string]any{"cleaned": true, "canonicalSection": receipt.Section, "tagUnchanged": true, "knownTaskRecoveries": len(recovered), "posts": trace.posts, "deletes": trace.deletes})
		t.Log("mixed discovery replacement ownership/Unknown recovery verified; imported tag unchanged", tagName, finalTags[tagName].ID)
	}
	expected := 1
	if replace {
		expected = 2
	}
	if trace.posts != expected || trace.deletes != expected || trace.attempts != 2*expected {
		t.Fatal("unexpected cleanup mutation count")
	}
	t.Log("owned discovery task/UUID/no-replay qualification and exact cleanup completed", trace.id, cleanup.Response.ID)
}
