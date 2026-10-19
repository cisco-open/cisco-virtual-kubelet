// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestAnycastSecondaryReleaseMinimum(t *testing.T) {
	for _, version := range []string{"3.2.3", "3.2.3-75346.100", "3.2.3.1", "3.2.4", "3.3.1", "4.0.0"} {
		if !supportsAnycastSecondaryPools(version) {
			t.Errorf("valid minimum rejected: %s", version)
		}
	}
	for _, version := range []string{"", "2.3.7.9", "3.2.2-999999", "3.1.99", "3.2", " 3.2.3", "3.2.3-beta", "3.2.3-rc1", "v3.2.3", "3.2.3.1.2", "3.2.3\n", "999999999999999999999999.2.3"} {
		if supportsAnycastSecondaryPools(version) {
			t.Errorf("unsupported release accepted: %s", version)
		}
	}
}
func TestAnycastCapabilityEvidence(t *testing.T) {
	for _, mode := range []string{"supported", "ordinary", "old", "partial", "missing", "wrong-type", "release-changed"} {
		t.Run(mode, func(t *testing.T) {
			version := "3.2.3-75346.100"
			calls := 0
			if mode == "old" {
				version = "2.3.7.9"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != applianceReleasePath || r.Method != "GET" {
					t.Error("unexpected API")
					w.WriteHeader(404)
					return
				}
				calls++
				if mode == "partial" {
					w.WriteHeader(206)
				}
				if mode == "missing" {
					fmt.Fprint(w, `{"response":{}}`)
					return
				}
				if mode == "wrong-type" {
					fmt.Fprint(w, `{"response":{"installedVersion":323}}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]string{"installedVersion": version}})
			})
			want := anycastGatewayIntent{needsSecondaryPools: mode != "ordinary"}
			got, err := c.anycastCapabilityEvidence(context.Background(), want)
			valid := mode == "supported" || mode == "ordinary" || mode == "release-changed"
			if (err == nil) != valid {
				t.Fatal("unexpected capability result", err)
			}
			if mode == "ordinary" {
				if calls != 0 || got != "" {
					t.Fatal("ordinary intent unnecessarily queried release")
				}
				return
			}
			if valid && got != version {
				t.Fatal("release evidence lost")
			}
			if !valid && got != "" {
				t.Fatal("invalid capability returned evidence")
			}
			if mode == "release-changed" {
				version = "3.2.4-80000.100"
				next, err := c.anycastCapabilityEvidence(context.Background(), want)
				if err != nil || next == got || calls != 2 {
					t.Fatal("release evidence incorrectly cached", err)
				}
				if err := c.CheckStandardReloadProfile(context.Background(), "device"); err == nil {
					t.Fatal("anycast minimum broadened SWIM qualified-build fence")
				}
			}
		})
	}
}
