// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type lanStartFixture struct {
	onRead     func(*http.Request)
	changed    string
	usageReads int
	posts      int
	requests   int
	body       map[string]any
}

func newLANStartFixture(t *testing.T) (*client, lanSessionIntent, *lanStartFixture) {
	t.Helper()
	rows, _, err := parseLANSessionIntents([]byte(lanSessionModelFixture()), false)
	if err != nil {
		t.Fatal(err)
	}
	want := rows[0]
	want.Pools = []lanSessionPoolIntent{{Name: "access", Role: "PRINCIPAL_IP_ADDRESS_POOL"}}
	want.Password = "fixture-secret"
	state := &lanStartFixture{}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		state.requests++
		if r.Method == http.MethodGet && state.onRead != nil {
			state.onRead(r)
		}
		if r.Method == http.MethodPost {
			if r.URL.Path != lanSessionStartPath {
				t.Error("wrong start path", r.URL.Path)
				w.WriteHeader(404)
				return
			}
			state.posts++
			var rows []map[string]any
			if json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
				t.Error("bad start body")
			} else {
				state.body = rows[0]
			}
			if state.changed == "lost-receipt" {
				w.WriteHeader(500)
				return
			}
			if state.changed == "missing-receipt" {
				fmt.Fprint(w, `{"response":{}}`)
				return
			}
			if state.changed == "unexpected-accepted-status" {
				w.WriteHeader(202)
				fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				return
			}
			fmt.Fprint(w, `{"response":{"taskId":"task","url":"https://untrusted.example/task"}}`)
			return
		}
		if r.Method != http.MethodGet {
			t.Error("unexpected mutation", r.Method)
			w.WriteHeader(400)
			return
		}
		var response any
		switch r.URL.Path {
		case lanActiveSessionsPath:
			response = map[string]any{"maxSupportedCount": "5", "activeSessions": "0", "activeSessionIds": []string{}}
			if state.changed == "active" {
				response = map[string]any{"maxSupportedCount": "5", "activeSessions": "1", "activeSessionIds": []string{"other"}}
			}
			if state.changed == "disabled" {
				response = map[string]any{"maxSupportedCount": "0", "activeSessions": "0", "activeSessionIds": []string{}}
			}
		case sitesPath:
			response = sampleSites()
		case devicesPath:
			devices := []Device{{ID: "seed", ManagementIP: "192.0.2.10", Serial: "SEED-SERIAL"}, {ID: "peer", ManagementIP: "192.0.2.11", Serial: "PEER-SERIAL"}}
			if state.changed == "managed-discovery" {
				devices = append(devices, Device{ID: "existing", ManagementIP: "192.0.2.20", Serial: "FOC12345678"})
			}
			if state.changed == "seed-replaced" {
				devices[0].Serial = "OTHER-SEED"
			}
			response = devices
		case "/dna/intent/api/v1/interface/network-device/seed":
			ports := []lanLinkInterface{lanPortFixture("seed", "TenGigabitEthernet1/0/1")}
			if state.changed == "port-replaced" {
				ports[0].ID = "other-port"
			}
			response = ports
		case globalPoolsPath:
			response = fixturePools()
		case sitePoolsPath:
			pools := fixtureReservations()
			if state.changed == "foreign-pool" {
				pools[0].SiteID = "building"
			}
			response = pools
		case sitePoolsPath + "/reservation":
			state.usageReads++
			row := lanUsageFixture()
			space := row["ipV4AddressSpace"].(map[string]any)
			if state.changed == "exhausted" {
				space["assignedAddresses"] = "125"
			}
			if state.changed == "usage-race" && state.usageReads%2 == 0 {
				space["assignedAddresses"] = "3"
			}
			if state.changed == "usage-changed" {
				space["assignedAddresses"] = "3"
			}
			response = row
		case pnpDevicesPath:
			row := pnpFixture("pnp", "FOC12345678")
			info := row["deviceInfo"].(map[string]any)
			// Workflow state must be retained in the commitment; raw outputs and
			// credentials in this fixture must not enter dependency evidence.
			if state.changed == "claimed" {
				info["state"] = "Provisioned"
			}
			if state.changed == "auth-changed" {
				info["authStatus"] = "changed-auth"
			}
			if state.changed == "workflow-changed" {
				row["workflow"].(map[string]any)["state"] = "changed-state"
			}
			if state.changed == "pnp-replaced" {
				row["id"] = "other-pnp"
			}
			rows := []any{row}
			if state.changed == "missing-pnp" {
				rows = []any{}
			}
			json.NewEncoder(w).Encode(rows)
			return
		default:
			t.Error("unexpected API", r.URL)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": response})
	})
	return c, want, state
}

