// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/base64"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestWirelessTagMembershipScopeEnvelope(t *testing.T) {
	scope, err := encodeWirelessTagMembershipScope([]string{"site-a", "site-b"})
	if err != nil {
		t.Fatal(err)
	}
	canonical := "wireless-policy-tags/policy-tag:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	resource := "wireless-policy-tags/" + wirelessTagMembershipPrefix + strings.TrimPrefix(canonical, "wireless-policy-tags/") + ":" + scope + ":" + strings.Repeat("c", 64) + ":" + strings.Repeat("d", 64) + ":" + strings.Repeat("e", 64)
	for _, mode := range []string{"valid", "empty", "unsorted", "duplicate", "null", "noncanonical", "future", "create", "over-capacity", "cohort"} {
		t.Run(mode, func(t *testing.T) {
			value := resource
			raw := ""
			switch mode {
			case "empty":
				raw = "[]"
			case "unsorted":
				raw = `["site-b","site-a"]`
			case "duplicate":
				raw = `["site-a","site-a"]`
			case "null":
				raw = "null"
			case "noncanonical":
				raw = `[ "site-a", "site-b" ]`
			case "future":
				value = strings.Replace(value, "tag-proof:v2:", "tag-proof:v9:", 1)
			case "over-capacity":
				value = strings.Replace(value, scope, base64.RawURLEncoding.EncodeToString([]byte(`["`+strings.Repeat("a", 256)+`","`+strings.Repeat("b", 256)+`"]`)), 1)
			case "cohort":
				value = strings.Replace(value, "policy-tag:"+strings.Repeat("a", 64)+":"+strings.Repeat("b", 64), wirelessPolicyCohortPrefix+strings.Repeat("a", 64), 1)
			}
			if raw != "" {
				value = strings.Replace(value, scope, base64.RawURLEncoding.EncodeToString([]byte(raw)), 1)
			}
			at := metav1.NewTime(time.Now().Add(-time.Minute).UTC().Truncate(time.Second))
			op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("1", 32), Section: "network_profiles", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Phase: "Unknown", TaskID: "task", SubmittedAt: &at, Resource: value, Action: "Update", ParentID: "profile", TargetID: "tag"}
			if mode == "create" {
				op.Action = "Create"
				op.TargetID = ""
			}
			// Parsing and recovery envelopes are separate: even syntactically valid
			// v2 selectors cannot admit Create or an oversized journal operation.
			base, _, _, valid := wirelessTagPrivateParts(value)
			if mode == "valid" {
				ids, err := decodeWirelessTagMembershipScope(value)
				if err != nil || !reflect.DeepEqual(ids, []string{"site-a", "site-b"}) || !valid || base != canonical || !wirelessTagPrivateEnvelope(op) {
					t.Fatal("lost private scope", err)
				}
			} else if mode == "create" || mode == "over-capacity" {
				if wirelessTagPrivateEnvelope(op) {
					t.Fatal("invalid operation envelope admitted")
				}
			} else if valid {
				t.Fatal("malformed membership selector admitted")
			}
		})
	}
}
func TestWirelessTagMembershipAffectedScopes(t *testing.T) {
	for _, mode := range []string{"site", "zone", "empty", "unchanged"} {
		t.Run(mode, func(t *testing.T) {
			row := wirelessProfileTagObservation{actual: &wirelessProfileTagMetadata{fields: map[string]any{"siteIds": []string{"a", "b"}, "apZones": []string{"z1", "z2"}}}, payload: map[string]any{"siteIds": []string{"a"}, "apZones": []string{"z1", "z2"}}}
			if mode == "zone" {
				row.payload["siteIds"] = []string{"a", "b"}
				row.payload["apZones"] = []string{"z1"}
			}
			if mode == "empty" {
				row.payload["siteIds"] = []string{}
			}
			if mode == "unchanged" {
				row.payload["siteIds"] = []string{"a", "b"}
			}
			ids, err := wirelessTagRemovedScopes(row)
			if mode == "empty" || mode == "unchanged" {
				if err == nil {
					t.Fatal("unqualified scope admitted")
				}
				return
			}
			expected := []string{"b"}
			if mode == "zone" {
				expected = []string{"a", "b"}
			}
			if err != nil || !reflect.DeepEqual(ids, expected) {
				t.Fatal("affected sites incomplete", ids, err)
			}
		})
	}
}

func TestWirelessTagMembershipCapacityPreflight(t *testing.T) {
	// A valid native ID may still make the versioned selector exceed the existing
	// journal capacity. Refuse in whole-source preflight before any native read or
	// prerequisite write, rather than relying on a later API validation failure.
	comparison := &templateInputComparison{key: make([]byte, 32), keyID: strings.Repeat("d", 64)}
	key := strings.Repeat("a", 64)
	baseline := map[string]any{"native": "snapshot"}
	mac, err := wirelessTagMAC(comparison, "wireless-site-tags/", "profile", key, "tag", baseline)
	if err != nil {
		t.Fatal(err)
	}
	tags := &wirelessProfileTagExecutor{}
	e := &networkProfileConfiguration{comparison: comparison, tagExecutors: []*wirelessProfileTagExecutor{tags}}
	latest := map[string]wirelessTagMemberProof{"wireless-site-tags/" + key: {id: "tag", mac: mac, row: cvk.NetworkControllerConfigurationActionApplication{Operation: cvk.NetworkControllerConfigurationOperation{ParentID: "profile"}}}}
	checked := 0
	e.bindWirelessTagMembership(&configapi.NetworkControllerConfig{Spec: configapi.NetworkControllerConfigSpec{PrunePolicy: configapi.NetworkControllerRetentionPolicyDelete}}, latest, func(context.Context, string) error { checked++; return nil }, strings.Repeat("e", 64))
	row := wirelessProfileTagObservation{resource: "site-tag:" + key + ":" + strings.Repeat("b", 64), profileID: "profile", tagID: "tag", baseline: baseline, actual: &wirelessProfileTagMetadata{fields: map[string]any{"siteIds": []string{"keep", strings.Repeat("x", 128)}}}, payload: map[string]any{"siteIds": []string{"keep"}}}
	if tags.removal == nil || tags.removal(context.Background(), row) != errConfigurationOwned || checked != 1 {
		t.Fatal("oversized membership scope bypassed preflight")
	}
}
