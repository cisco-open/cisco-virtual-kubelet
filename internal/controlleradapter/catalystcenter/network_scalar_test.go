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

func TestNetworkScalarSettings(t *testing.T) {
	for _, tc := range []struct {
		name, kind, apiKind, field, value, body string
		valid                                   bool
		drift                                   int
	}{
		{"timezone match", "timezone", "timeZone", "timezone", `"Europe/Berlin"`, `{"identifier":"Europe/Berlin"}`, true, 0},
		{"timezone changed", "timezone", "timeZone", "timezone", `"Europe/Berlin"`, `{"identifier":"GMT"}`, true, 1},
		{"timezone inherited", "timezone", "timeZone", "timezone", `"Europe/Berlin"`, `null`, true, 1},
		{"timezone unset", "timezone", "timeZone", "timezone", `"Europe/Berlin"`, `{}`, true, 1},
		{"timezone missing", "timezone", "timeZone", "timezone", `"Europe/Berlin"`, `{"unexpected":true}`, false, 0},
		{"timezone null", "timezone", "timeZone", "timezone", `"Europe/Berlin"`, `{"identifier":null}`, false, 0},
		{"banner match", "banner", "banner", "banner", `"Private banner\nSecond line"`, `{"type":"Custom","message":"Private banner\nSecond line"}`, true, 0},
		{"banner changed", "banner", "banner", "banner", `"Private banner"`, `{"type":"Custom","message":"other"}`, true, 1},
		{"banner builtin", "banner", "banner", "banner", `"Private banner"`, `{"type":"Builtin"}`, true, 1},
		{"banner empty explicit", "banner", "banner", "banner", `""`, `{"type":"Custom","message":""}`, true, 0},
		{"banner empty unset", "banner", "banner", "banner", `""`, `{}`, true, 1},
		{"banner inherited", "banner", "banner", "banner", `"Private banner"`, `null`, true, 1},
		{"banner missing message", "banner", "banner", "banner", `"Private banner"`, `{"type":"Custom"}`, false, 0},
		{"banner null message", "banner", "banner", "banner", `"Private banner"`, `{"type":"Custom","message":null}`, false, 0},
		{"banner unknown type", "banner", "banner", "banner", `"Private banner"`, `{"type":"Unknown"}`, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(networkReportModel, `"ntp_servers":["192.0.2.1"]`, `"`+tc.field+`":`+tc.value, 1)
			bindings, err := parseNetworkSettings([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != sitesPath+"/root/"+tc.apiKind+"Settings" || r.URL.Query().Get("_inherited") != "false" {
					t.Error("wrong request")
				}
				fmt.Fprintf(w, `{"response":{"%s":%s}}`, tc.apiKind, tc.body)
			})
			got, err := c.readNetworkSetting(context.Background(), "root", tc.kind)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if !tc.valid {
				return
			}
			drift := networkSettingDrift("Global", bindings[0].settings[0], got)
			if len(drift) != tc.drift {
				t.Fatalf("drift=%d", len(drift))
			}
			status, _ := json.Marshal(drift)
			if strings.Contains(string(status), "Private banner") || strings.Contains(string(status), "Europe/Berlin") {
				t.Fatal("values leaked")
			}
		})
	}
}

func TestNetworkScalarInvalidIntent(t *testing.T) {
	for _, field := range []string{`"timezone":""`, `"timezone":"../etc/passwd"`, `"timezone":17`, `"banner":true`, `"banner":"\u0000"`} {
		raw := strings.Replace(networkReportModel, `"ntp_servers":["192.0.2.1"]`, field, 1)
		if _, err := parseNetworkSettings([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid intent %s", field)
		}
	}
}

func TestNetworkScalarNullSelectsNothing(t *testing.T) {
	for _, field := range []string{`"timezone":null`, `"banner":null`} {
		raw := strings.Replace(networkReportModel, `"ntp_servers":["192.0.2.1"]`, field, 1)
		bindings, err := parseNetworkSettings([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if len(bindings) != 1 || len(bindings[0].settings) != 0 {
			t.Fatal("null scalar selected a native setting")
		}
	}
}
