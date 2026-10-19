// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/meta"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestTagOwnerArchiveIntegrity(t *testing.T) {
	for _, mode := range []string{"valid", "missing-carrier", "missing-creator", "missing-all", "changed-result", "changed-operation", "duplicate", "bad-frame-prior", "bad-frame-generation", "bad-frame-scope", "bad-frame-uid", "lost-condition", "wrong-root"} {
		t.Run(mode, func(t *testing.T) {
			n, c := tagRetirementFixture(t)
			r, _, err := tagRetirementRecord(n)
			if err != nil {
				t.Fatal(err)
			}
			original := n.Status.Configuration.DeepCopy()
			f, err := archiveTagOwner(n.Status.Configuration, r)
			if err != nil {
				t.Fatal(err)
			}
			g, err := tagOwnerArchiveGraph(n.Status.Configuration)
			if err != nil || g.root == nil {
				t.Fatal(err)
			}
			newer := c.DeepCopy()
			newer.UID = "new-owner"
			newer.Name = "new-owner"
			newer.DeletionTimestamp = nil
			for i := range n.Status.Configuration.Owners {
				n.Status.Configuration.Owners[i].ConfigUID = string(newer.UID)
				n.Status.Configuration.Owners[i].ConfigName = newer.Name
			}
			d, seal := tagArchiveIdentity(g)
			h := tagOwnershipHandoff{Protocol: tagHandoffProtocol, ControllerUID: string(n.UID), ControllerGeneration: n.Generation, Previous: *r, ConfigName: newer.Name, ConfigUID: string(newer.UID), ConfigGeneration: newer.Generation, Sections: newer.Spec.ManagedSections, SpecDigest: featureIdentityDigest(newer.Spec), InputDigest: featureIdentityDigest("source"), ArchiveDigest: d, RootSeal: seal}
			raw, _ := json.Marshal(h)
			meta.SetStatusCondition(&n.Status.Conditions, condition(tagRetirementCondition, true, "TagOwnershipHandedOff", string(raw), n.Generation))
			if mode == "valid" {
				if tagOwnerCustodyValid(n, newer) != nil {
					t.Fatal("valid custody rejected")
				}
				for _, old := range original.ActionApplications {
					found := false
					for _, now := range n.Status.Configuration.ActionApplications {
						if reflect.DeepEqual(old.Operation, now.Operation) && reflect.DeepEqual(old.VerifiedAt, now.VerifiedAt) {
							found = true
						}
					}
					if !found {
						t.Fatal("original audit operation changed")
					}
				}
				op := *original.ActionApplications[0].Operation.DeepCopy()
				op.ID = strings.Repeat("a", 32)
				op.ConfigUID = string(newer.UID)
				op.ConfigName = newer.Name
				op.Phase = "Prepared"
				op.TaskID = ""
				op.SubmittedAt = nil
				op.Resource = tagSuccessorIntent(op.Resource, n.Status.Configuration)
				if tagSuccessorSeal(op.Resource) != f.Seal || tagOwnedCapacity(n.Status.Configuration, &op) != nil {
					t.Fatal("valid successor did not bind full prior history")
				}
				plain := op
				plain.Resource = tagOwnedCanonical(op.Resource)
				if tagOwnedCapacity(n.Status.Configuration, &plain) == nil {
					t.Fatal("successor dropped predecessor binding")
				}
				_, oldID, _, _ := tagReceiptIdentity(original.ActionApplications[0])
				if tagRecreatedIDUnused(n.Status.Configuration, oldID) == nil {
					t.Fatal("retired native UUID became reusable")
				}
				return
			}
			switch mode {
			case "missing-carrier":
				n.Status.Configuration.ActionApplications = slices.DeleteFunc(n.Status.Configuration.ActionApplications, func(a cvk.NetworkControllerConfigurationActionApplication) bool {
					return strings.HasPrefix(a.Resource, tagOwnerRootPrefix)
				})
			case "missing-creator":
				n.Status.Configuration.ActionApplications = slices.DeleteFunc(n.Status.Configuration.ActionApplications, func(a cvk.NetworkControllerConfigurationActionApplication) bool {
					return a.Operation.Action == "Create"
				})
			case "missing-all":
				n.Status.Configuration.ActionApplications = nil
			case "changed-result":
				n.Status.Configuration.ActionApplications[0].Resource += "x"
			case "changed-operation":
				n.Status.Configuration.ActionApplications[0].Operation.TaskID = "changed"
			case "duplicate":
				n.Status.Configuration.ActionApplications = append(n.Status.Configuration.ActionApplications, n.Status.Configuration.ActionApplications[0])
			case "lost-condition":
				n.Status.Conditions = nil
			case "wrong-root":
				h.RootSeal = strings.Repeat("e", 64)
				raw, _ := json.Marshal(h)
				meta.SetStatusCondition(&n.Status.Conditions, condition(tagRetirementCondition, true, "TagOwnershipHandedOff", string(raw), n.Generation))
			default:
				for i, a := range n.Status.Configuration.ActionApplications {
					if !strings.HasPrefix(a.Resource, tagOwnerRootPrefix) {
						continue
					}
					parts := strings.SplitN(strings.TrimPrefix(a.Resource, tagOwnerRootPrefix), ":", 9)
					switch mode {
					case "bad-frame-prior":
						parts[6] = strings.Repeat("f", 64)
					case "bad-frame-generation":
						parts[2] = "0"
					case "bad-frame-scope":
						parts[0] = "b"
					case "bad-frame-uid":
						parts[1] = "other"
					}
					n.Status.Configuration.ActionApplications[i].Resource = tagOwnerRootPrefix + strings.Join(parts, ":")
				}
			}
			if tagOwnerCustodyValid(n, newer) == nil {
				t.Fatal("missing or changed predecessor history authorized custody", mode)
			}
		})
	}
}
