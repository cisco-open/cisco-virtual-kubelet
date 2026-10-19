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
	"sort"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func saveLiveAuthBaseline(t *testing.T, baseline map[string]map[string]any) {
	t.Helper()
	dir, err := os.MkdirTemp("/private/tmp", "cvk-auth-template-baseline-")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(baseline)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "baseline.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("private recovery baseline saved", path)
}

func liveAuthChangedPaths(before, after any, path string) []string {
	if reflect.DeepEqual(before, after) {
		return nil
	}
	left, lok := before.(map[string]any)
	right, rok := after.(map[string]any)
	if lok && rok {
		keys := map[string]bool{}
		for k := range left {
			keys[k] = true
		}
		for k := range right {
			keys[k] = true
		}
		out := []string{}
		for k := range keys {
			out = append(out, liveAuthChangedPaths(left[k], right[k], path+"/"+k)...)
		}
		sort.Strings(out)
		return out
	}
	la, lok := before.([]any)
	ra, rok := after.([]any)
	if lok && rok && len(la) == len(ra) {
		out := []string{}
		for i := range la {
			out = append(out, liveAuthChangedPaths(la[i], ra[i], fmt.Sprintf("%s/[%d]", path, i))...)
		}
		return out
	}
	return []string{path}
}

func liveAuthPayload(p authProfile, wake bool) []byte {
	item := map[string]any{"id": p.ID, "authenticationProfileName": p.Name, "authenticationOrder": *p.Order, "dot1xToMabFallbackTimeout": *p.Timeout, "wakeOnLan": wake, "numberOfHosts": *p.Hosts, "preAuthAcl": p.ACL}
	if p.Voice != nil {
		item["isVoiceVlanEnabled"] = p.Voice
	}
	raw, _ := json.Marshal([]any{item})
	return raw
}

func liveRawAuthCatalog(ctx context.Context, c *client) (map[string]map[string]any, error) {
	return liveRawAuthScope(ctx, c, "")
}

func liveRawAuthScope(ctx context.Context, c *client, fabricID string) (map[string]map[string]any, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	query := url.Values{"isGlobalAuthenticationProfile": {"true"}}
	if fabricID != "" {
		if !validAPIID(fabricID) {
			return nil, errInvalidResponse
		}
		query = url.Values{"fabricId": {fabricID}, "isGlobalAuthenticationProfile": {"false"}}
	}
	rows, err := listInventoryQueryWithGet(ctx, get, authProfilesPath, query, func(raw json.RawMessage) string {
		var row struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &row)
		return row.ID
	})
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, raw := range rows {
		var row map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&row) != nil {
			return nil, errInvalidResponse
		}
		id, _ := row["id"].(string)
		if !validAPIID(id) || out[id] != nil {
			return nil, errInvalidResponse
		}
		if acl, ok := row["preAuthAcl"].(map[string]any); ok {
			if contracts, ok := acl["accessContracts"].([]any); ok {
				seen := map[string]bool{}
				for _, entry := range contracts {
					item, ok := entry.(map[string]any)
					if !ok {
						return nil, errInvalidResponse
					}
					port, ok := item["port"].(string)
					if !ok || seen[port] {
						return nil, errInvalidResponse
					}
					seen[port] = true
				}
				sort.Slice(contracts, func(i, j int) bool {
					return contracts[i].(map[string]any)["port"].(string) < contracts[j].(map[string]any)["port"].(string)
				})
			}
		}
		out[id] = row
	}
	return out, nil
}

type liveAuthTemplateTrace struct {
	base    http.RoundTripper
	client  *client
	before  map[string]map[string]any
	payload []byte
	writes  int
}

