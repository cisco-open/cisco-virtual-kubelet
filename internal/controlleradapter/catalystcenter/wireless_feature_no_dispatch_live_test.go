// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type featureNoDispatchTrace struct {
	base                   http.RoundTripper
	a                      *adapter
	injected, retired      map[string]bool
	pending                string
	before                 *cvk.NetworkControllerConfigurationStatus
	mutations, atRefusal   int
	requests, summaryReads int
	kinds                  map[string]int
}

func (g *featureNoDispatchTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	g.requests++
	if req.Method == http.MethodGet && req.URL.Path == wirelessFeatureSummaryPath {
		g.summaryReads++
	}
	if req.Method != http.MethodGet {
		if strings.HasPrefix(req.URL.Path, wirelessCleanAirPath) || strings.HasPrefix(req.URL.Path, wirelessFRAPath) {
			g.mutations++
		}
		return g.base.RoundTrip(req)
	}
	nc, err := g.a.currentController(req.Context())
	if err != nil {
		return nil, err
	}
	state := nc.Status.Configuration
	if g.pending != "" && state != nil && state.Operation == nil {
		if g.mutations != g.atRefusal || !reflect.DeepEqual(g.before.Owners, state.Owners) || !reflect.DeepEqual(g.before.ActionApplications, state.ActionApplications) || !reflect.DeepEqual(g.before.CredentialApplications, state.CredentialApplications) {
			return nil, errors.New("qualification refusal changed native effects or original ownership/history")
		}
		g.retired[g.pending] = true
		g.pending = ""
	}
	if req.URL.Path == wirelessFeatureSummaryPath && state != nil && state.Operation != nil && state.Operation.Phase == "Submitting" && state.Operation.Section == "feature_templates" && noDispatchOperationSupported(state.Operation) {
		op := state.Operation
		key := fmt.Sprint(op.ConfigUID, "/", op.ConfigGeneration, "/", op.Action, "/", op.Resource)
		if !g.injected[key] {
			if g.pending != "" {
				return nil, errors.New("qualification started another action before no-dispatch retirement")
			}
			g.injected[key] = true
			g.pending = key
			g.before = state.DeepCopy()
			g.atRefusal = g.mutations
			g.kinds[op.Action]++
			return &http.Response{StatusCode: 503, Status: "503 Service Unavailable", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"isolated feature preflight read failure"}`)), Request: req}, nil
		}
	}
	return g.base.RoundTrip(req)
}
func installFeatureNoDispatchTrace(t *testing.T, a *adapter, base http.RoundTripper, save func(string, any)) *featureNoDispatchTrace {
	t.Helper()
	g := &featureNoDispatchTrace{base: base, a: a, injected: map[string]bool{}, retired: map[string]bool{}, kinds: map[string]int{}}
	t.Cleanup(func() {
		if t.Failed() {
			save("feature-no-dispatch-incomplete.json", map[string]any{"injected": len(g.injected), "retired": len(g.retired), "pending": g.pending, "mutationAttempts": g.mutations})
			return
		}
		nc, err := a.currentController(context.Background())
		if err != nil {
			t.Error(err)
			return
		}
		if g.pending != "" || len(g.injected) < 16 || len(g.retired) != len(g.injected) || g.kinds["Create"] < 2 || g.kinds["Update"] < 12 || g.kinds["Delete"] < 2 || nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
			t.Error("complete feature no-dispatch qualification missing", len(g.injected), len(g.retired), g.kinds)
			return
		}
		save("feature-no-dispatch-complete.json", map[string]any{"injected": len(g.injected), "retired": len(g.retired), "byAction": g.kinds, "mutationAttempts": g.mutations, "ownersRetained": len(nc.Status.Configuration.Owners), "retainedRecords": len(nc.Status.Configuration.ActionApplications), "qualified": true, "requests": g.requests, "summaryReads": g.summaryReads})
		t.Log("feature preflight refusals automatically retired before fresh dispatch; ownership/history retained", len(g.retired), g.kinds, "native mutations", g.mutations)
	})
	return g
}
