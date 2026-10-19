// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"
)

func TestTemplateDeploymentResourceBinding(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, suffix := range []string{"base/version/2/" + digest, "base/base/2/" + digest, "base/version/0/" + digest, "base/version/02/" + digest, "base/version/-1/" + digest, "base/version/2/ABC", "base/version/2/" + strings.Repeat("g", 64), "base/version/2/" + digest + "/extra"} {
		binding, err := parseTemplateDeploymentResource("template-deploy:" + suffix)
		valid := suffix == "base/version/2/"+digest
		if (err == nil) != valid {
			t.Fatal("invalid version binding acceptance")
		}
		if valid && (binding.baseID != "base" || binding.version.ID != "version" || binding.version.Number != 2) {
			t.Fatal("wrong parsed version")
		}
		if !valid && binding != nil {
			t.Fatal("partial version binding escaped")
		}
	}
}

func TestTemplateRecoveryRejectsInvalidReceiptBeforeIO(t *testing.T) {
	now := time.Now()
	for _, mode := range []string{"receipt", "zero-time", "future-time", "phase"} {
		at := metav1.NewTime(now.Add(-time.Minute))
		op := &cvk.NetworkControllerConfigurationOperation{Phase: "Accepted", Section: "inventory", Action: "Update", TaskID: deploymentFixtureID, SubmittedAt: &at}
		switch mode {
		case "receipt":
			op.TaskID = "generic-task"
		case "zero-time":
			op.SubmittedAt = &metav1.Time{}
		case "future-time":
			at = metav1.NewTime(now.Add(time.Minute))
		case "phase":
			op.Phase = "Unknown"
		}
		var c *client
		if done, err := c.observeRegularTemplateDeployment(context.Background(), inventoryDeviceIntent{}, regularTemplateTarget{}, op, now); done || err == nil {
			t.Fatal("invalid operation accepted", mode)
		}
	}
}
