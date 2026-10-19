// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Read-only qualification of existing links on the three authorized lab devices.
// A placeholder model pool is never read, adopted, submitted or qualified here.
func TestLiveCatalystCenterLANLinkAdjacency(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	topology, err := c.lanPhysicalTopology(ctx)
	if err != nil {
		t.Fatal("physical topology read", err)
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal("configuration inventory read", err)
	}
	byID := map[string]Device{}
	for _, device := range inventory {
		byID[device.ID] = device
	}
	allowed := map[string]bool{"198.51.100.100": true, "198.51.100.101": true, "198.51.100.103": true}
	sessions, err := c.lanSessions(ctx)
	if err != nil {
		t.Fatal("LAN session inventory read", err)
	}
	qualified := 0
	for i, link := range topology.Links {
		primary, secondary := byID[link.Source], byID[link.Target]
		if !allowed[primary.ManagementIP] || !allowed[secondary.ManagementIP] {
			continue
		}
		want := lanLinkIntent{name: fmt.Sprintf("read-only-%d", i), action: "ADD_LINK", primaryIP: primary.ManagementIP, primaryInterface: link.StartName, secondaryIP: secondary.ManagementIP, secondaryInterface: link.EndName, pool: "unqualified-placeholder"}
		snapshot, err := c.lanLinkAdjacencySnapshot(ctx, want)
		if err != nil {
			t.Fatal("exact existing lab adjacency binding", err)
		}
		if snapshot.link == nil || snapshot.link.ID != link.ID {
			t.Fatal("existing native adjacency lost")
		}
		if err := c.revalidateLANLinkAdjacency(ctx, want, snapshot); err != nil {
			t.Fatal("existing lab adjacency changed during read-only qualification", err)
		}
		if len(sessions) == 0 {
			if _, err := c.lanLinkPoolDependencies(ctx, want, snapshot.endpoints); !errors.Is(err, errConfigurationBusy) {
				t.Fatal("empty discovery history did not hold pool eligibility", err)
			}
		}
		qualified++
	}
	t.Logf("native topology nodes=%d links=%d; qualified existing lab adjacencies=%d; no link/session/pool writes", len(topology.Nodes), len(topology.Links), qualified)
	if qualified == 0 {
		t.Skip("no existing adjacency between authorized lab devices to qualify")
	}
	t.Logf("LAN session records=%d; empty discovery history holds pool eligibility; mutation admission and accepted execution remain unqualified", len(sessions))
}
