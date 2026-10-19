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

func TestFloorImagePathIdentity(t *testing.T) {
	want := floorImageIntent{floor: "Global/Lab/Building/Floor", filename: "maps/floor.pdf", digest: strings.Repeat("a", 64)}
	renamed := want
	renamed.filename = "maps/renamed/floor.pdf" // same basename and bytes, different source_path
	if want.resource() != renamed.resource() || want.operationResource() == renamed.operationResource() {
		t.Fatal("path replacement must change operation identity, not the bounded floor receipt key")
	}
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	for _, mode := range []string{"async-v2", "sync-v2", "async-v1", "sync-v1"} {
		t.Run(mode, func(t *testing.T) {
			resource, task := want.operationResource(), "task"
			if strings.HasSuffix(mode, "v1") {
				resource = want.legacyOperationResource()
			}
			if strings.HasPrefix(mode, "sync") {
				resource = strings.Replace(resource, "floor-image:", "floor-image-sync:", 1)
				task = ""
			}
			key, err := floorImageReceiptKey(resource)
			if err != nil || key != want.resource() || len(resource) > 512 {
				t.Fatal("versioned/legacy key did not round trip", err)
			}
			op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("b", 32), Resource: resource, Section: "sites", Action: "Update", Phase: "Verified", ConfigName: "model", ConfigUID: "owner", TargetID: "floor", TaskID: task, SubmittedAt: &now}
			rows := []cvk.NetworkControllerConfigurationActionApplication{{Resource: key, Section: "sites", Operation: op, VerifiedAt: &now}}
			for _, desired := range []floorImageIntent{want, renamed} {
				got, err := floorImageApplied(desired, "floor", "model", "owner", rows, now.Time)
				expected := desired.filename == want.filename || strings.HasSuffix(mode, "v1")
				if err != nil || got != expected {
					t.Fatal("new path identity or legacy no-replay boundary changed", got, expected, err)
				}
			}
			if strings.HasSuffix(mode, "v2") {
				if _, err := floorImageReceiptKey(strings.Replace(resource, ":v2:", ":v3:", 1)); err == nil {
					t.Fatal("unknown protocol version accepted")
				}
				if _, err := floorImageReceiptKey(resource[:len(resource)-1]); err == nil {
					t.Fatal("truncated path digest accepted")
				}
			}
		})
	}
}
