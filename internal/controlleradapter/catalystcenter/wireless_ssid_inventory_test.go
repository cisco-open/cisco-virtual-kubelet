// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestWirelessSSIDInventoryRedactsSecrets(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != wirelessSSIDPath("global") || r.URL.Query().Get("offset") != "1" || r.URL.Query().Get("limit") != "500" {
			t.Error("unexpected inventory request", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"response":[{"id":"ssid-id","ssid":"CVK","authType":"WPA2_PERSONAL","wlanType":"Enterprise","passphrase":"secret-sentinel","unmappedSecret":"secret-sentinel","authServer":"","sessionTimeOut":0,"authServers":["192.0.2.2","192.0.2.1"],"inheritedSiteId":"global","isSensorPnp":false,"multiPSKSettings":[{"priority":1,"passphraseType":"ASCII","passphrase":"secret-sentinel","futureSecret":"secret-sentinel"}]}]}`)
	})
	rows, err := c.wirelessSSIDInventory(context.Background(), "global")
	if err != nil {
		t.Fatal(err)
	}
	p := rows["CVK"]
	if !p.mainCredential || !p.unmapped {
		t.Fatal("credential presence or unmapped-field guard lost")
	}
	raw, _ := json.Marshal(p.fields)
	if strings.Contains(string(raw), "secret-sentinel") || strings.Contains(string(raw), `"passphrase":`) {
		t.Fatal("returned credential retained")
	}
	if p.id != "ssid-id" || p.inheritedSiteID != "global" || p.sensor || p.fields["authServer"] != "" || p.fields["sessionTimeOut"] != int64(0) {
		t.Fatal("metadata or observation sentinel lost")
	}
	if _, err = c.wirelessSSIDInventory(context.Background(), "../global"); err == nil {
		t.Fatal("invalid site accepted")
	}
}

func TestWirelessSSIDInventoryRejectsDuplicateAndInvalidMetadata(t *testing.T) {
	for name, body := range map[string]string{
		"duplicate name":     `{"response":[{"id":"one","ssid":"CVK","authType":"OPEN","wlanType":"Enterprise"},{"id":"two","ssid":"CVK","authType":"OPEN","wlanType":"Enterprise"}]}`,
		"wrong boolean":      `{"response":[{"id":"one","ssid":"CVK","authType":"OPEN","wlanType":"Enterprise","isEnabled":"false"}]}`,
		"duplicate priority": `{"response":[{"id":"one","ssid":"CVK","authType":"OPEN","wlanType":"Enterprise","multiPSKSettings":[{"priority":0},{"priority":0}]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
				} else {
					fmt.Fprint(w, body)
				}
			})
			if _, err := c.wirelessSSIDInventory(context.Background(), "global"); err == nil {
				t.Fatal("invalid observation accepted")
			}
		})
	}
}

func TestWirelessSSIDApplianceExtensions(t *testing.T) {
	for _, tc := range []struct {
		name, extra       string
		invalid, unmapped bool
	}{
		{"empty placeholders", `"inheritedSiteUUID":null,"ipv6AclName":"","urlAclName":"","randomMacFilterEnabled":false,"isRandomMacFilterEnabled":false`, false, false},
		{"disabled load balancing", `"isLoadBalancingEnabledForAuthGroup":false,"isLoadBalancingEnabledForAcctGroup":false`, false, false},
		{"enabled auth balancing", `"isLoadBalancingEnabledForAuthGroup":true`, false, true},
		{"enabled accounting balancing", `"isLoadBalancingEnabledForAcctGroup":true`, false, true},
		{"malformed balancing", `"isLoadBalancingEnabledForAuthGroup":"false"`, true, false},
		{"foreign inheritance", `"inheritedSiteUUID":"other-site"`, false, false},
		{"conflicting inheritance", `"inheritedSiteUUID":"other-site","inheritedSiteId":"global"`, true, false},
		{"populated ipv6 ACL", `"ipv6AclName":"retained-policy"`, false, true},
		{"populated URL ACL", `"urlAclName":"retained-policy"`, false, true},
		{"conflicting flag", `"randomMacFilterEnabled":true,"isRandomMacFilterEnabled":false`, true, false},
		{"flag without canonical", `"randomMacFilterEnabled":false`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				fmt.Fprintf(w, `{"response":[{"id":"one","ssid":"CVK","authType":"OPEN","wlanType":"Enterprise",%s}]}`, tc.extra)
			})
			rows, err := c.wirelessSSIDInventory(context.Background(), "global")
			if tc.invalid {
				if err == nil {
					t.Fatal("unsafe extension accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			actual := rows["CVK"]
			if actual.unmapped != tc.unmapped {
				t.Fatal("unsupported policy guard lost")
			}
			if tc.name == "foreign inheritance" && actual.inheritedSiteID != "other-site" {
				t.Fatal("inheritance lost")
			}
			want := wirelessSSIDIntent{metadata: map[string]any{"ssid": "CVK", "authType": "OPEN", "wlanType": "Enterprise", "clientExclusionTimeout": int64(240)}}
			_, _, err = want.payload(nil, "", &actual)
			if (err != nil) != tc.unmapped {
				t.Fatal("unexpected update eligibility", err)
			}
		})
	}
}
