// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"net/http"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestFabricIPAMExampleCompilesBothModes(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/ipam-fabric-apply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	c := &client{}
	if !supportedConfigurationApply(&config) {
		t.Fatal("combined Apply is not selected")
	}
	plan, err := c.compileConfigurationApply(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.section != "fabric" || plan.executor.count() != 6 || len(plan.sections) != 2 {
		t.Fatal("wrong composed plan", plan.sections)
	}
	reports, err := c.compileConfigReports(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections)
	if err != nil || len(reports) != 2 {
		t.Fatal("Report compilation failed", err)
	}
}

func TestFabricIPAMAnchorGatesPrerequisiteWrites(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/ipam-fabric-apply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	var model map[string]any
	if err := json.Unmarshal(config.Spec.Source.Inline.Raw, &model); err != nil {
		t.Fatal(err)
	}
	fabric := model["catalyst_center"].(map[string]any)["fabric"].(map[string]any)
	fabric["l3_virtual_networks"].([]any)[0].(map[string]any)["anchor_site"] = "Global/Lab"
	config.Spec.Source.Inline.Raw, err = json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			writes++
			t.Errorf("prerequisite mutation: %s %s", r.Method, r.URL.Path)
		}
		// A denied inventory also cannot authorize a prerequisite mutation.
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	plan, err := c.compileConfigurationApply(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections, nil)
	if err != nil {
		t.Fatal(err)
	}
	if write, err := plan.executor.next(context.Background()); !errors.Is(err, errConfigurationDeviceAdmission) || write != nil || writes != 0 {
		t.Fatal("combined gate escaped", write, err, writes)
	}
	// Exercise the fabric part directly: it must gate before even an inventory GET.
	group := plan.executor.(*fabricAnchorAdmissionGate).configurationExecutor.(*configurationGroup)
	if write, err := group.parts[1].executor.next(context.Background()); !errors.Is(err, errConfigurationDeviceAdmission) || write != nil {
		t.Fatal(write, err)
	}
}
