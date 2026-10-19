// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// This is an empty controller-definition qualification, not a substitute for
// physical group admission. Every write is individually armed and must prove
// zero assigned/fabric devices, zero child scopes and unchanged global profiles.
type liveEmptyFabricAuthTrace struct {
	base                   http.RoundTripper
	client                 *client
	sites                  []Site
	devices                []Device
	global                 map[string]map[string]any
	path, siteID, fabricID string
	profileName            string
	scoped                 map[string]map[string]any
	method, endpoint       string
	body                   []byte
	armed                  bool
	writes                 int
	failureDiagnostic      string
}

func (h *liveEmptyFabricAuthTrace) scope(ctx context.Context) error {
	sites, err := h.client.ListSites(ctx)
	if err != nil {
		return err
	}
	remaining := []Site{}
	found := false
	for _, site := range sites {
		if site.ID == h.siteID && h.siteID != "" {
			if site.NameHierarchy != h.path || site.Type != "area" {
				return errConfigurationStale
			}
			found = true
			continue
		}
		if site.NameHierarchy == h.path || strings.HasPrefix(site.NameHierarchy, h.path+"/") {
			return errConfigurationStale
		}
		remaining = append(remaining, site)
	}
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].ID < remaining[j].ID })
	if found != (h.siteID != "") || !reflect.DeepEqual(remaining, h.sites) {
		return errConfigurationStale
	}
	devices, err := h.client.configurationDevices(ctx)
	if err != nil {
		return err
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	if !reflect.DeepEqual(devices, h.devices) {
		return errConfigurationStale
	}
	global, err := liveRawAuthCatalog(ctx, h.client)
	if err != nil || !reflect.DeepEqual(global, h.global) {
		return errors.New("global authentication baseline changed")
	}
	zones, err := h.client.fabricZones(ctx)
	if err != nil || len(zones) != 0 {
		return errors.New("empty-fabric qualification requires zero zones")
	}
	fabrics, err := h.client.fabricSites(ctx)
	if err != nil {
		return err
	}
	if h.fabricID == "" {
		if len(fabrics) != 0 {
			return errConfigurationStale
		}
	} else {
		fabric, ok := fabrics[h.siteID]
		if len(fabrics) != 1 || !ok || fabric.ID != h.fabricID || fabric.Authentication != h.profileName || fabric.PubSub == nil || *fabric.PubSub {
			return errConfigurationStale
		}
		members, err := h.client.fabricDeviceMemberships(ctx, h.fabricID)
		if err != nil || len(members) != 0 {
			return errors.New("fabric fixture has members; mutation refused")
		}
		if h.scoped != nil {
			current, err := liveRawAuthScope(ctx, h.client, h.fabricID)
			if err != nil || !reflect.DeepEqual(current, h.scoped) {
				return errors.New("fabric profile baseline changed before dispatch")
			}
		}
	}
	if h.siteID != "" {
		assigned, err := h.client.assignedDevices(ctx, h.siteID)
		if err != nil || len(assigned) != 0 {
			return errors.New("fabric fixture hierarchy has assigned devices")
		}
	}
	return nil
}

func (h *liveEmptyFabricAuthTrace) arm(method, endpoint string, body any) {
	h.method, h.endpoint, h.armed = method, endpoint, true
	h.body = nil
	if body != nil {
		h.body, _ = json.Marshal(body)
	}
}

func (h *liveEmptyFabricAuthTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet || req.Method == http.MethodPost && req.URL.Path == authPath {
		return h.base.RoundTrip(req)
	}
	if !h.armed || req.Method != h.method || req.URL.Path != h.endpoint || req.URL.RawQuery != "" {
		return nil, errors.New("empty-fabric qualification refused an unarmed write")
	}
	h.armed = false // A denied or uncertain attempt cannot be replayed.
	var raw []byte
	if req.Body != nil {
		var err error
		raw, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
	}
	if len(h.body) == 0 {
		if len(raw) != 0 {
			return nil, errors.New("empty-fabric delete carried unexpected data")
		}
	} else {
		var actual, expected any
		if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(h.body, &expected) != nil || !reflect.DeepEqual(actual, expected) {
			return nil, errors.New("empty-fabric write payload changed")
		}
	}
	if err := h.scope(req.Context()); err != nil {
		return nil, err
	}
	h.writes++
	response, err := h.base.RoundTrip(req)
	if err == nil && response != nil && response.StatusCode >= 400 && response.Body != nil {
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		_ = response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		response.Body = io.NopCloser(bytes.NewReader(raw))
		dir, fileErr := os.MkdirTemp("/private/tmp", "cvk-empty-fabric-response-")
		if fileErr != nil {
			return nil, fileErr
		}
		h.failureDiagnostic = filepath.Join(dir, "response.json")
		if fileErr := os.WriteFile(h.failureDiagnostic, raw, 0600); fileErr != nil {
			return nil, fileErr
		}
	}
	return response, err
}

