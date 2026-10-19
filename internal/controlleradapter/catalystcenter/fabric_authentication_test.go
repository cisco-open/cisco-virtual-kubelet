// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func fabricAuthModel(t *testing.T, name string, bpdu any) []byte {
	t.Helper()
	model := []byte(authModel)
	if name == "Low Impact" {
		model = lowImpactAuthModel()
	}
	profiles, err := parseAuthTemplates(model)
	if err != nil {
		t.Fatal(err)
	}
	p := authTemplateModel(profiles[0])
	p["name"] = name
	if bpdu != nil {
		p["bpdu_guard"] = bpdu
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"fabric_sites": []any{map[string]any{"name": "Global/Lab", "pub_sub_enabled": false, "authentication_template": p}}}}})
	return raw
}

func fabricAuthCompiled(t *testing.T, c *client, raw []byte) *fabricAuthConfiguration {
	t.Helper()
	e, err := c.compileFabricConfiguration(raw)
	if err != nil {
		t.Fatal(err)
	}
	gated, ok := e.configurationExecutor.(*fabricAuthConfiguration)
	if !ok {
		t.Fatal("fabric authentication admission gate missing")
	}
	return gated
}

func TestFabricAuthenticationModelAndWholeCompositionGate(t *testing.T) {
	for _, name := range []string{"Closed Authentication", "Open Authentication", "Low Impact"} {
		t.Run(name, func(t *testing.T) {
			var bpdu any
			if name == "Closed Authentication" {
				bpdu = false
			}
			c := &client{}
			e := fabricAuthCompiled(t, c, fabricAuthModel(t, name, bpdu))
			if e.count() != 2 || len(e.desired) != 1 || e.desired[0].profile.Name != name || name == "Closed Authentication" && (e.desired[0].profile.BPDU == nil || *e.desired[0].profile.BPDU) {
				t.Fatal("pinned override fields lost")
			}
			if w, err := e.next(context.Background()); w != nil || !errors.Is(err, errConfigurationDeviceAdmission) {
				t.Fatal("unadmitted overrides allowed prerequisite writes", err)
			}
			if ok, err := e.verify(context.Background(), nil); ok || !errors.Is(err, errConfigurationDeviceAdmission) {
				t.Fatal("public gate allowed unadmitted verification", err)
			}
		})
	}
	for _, raw := range [][]byte{
		fabricAuthModel(t, "Low Impact", true),
		fabricAuthModel(t, "Open Authentication", false),
		fabricAuthModel(t, "Closed Authentication", "false"),
		fabricAuthModel(t, "No Authentication", nil),
		[]byte(strings.Replace(string(fabricAuthModel(t, "Closed Authentication", false)), `"authentication_order":"dot1x",`, "", 1)),
		[]byte(strings.Replace(string(fabricAuthModel(t, "Closed Authentication", false)), `"bpdu_guard":false`, `"unknown":false`, 1)),
	} {
		if _, err := (&client{}).compileFabricConfiguration(raw); err == nil {
			t.Fatal("invalid or unresolved override accepted")
		}
	}
}

type fabricAuthFixture struct {
	sites           []Site
	fabrics         []fabricSite
	profile         authProfile
	otherProfiles   map[string]authProfile
	reads           int
	writes          int
	lostAck         bool
	onRead          func()
	zones           []fabricZone
	inventory       []Device
	memberships     map[string][]fabricDeviceMembership
	inventoryReads  int
	onInventoryRead func()
	afterWrite      func()
	taskRows        []map[string]any
	onTaskRead      func()
}

