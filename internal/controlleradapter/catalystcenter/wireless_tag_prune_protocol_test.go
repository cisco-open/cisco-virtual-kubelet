// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWirelessTagPruneEnvelope(t *testing.T) {
	for _, mode := range []string{"valid", "update", "create", "cohort", "future", "empty", "duplicate", "bad-group", "no-target", "physical", "no-task", "oversized", "wrong-group"} {
		t.Run(mode, func(t *testing.T) {
			scope, _ := encodeWirelessTagMembershipScope([]string{"site"})
			key := strings.Repeat("a", 64)
			resource := "wireless-policy-tags/" + wirelessTagPrunePrefix + "policy-tag:" + key + ":" + strings.Repeat("b", 64) + ":" + scope + ":" + wirelessTagGroupKey([]string{key + ":"}) + ":" + strings.Repeat("c", 64) + ":" + strings.Repeat("d", 64)
			at := metav1.NewTime(time.Now().Add(-time.Minute).UTC().Truncate(time.Second))
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("1", 32), Section: "network_profiles", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Resource: resource, Action: "Delete", Phase: "Unknown", TaskID: "task", SubmittedAt: &at, ParentID: "profile", TargetID: "tag"}
			switch mode {
			case "update":
				op.Action = "Update"
			case "create":
				op.Action = "Create"
				op.TargetID = ""
			case "cohort":
				op.Resource = strings.Replace(resource, "policy-tag:"+key+":"+strings.Repeat("b", 64), wirelessPolicyCohortPrefix+key, 1)
			case "future":
				op.Resource = strings.Replace(resource, "tag-proof:v3:", "tag-proof:v9:", 1)
			case "empty":
				op.Resource = strings.Replace(resource, scope, base64.RawURLEncoding.EncodeToString([]byte("[]")), 1)
			case "duplicate":
				op.Resource = strings.Replace(resource, scope, base64.RawURLEncoding.EncodeToString([]byte(`["site","site"]`)), 1)
			case "bad-group":
				op.Resource = strings.Replace(resource, wirelessTagGroupKey([]string{key + ":"}), "invalid", 1)
			case "wrong-group":
				op.Resource = strings.Replace(resource, wirelessTagGroupKey([]string{key + ":"}), strings.Repeat("e", 64), 1)
			case "no-target":
				op.TargetID = ""
			case "physical":
				op.TargetSerial = "FOC12345678"
			case "no-task":
				op.TaskID = ""
			case "oversized":
				op.Resource = strings.Replace(resource, scope, base64.RawURLEncoding.EncodeToString([]byte(`["`+strings.Repeat("a", 128)+`","`+strings.Repeat("b", 128)+`"]`)), 1)
			}
			admitted := wirelessTagPrivateEnvelope(op)
			if mode == "valid" {
				ns, logical, ids, ok := wirelessTagPruneParts(op.Resource)
				if !admitted || !ok || ns != "wireless-policy-tags/" || logical != key || len(ids) != 1 || ids[0] != "site" || !networkProfileMetadataKindSupported(op) {
					t.Fatal("valid acknowledged deletion envelope refused")
				}
			} else if admitted {
				t.Fatal("invalid deletion envelope admitted")
			}
		})
	}
}