func TestLiveFabricAuthenticationEmptyExecution(t *testing.T) {
	if os.Getenv("CATC_FABRIC_AUTH_EXECUTION") != "isolated-empty-fabric" {
		t.Skip("empty fabric definition qualification not enabled")
	}
	for _, name := range []string{"Closed Authentication", "Open Authentication", "Low Impact"} {
		if !t.Run(name, func(t *testing.T) { qualifyLiveEmptyFabricAuthentication(t, name) }) {
			break // Never create another fixture after an uncertain operation.
		}
	}
}

func qualifyLiveEmptyFabricAuthentication(t *testing.T, name string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	c := liveClient(t)
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].ID < sites[j].ID })
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	global, err := liveRawAuthCatalog(ctx, c)
	if err != nil || len(global) != 3 {
		t.Fatal("global baseline unavailable", err)
	}
	saveLiveAuthBaseline(t, global)
	fixtureName := "CVK-Q-Auth-" + time.Now().UTC().Format("20060102-150405-000000000")
	path := "Global/" + fixtureName
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	h := &liveEmptyFabricAuthTrace{base: base, client: c, sites: sites, devices: devices, global: global, path: path, profileName: name}
	rest.HTTPClient.Transport = h
	if err := h.scope(ctx); err != nil {
		t.Fatal("initial empty fabric scope unavailable", err)
	}
	settled, cleaned := true, false
	t.Cleanup(func() {
		if !cleaned {
			t.Log("retained isolated fixture; inspect before any further write", path, h.siteID, h.fabricID, "terminal observation", settled)
		}
	})
	wait := func(task Task, submitted time.Time) {
		t.Helper()
		if err := waitLiveEmptyFabricAuthTask(ctx, c, task, submitted); err != nil {
			t.Fatal("task incomplete or uncertain; fixture retained", task.ID, err)
		}
		settled = true
	}
	submit := func(method, endpoint string, body any) {
		t.Helper()
		h.arm(method, endpoint, body)
		settled = false
		submitted := time.Now()
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		response, err := c.mutate(ctx, method, endpoint, raw)
		if err != nil {
			t.Log("private response diagnostic", h.failureDiagnostic)
			t.Fatal("isolated operation uncertain; retain exact fixture", method, endpoint, err)
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(response, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Fatal("isolated operation receipt missing; retain fixture")
		}
		t.Log("isolated operation", method, endpoint, receipt.Response.ID)
		wait(receipt.Response, submitted)
	}
	desired := []desiredSite{{kind: "area", path: path, fields: map[string]any{}}}
	m, err := nextSiteMutation(desired, sites)
	if err != nil || m == nil || m.method != http.MethodPost {
		t.Fatal("isolated area plan missing", err)
	}
	var areaBody any
	if json.Unmarshal(m.body, &areaBody) != nil {
		t.Fatal("isolated area body invalid")
	}
	submit(m.method, m.endpoint, areaBody)
	currentSites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range currentSites {
		if s.NameHierarchy == path {
			h.siteID = s.ID
		}
	}
	if !validAPIID(h.siteID) {
		t.Fatal("isolated area readback missing")
	}
	enableLiveEmptyFabricWiredCollection(t, ctx, c, h, submit)
	pubsub := false
	submit(http.MethodPost, fabricSitesPath, []fabricSite{{SiteID: h.siteID, Authentication: name, PubSub: &pubsub}})
	fabrics, err := c.fabricSites(ctx)
	if err != nil || len(fabrics) != 1 || !validAPIID(fabrics[h.siteID].ID) {
		t.Fatal("isolated fabric identity unavailable", err)
	}
	h.fabricID = fabrics[h.siteID].ID
	t.Log("owned empty fabric", name, h.siteID, h.fabricID)
	profiles, err := c.listAuthProfiles(ctx, h.fabricID)
	if err != nil {
		t.Fatal("fabric authentication readback unavailable", err)
	}
	var original authProfile
	for _, p := range profiles {
		if p.Name == name {
			original = p
		}
	}
	if !validAuthProfile(original) || !validAPIID(original.ID) {
		t.Fatal("selected native fabric profile unavailable")
	}
	baseline, err := liveRawAuthScope(ctx, c, h.fabricID)
	if err != nil {
		t.Fatal(err)
	}
	saveLiveAuthBaseline(t, baseline)
	h.scoped = baseline
	for step := 0; step < 2; step++ {
		model := authTemplateModel(original)
		wake := *original.Wake
		if step == 0 {
			wake = !wake
		}
		model["wake_on_lan"] = wake
		if name == "Closed Authentication" && original.BPDU != nil {
			bpdu := *original.BPDU
			if step == 0 {
				bpdu = !bpdu
			}
			model["bpdu_guard"] = bpdu
		}
		if name == "Low Impact" {
			model["pre_auth_acl"].(map[string]any)["description"] = "CVK ignored source description"
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"fabric_sites": []any{map[string]any{"name": path, "pub_sub_enabled": false, "authentication_template": model}}}}})
		compiled, err := c.compileFabricConfiguration(raw)
		if err != nil {
			t.Fatal(err)
		}
		auth, ok := compiled.configurationExecutor.(*fabricAuthConfiguration)
		if !ok || len(auth.desired) != 1 {
			t.Fatal("fabric model lost its guarded override")
		}
		write, err := c.planFabricAuthentication(ctx, auth.desired, h.scope)
		if err != nil || write == nil || write.identity.target != original.ID || write.identity.parent != h.fabricID {
			t.Fatal("isolated fabric update plan unavailable", err)
		}
		item := map[string]any{"id": original.ID, "fabricId": h.fabricID, "authenticationProfileName": name, "authenticationOrder": *original.Order, "dot1xToMabFallbackTimeout": *original.Timeout, "wakeOnLan": wake, "numberOfHosts": *original.Hosts}
		if original.Voice != nil {
			item["isVoiceVlanEnabled"] = *original.Voice
		}
		if name == "Closed Authentication" && original.BPDU != nil {
			item["isBpduGuardEnabled"] = model["bpdu_guard"]
		}
		if name == "Low Impact" {
			item["preAuthAcl"] = original.ACL
		}
		h.arm(http.MethodPut, authProfilesPath, []any{item})
		settled = false
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal("fabric profile task uncertain; fixture retained", err)
		}
		t.Log("fabric authentication update", name, step, original.ID, task.ID)
		wait(task, submitted)
		op := &cvk.NetworkControllerConfigurationOperation{Action: "Update", Resource: write.resource, TargetID: original.ID, ParentID: h.fabricID}
		if verified, err := c.verifyFabricAuthentication(ctx, auth.desired, op); err != nil || !verified {
			t.Fatal("native fabric profile verification incomplete", err)
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
		if bpdu, ok := model["bpdu_guard"]; ok {
			expected[original.ID]["isBpduGuardEnabled"] = bpdu
		}
		actual, err := liveRawAuthScope(ctx, c, h.fabricID)
		if err != nil || !reflect.DeepEqual(actual, expected) {
			for id, row := range expected {
				t.Log("unexpected scoped raw field paths", id, liveAuthChangedPaths(row, actual[id], ""))
			}
			t.Fatal("native fabric update changed unexpected fields", err)
		}
		h.scoped = actual
		before := h.writes
		if next, err := c.planFabricAuthentication(ctx, auth.desired, h.scope); err != nil || next != nil || before != h.writes {
			t.Fatal("fresh fabric planning replayed a converged update", err)
		}
		t.Log("native fabric profile phase verified", name, step, "BPDU observed", original.BPDU != nil)
	}
	if err := h.scope(ctx); err != nil || !reflect.DeepEqual(h.scoped, baseline) {
		t.Fatal("original scoped baseline not restored", err)
	}
	submit(http.MethodDelete, fabricSitesPath+"/"+url.PathEscape(h.fabricID), nil)
	fabrics, err = c.fabricSites(ctx)
	if err != nil || len(fabrics) != 0 {
		t.Fatal("fabric cleanup not verified", err)
	}
	h.fabricID, h.scoped = "", nil
	submit(http.MethodDelete, "/dna/intent/api/v1/areas/"+url.PathEscape(h.siteID), nil)
	h.siteID = ""
	if err := h.scope(ctx); err != nil {
		t.Fatal("full isolated baseline restoration failed", err)
	}
	cleaned = true
	t.Log("empty fabric and area removed; global profiles, hierarchy and device baseline restored", name, "writes", h.writes)
}

