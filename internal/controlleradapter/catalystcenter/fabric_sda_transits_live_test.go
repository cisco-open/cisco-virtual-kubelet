// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

func TestLiveSDATransitPrerequisites(t *testing.T) {
	if os.Getenv("CATC_SDA_TRANSIT_QUALIFICATION") != "read-only-prerequisites" {
		t.Skip("explicit read-only SDA prerequisite qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal("Catalyst Center session unavailable")
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	writes := &atomic.Int64{}
	rest.HTTPClient.Transport = livePlannedAPTrace{base: base, t: t, writes: writes}
	defer func() { rest.HTTPClient.Transport = base }()
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal("read-only native inventory unavailable")
	}
	for _, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		d, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			t.Log("lab target absent or ambiguous", ip)
			continue
		}
		want := sdaTransitIntent{name: "CVK-ReadOnly-SDA-Transit-Probe", kind: "SDA_LISP_PUB_SUB_TRANSIT", controlPlanes: []string{d.Hostname}}
		s, err := c.sdaTransitSnapshot(ctx, want)
		if err != nil {
			if errors.Is(err, errConfigurationBusy) {
				p, provisioned, readErr := c.wiredProvisionedDevice(ctx, d.ID)
				assigned := false
				if readErr == nil && provisioned {
					members, assignmentErr := c.assignedDevices(ctx, p.SiteID)
					assigned, readErr = members[d.ID], assignmentErr
				}
				t.Log("lab prerequisite readback", ip, "provisionedRecord", provisioned, "assignedToProvisionedSite", assigned, "readError", readErr != nil)
			}
			status := 0
			var nativeError *transport.RESTError
			if errors.As(err, &nativeError) {
				status = nativeError.StatusCode
			}
			t.Log("lab SDA native prerequisites unavailable", ip, "pendingProvisionOrAssignment", errors.Is(err, errConfigurationBusy), "identityOrPlatformRefusal", errors.Is(err, errConfigurationTransition) || errors.Is(err, errConfigurationStale), "HTTP", status)
			continue
		}
		if s.actual != nil {
			t.Fatal("read-only probe name already exists; no mutation attempted")
		}
		if write, err := c.planSDATransitCreate(ctx, want, nil); !errors.Is(err, errConfigurationDeviceAdmission) || write != nil {
			t.Fatal("missing physical admission did not fence")
		}
		t.Log("native prerequisites resolved; absent physical admission fenced without mutation", ip)
	}
	if writes.Load() != 0 {
		t.Fatal("read-only prerequisite check emitted a product mutation")
	}
	t.Log("read-only prerequisite assessment complete; zero product mutations; no SDA execution/convergence claim")
}
