// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPrivateTemplateInputsCanonicalAndRedacted(t *testing.T) {
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64)}
	base := "template-deploy:base/version/2/" + strings.Repeat("b", 64)
	first := []byte(`{"templateId":"base","copyingConfig":true,"forcePushTemplate":true,"targetInfo":[{"id":"device","type":"MANAGED_DEVICE_UUID","params":{"z":"secret-value","a":["1","2"]},"versionedTemplateId":"version"}]}`)
	a, err := comparison.bind(base, first)
	if err != nil {
		t.Fatal(err)
	}
	reordered := []byte(`{"targetInfo":[{"versionedTemplateId":"version","params":{"a":["1","2"],"z":"secret-value"},"type":"MANAGED_DEVICE_UUID","id":"device"}],"templateId":"base","copyingConfig":false,"forcePushTemplate":false}`)
	b, err := comparison.bind(base, reordered)
	if err != nil || a != b {
		t.Fatal("formatting or top-level execution flags changed provider target comparison", err)
	}
	changed, err := comparison.bind(base, []byte(strings.Replace(string(first), "secret-value", "other-value", 1)))
	if err != nil || changed == a {
		t.Fatal("parameter change was hidden")
	}
	if strings.Contains(a, "secret-value") || strings.Contains(fmt.Sprintf("%v %#v", comparison, comparison), strings.Repeat("k", 32)) {
		t.Fatal("private input/key exposed")
	}
	if _, err := parseTemplateDeploymentResource(a); err != nil {
		t.Fatal("version recovery rejected private resource", err)
	}
	for _, bad := range []string{a + "/extra", strings.Replace(a, "/private-v1/", "/private-v2/", 1), a[:len(a)-1]} {
		if _, _, err := splitTemplateComparisonResource(bad); err == nil {
			t.Fatal("malformed comparison resource accepted")
		}
	}
}

func TestPrivateCompositeInputOrderAndFlags(t *testing.T) {
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("a", 64)}
	base := "template-composite-deploy:" + strings.Repeat("b", 64)
	body := map[string]any{"templateId": "parent-version", "targetInfo": []any{map[string]any{"id": "device", "params": map[string]any{}}}, "memberTemplateDeploymentInfo": []any{
		map[string]any{"templateId": "one-version", "mainTemplateId": "one", "copyingConfig": true, "targetInfo": []any{map[string]any{"id": "device", "params": map[string]any{"p": "private-one"}}}},
		map[string]any{"templateId": "two-version", "mainTemplateId": "two", "targetInfo": []any{map[string]any{"id": "device", "params": map[string]any{"p": "private-two"}}}},
	}}
	encode := func() []byte {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	a, err := comparison.bind(base, encode())
	if err != nil {
		t.Fatal(err)
	}
	members := body["memberTemplateDeploymentInfo"].([]any)
	members[0].(map[string]any)["copyingConfig"] = false
	b, err := comparison.bind(base, encode())
	if err != nil || a != b {
		t.Fatal("execution option altered target-input identity")
	}
	members[0], members[1] = members[1], members[0]
	c, err := comparison.bind(base, encode())
	if err != nil || c == a {
		t.Fatal("member order disappeared from comparison")
	}
	_, binding, err := splitTemplateComparisonResource(c)
	if err != nil || binding == nil {
		t.Fatal("composite private resource", err)
	}
}

func TestPrivateTemplateOnChangeUsesLatestEffect(t *testing.T) {
	for _, mode := range []string{"same", "changed", "rollback", "unrelated-revision", "rotated-key", "legacy", "version", "tied-history"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			submitted := metav1.NewTime(now.Add(-time.Minute))
			verified := metav1.NewTime(now.Add(-10 * time.Second))
			resource := func(input string) string {
				return "template-deploy:base/version/2/" + strings.Repeat("b", 64) + "/private-v1/" + strings.Repeat("a", 64) + "/" + strings.Repeat(input, 64)
			}
			desired := &cvk.NetworkControllerConfigurationOperation{Resource: resource("c"), Section: "inventory", Action: "Update", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 2, ControllerGeneration: 1, TargetID: "device", TargetSerial: "serial", ParentID: "site"}
			old := desired.DeepCopy()
			old.ConfigGeneration = 1
			old.ID = strings.Repeat("d", 32)
			old.Phase = "Verified"
			old.TaskID = deploymentFixtureID
			old.SubmittedAt = &submitted
			state := &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: "config", ConfigUID: "owner"}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: old.Resource, Operation: *old, VerifiedAt: &verified}}}
			switch mode {
			case "changed":
				desired.Resource = resource("e")
			case "rotated-key":
				desired.Resource = strings.Replace(desired.Resource, "/"+strings.Repeat("a", 64)+"/", "/"+strings.Repeat("f", 64)+"/", 1)
			case "legacy":
				state.ActionApplications[0].Resource = strings.Split(old.Resource, "/private-v1/")[0]
				state.ActionApplications[0].Operation.Resource = state.ActionApplications[0].Resource
			case "version":
				desired.Resource = strings.Replace(desired.Resource, "/version/2/", "/new-version/3/", 1)
			case "rollback", "tied-history":
				next := state.ActionApplications[0].DeepCopy()
				next.Operation.ID = strings.Repeat("e", 32)
				next.Operation.Resource = resource("e")
				next.Resource = next.Operation.Resource
				if mode == "rollback" {
					at := metav1.NewTime(now.Add(-5 * time.Second))
					next.VerifiedAt = &at
				}
				state.ActionApplications = append(state.ActionApplications, *next)
			case "unrelated-revision":
				desired.SourceVersion = "new-source-version"
				desired.ControllerGeneration++
			}
			done, err := templateDeploymentAlreadyApplied(state, desired, "ON_CHANGE", now)
			wantErr := mode == "rotated-key" || mode == "legacy" || mode == "tied-history"
			wantDone := mode == "same" || mode == "unrelated-revision"
			if (err != nil) != wantErr || done != wantDone {
				t.Fatal("wrong private redeploy decision", mode, done, err)
			}
		})
	}
}