// Task-tree publication can briefly lag a successful request. Reobserve only
// the same acknowledged task; never retry its mutation or conceal a task error.
func waitLiveEmptyFabricAuthTask(ctx context.Context, c *client, task Task, submitted time.Time) error {
	grace := time.Now().Add(5 * time.Second)
	for {
		done, err := c.configurationTaskTreeComplete(ctx, task.ID, submitted, time.Now())
		if err != nil && (!errors.Is(err, errInvalidResponse) || time.Now().After(grace)) {
			return err
		}
		if err == nil && done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// Resolve and retain all other effective telemetry settings using the existing
// executor, rather than inventing defaults or altering any parent site.
func enableLiveEmptyFabricWiredCollection(t *testing.T, ctx context.Context, c *client, h *liveEmptyFabricAuthTrace, mutate func(string, string, any)) {
	t.Helper()
	if !validAPIID(h.siteID) || h.fabricID != "" {
		t.Fatal("wired prerequisite requires exact empty area before fabric creation")
	}
	if err := h.scope(ctx); err != nil {
		t.Fatal("wired prerequisite scope changed", err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Site{}
	for _, s := range sites {
		byPath[s.NameHierarchy] = s
	}
	inherited, err := c.effectiveNetworkSetting(ctx, byPath, h.path, "telemetry")
	if err != nil {
		t.Fatal("resolved inherited telemetry unavailable", err)
	}
	desired := map[string]map[string]any{}
	for group, fields := range inherited.telemetry {
		copy := map[string]any{}
		for key, value := range fields {
			copy[key] = value
		}
		desired[group] = copy
	}
	if desired["wiredDataCollection"] == nil {
		t.Fatal("inherited telemetry values incomplete")
	}
	desired["wiredDataCollection"]["enableWiredDataCollection"] = true
	setting := networkSetting{kind: "telemetry", telemetry: desired}
	e := &networkConfigurationExecutor{client: c, bindings: []networkBinding{{path: h.path, kind: "area", settings: []networkSetting{setting}}}}
	write, err := e.next(ctx)
	if err != nil {
		t.Fatal("isolated wired prerequisite plan failed", err)
	}
	if write != nil {
		if write.identity.target != h.siteID {
			t.Fatal("wired prerequisite plan escaped owned area")
		}
		payload, err := networkSettingPayload(setting)
		if err != nil {
			t.Fatal(err)
		}
		var body any
		if json.Unmarshal(payload, &body) != nil {
			t.Fatal("telemetry payload invalid")
		}
		mutate(http.MethodPut, sitesPath+"/"+url.PathEscape(h.siteID)+"/telemetrySettings", body)
	}
	if next, err := e.next(ctx); err != nil || next != nil {
		t.Fatal("isolated wired collection did not converge", err)
	}
	current, err := c.effectiveNetworkSetting(ctx, byPath, h.path, "telemetry")
	if err != nil || current.telemetry["wiredDataCollection"]["enableWiredDataCollection"] != true {
		t.Fatal("effective wired prerequisite not verified", err)
	}
	t.Log("documented wired endpoint prerequisite and no-op verified on exact empty area", h.siteID)
}
