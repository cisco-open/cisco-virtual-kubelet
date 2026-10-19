// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// A scope change must exchange the whole retired claim while preserving other
// families. Corrupt/partial claims may not be repaired by taking ownership.
func TestTagHandoffSectionClaims(t *testing.T) {
	for _, mode := range []string{"valid", "foreign-overlap", "missing-old-claim", "duplicate-old-claim", "extra-old-claim", "wrong-old-name", "same-uid", "unsupported-section"} {
		t.Run(mode, func(t *testing.T) {
			n, old := tagRetirementFixture(t)
			retired, _, err := tagRetirementRecord(n)
			if err != nil {
				t.Fatal(err)
			}
			next := old.DeepCopy()
			next.Name, next.UID = "successor", "successor"
			next.Spec.ManagedSections = []string{"inventory", "templates"}
			other := cvk.NetworkControllerSectionOwner{Section: "sites", ConfigName: "site-owner", ConfigUID: "site-owner-uid"}
			owners := append(n.Status.Configuration.Owners, other)
			switch mode {
			case "foreign-overlap":
				owners = append(owners, cvk.NetworkControllerSectionOwner{Section: "templates", ConfigName: "foreign", ConfigUID: "foreign"})
			case "missing-old-claim":
				owners = owners[1:]
			case "duplicate-old-claim":
				owners = append(owners, owners[0])
			case "extra-old-claim":
				extra := owners[0]
				extra.Section = "system_settings"
				owners = append(owners, extra)
			case "wrong-old-name":
				owners[0].ConfigName = "different"
			case "same-uid":
				next.UID = old.UID
			case "unsupported-section":
				next.Spec.ManagedSections = []string{"sites"}
			}
			before := append([]cvk.NetworkControllerSectionOwner(nil), owners...)
			got, err := tagHandoffOwners(owners, retired, next)
			if !reflect.DeepEqual(owners, before) {
				t.Fatal("planning changed input claims")
			}
			if mode != "valid" {
				if err == nil {
					t.Fatal("unproven ownership accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 || configurationOwnersMatch(&cvk.NetworkControllerConfigurationStatus{Owners: got}, next) != nil {
				t.Fatal("new claims incomplete", got)
			}
			found := false
			for _, o := range got {
				if o == other {
					found = true
				}
				if o.ConfigUID == string(old.UID) {
					t.Fatal("retired claim survived")
				}
			}
			if !found {
				t.Fatal("unrelated owner changed")
			}
			// Returning to a single section removes the other claim, not its exclusion:
			// configurationSectionsOverlap still reserves the shared native namespace.
			retired.ConfigName = next.Name
			retired.Completion.ConfigUID = string(next.UID)
			retired.Sections = next.Spec.ManagedSections
			next = next.DeepCopy()
			next.Name = "third"
			next.UID = "third"
			next.Spec.ManagedSections = []string{"templates"}
			got, err = tagHandoffOwners(got, retired, next)
			if err != nil || len(got) != 2 {
				t.Fatal("scope contraction failed", got, err)
			}
			intruder := next.DeepCopy()
			intruder.Name = "intruder"
			intruder.UID = "intruder"
			intruder.Spec.ManagedSections = []string{"inventory"}
			if configurationOwnersMatch(&cvk.NetworkControllerConfigurationStatus{Owners: got}, intruder) == nil {
				t.Fatal("scope contraction split shared tag custody")
			}
		})
	}
}
