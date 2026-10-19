// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAuthResultBindingSerializedRecovery(t *testing.T) {
	for _, mode := range []string{"stable", "voice-drift", "bpdu-drift", "late-voice-drift", "description", "reordered-contracts", "replaced-profile", "corrupt-result", "unknown-version", "legacy-acknowledged"} {
		t.Run(mode, func(t *testing.T) {
			model := lowImpactAuthModel()
			if mode == "bpdu-drift" {
				model = []byte(authModel)
			}
			desired, err := parseAuthTemplates(model)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(desired[0])
			var actual authProfile
			_ = json.Unmarshal(raw, &actual)
			actual.ID = "original-profile"
			hosts, voice, bpdu := "Unlimited", true, true
			actual.Hosts, actual.Voice = &hosts, &voice
			if mode == "bpdu-drift" {
				actual.BPDU = &bpdu
			}
			if actual.ACL != nil {
				description := "SECRET-DESCRIPTION"
				actual.ACL.Description = &description
			}
			reads, writes := 0, 0
			recovering := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != authProfilesPath {
					t.Error("unexpected API")
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodGet {
					if recovering {
						reads++
						if mode == "late-voice-drift" && reads == 2 {
							v := false
							actual.Voice = &v
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": []authProfile{actual}})
					return
				}
				var rows []authProfile
				if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&rows) != nil || len(rows) != 1 {
					t.Error("wrong update")
					return
				}
				if rows[0].BPDU != nil || rows[0].FabricID != "" {
					t.Error("global update attempted fabric-only setter")
				}
				rows[0].BPDU = actual.BPDU
				actual = rows[0]
				writes++
				fmt.Fprint(w, `{"response":{"taskId":"owned-task"}}`)
			})
			plan, err := (&authConfigurationExecutor{client: c, desired: desired}).next(context.Background())
			if err != nil || plan == nil {
				t.Fatal("missing global plan", err)
			}
			parts, err := parseGlobalAuthOperationResource(plan.resource)
			if err != nil || parts[0] != authConfigurationValueDigest(desired[0].Name) || parts[1] == parts[2] {
				t.Fatal("original/result identity missing", err)
			}
			if _, err := plan.submit(context.Background()); err != nil {
				t.Fatal(err)
			}
			op := &cvk.NetworkControllerConfigurationOperation{Section: "authentication_templates", Action: "Update", Resource: plan.resource, TargetID: plan.identity.target, TaskID: "owned-task", Phase: "Verifying"}
			switch mode {
			case "voice-drift":
				v := false
				actual.Voice = &v
			case "bpdu-drift":
				v := false
				actual.BPDU = &v
			case "description":
				v := "independent ignored description"
				actual.ACL.Description = &v
			case "reordered-contracts":
				v := *actual.ACL.Contracts
				v[0], v[1] = v[1], v[0]
			case "replaced-profile":
				actual.ID = "replacement-profile"
			case "corrupt-result":
				op.Resource = globalAuthResourcePrefix + strings.Join([]string{parts[0], parts[1], parts[1]}, ":")
			case "unknown-version":
				op.Resource = strings.Replace(op.Resource, ":v2:", ":v9:", 1)
			case "legacy-acknowledged":
				op.Resource = desired[0].Name
			}
			raw, _ = json.Marshal(op)
			if strings.Contains(string(raw), "SECRET-DESCRIPTION") {
				t.Fatal("description entered durable identity")
			}
			var recovered cvk.NetworkControllerConfigurationOperation
			_ = json.Unmarshal(raw, &recovered)
			future, _ := parseAuthTemplates([]byte(strings.Replace(authModel, "Closed Authentication", "Open Authentication", 1)))
			recovering = true
			ok, err := (&authConfigurationExecutor{client: c, desired: append(desired, future...)}).verify(context.Background(), &recovered)
			valid := mode == "stable" || mode == "description" || mode == "reordered-contracts" || mode == "legacy-acknowledged"
			if valid && (err != nil || !ok || reads != 2) {
				t.Fatal("scoped recovery failed", ok, err, reads)
			}
			if !valid && (ok || !errors.Is(err, errGlobalAuthBinding)) {
				t.Fatal("changed/unsupported binding accepted", ok, err)
			}
			if writes != 1 {
				t.Fatal("recovery replayed update")
			}
		})
	}
}

func TestAuthResultCompletionGateAndPublicStatus(t *testing.T) {
	for _, mode := range []string{"bound", "legacy", "malformed", "unknown-version", "missing-task", "missing-submission-time", "wrong-scope", "prepared-cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"authentication_templates"}
			config.Spec.Source.Inline.Raw = []byte(authModel)
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			desired, _ := parseAuthTemplates([]byte(authModel))
			actual := desired[0]
			resource := globalAuthOperationResource(desired[0], actual)
			if _, err := a.prepareConfiguration(ctx, config, nil, "authentication_templates", resource, "Update", configurationResourceIdentity{target: "profile"}); err != nil {
				t.Fatal(err)
			}
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			op := nc.Status.Configuration.Operation
			op.Phase, op.TaskID = "Verified", "owned-task"
			at := metav1.Now()
			op.SubmittedAt = &at
			switch mode {
			case "legacy":
				op.Resource = "Closed Authentication"
			case "malformed":
				op.Resource = globalAuthResourcePrefix + "pruned"
			case "unknown-version":
				op.Resource = strings.Replace(resource, ":v2:", ":v9:", 1)
			case "missing-task":
				op.TaskID = ""
			case "missing-submission-time":
				op.SubmittedAt = nil
			case "wrong-scope":
				op.ParentID = "fabric-id"
			case "prepared-cancel":
				op.Phase, op.TaskID, op.Resource = "Prepared", "", "Closed Authentication"
				op.SubmittedAt = nil
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			before := journalState(t, a).Operation.DeepCopy()
			err = a.finishConfiguration(ctx, before.ID)
			valid := mode == "bound" || mode == "legacy" || mode == "prepared-cancel"
			if valid {
				if err != nil || journalState(t, a).Operation != nil {
					t.Fatal("valid boundary not retired", err)
				}
				return
			}
			if !errors.Is(err, errGlobalAuthBinding) || !reflect.DeepEqual(before, journalState(t, a).Operation) {
				t.Fatal("invalid binding escaped completion gate", err)
			}
			a.client = &client{} // Verified recovery must not need an HTTP request.
			status := &configapi.NetworkControllerConfigStatus{}
			_, _, reason, _ := (&configReconciler{a: a}).evaluateConfigurationApply(ctx, nc, config, status)
			if reason != "OperationRecoveryRequired" || !reflect.DeepEqual(before, journalState(t, a).Operation) {
				t.Fatal("missing public binding recovery status", reason)
			}
		})
	}
}
