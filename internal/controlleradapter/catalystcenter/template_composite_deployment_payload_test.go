// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func compositeDeploymentFixture() compositeTemplateDeployment {
	return compositeTemplateDeployment{
		base:    templateSummary{ID: "composite", ProjectID: "project", Name: "Combined"},
		version: templateVersion{ID: "composite-committed", Number: 3}, deviceID: "device", forcePush: true,
		members: []regularTemplateDeployment{
			{base: templateSummary{ID: "baseline", ProjectID: "project", Name: "Baseline"}, version: templateVersion{ID: "baseline-committed", Number: 2}, deviceID: "device", copyingConfig: true, forcePush: true, parameters: map[string][]string{"hostname": {"edge"}, "vlans": {"10", "20"}}},
			{base: templateSummary{ID: "telemetry", ProjectID: "other-project", Name: "Telemetry"}, version: templateVersion{ID: "telemetry-committed", Number: 7}, deviceID: "device", parameters: map[string][]string{"optional": {}}},
		},
	}
}

func TestCompositeTemplateDeploymentPayload(t *testing.T) {
	raw, err := compositeTemplateDeploymentPayload(compositeDeploymentFixture())
	if err != nil {
		t.Fatal(err)
	}
	// Independent expected native contract: committed and base UUIDs must not be
	// interchanged, member order and explicit false flags must survive conversion.
	expected := `{
 "templateId":"composite-committed","mainTemplateId":"composite","isComposite":true,"forcePushTemplate":true,
 "targetInfo":[{"id":"device","type":"MANAGED_DEVICE_UUID","params":{},"resourceParams":[{"type":"MANAGED_DEVICE_UUID","scope":"RUNTIME","value":"device"}]}],
 "memberTemplateDeploymentInfo":[
 {"templateId":"baseline-committed","mainTemplateId":"baseline","isComposite":false,"copyingConfig":true,"forcePushTemplate":true,"targetInfo":[{"id":"device","type":"MANAGED_DEVICE_UUID","params":{"hostname":"edge","vlans":["10","20"]},"resourceParams":[{"type":"MANAGED_DEVICE_UUID","scope":"RUNTIME","value":"device"}]}]},
 {"templateId":"telemetry-committed","mainTemplateId":"telemetry","isComposite":false,"copyingConfig":false,"forcePushTemplate":false,"targetInfo":[{"id":"device","type":"MANAGED_DEVICE_UUID","params":{"optional":[]},"resourceParams":[{"type":"MANAGED_DEVICE_UUID","scope":"RUNTIME","value":"device"}]}]}
 ]}`
	var got, want any
	if json.Unmarshal(raw, &got) != nil || json.Unmarshal([]byte(expected), &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("native composite mapping mismatch: %s", raw)
	}
}

func TestCompositeTemplateDeploymentPayloadRejectsUnsafeBindings(t *testing.T) {
	for _, mode := range []string{"empty", "too-many", "duplicate", "self-base", "self-version", "cross-version", "wrong-device", "bad-project", "draft-parent", "draft-member", "same-parent-id", "same-member-id", "nil-parameter", "bad-parameter", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			want := compositeDeploymentFixture()
			switch mode {
			case "empty":
				want.members = nil
			case "too-many":
				want.members = make([]regularTemplateDeployment, 1001)
			case "duplicate":
				want.members = append(want.members, want.members[0])
			case "self-base":
				want.members[0].base.ID = want.base.ID
			case "self-version":
				want.members[0].version.ID = want.version.ID
			case "cross-version":
				want.members[1].base.ID = want.members[0].version.ID
			case "wrong-device":
				want.members[1].deviceID = "other-device"
			case "bad-project":
				want.members[1].base.ProjectID = "../project"
			case "draft-parent":
				want.version.Number = 0
			case "draft-member":
				want.members[1].version.Number = 0
			case "same-parent-id":
				want.version.ID = want.base.ID
			case "same-member-id":
				want.members[1].version.ID = want.members[1].base.ID
			case "nil-parameter":
				want.members[0].parameters["hostname"] = nil
			case "bad-parameter":
				want.members[0].parameters["secret\nname"] = []string{"secret-value"}
			case "oversize":
				want.members[0].parameters["hostname"] = []string{strings.Repeat("x", 1<<20)}
			}
			raw, err := compositeTemplateDeploymentPayload(want)
			if err == nil || raw != nil {
				t.Fatal("unsafe binding produced payload")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("parameter leaked in error")
			}
		})
	}
}
