// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestWirelessTagProducerMemberBinding(t *testing.T) {
	for _, mode := range []string{"valid", "member-omitted", "member-added", "duplicate-logical", "duplicate-uuid", "future-version", "owner", "nonce", "creator-overlap", "native-alias"} {
		t.Run(mode, func(t *testing.T) {
			effects := []string{}
			for i, digit := range []string{"1", "2", "3"} {
				effects = append(effects, strings.Repeat(digit, 64)+":tag-"+string(rune('a'+i))+":"+strings.Repeat("a", 64))
			}
			at := metav1.NewTime(time.Now().Add(-time.Minute).UTC().Truncate(time.Second))
			verified := metav1.NewTime(at.Add(10 * time.Second))
			op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("1", 32), Section: "network_profiles", Action: "Create", Phase: "Verified", TaskID: "task", ParentID: "profile", ConfigUID: "owner", ConfigName: "config", ConfigGeneration: 1, ControllerGeneration: 1, SubmittedAt: &at, Resource: "wireless-policy-tags/" + wirelessTagPrivatePrefix + wirelessPolicyCohortPrefix + strings.Repeat("a", 64) + ":" + wirelessTagGroupKey(effects) + ":" + strings.Repeat("b", 64) + ":" + strings.Repeat("c", 64)}
			row := cvk.NetworkControllerConfigurationActionApplication{Section: "network_profiles", Operation: op, VerifiedAt: &verified, EffectReceipts: effects}
			switch mode {
			case "member-omitted":
				row.EffectReceipts = append([]string{}, effects[:2]...)
			case "member-added":
				row.EffectReceipts = append(row.EffectReceipts, strings.Repeat("4", 64)+":tag-d:"+strings.Repeat("a", 64))
			case "duplicate-logical":
				row.EffectReceipts[2] = strings.Replace(row.EffectReceipts[2], strings.Repeat("3", 64), strings.Repeat("1", 64), 1)
			case "duplicate-uuid":
				row.EffectReceipts[2] = strings.Replace(row.EffectReceipts[2], "tag-c", "tag-a", 1)
			case "future-version":
				row.Operation.Resource = strings.Replace(row.Operation.Resource, wirelessTagPrivatePrefix, "tag-proof:v9:", 1)
			}
			sort.Strings(row.EffectReceipts)
			row.Resource = wirelessTagReceiptBase(&row.Operation, row.EffectReceipts) + ":" + wirelessTagRowSeal(row)
			state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "network_profiles", ConfigName: "config", ConfigUID: "owner"}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{row}}
			if mode == "owner" {
				state.Owners[0].ConfigUID = "other"
			}
			if mode == "nonce" {
				other := row
				other.Section = "templates"
				other.Resource = "unrelated"
				other.Operation.Section = "templates"
				other.Operation.Resource = "unrelated"
				state.ActionApplications = append(state.ActionApplications, other)
			}
			if mode == "creator-overlap" || mode == "native-alias" {
				other := row
				other.Operation = *row.Operation.DeepCopy()
				other.Operation.ID = strings.Repeat("2", 32)
				other.EffectReceipts = append([]string{}, row.EffectReceipts[:2]...)
				if mode == "native-alias" {
					other.EffectReceipts[0] = strings.Replace(other.EffectReceipts[0], strings.Repeat("1", 64), strings.Repeat("4", 64), 1)
					other.EffectReceipts[1] = strings.Replace(other.EffectReceipts[1], strings.Repeat("2", 64), strings.Repeat("5", 64), 1)
				}
				p := strings.Split(other.Operation.Resource, ":")
				p[len(p)-3] = wirelessTagGroupKey(other.EffectReceipts)
				other.Operation.Resource = strings.Join(p, ":")
				other.Resource = wirelessTagReceiptBase(&other.Operation, other.EffectReceipts) + ":" + wirelessTagRowSeal(other)
				state.ActionApplications = append(state.ActionApplications, other)
			}
			// Recompute every public seal after tampering. The producer member binding
			// must still reject an altered complete set, rather than relying on a stale hash.
			for i := range state.ActionApplications {
				if state.ActionApplications[i].Section == "network_profiles" {
					state.ActionApplications[i].Resource += ":" + strings.Repeat("d", 64)
				}
			}
			_, err := wirelessTagRecords(state)
			if (err == nil) != (mode == "valid") {
				t.Fatal("incorrect complete producer-set qualification", mode, err)
			}
		})
	}
}