func TestLANSessionLaunchPreflight(t *testing.T) {
	for _, mode := range []string{"stable", "active", "disabled", "exhausted", "usage-race", "foreign-pool", "claimed", "missing-pnp", "managed-discovery", "unbounded"} {
		t.Run(mode, func(t *testing.T) {
			c, want, s := newLANStartFixture(t)
			s.changed = mode
			if mode == "unbounded" {
				want.Devices = nil
			}
			observed, err := c.lanSessionPreflight(context.Background(), want)
			if (err == nil) != (mode == "stable") {
				t.Fatal("wrong readiness outcome", err)
			}
			if err == nil {
				if len(observed.usage) != 1 || len(observed.discovery) != 1 || len(observed.observationDigest()) != 64 {
					t.Fatal("incomplete launch observation")
				}
				if strings.Contains(fmt.Sprintf("%+v %#v", observed, observed), want.Password) || strings.Contains(fmt.Sprintf("%+v", observed), "private-value") {
					t.Fatal("credential entered observation")
				}
			}
			if s.posts != 0 {
				t.Fatal("preflight submitted start")
			}
		})
	}
}

func TestLANSessionStartOneUseAndAuthority(t *testing.T) {
	for _, mode := range []string{"accepted", "nil-authority", "denied", "before-authority-change", "after-authority-change", "seed-replaced", "port-replaced", "pnp-replaced", "auth-changed", "workflow-changed", "usage-changed", "lost-receipt", "missing-receipt", "unexpected-accepted-status", "caller-mutated"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, want, s := newLANStartFixture(t)
			plan, err := c.planLANSessionStart(ctx, want)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(plan.resource, "lan-session-start:") || len(plan.observation) != 64 || strings.Contains(fmt.Sprintf("%+v", plan), want.Password) {
				t.Fatal("invalid nonsecret plan identity")
			}
			calls := 0
			authorize := func(context.Context) error {
				calls++
				if mode == "denied" {
					return errors.New("denied")
				}
				if mode == "after-authority-change" {
					s.changed = "active"
				}
				return nil
			}
			switch mode {
			case "nil-authority":
				authorize = nil
			case "before-authority-change":
				s.changed = "active"
			case "seed-replaced", "port-replaced", "pnp-replaced", "auth-changed", "workflow-changed", "usage-changed":
				s.changed = mode
			case "lost-receipt", "missing-receipt", "unexpected-accepted-status":
				s.changed = mode
			case "caller-mutated":
				want.Ports[0] = "OTHER"
				want.Devices[0].Serial = "OTHER-SERIAL"
				want.Password = "different-secret"
			}
			task, err := plan.submit(ctx, authorize)
			accepted := mode == "accepted" || mode == "caller-mutated"
			if (err == nil) != accepted {
				t.Fatal("wrong start result", err)
			}
			shouldPost := accepted || mode == "lost-receipt" || mode == "missing-receipt" || mode == "unexpected-accepted-status"
			if (s.posts == 1) != shouldPost {
				t.Fatal("native write escaped admission", s.posts)
			}
			if accepted && (task.ID != "task" || s.body["isisDomainPwd"] != "fixture-secret") {
				t.Fatal("immutable body/receipt lost")
			}
			if mode == "before-authority-change" && calls != 0 {
				t.Fatal("authority called on stale preflight")
			}
			prior := s.posts
			reads := s.requests
			if _, err := plan.submit(ctx, func(context.Context) error { return nil }); err == nil || s.posts != prior || s.requests != reads {
				t.Fatal("one-use plan replayed after outcome")
			}
		})
	}
}

func TestLANSessionStartObservationAndVersionCommitments(t *testing.T) {
	c, want, s := newLANStartFixture(t)
	ctx := context.Background()
	first, err := c.planLANSessionStart(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	s.changed = "auth-changed"
	changed, err := c.planLANSessionStart(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if first.resource == changed.resource || first.observation == changed.observation {
		t.Fatal("PnP auth metadata omitted from commitment")
	}
	version := *want.PasswordVersion + 1
	want.PasswordVersion = &version
	rotated, err := c.planLANSessionStart(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.resource == changed.resource || rotated.observation != changed.observation {
		t.Fatal("credential version commitment missing")
	}
	want.Password = "other-value"
	sameVersion, err := c.planLANSessionStart(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if sameVersion.resource != rotated.resource {
		t.Fatal("secret bytes entered persistent commitment")
	}
	if s.posts != 0 {
		t.Fatal("planning submitted native write")
	}
}

func TestLANSessionStartConcurrentConsumption(t *testing.T) {
	c, want, s := newLANStartFixture(t)
	ctx := context.Background()
	plan, err := c.planLANSessionStart(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var grants atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := plan.submit(ctx, func(context.Context) error { grants.Add(1); return nil }); err == nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 || grants.Load() != 1 || s.posts != 1 {
		t.Fatal("concurrent plan reuse", winners.Load(), grants.Load(), s.posts)
	}
}

func TestLANSessionStartRejectsUnversionedCredential(t *testing.T) {
	c, want, s := newLANStartFixture(t)
	want.PasswordVersion = nil
	if plan, err := c.planLANSessionStart(context.Background(), want); err == nil || plan != nil || s.requests != 0 {
		t.Fatal("unversioned projected credential reached planning", err)
	}
}
