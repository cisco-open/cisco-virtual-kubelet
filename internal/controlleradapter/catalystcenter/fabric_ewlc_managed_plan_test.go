// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFabricEWLCManagedPlan(t *testing.T) {
	for _, mode := range []string{"update", "noop", "late-settings", "missing-managed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config, _, members, _ := portChannelJournalFixture(t, "valid")
			if mode == "missing-managed" {
				if err := a.statusWriter.Delete(ctx, members[1].device); err != nil {
					t.Fatal(err)
				}
			}
			c, want := ewlcComposedFixture(t, mode)
			want.settings.rolling = mode != "noop"
			write, targets, err := c.planManagedFabricEWLC(ctx, a.statusReader, config.Namespace, want)
			if mode == "late-settings" || mode == "missing-managed" {
				if err == nil || write != nil || targets != nil {
					t.Fatal("partial plan escaped", err)
				}
				return
			}
			if err != nil || len(targets) != 2 {
				t.Fatal(targets, err)
			}
			if mode == "noop" {
				if write != nil {
					t.Fatal("matching intent planned a write")
				}
				return
			}
			if write == nil {
				t.Fatal("missing plan")
			}
			if _, err := write.submit(ctx); !errors.Is(err, errConfigurationDeviceAdmission) {
				t.Fatal("preparation obtained write authority", err)
			}
		})
	}
}

func TestFabricEWLCGroupResourceClassification(t *testing.T) {
	for _, suffix := range []string{strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), strings.Repeat("a", 64), "", "pruned", strings.Repeat("A", 64), strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		resource := "fabric-ewlc-update:" + suffix
		if !isReservedConfigurationGroupResource("inventory", resource) {
			t.Fatal("malformed/pruned group resource lost fence", resource)
		}
		for _, action := range []string{"Update", "Create", "Delete"} {
			valid := suffix == strings.Repeat("a", 64)+":"+strings.Repeat("b", 64) && action == "Update"
			if validConfigurationGroupAction("inventory", resource, action) != valid {
				t.Fatal("invalid action classification", resource, action)
			}
		}
	}
	if validConfigurationGroupAction("fabric", "fabric-ewlc-update:"+strings.Repeat("a", 64), "Update") {
		t.Fatal("wrong section accepted")
	}
}
