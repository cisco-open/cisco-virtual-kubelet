// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"
)

type liveAAARegistrationTrace struct {
	base       http.RoundTripper
	t          *testing.T
	redactions []string
}

func (trace *liveAAARegistrationTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := trace.base.RoundTrip(req)
	if req.Method == http.MethodPost && req.URL.Path == authenticationPolicyServersPath {
		logLiveAAAValidation(trace.t, response, trace.redactions)
	}
	return response, err
}

// Reuse the existing isolated registration helper as a lab prerequisite. It is
// deliberately separate from the public site-settings journal qualification.
func liveAAASettingsRegistrations(t *testing.T, c *client, ctx context.Context, areaID *string) map[string]bool {
	t.Helper()
	baseline, err := c.authenticationPolicyServers(ctx)
	if err != nil || len(baseline) != 0 {
		t.Fatal("isolated AAA qualification needs an empty registration baseline", err)
	}
	enabled, err := c.externalAuthentication(ctx)
	if err != nil || enabled {
		t.Fatal("isolated AAA qualification requires disabled external authentication", err)
	}
	owned := map[string]authenticationPolicyServer{}
	ids := map[string]bool{}
	settled := true
	rest, token, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &liveAAARegistrationTrace{base: base, t: t, redactions: []string{token, os.Getenv("CATC_PASSWORD")}}
	rest.HTTPClient.Transport = trace
	t.Cleanup(func() {
		if t.Failed() || !settled {
			t.Log("isolated AAA registration prerequisites retained", len(owned))
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		enabled, err := c.externalAuthentication(cleanup)
		if err != nil || enabled {
			t.Error("registration cleanup refused external authentication", err)
			return
		}
		sites, err := c.ListSites(cleanup)
		if err != nil {
			t.Error(err)
			return
		}
		for _, site := range sites {
			if site.ID == *areaID {
				t.Error("registration cleanup requires verified area removal")
				return
			}
		}
		for ip, expected := range owned {
			current, err := c.authenticationPolicyServers(cleanup)
			if err != nil || len(current) != len(owned) {
				t.Error("registration cleanup scope changed", err)
				return
			}
			for address, server := range current {
				known, exists := owned[address]
				if !exists || server.ID != known.ID || !policyServerMatches(known, server) {
					t.Error("registration cleanup refuses foreign/replaced metadata")
					return
				}
			}
			submitted := time.Now()
			raw, err := c.mutate(cleanup, http.MethodDelete, authenticationPolicyServersPath+"/"+expected.ID, nil)
			if err != nil {
				t.Error("registration cleanup uncertain; no replay", err)
				return
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				t.Error("registration cleanup missing receipt")
				return
			}
			if err := waitSiteCredentialTask(cleanup, c, receipt.Response, submitted); err != nil {
				t.Error(err)
				return
			}
			current, err = c.authenticationPolicyServers(cleanup)
			if err != nil {
				t.Error(err)
				return
			}
			if _, exists := current[ip]; exists {
				t.Error("registration deletion not verified")
				return
			}
			delete(owned, ip)
		}
		current, err := c.authenticationPolicyServers(cleanup)
		if err != nil || len(current) != 0 {
			t.Error("registration baseline not restored", err)
			return
		}
		t.Log("both isolated registration UUIDs deleted; original empty baseline restored")
	})
	for _, ip := range []string{"192.0.2.240", "192.0.2.241"} {
		want := policyServerFixture()
		port := 49
		want.IP, want.Protocol, want.Port = ip, "RADIUS_TACACS", &port
		nonce := make([]byte, 24)
		if _, err := rand.Read(nonce); err != nil {
			t.Fatal(err)
		}
		secret := hex.EncodeToString(nonce)
		trace.redactions = append(trace.redactions, secret)
		write, err := c.planAuthenticationPolicyServerRegistration(ctx, want, secret)
		if err != nil || write == nil {
			t.Fatal("isolated registration plan", err)
		}
		settled = false
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal("isolated registration outcome retained", err)
		}
		if err := waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
			t.Fatal("isolated registration task", err)
		}
		current, err := c.authenticationPolicyServers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		server, exists := current[ip]
		if !exists || !policyServerMatches(want, server) || !validAPIID(server.ID) || len(current) != len(owned)+1 {
			t.Fatal("isolated registration identity/metadata/baseline did not verify")
		}
		owned[ip], ids[server.ID] = server, true
		settled = true
		t.Log("isolated dual-protocol registration verified", server.ID)
	}
	return ids
}
