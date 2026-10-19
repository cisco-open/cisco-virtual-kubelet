// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "context"

type aaaRegistrationDependency struct{ address, id, protocol string }

// The site API requires plain AAA primary/secondary addresses to be registered
// first (native NCND01221). Reuse the system-settings metadata reader; do not
// register servers implicitly, adopt their secrets, or infer ISE PSN/PAN mapping.
func (c *client) aaaRegistrationDependencies(ctx context.Context, bindings []networkBinding) ([]aaaRegistrationDependency, error) {
	type reference struct{ address, protocol string }
	refs := []reference{}
	seen := map[reference]bool{}
	for _, binding := range bindings {
		for _, setting := range binding.settings {
			if setting.kind != "aaa" {
				continue
			}
			for _, group := range aaaGroups {
				fields := setting.aaa[group.api]
				if fields["serverType"] != "AAA" {
					continue
				}
				for _, field := range []string{"primaryServerIp", "secondaryServerIp"} {
					if fields[field] == "" {
						continue
					}
					ref := reference{fields[field], fields["protocol"]}
					if !seen[ref] {
						refs = append(refs, ref)
						seen[ref] = true
					}
				}
			}
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	servers, err := c.authenticationPolicyServers(ctx)
	if err != nil {
		return nil, err
	}
	dependencies := make([]aaaRegistrationDependency, 0, len(refs))
	for _, ref := range refs {
		server, exists := servers[ref.address]
		if !exists || server.ISE == nil || *server.ISE || (server.Protocol != ref.protocol && server.Protocol != "RADIUS_TACACS") {
			return nil, errConfigurationTransition
		}
		dependencies = append(dependencies, aaaRegistrationDependency{ref.address, server.ID, server.Protocol})
	}
	return dependencies, nil
}