func (trace *liveAuthTemplateTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.URL.Path != authPath {
		if req.Method != http.MethodPut || req.URL.Path != authProfilesPath {
			return nil, errors.New("auth qualification refused an out-of-scope write")
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
		var actual, expected any
		if json.Unmarshal(body, &actual) != nil || json.Unmarshal(trace.payload, &expected) != nil || !reflect.DeepEqual(actual, expected) {
			return nil, errors.New("auth qualification refused unexpected profile payload")
		}
		sites, err := trace.client.fabricSites(req.Context())
		if err != nil || len(sites) != 0 {
			return nil, errors.New("auth qualification requires no fabric sites")
		}
		zones, err := trace.client.fabricZones(req.Context())
		if err != nil || len(zones) != 0 {
			return nil, errors.New("auth qualification requires no fabric zones")
		}
		current, err := liveRawAuthCatalog(req.Context(), trace.client)
		if err != nil || !reflect.DeepEqual(current, trace.before) {
			return nil, errors.New("auth qualification baseline changed before dispatch")
		}
		trace.writes++
	}
	return trace.base.RoundTrip(req)
}

func TestLiveCatalystCenterAuthTemplatesExecution(t *testing.T) {
	if os.Getenv("CATC_AUTH_TEMPLATE_EXECUTION") != "isolated-no-fabrics" {
		t.Skip("unused global profile qualification not enabled")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	sites, err := c.fabricSites(ctx)
	if err != nil || len(sites) != 0 {
		t.Fatal("global test requires no fabric sites", err)
	}
	zones, err := c.fabricZones(ctx)
	if err != nil || len(zones) != 0 {
		t.Fatal("global test requires no fabric zones", err)
	}
	baseline, err := liveRawAuthCatalog(ctx, c)
	if err != nil || len(baseline) != 3 {
		t.Fatal("global baseline unavailable", err)
	}
	saveLiveAuthBaseline(t, baseline)
	profiles, err := c.listAuthTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var original authProfile
	for _, p := range profiles {
		if p.Name == "Low Impact" {
			original = p
		}
	}
	if !validAPIID(original.ID) || original.Wake == nil {
		t.Fatal("Low Impact identity unavailable")
	}
	originalWake := *original.Wake
	t.Log("unused global profile baseline", original.ID, "original wakeOnLan", originalWake)
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &liveAuthTemplateTrace{base: base, client: c, before: baseline}
	rest.HTTPClient.Transport = trace
	r, config, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	a := r.a
	a.client = c
	config.Spec.Mode = configapi.NetworkControllerApplyModeApply
	config.Spec.ManagedSections = []string{"authentication_templates"}
	key := ctrlclient.ObjectKeyFromObject(config)
	restored := false
	t.Cleanup(func() {
		if !restored {
			t.Log("global qualification not fully restored; retain original task evidence and inspect before another write", original.ID, "original wakeOnLan", originalWake)
		}
	})
	for step := 0; step < 2; step++ {
		wake := !originalWake
		if step == 1 {
			wake = originalWake
		}
		model := authTemplateModel(original)
		model["wake_on_lan"] = wake
		model["pre_auth_acl"].(map[string]any)["description"] = "CVK ignored model description"
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"authentication_templates": []any{model}}})
		trace.payload = liveAuthPayload(original, wake)
		config.Generation++
		config.Spec.Source.Inline = &runtime.RawExtension{Raw: raw}
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			if state != nil && state.Operation != nil {
				op := state.Operation
				parts, err := parseGlobalAuthOperationResource(op.Resource)
				if err != nil || parts[0] != authConfigurationValueDigest(original.Name) || op.TargetID != original.ID || op.ParentID != "" {
					t.Fatal("public global authentication journal lost its versioned result/UUID binding")
				}
				if op.TaskID != "" && !seen[op.TaskID] {
					seen[op.TaskID] = true
					t.Log("global auth operation", op.TargetID, op.TaskID)
				}
				if op.Phase == "Unknown" || op.Phase == "Submitting" {
					t.Fatal("uncertain global auth operation retained", op.ID, op.TaskID)
				}
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				break
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
				t.Fatal("global auth qualification failed", config.Status.Conditions)
			}
			select {
			case <-ctx.Done():
				t.Fatal("global auth deadline exceeded", config.Status.Conditions)
			case <-time.After(time.Second):
			}
		}
		if len(seen) != 1 || trace.writes != step+1 || journalState(t, a).Operation != nil {
			t.Fatal("global auth phase did not complete exactly one operation")
		}
		expected := map[string]map[string]any{}
		for id, row := range baseline {
			copy := map[string]any{}
			for k, v := range row {
				copy[k] = v
			}
			expected[id] = copy
		}
		expected[original.ID]["wakeOnLan"] = wake
		current, err := liveRawAuthCatalog(ctx, c)
		if err != nil || !reflect.DeepEqual(current, expected) {
			for id, row := range expected {
				t.Log("unexpected raw field paths", id, liveAuthChangedPaths(row, current[id], ""))
			}
			t.Fatal("global auth changed fields beyond the selected wakeOnLan value", err)
		}
		trace.before = current
		for range 2 {
			fresh := *a
			if _, err := (&configReconciler{a: &fresh}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
		}
		if trace.writes != step+1 || journalState(t, a).Operation != nil {
			t.Fatal("restarted global auth no-op replayed")
		}
		t.Log("global auth phase verified", step, "unmanaged descriptions and other profiles unchanged")
	}
	final, err := liveRawAuthCatalog(ctx, c)
	if err != nil || !reflect.DeepEqual(final, baseline) {
		t.Fatal("global auth original baseline not restored", err)
	}
	restored = true
	t.Log("original complete global authentication baseline restored; no fabric or device operation submitted")
}

