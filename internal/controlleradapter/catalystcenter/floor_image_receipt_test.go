// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFloorImageReceiptBinding(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	submitted := metav1.NewTime(now.Add(-time.Minute))
	verified := metav1.NewTime(now.Add(-time.Second))
	want := floorImageIntent{floor: "Global/Lab/Building/Floor", digest: strings.Repeat("a", 64), filename: "floor.pdf"}
	original := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: want.resource(), VerifiedAt: &verified, Operation: cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("b", 32), Resource: want.operationResource(), Section: "sites", Phase: "Verified", Action: "Update", ConfigName: "model", ConfigUID: "owner", TargetID: "floor", TaskID: "task", SubmittedAt: &submitted}}
	for _, mode := range []string{"applied", "absent", "other-section", "changed-bytes", "renamed-file", "foreign-owner", "foreign-name", "replaced-floor", "duplicate", "wrong-operation", "wrong-key", "wrong-section", "unknown", "missing-task", "missing-time", "future-time", "reversed-time", "missing-verification", "serial", "parent", "bad-id", "wrong-action"} {
		t.Run(mode, func(t *testing.T) {
			receipt := original
			receipt.Operation = *original.Operation.DeepCopy()
			receipt.VerifiedAt = original.VerifiedAt.DeepCopy()
			desired := want
			switch mode {
			case "other-section":
				receipt.Section = "wireless"
			case "changed-bytes":
				desired.digest = strings.Repeat("c", 64)
			case "renamed-file":
				desired.filename = "renamed.pdf"
			case "foreign-owner":
				receipt.Operation.ConfigUID = "foreign"
			case "foreign-name":
				receipt.Operation.ConfigName = "foreign"
			case "replaced-floor":
				receipt.Operation.TargetID = "replacement"
			case "wrong-operation":
				receipt.Operation.Resource = "floor-image:invalid"
			case "wrong-key":
				receipt.Operation.Resource = "floor-image:" + strings.Repeat("d", 64) + ":" + want.digest
			case "wrong-section":
				receipt.Operation.Section = "wireless"
			case "unknown":
				receipt.Operation.Phase = "Unknown"
			case "missing-task":
				receipt.Operation.TaskID = ""
			case "missing-time":
				receipt.Operation.SubmittedAt = nil
			case "future-time":
				receipt.VerifiedAt = &metav1.Time{Time: now.Add(time.Hour)}
			case "reversed-time":
				receipt.VerifiedAt = &metav1.Time{Time: submitted.Add(-time.Second)}
			case "missing-verification":
				receipt.VerifiedAt = nil
			case "serial":
				receipt.Operation.TargetSerial = "device"
			case "parent":
				receipt.Operation.ParentID = "parent"
			case "bad-id":
				receipt.Operation.ID = "invalid"
			case "wrong-action":
				receipt.Operation.Action = "Delete"
			}
			receipts := []cvk.NetworkControllerConfigurationActionApplication{receipt}
			if mode == "absent" {
				receipts = nil
			}
			if mode == "duplicate" {
				receipts = append(receipts, receipt)
			}
			applied, err := floorImageApplied(desired, "floor", "model", "owner", receipts, now)
			success := mode == "applied"
			valid := success || mode == "absent" || mode == "changed-bytes" || mode == "renamed-file" || mode == "other-section"
			if applied != success || (err == nil) != valid {
				t.Fatalf("applied=%v error=%v", applied, err)
			}
		})
	}
}

func TestFloorImageReceiptResourceSyntax(t *testing.T) {
	valid := "floor-image:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	for _, input := range []string{valid, strings.ToUpper(valid), valid + ":extra", strings.Replace(valid, "floor-image:", "planned-ap:", 1), valid[:len(valid)-1], strings.Replace(valid, "b", "z", 1)} {
		key, err := floorImageReceiptKey(input)
		if input == valid {
			if err != nil || key != strings.TrimSuffix(valid, ":"+strings.Repeat("b", 64)) {
				t.Fatal("valid resource rejected")
			}
		} else if err == nil || key != "" {
			t.Fatal("malformed resource accepted")
		}
	}
}
