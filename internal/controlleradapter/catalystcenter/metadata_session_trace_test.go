//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestMetadataSessionTraceSurvivesRenewal(t *testing.T) {
	ctx := context.Background()
	logins, mutations, denied := 0, 0, 0
	unauthorized := false
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			logins++
			fmt.Fprintf(w, `{"Token":"token-%d"}`, logins)
		case "/observe":
			if unauthorized {
				unauthorized = false
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, `{"response":[]}`)
		case "/mutate":
			mutations++
			fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
		default:
			denied++
			w.WriteHeader(http.StatusBadRequest)
		}
	})
	r, _, _ := reportFixtureEndpoint(t, c.cfg.Endpoint)
	trace := &metadataNoDispatchTrace{a: r.a, allowMutation: func(req *http.Request) bool {
		return req.Method == http.MethodPut && req.URL.Path == "/mutate"
	}}
	restore, err := installMetadataSessionTrace(ctx, c, trace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restore)
	check := func(want int) {
		t.Helper()
		if _, err := c.mutate(ctx, http.MethodPut, "/mutate", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if mutations != want || trace.mutations != want {
			t.Fatalf("native writes=%d observed writes=%d expected=%d", mutations, trace.mutations, want)
		}
		if _, err := c.mutate(ctx, http.MethodDelete, "/denied", nil); err == nil || denied != 0 {
			t.Fatal("renewal bypassed mutation scope guard")
		}
	}
	check(1)
	c.mu.Lock()
	c.builtAt = time.Now().Add(-c.cfg.MaxSessionLifetime - time.Second)
	c.mu.Unlock()
	if _, err := c.get(ctx, "/observe", nil, http.StatusOK); err != nil {
		t.Fatal(err)
	}
	check(2)
	unauthorized = true
	if _, err := c.get(ctx, "/observe", nil, http.StatusOK); err != nil {
		t.Fatal(err)
	}
	check(3)
	if logins != 3 {
		t.Fatalf("expected initial, lifetime and GET-401 sessions; got %d", logins)
	}
	restore()
	if _, err := c.mutate(ctx, http.MethodPut, "/mutate", []byte(`{}`)); err != nil || mutations != 4 || trace.mutations != 3 {
		t.Fatal("observer was not removed from the latest session", err)
	}
}
