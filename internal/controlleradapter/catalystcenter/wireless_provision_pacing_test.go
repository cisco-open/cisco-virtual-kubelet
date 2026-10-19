// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestWirelessProvisionDependencyPacing(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	resource := "managed-ap:" + strings.Repeat("a", 64)
	for _, scenario := range []string{"settled", "nine seconds", "legacy receipt", "future", "before submission", "wrong revision", "accepted", "duplicate", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			config := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "intent", UID: "intent-uid", Generation: 1}}
			receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: resource, VerifiedAt: &metav1.Time{Time: now.Add(-11 * time.Second)}, Operation: cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("b", 32), ConfigName: "intent", ConfigUID: "intent-uid", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: resource, Phase: "Verified", Action: "Update", TargetID: "wlc", TargetSerial: "serial", TaskID: "task", SubmittedAt: &metav1.Time{Time: now.Add(-time.Minute)}}}
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Generation: 1}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{receipt}}}}
			row := &nc.Status.Configuration.ActionApplications[0]
			switch scenario {
			case "nine seconds":
				row.VerifiedAt = &metav1.Time{Time: now.Add(-9 * time.Second)}
			case "legacy receipt":
				row.VerifiedAt = nil
			case "future":
				row.VerifiedAt = &metav1.Time{Time: now.Add(time.Second)}
			case "before submission":
				row.VerifiedAt = &metav1.Time{Time: now.Add(-2 * time.Minute)}
			case "wrong revision":
				row.Operation.ConfigGeneration++
			case "accepted":
				row.Operation.Phase = "Accepted"
			case "duplicate":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, receipt)
			case "missing":
				nc.Status.Configuration.ActionApplications = nil
			}
			err := wirelessProvisionDependenciesReady(nc.DeepCopy(), config, nil, []string{resource}, now)
			if (err == nil) != (scenario == "settled") {
				t.Fatalf("unexpected pacing result: %v", err)
			}
		})
	}
}
