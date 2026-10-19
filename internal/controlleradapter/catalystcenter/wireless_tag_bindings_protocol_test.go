// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func boundTagEnvelopeFixture(mode, action string) *cvk.NetworkControllerConfigurationOperation {
	key := strings.Repeat("a", 64)
	inner := "policy-tag:" + key + ":" + strings.Repeat("b", 64)
	if mode != "write" {
		scope, _ := encodeWirelessTagMembershipScope([]string{"site"})
		inner += ":" + scope
	}
	at := metav1.NewTime(time.Now().Add(-time.Minute).UTC().Truncate(time.Second))
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("1", 32), Section: "network_profiles", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, ParentID: "profile", TargetID: "tag", TaskID: "task", SubmittedAt: &at, Phase: "Unknown", Action: action,
		Resource: "wireless-policy-tags/" + wirelessTagBoundPrefix + mode + ":" + inner + ":" + wirelessTagGroupKey([]string{key + ":"}) + ":" + strings.Repeat("c", 64) + ":" + strings.Repeat("d", 64)}
	if action == "Create" {
		op.TargetID = ""
	}
	return op
}

func TestWirelessTagBindingEnvelope(t *testing.T) {
	for _, mode := range []string{"write", "members", "delete"} {
		for _, action := range []string{"Create", "Update", "Delete"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				op := boundTagEnvelopeFixture(mode, action)
				valid := mode == "write" && action != "Delete" || mode == "members" && action == "Update" || mode == "delete" && action == "Delete"
				if wirelessTagPrivateEnvelope(op) != valid {
					t.Fatal("binding operation class was not enforced")
				}
				canonical, _, _, ok := wirelessTagPrivateParts(op.Resource)
				if !ok || !strings.HasPrefix(canonical, "wireless-policy-tags/policy-tag:") || wirelessTagBindingMode(op.Resource) != mode {
					t.Fatal("binding selector failed canonical decoding")
				}
				if wirelessTagMembershipSelected(op.Resource) != (mode == "members") || wirelessTagPruneSelected(op.Resource) != (mode == "delete") {
					t.Fatal("operation family classification ambiguous")
				}
			})
		}
	}
	for _, variant := range []string{"future", "unknown-mode", "no-task", "physical", "oversized", "wrong-delete-group", "scope-on-write", "cohort-on-members", "namespace"} {
		t.Run(variant, func(t *testing.T) {
			op := boundTagEnvelopeFixture("delete", "Delete")
			switch variant {
			case "future":
				op.Resource = strings.Replace(op.Resource, "v4:", "v9:", 1)
			case "unknown-mode":
				op.Resource = strings.Replace(op.Resource, "delete:", "unknown:", 1)
			case "no-task":
				op.TaskID = ""
			case "physical":
				op.TargetSerial = "serial"
			case "oversized":
				op.Resource += strings.Repeat("x", 512)
			case "wrong-delete-group":
				parts := strings.Split(op.Resource, ":")
				parts[len(parts)-3] = strings.Repeat("f", 64)
				op.Resource = strings.Join(parts, ":")
			case "scope-on-write":
				op.Resource = strings.Replace(op.Resource, "delete:", "write:", 1)
				op.Action = "Update"
			case "cohort-on-members":
				op = boundTagEnvelopeFixture("members", "Update")
				op.Resource = strings.Replace(op.Resource, "policy-tag:"+strings.Repeat("a", 64)+":"+strings.Repeat("b", 64), wirelessPolicyCohortPrefix+strings.Repeat("a", 64), 1)
			case "namespace":
				op.Resource = strings.Replace(op.Resource, "wireless-policy-tags/", "wireless-site-tags/", 1)
			}
			if wirelessTagPrivateEnvelope(op) {
				t.Fatal("malformed bound operation accepted")
			}
		})
	}
}

func TestWirelessTagBindingPruneVersionCannotChangeCustody(t *testing.T) {
	for _, priorBound := range []bool{false, true} {
		old := boundTagEnvelopeFixture("write", "Update")
		old.Phase = "Verified"
		if !priorBound {
			old.Resource = strings.Replace(old.Resource, wirelessTagBoundPrefix+"write:", wirelessTagPrivatePrefix, 1)
		}
		at := metav1.NewTime(time.Now().Add(-time.Minute).UTC().Truncate(time.Second))
		key := strings.Repeat("a", 64)
		row := cvk.NetworkControllerConfigurationActionApplication{Operation: *old, VerifiedAt: &at}
		e := &wirelessTagPruneExecutor{enabled: true, comparison: &templateInputComparison{keyID: strings.Repeat("c", 64)}, predecessor: strings.Repeat("d", 64), wanted: map[string]bool{}, latest: map[string]wirelessTagMemberProof{"wireless-policy-tags/" + key: {row: row, id: "tag"}}}
		for _, deleteBound := range []bool{false, true} {
			op := boundTagEnvelopeFixture("delete", "Delete")
			if !deleteBound {
				op.Resource = strings.Replace(op.Resource, wirelessTagBoundPrefix+"delete:", wirelessTagPrunePrefix, 1)
			}
			if e.selected(op) != (priorBound == deleteBound) {
				t.Fatal("deletion silently upgraded/downgraded retained proof semantics")
			}
		}
	}
}
