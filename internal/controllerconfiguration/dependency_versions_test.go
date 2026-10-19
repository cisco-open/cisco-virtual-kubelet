// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
)

func TestDependencyVersionScopeRemainsGated(t *testing.T) {
	op, targets := groupIdentityFixture()
	op.TargetSerial = "serial"
	if _, err := OperationDigest(op); err != nil {
		t.Fatal(err)
	}
	op.DependencyVersions = &cvk.NetworkControllerConfigurationDependencyVersions{Kind: "template-graph-v1", Entries: []cvk.NetworkControllerConfigurationDependencyVersion{{ID: "parent", VersionID: "parent-v2", Version: 2}, {ID: "member", VersionID: "member-v4", Version: 4}}}
	if _, err := OperationDigest(op); err == nil {
		t.Fatal("legacy physical protocol admitted unqualified graph")
	}
	op.TargetSerial = ""
	if _, err := GroupOperationDigest(op, targets); err == nil {
		t.Fatal("group protocol admitted unqualified graph")
	}
}

func dependencyGraphFixture() *cvk.NetworkControllerConfigurationDependencyVersions {
	return &cvk.NetworkControllerConfigurationDependencyVersions{Kind: "template-graph-v1", Entries: []cvk.NetworkControllerConfigurationDependencyVersion{{ID: "parent", VersionID: "parent-v2", Version: 2}, {ID: "member", VersionID: "member-v4", Version: 4}, {ID: "second", VersionID: "second-v1", Version: 1}}}
}
func TestDependencyVersionDigestBinding(t *testing.T) {
	for _, mode := range []string{"same", "parent", "member", "order", "number", "phase", "missing", "duplicate", "wrong-scope", "bad-resource", "wrong-kind"} {
		t.Run(mode, func(t *testing.T) {
			op, _ := groupIdentityFixture()
			op.Section = "inventory"
			op.TargetSerial = "serial"
			op.ParentID = "site"
			op.Resource = "template-composite-deploy:" + strings.Repeat("c", 64)
			op.DependencyVersions = dependencyGraphFixture()
			before, err := OperationDigest(op)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "parent":
				op.DependencyVersions.Entries[0].VersionID = "parent-v3"
			case "member":
				op.DependencyVersions.Entries[1].VersionID = "member-v5"
			case "order":
				op.DependencyVersions.Entries[1], op.DependencyVersions.Entries[2] = op.DependencyVersions.Entries[2], op.DependencyVersions.Entries[1]
			case "number":
				op.DependencyVersions.Entries[1].Version++
			case "phase":
				op.Phase = "Accepted"
				op.TaskID = "receipt"
				now := metav1.Now()
				op.SubmittedAt = &now
			case "missing":
				op.DependencyVersions = nil
			case "duplicate":
				op.DependencyVersions.Entries[1] = op.DependencyVersions.Entries[0]
			case "wrong-scope":
				op.Section = "fabric"
			case "bad-resource":
				op.Resource = "template-composite-deploy:invalid"
			case "wrong-kind":
				op.DependencyVersions.Kind = "unknown"
			}
			after, err := OperationDigest(op)
			valid := mode == "same" || mode == "phase" || mode == "parent" || mode == "member" || mode == "order" || mode == "number"
			if !valid {
				if err == nil {
					t.Fatal("invalid graph digested")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (before == after) != (mode == "same" || mode == "phase") {
				t.Fatal("graph not bound or phase altered immutable digest")
			}
		})
	}
}

func TestPrivateComparisonDependencyDigestBinding(t *testing.T) {
	op, _ := groupIdentityFixture()
	op.Section, op.TargetSerial, op.ParentID = "inventory", "serial", "site"
	base := "template-composite-deploy:" + strings.Repeat("c", 64)
	op.Resource = base + ":private-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	op.DependencyVersions = dependencyGraphFixture()
	before, err := OperationDigest(op)
	if err != nil {
		t.Fatal(err)
	}
	op.Resource = base + ":private-v1:" + strings.Repeat("a", 64) + ":" + strings.Repeat("d", 64)
	after, err := OperationDigest(op)
	if err != nil || before == after {
		t.Fatal("private inputs not bound to reservation digest", err)
	}
	for _, suffix := range []string{":private-v2:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), ":private-v1:a:b", ":private-v1:" + strings.Repeat("A", 64) + ":" + strings.Repeat("b", 64)} {
		op.Resource = base + suffix
		if _, err := OperationDigest(op); err == nil {
			t.Fatal("malformed private comparison admitted")
		}
	}
}
