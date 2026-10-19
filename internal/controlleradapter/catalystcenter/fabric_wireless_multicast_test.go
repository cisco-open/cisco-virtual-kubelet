// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestFabricWirelessMulticastObservation(t *testing.T) {
	for _, tc := range []struct {
		name, body  string
		status      int
		want, valid bool
	}{
		{"enabled", `{"response":{"multicastEnabled":true}}`, 200, true, true},
		{"disabled", `{"response":{"multicastEnabled":false}}`, 200, false, true},
		{"missing-envelope", `{}`, 200, false, false},
		{"null-envelope", `{"response":null}`, 200, false, false},
		{"missing-setting", `{"response":{}}`, 200, false, false},
		{"null-setting", `{"response":{"multicastEnabled":null}}`, 200, false, false},
		{"string-setting", `{"response":{"multicastEnabled":"false"}}`, 200, false, false},
		{"array-envelope", `{"response":[]}`, 200, false, false},
		{"unsupported", `{}`, 404, false, false},
		{"failure", `{}`, 500, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast" || r.URL.RawQuery != "" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				reads++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			got, err := c.fabricWirelessMulticast(context.Background(), "fabric")
			if (err == nil) != tc.valid || got != tc.want || reads == 0 {
				t.Fatalf("observation = %v, %v; reads=%d", got, err, reads)
			}
		})
	}
}

func TestFabricWirelessMulticastInvalidIdentity(t *testing.T) {
	for _, id := range []string{"", "../fabric", "fabric/other", "fabric?other", "fabric\n"} {
		if path, err := fabricWirelessMulticastPath(id); err == nil || path != "" {
			t.Errorf("accepted invalid fabric ID %q", id)
		}
	}
}
