// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestWirelessCleanAirRecoveryScope(t *testing.T) { exerciseWirelessFeatureRecoveryScope(t, false) }
func TestWirelessFRARecoveryScope(t *testing.T)      { exerciseWirelessFeatureRecoveryScope(t, true) }

func exerciseWirelessFeatureRecoveryScope(t *testing.T, fra bool) {
	t.Helper()
	for _, mixed := range []bool{false, true} {
		raw := wirelessCleanAirModel
		baseResource, prefix := "cleanair:CVK_LAB", "cleanair/"
		secondary := `"rrm_fra":[{"name":"FRA","radio_band":"2.4GHz_5GHz"}],`
		if fra {
			raw = wirelessFRAModel
			baseResource = "rrm-fra:CVK_LAB"
			prefix = "rrm_fra/"
			secondary = `"cleanair":[{"name":"CA","radio_band":"2.4GHz"}],`
		}
		resource := baseResource
		if mixed {
			raw = strings.Replace(raw, `"wireless":{`, `"wireless":{`+secondary, 1)
			resource = prefix + baseResource
		}
		plan, err := (&client{}).compileConfigurationApply([]byte(raw), []string{"feature_templates"}, nil)
		if err != nil || plan.recoverKnownTask == nil {
			t.Fatal("missing qualified recovery", err)
		}
		now := metav1.NewTime(time.Now().Add(-time.Second))
		original := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "feature_templates", Resource: resource, Action: "Update", Phase: "Unknown", TaskID: "task-id", TargetID: "native-id", SubmittedAt: &now}
		for _, mode := range []string{"known", "missing-task", "missing-target", "create", "delete", "section", "phase", "serial", "parent", "created", "group", "dependencies", "cancellation", "prepared", "credential", "history", "missing-time", "zero-time", "id", "owner", "generation", "other-name", "other-prefix", "double-prefix", "fra", "unqualified-namespace"} {
			t.Run(resource+"/"+mode, func(t *testing.T) {
				op := original.DeepCopy()
				switch mode {
				case "missing-task":
					op.TaskID = ""
				case "missing-target":
					op.TargetID = ""
				case "create":
					op.Action = "Create"
					op.TargetID = ""
				case "delete":
					op.Action = "Delete"
				case "section":
					op.Section = "wireless"
				case "phase":
					op.Phase = "Accepted"
				case "serial":
					op.TargetSerial = "serial"
				case "parent":
					op.ParentID = "parent"
				case "created":
					op.CreatedResourceID = "created"
				case "group":
					op.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{}
				case "dependencies":
					op.DependencyVersions = &cvk.NetworkControllerConfigurationDependencyVersions{}
				case "cancellation":
					op.CancellationRequestedAt = &now
				case "prepared":
					op.PreparedAt = &now
				case "credential":
					op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{Version: 1}}
				case "history":
					op.SSIDCredentialHistory = []cvk.NetworkControllerConfigurationSSIDCredentialHistory{{Version: 1}}
				case "missing-time":
					op.SubmittedAt = nil
				case "zero-time":
					op.SubmittedAt = &metav1.Time{}
				case "id":
					op.ID = "not-an-operation-id"
				case "owner":
					op.ConfigUID = ""
				case "generation":
					op.ConfigGeneration = 0
				case "other-name":
					op.Resource = strings.Replace(resource, "CVK_LAB", "OTHER", 1)
				case "other-prefix":
					op.Resource = "objects/" + resource
				case "double-prefix":
					op.Resource = prefix + prefix + baseResource
				case "fra":
					op.Resource = "rrm_fra/rrm-fra:UNSELECTED"
					if fra {
						op.Resource = "cleanair/cleanair:UNSELECTED"
					}
				case "unqualified-namespace":
					if mixed {
						op.Resource = baseResource
					} else {
						op.Resource = prefix + baseResource
					}
				}
				before := op.DeepCopy()
				if plan.canObserveUnknownTask(op) != (mode == "known") {
					t.Fatal("recovery scope mismatch", mode)
				}
				if !reflect.DeepEqual(before, op) {
					t.Fatal("selector mutated retained operation")
				}
			})
		}
		if plan.canObserveUnknownTask(nil) {
			t.Fatal("nil operation accepted")
		}
	}
	if callback := wirelessFeatureKnownTaskRecovery(&tagExecutor{}); callback != nil {
		t.Fatal("unrelated executor recovery widened")
	}
	if callback := wirelessFeatureKnownTaskRecovery(&wirelessConfiguration{configurationExecutor: &wirelessFRAExecutor{}}); callback != nil {
		t.Fatal("empty FRA selection gained recovery")
	}
}
