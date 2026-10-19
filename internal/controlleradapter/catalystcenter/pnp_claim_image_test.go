// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestPnPClaimImageEvidence(t *testing.T) {
	for _, mode := range []string{"exact", "build-suffix", "wrong-version", "version-prefix", "no-version", "unreachable", "no-inventory", "skip", "default-skip", "missing-image-version", "unbound-activation", "foreign-pnp", "foreign-site"} {
		t.Run(mode, func(t *testing.T) {
			skip := false
			want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P", ImageID: "image-id", ImageSkip: &skip}
			deps := &pnpClaimDependencies{refs: pnpClaimReferences{deviceID: "pnp-id", siteID: "site-id", imageID: "image-id"}, site: Site{ID: "site-id", NameHierarchy: want.Site}, image: &Image{ID: "image-id", Version: "17.18.04"}}
			observed := &pnpClaimInventorySnapshot{pnp: pnpDevice{ID: "pnp-id"}, site: deps.site, inventory: &Device{ID: "network-id", Serial: want.Serial, SoftwareVersion: "17.18.04", Reachability: "Reachable"}}
			expected := "Matched"
			switch mode {
			case "build-suffix":
				observed.inventory.SoftwareVersion = "17.18.04.0.123"
			case "wrong-version":
				observed.inventory.SoftwareVersion = "26.02.01"
				expected = "PendingVersion"
			case "version-prefix":
				observed.inventory.SoftwareVersion = "17.18.040"
				expected = "PendingVersion"
			case "no-version":
				observed.inventory.SoftwareVersion = ""
				expected = "PendingVersion"
			case "unreachable":
				observed.inventory.Reachability = "Unreachable"
				expected = "PendingReachability"
			case "no-inventory":
				observed.inventory = nil
				expected = "PendingInventory"
			case "skip":
				skip = true
				expected = "Skipped"
			case "default-skip":
				want.ImageSkip = nil
				expected = "Skipped"
			case "missing-image-version":
				deps.image.Version = ""
				expected = "error"
			case "unbound-activation":
				want.ImageID = ""
				deps.refs.imageID = ""
				deps.image = nil
				expected = "error"
			case "foreign-pnp":
				observed.pnp.ID = "other"
				expected = "error"
			case "foreign-site":
				observed.site.ID = "other"
				expected = "error"
			}
			got, err := observePnPClaimImage(want, deps, observed)
			if (err != nil) != (expected == "error") {
				t.Fatal("image observation", err)
			}
			if expected == "error" {
				return
			}
			if got.state != expected || got.imageID != "image-id" || got.expectedVersion != "17.18.04" {
				t.Fatal("incorrect image evidence", got)
			}
			if observed.inventory != nil && got.runningVersion != observed.inventory.SoftwareVersion {
				t.Fatal("invented running version")
			}
		})
	}
}
