// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
)

// A launch observation, not a physical grant or allocator reservation. Only
// same-site IPv4 pools and an explicit, completely known PnP set are admitted
// here; shared visibility and unbounded DHCP discovery need separate contracts.
type lanSessionPreflight struct {
	dependencies *lanSessionDependencies
	sessions     *lanActiveSessionSnapshot
	usage        []lanPoolUsage
	discovery    []pnpDevice
}

func (c *client) lanSessionPreflight(ctx context.Context, want lanSessionIntent) (*lanSessionPreflight, error) {
	active, err := c.lanActiveSessions(ctx)
	if err != nil {
		return nil, err
	}
	if active.Maximum == 0 || len(active.Active) != 0 {
		return nil, errConfigurationBusy
	}
	deps, err := c.lanSessionDependencies(ctx, want)
	if err != nil {
		return nil, err
	}
	if len(want.Devices) == 0 {
		return nil, errConfigurationDeviceAdmission
	}
	result := &lanSessionPreflight{dependencies: deps, sessions: active}
	for _, pool := range deps.pools {
		bound := pool.evidence.primary
		if pool.owner.ID != deps.site.ID || bound.V4 == nil || bound.V6 != nil {
			return nil, errConfigurationDeviceAdmission
		}
		usage, err := c.lanPoolUsage(ctx, bound)
		if err != nil {
			return nil, err
		}
		// Necessary count bound only: it does not establish address contiguity or
		// account for the discovered topology's link allocations and seed needs.
		minimum := uint64(1)
		if pool.role == "PRINCIPAL_IP_ADDRESS_POOL" {
			minimum = uint64(len(want.Devices))
		}
		if pool.role == "LINK_OVERLAPPING_IP_POOL" {
			minimum = 2
		}
		if usage.remaining() < minimum {
			return nil, errConfigurationBusy
		}
		result.usage = append(result.usage, *usage)
	}
	pnp, err := c.pnpDevices(ctx)
	if err != nil {
		return nil, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	result.discovery, err = bindLANDiscoveryDevices(want, pnp, inventory)
	if err != nil {
		return nil, err
	}
	for _, d := range result.discovery {
		// Retain authentication/workflow metadata without interpreting unknown
		// native states as readiness. Physical admission must qualify those states.
		if d.Info == nil || d.Info.State != "Unclaimed" {
			return nil, errConfigurationDeviceAdmission
		}
	}
	if err = c.revalidateLANSessionDependencies(ctx, want, deps); err != nil {
		return nil, err
	}
	freshPnP, err := c.pnpDevices(ctx)
	if err != nil {
		return nil, err
	}
	freshInventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	freshDiscovery, err := bindLANDiscoveryDevices(want, freshPnP, freshInventory)
	if err != nil {
		return nil, err
	}
	current, err := c.lanActiveSessions(ctx)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(result.discovery, freshDiscovery) || !reflect.DeepEqual(active, current) {
		return nil, errConfigurationStale
	}
	// Recheck counters after the dependency/identity bracket. A free count is
	// not a lease: any changing count must invalidate this prepared observation.
	for i, pool := range deps.pools {
		fresh, err := c.lanPoolUsage(ctx, pool.evidence.primary)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(result.usage[i], *fresh) {
			return nil, errConfigurationStale
		}
	}
	return result, nil
}

func (s *lanSessionPreflight) observationDigest() string {
	if s == nil || s.dependencies == nil {
		return ""
	}
	// Dependency structs intentionally have private fields. Hash an explicit
	// projection; marshaling the structs themselves would silently hash {}.
	pools := []any{}
	for _, p := range s.dependencies.pools {
		pools = append(pools, struct {
			Role   string
			Owner  Site
			Digest string
		}{p.role, p.owner, p.evidence.digest})
	}
	raw, err := json.Marshal(struct {
		Site           Site
		DiscoverySites []Site
		Seeds          []Device
		Ports          []lanLinkInterface
		Pools          []any
		Sessions       *lanActiveSessionSnapshot
		Usage          []lanPoolUsage
		Discovery      []pnpDevice
	}{s.dependencies.site, s.dependencies.discoverySites, s.dependencies.seeds, s.dependencies.primaryPorts, pools, s.sessions, s.usage, s.discovery})
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
