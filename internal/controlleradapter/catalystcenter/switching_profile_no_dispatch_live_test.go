//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Qualification-only read failure, never a replacement for native mutation or
// task results. The real transport counts mutations and remains authoritative.
type switchingPreflightQualification struct {
	a                 *adapter
	config            *configapi.NetworkControllerConfig
	before            *cvk.NetworkControllerConfigurationStatus
	injected, retired bool
	mutations         int
}

func installSwitchingPreflightQualification(a *adapter, config *configapi.NetworkControllerConfig, c *client) *switchingPreflightQualification {
	h := &switchingPreflightQualification{a: a, config: config}
	prior := c.wrapTransport
	c.wrapTransport = func(base http.RoundTripper) http.RoundTripper {
		if prior != nil {
			base = prior(base)
		}
		return siteLifecycleRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Method == http.MethodGet && req.URL.Path == networkProfilesPath && !h.retired {
				nc, err := a.currentController(req.Context())
				if err != nil {
					return nil, err
				}
				if s := nc.Status.Configuration; s != nil && s.Operation != nil && s.Operation.Phase == "Submitting" && strings.HasPrefix(s.Operation.Resource, "profiles/") {
					h.before = s.DeepCopy()
					h.injected = true
					return &http.Response{StatusCode: 503, Status: "503 Service Unavailable", Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"isolated preflight read qualification"}`)), Request: req}, nil
				}
			}
			if (req.Method == http.MethodPost || req.Method == http.MethodPut) && (req.URL.Path == networkProfilesPath || strings.HasPrefix(req.URL.Path, networkProfilesPath+"/")) {
				h.mutations++
			}
			return base.RoundTrip(req)
		})
	}
	c.Invalidate()
	return h
}

func (h *switchingPreflightQualification) afterTick(t *testing.T) {
	t.Helper()
	if !h.injected || h.retired {
		return
	}
	for _, condition := range h.config.Status.Conditions {
		if condition.Reason != "NativePreflightRefused" {
			continue
		}
		nc, err := h.a.currentController(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		s := nc.Status.Configuration
		if h.before == nil || s == nil || s.Operation != nil || h.mutations != 0 || !reflect.DeepEqual(s.Owners, h.before.Owners) || !reflect.DeepEqual(s.ActionApplications, h.before.ActionApplications) || !reflect.DeepEqual(s.CredentialApplications, h.before.CredentialApplications) {
			t.Fatal("switching preflight retirement changed custody or dispatched")
		}
		h.retired = true
		t.Log("switching preflight refused before mutation; exact journal retired with owner and receipts preserved")
	}
}
