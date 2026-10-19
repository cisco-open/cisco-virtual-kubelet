//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type siteLifecycleRoundTripFunc func(*http.Request) (*http.Response, error)

func (f siteLifecycleRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSiteHierarchyNativeAllowlist(t *testing.T) {
	forwarded := 0
	trace := &siteLifecycleNativeTrace{maxPosts: 1, allowedDeletes: map[string]bool{}, base: siteLifecycleRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		forwarded++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}, Request: r}, nil
	})}
	trace.allowCreate = func(path string, body map[string]any) bool {
		return path == siteDesignMutationEndpoint("floor") && len(body) == 2 && body["name"] == "GeneratedFloor" && body["parentId"] == "owned-building"
	}
	send := func(method, path, body string, want bool) {
		t.Helper()
		r, err := http.NewRequest(method, "https://example.invalid"+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := trace.RoundTrip(r)
		if (err == nil) != want {
			t.Fatalf("unexpected allowlist result for %s %s: %v", method, path, err)
		}
		if resp != nil {
			resp.Body.Close()
		}
	}
	send("POST", siteDesignMutationEndpoint("floor"), `{"name":"Foreign","parentId":"owned-building"}`, false)
	send("POST", siteDesignMutationEndpoint("floor"), `{"name":"GeneratedFloor","parentId":"foreign-building"}`, false)
	send("PUT", siteDesignMutationEndpoint("floor"), `{"name":"GeneratedFloor","parentId":"owned-building"}`, false)
	send("POST", siteDesignMutationEndpoint("building"), `{"name":"GeneratedFloor","parentId":"owned-building"}`, false)
	send("POST", siteDesignMutationEndpoint("floor"), `{"name":"GeneratedFloor","parentId":"owned-building","extra":true}`, false)
	send("POST", siteDesignMutationEndpoint("floor"), `{"name":"GeneratedFloor","parentId":"owned-building"}`, true)
	send("POST", siteDesignMutationEndpoint("floor"), `{"name":"GeneratedFloor","parentId":"owned-building"}`, false)
	trace.permitKindDelete("floor", "owned-floor")
	send("DELETE", siteDesignMutationEndpoint("building")+"/owned-floor", "", false)
	send("DELETE", siteDesignMutationEndpoint("floor")+"/foreign-floor", "", false)
	send("DELETE", siteDesignMutationEndpoint("floor")+"/owned-floor", "", true)
	send("DELETE", siteDesignMutationEndpoint("floor")+"/owned-floor", "", false)
	if forwarded != 2 || trace.posts != 1 || trace.deletes != 1 {
		t.Fatal("allowances did not enforce exact single dispatch")
	}
}
