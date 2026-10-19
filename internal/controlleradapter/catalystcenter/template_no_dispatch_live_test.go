// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Fault only one metadata preflight read per resource/action; all successful
// reads/mutations still go to the appliance. This is not a native outage or an
// installed-worker qualification. The public reconciler owns retirement/replan.
type metadataNoDispatchTrace struct {
	base                           http.RoundTripper
	a                              *adapter
	section, preflightPath         string
	allowMutation                  func(*http.Request) bool
	injected, retired              map[string]bool
	recoveryTrees                  map[string]int
	pending                        string
	before                         *cvk.NetworkControllerConfigurationStatus
	mutations, atRefusal, prepared int
}

// Keep existing template qualification defaults and call sites unchanged.
type templateNoDispatchTrace = metadataNoDispatchTrace

// Native qualification is serial. Keep its observer on each fresh authenticated
// session, including the proactive lifetime boundary and GET-401 renewal.
func installMetadataSessionTrace(ctx context.Context, c *client, g *metadataNoDispatchTrace) (func(), error) {
	rest, _, err := c.session(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	previous := c.wrapTransport
	g.base = rest.HTTPClient.Transport
	if g.base == nil {
		g.base = http.DefaultTransport
	}
	c.wrapTransport = func(base http.RoundTripper) http.RoundTripper {
		if previous != nil {
			base = previous(base)
		}
		g.base = base
		return g
	}
	rest.HTTPClient.Transport = g
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.wrapTransport = previous
		if c.rest != nil && c.rest.HTTPClient.Transport == g {
			c.rest.HTTPClient.Transport = g.base
		}
	}, nil
}

func (g *metadataNoDispatchTrace) CloseIdleConnections() {
	if closer, ok := g.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

func (g *metadataNoDispatchTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == authPath {
		return g.base.RoundTrip(req)
	}
	nc, err := g.a.currentController(req.Context())
	if err != nil {
		return nil, err
	}
	state := nc.Status.Configuration
	section, preflightPath := g.section, g.preflightPath
	if section == "" {
		section = "templates"
	}
	if preflightPath == "" {
		preflightPath = templateProjectsPath
	}
	if req.Method == http.MethodGet && state != nil && state.Operation != nil && state.Operation.Phase == "Unknown" && state.Operation.TaskID != "" && req.URL.Path == taskPath+state.Operation.TaskID+"/tree" {
		if g.recoveryTrees == nil {
			g.recoveryTrees = map[string]int{}
		}
		g.recoveryTrees[state.Operation.TaskID]++
	}
	if g.pending != "" && state != nil && state.Operation == nil {
		if g.mutations != g.atRefusal || !reflect.DeepEqual(g.before.Owners, state.Owners) || !reflect.DeepEqual(g.before.ActionApplications, state.ActionApplications) || !reflect.DeepEqual(g.before.CredentialApplications, state.CredentialApplications) {
			return nil, errors.New("metadata refusal changed native effects or retained ownership/history")
		}
		g.retired[g.pending] = true
		g.pending = ""
	}
	if req.Method == http.MethodGet && req.URL.Path == preflightPath && state != nil && state.Operation != nil && state.Operation.Phase == "Submitting" && state.Operation.Section == section && noDispatchOperationSupported(state.Operation) {
		op := state.Operation
		key := op.SourceVersion + "/" + op.Action + "/" + op.Resource
		if !g.injected[key] {
			if g.pending != "" {
				return nil, errors.New("metadata action started before refusal retirement")
			}
			g.injected[key] = true
			g.pending = key
			g.before = state.DeepCopy()
			g.atRefusal = g.mutations
			if op.PreparedAt != nil {
				g.prepared++
			}
			return &http.Response{StatusCode: 503, Status: "503 Service Unavailable", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"isolated metadata preflight read failure"}`)), Request: req}, nil
		}
	}
	if req.Method != http.MethodGet {
		allowed := strings.HasPrefix(req.URL.Path, templateProjectsPath) || strings.HasPrefix(req.URL.Path, templatesPath)
		if g.allowMutation != nil {
			allowed = g.allowMutation(req)
		}
		if !allowed {
			return nil, errors.New("metadata qualification refused out-of-scope mutation")
		}
		if g.pending != "" {
			return nil, errors.New("native metadata mutation before refusal retirement")
		}
		g.mutations++
	}
	return g.base.RoundTrip(req)
}
