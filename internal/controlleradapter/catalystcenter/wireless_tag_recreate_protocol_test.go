// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestWirelessTagRecreationEnvelope(t *testing.T) {
	for _, ns := range []string{"wireless-site-tags/", "wireless-policy-tags/"} {
		for _, mode := range []string{"valid", "update", "delete", "missing-parent", "bad-parent", "extra-parent", "wrong-group", "wrong-key", "future", "unknown-mode", "physical", "oversized"} {
			t.Run(ns+mode, func(t *testing.T) {
				op := boundTagEnvelopeFixture("write", "Create")
				if ns == "wireless-site-tags/" {
					op.Resource = strings.Replace(op.Resource, "wireless-policy-tags/", ns, 1)
					op.Resource = strings.Replace(op.Resource, "policy-tag:", "site-tag:", 1)
				}
				cohort := wirelessTagRecreationCohort{Namespace: ns, ComparisonKeyID: strings.Repeat("c", 64), Digest: strings.Repeat("e", 64), Keys: []string{strings.Repeat("a", 64)}}
				resource, err := wirelessTagRecreationResource(op.Resource, cohort)
				if err != nil {
					t.Fatal(err)
				}
				canonical := wirelessTagCanonical(op.Resource)
				op.Resource = resource
				switch mode {
				case "update":
					op.Action, op.TargetID = "Update", "tag"
				case "delete":
					op.Action, op.TargetID = "Delete", "tag"
				case "missing-parent":
					op.Resource = strings.Replace(resource, ":"+cohort.Digest, "", 1)
				case "bad-parent":
					op.Resource = strings.Replace(resource, cohort.Digest, "bad", 1)
				case "extra-parent":
					op.Resource = strings.Replace(resource, cohort.Digest, cohort.Digest+":"+cohort.Digest, 1)
				case "wrong-group":
					cohort.Keys = []string{strings.Repeat("f", 64)}
				case "wrong-key":
					cohort.ComparisonKeyID = strings.Repeat("f", 64)
				case "future":
					op.Resource = strings.Replace(resource, "v5:", "v9:", 1)
				case "unknown-mode":
					op.Resource = strings.Replace(resource, "recreate:", "write:", 1)
				case "physical":
					op.TargetSerial = "FOC12345678"
				case "oversized":
					op.Resource += strings.Repeat("f", 512)
				}
				if mode == "wrong-group" || mode == "wrong-key" {
					legacy := strings.Replace(resource, wirelessTagRecreatePrefix, wirelessTagBoundPrefix+"write:", 1)
					legacy = strings.Replace(legacy, ":"+strings.Repeat("e", 64), "", 1)
					if _, err := wirelessTagRecreationResource(legacy, cohort); err == nil {
						t.Fatal("encoder borrowed other cohort identity")
					}
					return
				}
				if wirelessTagPrivateEnvelope(op) != (mode == "valid") {
					t.Fatal("incorrect recreation envelope acceptance", mode)
				}
				if mode == "valid" {
					digest, ok := wirelessTagRecreateParent(resource)
					if !ok || digest != cohort.Digest || wirelessTagCanonical(resource) != canonical || wirelessTagBindingMode(resource) != "write" {
						t.Fatal("recreation lost canonical selector or predecessor digest")
					}
				}
			})
		}
	}
}