func newFabricAuthFixture(t *testing.T, name string) (*client, *fabricAuthFixture, []fabricAuthIntent) {
	t.Helper()
	e := fabricAuthCompiled(t, &client{}, fabricAuthModel(t, name, nil))
	desired := e.desired
	raw, _ := json.Marshal(desired[0].profile)
	f := &fabricAuthFixture{sites: sampleSites()}
	_ = json.Unmarshal(raw, &f.profile)
	f.profile.ID, f.profile.FabricID = "profile-id", "fabric-id"
	pubsub, hosts, voice, bpdu := false, "Unlimited", true, true
	f.profile.Hosts, f.profile.Voice = &hosts, &voice
	if name == "Closed Authentication" {
		f.profile.BPDU = &bpdu
	}
	if f.profile.ACL != nil {
		description := "native ACL description"
		f.profile.ACL.Description = &description
	}
	f.fabrics = []fabricSite{{ID: "fabric-id", SiteID: "area", Authentication: name, PubSub: &pubsub}}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case sitesPath:
			_ = json.NewEncoder(w).Encode(map[string]any{"response": f.sites})
		case fabricSitesPath:
			if r.Method != http.MethodGet {
				t.Error("fabric authentication wrote foundation resources")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": f.fabrics})
		case fabricZonesPath:
			rows := f.zones
			if rows == nil {
				rows = []fabricZone{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case fabricDevicesPath:
			rows := f.memberships[r.URL.Query().Get("fabricId")]
			if rows == nil {
				rows = []fabricDeviceMembership{}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
		case devicesPath:
			f.inventoryReads++
			if f.onInventoryRead != nil {
				f.onInventoryRead()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": f.inventory})
		case taskPath + "fabric-auth-task/tree":
			if f.onTaskRead != nil {
				f.onTaskRead()
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": f.taskRows})
		case authProfilesPath:
			if r.Method == http.MethodGet {
				fabricID := r.URL.Query().Get("fabricId")
				if fabricID == "" || r.URL.Query().Get("isGlobalAuthenticationProfile") != "false" {
					t.Error("fabric profile query lost scope")
				}
				f.reads++
				if f.onRead != nil {
					f.onRead()
				}
				rows := []authProfile{}
				if fabricID == f.profile.FabricID {
					rows = append(rows, f.profile)
				} else if p, ok := f.otherProfiles[fabricID]; ok {
					rows = append(rows, p)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
				return
			}
			var rows []authProfile
			if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
				t.Error("unexpected scoped update")
				return
			}
			p := rows[0]
			if p.ID != f.profile.ID || p.FabricID != "fabric-id" || p.Name != name || p.Voice == nil || !*p.Voice || name == "Low Impact" && (p.ACL.Description == nil || *p.ACL.Description != "native ACL description") {
				t.Error("native scope or unmanaged metadata not preserved")
			}
			f.profile = p
			f.writes++
			if f.afterWrite != nil {
				f.afterWrite()
			}
			if f.lostAck {
				fmt.Fprint(w, `{}`)
			} else {
				fmt.Fprint(w, `{"response":{"taskId":"fabric-auth-task"}}`)
			}
		default:
			t.Error("unexpected endpoint")
			http.NotFound(w, r)
		}
	})
	return c, f, desired
}

func TestFabricAuthenticationWholeSelectionAndLateRecoveryChanges(t *testing.T) {
	for _, mode := range []string{"later-profile", "later-site", "mid-observation", "late-recovery-profile", "late-recovery-values"} {
		t.Run(mode, func(t *testing.T) {
			c, f, desired := newFabricAuthFixture(t, "Closed Authentication")
			later := desired[0]
			later.site.path = "Global/Other"
			desired = append(desired, later)
			f.sites = append(f.sites, Site{ID: "other-area", Name: "Other", NameHierarchy: "Global/Other", ParentID: "root", Type: "area"})
			pubsub := false
			f.fabrics = append(f.fabrics, fabricSite{ID: "other-fabric", SiteID: "other-area", Authentication: "Closed Authentication", PubSub: &pubsub})
			other := f.profile
			other.ID, other.FabricID = "other-profile", "other-fabric"
			f.otherProfiles = map[string]authProfile{"other-fabric": other}
			allow := func(context.Context) error { return nil }
			write, err := c.planFabricAuthentication(context.Background(), desired, allow)
			if err != nil || write == nil {
				t.Fatal("whole selection plan failed", err)
			}
			switch mode {
			case "later-profile":
				other.ID = "replacement-later-profile"
				f.otherProfiles["other-fabric"] = other
			case "later-site":
				f.sites[3].ID = "replacement-other-area"
				f.fabrics[1].SiteID = "replacement-other-area"
			case "mid-observation":
				start := f.reads
				f.onRead = func() {
					if f.reads == start+2 {
						f.profile.ID = "replacement-first-profile"
					}
				}
			}
			_, err = write.submit(context.Background())
			if !strings.HasPrefix(mode, "late-recovery-") {
				if err == nil || f.writes != 0 {
					t.Fatal("whole selection changed before dispatch", err, f.writes)
				}
				return
			}
			if err != nil || f.writes != 1 {
				t.Fatal("update failed before recovery", err)
			}
			if f.profile.BPDU == nil || !*f.profile.BPDU {
				t.Fatal("omitted BPDU override did not preserve the native value")
			}
			start := f.reads
			f.onRead = func() {
				if f.reads == start+3 {
					if mode == "late-recovery-profile" {
						f.profile.ID = "replacement-profile"
					} else {
						hosts := "Unlimited"
						f.profile.Hosts = &hosts
					}
				}
			}
			op := &cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
			if ok, err := c.verifyFabricAuthentication(context.Background(), desired, op); ok || mode == "late-recovery-profile" && err == nil {
				t.Fatal("late recovery change accepted", ok, err)
			}
		})
	}
}

func TestFabricAuthenticationReaderRejectsWrongScopeAndIncompleteResults(t *testing.T) {
	for _, mode := range []string{"valid", "global", "foreign-fabric", "duplicate", "partial", "missing-id", "missing-field", "bpdu-on-open", "missing-response"} {
		t.Run(mode, func(t *testing.T) {
			profiles, _ := parseAuthTemplates([]byte(authModel))
			p := profiles[0]
			p.ID, p.FabricID = "profile-id", "fabric-id"
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != authProfilesPath || r.URL.Query().Get("fabricId") != "fabric-id" || r.URL.Query().Get("isGlobalAuthenticationProfile") != "false" {
					t.Error("scoped reader sent unexpected request")
				}
				switch mode {
				case "global":
					p.FabricID = ""
				case "foreign-fabric":
					p.FabricID = "other-fabric"
				case "missing-id":
					p.ID = ""
				case "missing-field":
					p.Wake = nil
				case "bpdu-on-open":
					v := true
					p.Name, p.BPDU = "Open Authentication", &v
				case "partial":
					w.WriteHeader(http.StatusPartialContent)
				case "missing-response":
					fmt.Fprint(w, `{}`)
					return
				}
				rows := []authProfile{p}
				if mode == "duplicate" {
					q := p
					q.ID = "other-profile"
					rows = append(rows, q)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.listAuthProfiles(context.Background(), "fabric-id")
			if mode == "valid" {
				if err != nil || len(rows) != 1 {
					t.Fatal("valid scoped read failed", err)
				}
			} else if err == nil || rows != nil {
				t.Fatal("unscoped or incomplete profile evidence accepted", err)
			}
		})
	}
}

func TestFabricAuthenticationRecoveryRefusesForeignOperationBindings(t *testing.T) {
	for _, mode := range []string{"nil", "action", "target", "parent", "hierarchy", "resource", "serial", "duplicate-source"} {
		t.Run(mode, func(t *testing.T) {
			c, f, desired := newFabricAuthFixture(t, "Closed Authentication")
			f.profile.Hosts = desired[0].profile.Hosts
			op := &cvk.NetworkControllerConfigurationOperation{Action: "Update", TargetID: "profile-id", ParentID: "fabric-id", Resource: desired[0].resource("area")}
			switch mode {
			case "nil":
				op = nil
			case "action":
				op.Action = "Create"
			case "target":
				op.TargetID = "foreign-profile"
			case "parent":
				op.ParentID = "foreign-fabric"
			case "hierarchy":
				op.Resource = desired[0].resource("foreign-area")
			case "resource":
				op.Resource = "fabric-auth:legacy"
			case "serial":
				op.TargetSerial = "device-serial"
			case "duplicate-source":
				desired = append(desired, desired[0])
			}
			if ok, err := c.verifyFabricAuthentication(context.Background(), desired, op); ok || err == nil || f.writes != 0 {
				t.Fatal("foreign operation binding accepted", ok, err)
			}
		})
	}
}

func TestFabricAuthenticationReservedJournalCannotFallThrough(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting", "Accepted", "Verifying", "Verified", "Unknown"} {
		for _, resource := range []string{"fabric-auth:v1:malformed", "fabric-auth:future:unknown"} {
			t.Run(phase+"/"+resource, func(t *testing.T) {
				ctx := context.Background()
				a, config := journalFixture(t)
				if _, err := a.prepareConfiguration(ctx, config, nil, "sites", "ordinary-placeholder", "Update"); err != nil {
					t.Fatal(err)
				}
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				op := nc.Status.Configuration.Operation
				op.Section, op.Resource, op.Phase = "fabric", resource, phase
				op.TargetID, op.ParentID = "profile-id", "fabric-id"
				// Missing/pruned group metadata cannot authorize logical completion.
				op.PhysicalGroup = nil
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				before := journalState(t, a).Operation.DeepCopy()
				status := &configapi.NetworkControllerConfigStatus{}
				_, pending, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
				if !pending || reason != "GroupReservationRecoveryRequired" || status.Phase != configapi.NetworkControllerConfigPhaseWaiting {
					t.Fatal("reserved operation escaped physical recovery", pending, reason, status.Phase)
				}
				for _, err := range []error{a.startConfiguration(ctx, config, nil, before.ID), a.advanceConfiguration(ctx, before.ID, "Unknown", ""), a.finishConfiguration(ctx, before.ID)} {
					if !errors.Is(err, errConfigurationDeviceAdmission) {
						t.Fatal("legacy helper accepted staged physical operation", err)
					}
				}
				if !reflect.DeepEqual(before, journalState(t, a).Operation) || validConfigurationGroupAction("fabric", resource, "Update") {
					t.Fatal("unadmitted journal changed or public action enabled")
				}
			})
		}
	}
}

func TestFabricAuthenticationPrivateLifecycleAndRecovery(t *testing.T) {
	for _, name := range []string{"Closed Authentication", "Open Authentication", "Low Impact"} {
		t.Run(name, func(t *testing.T) {
			c, f, desired := newFabricAuthFixture(t, name)
			ctx := context.Background()
			if name == "Closed Authentication" {
				bpdu := false
				desired[0].profile.BPDU = &bpdu
			}
			gate := &fabricAuthConfiguration{client: c, desired: desired}
			if drift, err := gate.report(ctx); err != nil || len(drift) != 1 || f.writes != 0 {
				t.Fatal("scoped report failed", err)
			}
			allow := func(context.Context) error { return nil }
			write, err := c.planFabricAuthentication(ctx, desired, allow)
			if err != nil || write == nil || write.identity.target != "profile-id" || write.identity.parent != "fabric-id" || write.resource != desired[0].resource("area") {
				t.Fatal("scope was not durably bound", err)
			}
			task, err := write.submit(ctx)
			if err != nil || task.ID != "fabric-auth-task" || f.writes != 1 {
				t.Fatal("scoped update failed", err)
			}
			if name == "Closed Authentication" && (f.profile.BPDU == nil || *f.profile.BPDU) {
				t.Fatal("explicit false BPDU override lost")
			}
			raw, _ := json.Marshal(&cvk.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, ParentID: write.identity.parent, TargetID: write.identity.target})
			var recovered cvk.NetworkControllerConfigurationOperation
			_ = json.Unmarshal(raw, &recovered)
			future := desired[0]
			future.site.path = "Global/Missing"
			if ok, err := c.verifyFabricAuthentication(ctx, append(desired, future), &recovered); err != nil || !ok {
				t.Fatal("scoped serialized recovery failed", err)
			}
			if next, err := c.planFabricAuthentication(ctx, desired, allow); err != nil || next != nil {
				t.Fatal("did not converge", err)
			}
			reads := f.reads
			if _, err := write.submit(ctx); err == nil || f.writes != 1 || f.reads != reads {
				t.Fatal("consumed plan replayed")
			}
			f.sites[1].ID = "replacement-area"
			f.sites[2].ParentID = "replacement-area"
			f.fabrics[0].SiteID = "replacement-area"
			if ok, err := c.verifyFabricAuthentication(ctx, desired, &recovered); ok || err == nil {
				t.Fatal("same-named hierarchy replacement accepted")
			}
		})
	}
}

func TestFabricAuthenticationRefusesStaleOrUnadmittedSubmission(t *testing.T) {
	for _, mode := range []string{"site", "fabric", "profile", "description", "voice", "bpdu", "authority", "caller-mutation", "lost-ack", "nil-authority", "missing-fabric"} {
		t.Run(mode, func(t *testing.T) {
			name := "Low Impact"
			if mode == "bpdu" {
				name = "Closed Authentication"
			}
			c, f, desired := newFabricAuthFixture(t, name)
			allowed := true
			var authority func(context.Context) error = func(context.Context) error {
				if !allowed {
					return errConfigurationDeviceAdmission
				}
				return nil
			}
			if mode == "nil-authority" {
				authority = nil
			}
			if mode == "missing-fabric" {
				f.fabrics = []fabricSite{}
			}
			write, err := c.planFabricAuthentication(context.Background(), desired, authority)
			if mode == "nil-authority" || mode == "missing-fabric" {
				if err == nil || write != nil || f.writes != 0 {
					t.Fatal("unready or unadmitted scope planned")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal("missing update", err)
			}
			switch mode {
			case "site":
				f.sites[1].ID = "replacement-area"
				f.sites[2].ParentID = "replacement-area"
				f.fabrics[0].SiteID = "replacement-area"
			case "fabric":
				f.fabrics[0].ID = "replacement-fabric"
			case "profile":
				f.profile.ID = "replacement-profile"
			case "description":
				v := "concurrent native description"
				f.profile.ACL.Description = &v
			case "voice":
				v := false
				f.profile.Voice = &v
			case "bpdu":
				v := false
				f.profile.BPDU = &v
			case "authority":
				allowed = false
			case "caller-mutation":
				*desired[0].profile.Hosts = "Unlimited"
			case "lost-ack":
				f.lostAck = true
			}
			_, err = write.submit(context.Background())
			if mode == "caller-mutation" {
				if err != nil || f.writes != 1 || *f.profile.Hosts != "Single" {
					t.Fatal("compiled intent was mutable", err)
				}
			} else if err == nil || mode == "lost-ack" && f.writes != 1 || mode != "lost-ack" && f.writes != 0 {
				t.Fatal("stale or uncertain request accepted", err, f.writes)
			}
			reads := f.reads
			if _, err := write.submit(context.Background()); err == nil || f.reads != reads {
				t.Fatal("consumed fabric plan reused")
			}
		})
	}
}