// Restore only the original Wake-on-LAN value from a previously acknowledged
// qualification. This is a new correlated operation, never a replay or journal
// deletion. Its exact UUID, task and original value must be supplied explicitly.
func TestLiveCatalystCenterAuthTemplateRestoreWake(t *testing.T) {
	if os.Getenv("CATC_AUTH_TEMPLATE_RESTORE") != "owned-no-fabrics" {
		t.Skip("explicit owned restoration not enabled")
	}
	id, taskID := os.Getenv("CATC_AUTH_TEMPLATE_RESTORE_ID"), os.Getenv("CATC_AUTH_TEMPLATE_RESTORE_TASK")
	wakeText := os.Getenv("CATC_AUTH_TEMPLATE_RESTORE_WAKE")
	if !validAPIID(id) || !validAPIID(taskID) || wakeText != "false" && wakeText != "true" {
		t.Fatal("exact restoration binding missing")
	}
	wake := wakeText == "true"
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	raw, err := c.get(ctx, taskPath+taskID, nil, http.StatusOK)
	if err != nil {
		t.Fatal(err)
	}
	var ack struct {
		Response struct {
			ID    string `json:"id"`
			Start int64  `json:"startTime"`
			End   int64  `json:"endTime"`
			Error bool   `json:"isError"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &ack) != nil || ack.Response.ID != taskID || ack.Response.Start == 0 || ack.Response.End == 0 || ack.Response.Error {
		t.Fatal("original task is not successful and terminal")
	}
	if done, err := c.configurationTaskTreeComplete(ctx, taskID, time.UnixMilli(ack.Response.Start), time.Now()); err != nil || !done {
		t.Fatal("original task tree unavailable", err)
	}
	baseline, err := liveRawAuthCatalog(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	saveLiveAuthBaseline(t, baseline)
	profiles, err := c.listAuthTemplates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var current authProfile
	for _, p := range profiles {
		if p.ID == id && p.Name == "Low Impact" {
			current = p
		}
	}
	if current.Wake == nil || *current.Wake == wake {
		t.Fatal("captured owned change not present; no restoration submitted")
	}
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &liveAuthTemplateTrace{base: base, client: c, before: baseline, payload: liveAuthPayload(current, wake)}
	rest.HTTPClient.Transport = trace
	want := current
	want.Wake = &wake
	e := &authConfigurationExecutor{client: c, desired: []authProfile{want}}
	write, err := e.next(ctx)
	if err != nil || write == nil {
		t.Fatal("restoration plan unavailable", err)
	}
	submitted := time.Now()
	task, err := write.submit(ctx)
	if err != nil {
		t.Fatal("restoration outcome uncertain; retain task evidence", err)
	}
	t.Log("owned wake restoration accepted", id, task.ID)
	for {
		done, err := c.configurationTaskTreeComplete(ctx, task.ID, submitted, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("restoration task did not complete")
		case <-time.After(time.Second):
		}
	}
	op := &ciskov1.NetworkControllerConfigurationOperation{Action: write.action, Resource: write.resource, TargetID: write.identity.target}
	if ok, err := e.verify(ctx, op); err != nil || !ok {
		t.Fatal("restoration readback unavailable", err)
	}
	after, err := liveRawAuthCatalog(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for key, row := range baseline {
		expected := map[string]any{}
		for k, v := range row {
			expected[k] = v
		}
		if key == id {
			expected["wakeOnLan"] = wake
		}
		t.Log("restoration additional field paths", key, liveAuthChangedPaths(expected, after[key], ""))
	}
	t.Log("original owned Wake-on-LAN value restored", id, wake, "native writes", trace.writes)
}
