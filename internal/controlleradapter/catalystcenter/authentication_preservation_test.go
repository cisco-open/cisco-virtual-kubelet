// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func lowImpactAuthModel() []byte {
	model := strings.Replace(authModel, "Closed Authentication", "Low Impact", 1)
	model = strings.Replace(model, `"number_of_hosts":"Single"`, `"number_of_hosts":"Single","pre_auth_acl":{"enabled":true,"implicit_action":"DENY","description":"model-description-ignored","access_contracts":[{"action":"PERMIT","protocol":"UDP","port":"domain"},{"action":"PERMIT","protocol":"UDP","port":"bootpc"}]}`, 1)
	return []byte(model)
}

func TestAuthContractsRejectDuplicatePortsAndDhcpTCP(t *testing.T) {
	for _, model := range []string{
		strings.Replace(string(lowImpactAuthModel()), `"port":"bootpc"`, `"port":"domain"`, 1),
		strings.Replace(string(lowImpactAuthModel()), `"protocol":"UDP","port":"bootpc"`, `"protocol":"TCP","port":"bootpc"`, 1),
		strings.Replace(string(lowImpactAuthModel()), `"protocol":"UDP","port":"bootpc"`, `"protocol":"TCP_UDP","port":"bootps"`, 1),
	} {
		if _, err := parseAuthTemplates([]byte(model)); err == nil {
			t.Fatal("invalid API contract combination accepted")
		}
	}
}

func TestAuthPreservingSubmissionGuards(t *testing.T) {
	for _, mode := range []string{"stable", "description-drift", "voice-drift", "selected-replaced", "later-replaced", "later-drift", "mid-read-replaced", "reordered-contracts", "caller-mutated", "unrelated", "lost-ack"} {
		t.Run(mode, func(t *testing.T) {
			desired, err := parseAuthTemplates(lowImpactAuthModel())
			if err != nil {
				t.Fatal(err)
			}
			other, _ := parseAuthTemplates([]byte(strings.Replace(authModel, "Closed Authentication", "Open Authentication", 1)))
			desired = append(desired, other...)
			raw, _ := json.Marshal(desired)
			var actual []authProfile
			_ = json.Unmarshal(raw, &actual)
			actual[0].ID, actual[1].ID = "low-id", "open-id"
			description, hosts := "native-description-preserved", "Unlimited"
			actual[0].ACL.Description = &description
			actual[0].Hosts = &hosts
			voice := true
			actual[0].Voice = &voice
			closed, _ := parseAuthTemplates([]byte(authModel))
			closed[0].ID = "closed-id"
			actual = append(actual, closed[0])
			reads, writes, changed := 0, 0, false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != authProfilesPath {
					t.Error("unexpected auth endpoint")
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodGet {
					reads++
					_ = json.NewEncoder(w).Encode(map[string]any{"response": actual})
					if changed && mode == "mid-read-replaced" && reads == 2 {
						actual[0].ID = "replacement"
					}
					return
				}
				var body []authProfile
				if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
					t.Error("wrong auth update shape")
					return
				}
				if body[0].ID != "low-id" || body[0].FabricID != "" || body[0].ACL.Description == nil || *body[0].ACL.Description != "native-description-preserved" || *body[0].Hosts != "Single" || body[0].Voice == nil || !*body[0].Voice {
					t.Error("native preservation or destination changed")
				}
				actual[0] = body[0]
				writes++
				if mode == "lost-ack" {
					fmt.Fprint(w, `{}`)
				} else {
					fmt.Fprint(w, `{"response":{"taskId":"auth-task"}}`)
				}
			})
			e := &authConfigurationExecutor{client: c, desired: desired}
			write, err := e.next(context.Background())
			if err != nil || write == nil {
				t.Fatal("missing preserving auth plan", err)
			}
			changed = true
			switch mode {
			case "description-drift":
				replacement := "concurrent-description"
				actual[0].ACL.Description = &replacement
			case "voice-drift":
				replacement := false
				actual[0].Voice = &replacement
			case "selected-replaced":
				actual[0].ID = "replacement"
			case "later-replaced":
				actual[1].ID = "replacement"
			case "later-drift":
				actual[1].Hosts = &hosts
			case "reordered-contracts":
				contracts := *actual[0].ACL.Contracts
				contracts[0], contracts[1] = contracts[1], contracts[0]
			case "caller-mutated":
				*desired[0].Hosts = "Unlimited"
				(*desired[0].ACL.Contracts)[0].Action = "DENY"
			case "unrelated":
				actual[2].ID = "replacement-closed"
			}
			_, err = write.submit(context.Background())
			valid := mode == "stable" || mode == "reordered-contracts" || mode == "caller-mutated" || mode == "unrelated"
			if valid && (err != nil || writes != 1) {
				t.Fatal("valid preserving update failed", err, writes)
			}
			if !valid && (err == nil || mode != "lost-ack" && writes != 0 || mode == "lost-ack" && writes != 1) {
				t.Fatal("stale or uncertain update accepted", writes)
			}
			beforeReads, beforeWrites := reads, writes
			if _, err := write.submit(context.Background()); err == nil || reads != beforeReads || writes != beforeWrites {
				t.Fatal("auth request closure reused")
			}
		})
	}
}

func TestAuthDescriptionAndContractOrderRemainUnmanaged(t *testing.T) {
	desired, err := parseAuthTemplates(lowImpactAuthModel())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(desired[0])
	var got authProfile
	_ = json.Unmarshal(raw, &got)
	got.ID = "profile"
	description := "controller-description"
	got.ACL.Description = &description
	contracts := *got.ACL.Contracts
	contracts[0], contracts[1] = contracts[1], contracts[0]
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != http.MethodGet {
			t.Error("no-op/report mutated native state")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": []authProfile{got}})
	})
	if drift, err := c.reportAuthTemplates(context.Background(), desired); err != nil || len(drift) != 0 {
		t.Fatal("ignored metadata caused drift", err)
	}
	e := &authConfigurationExecutor{client: c, desired: desired}
	if write, err := e.next(context.Background()); err != nil || write != nil {
		t.Fatal("ignored metadata caused mutation", err)
	}
	op := &cvk.NetworkControllerConfigurationOperation{Action: "Update", Resource: got.Name, TargetID: got.ID}
	if ok, err := e.verify(context.Background(), op); err != nil || !ok {
		t.Fatal("semantic auth verification failed", err)
	}
}

func TestAuthVerificationScopesSerializedOperationAndRepeatsReadback(t *testing.T) {
	for _, mode := range []string{"stable", "late-replacement", "late-drift", "late-read-error", "nil", "foreign-parent", "unknown-resource", "wrong-action"} {
		t.Run(mode, func(t *testing.T) {
			desired, err := parseAuthTemplates(lowImpactAuthModel())
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(desired[0])
			var actual authProfile
			if err := json.Unmarshal(raw, &actual); err != nil {
				t.Fatal(err)
			}
			actual.ID = "original-profile"
			// This future selection is deliberately absent. Recovery must verify
			// only the accepted operation, rather than planning its successor.
			future, _ := parseAuthTemplates([]byte(authModel))
			desired = append(desired, future...)
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != authProfilesPath {
					t.Error("recovery attempted an unexpected request")
					http.NotFound(w, r)
					return
				}
				reads++
				if reads == 2 {
					switch mode {
					case "late-replacement":
						actual.ID = "replacement-profile"
					case "late-drift":
						hosts := "Unlimited"
						actual.Hosts = &hosts
					case "late-read-error":
						w.WriteHeader(http.StatusForbidden)
						return
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"response": []authProfile{actual}})
			})
			op := &cvk.NetworkControllerConfigurationOperation{Action: "Update", Resource: actual.Name, TargetID: actual.ID}
			switch mode {
			case "nil":
				op = nil
			case "foreign-parent":
				op.ParentID = "fabric-id"
			case "unknown-resource":
				op.Resource = "unselected-profile"
			case "wrong-action":
				op.Action = "Create"
			}
			serialized, _ := json.Marshal(op)
			var recovered *cvk.NetworkControllerConfigurationOperation
			if err := json.Unmarshal(serialized, &recovered); err != nil {
				t.Fatal(err)
			}
			e := &authConfigurationExecutor{client: c, desired: desired}
			ok, err := e.verify(context.Background(), recovered)
			if mode == "stable" {
				if !ok || err != nil || reads != 2 {
					t.Fatal("scoped serialized recovery failed", ok, err, reads)
				}
			} else if strings.HasPrefix(mode, "late-") {
				if ok || reads != 2 || mode != "late-drift" && err == nil {
					t.Fatal("changed recovery readback accepted", ok, err, reads)
				}
			} else if ok || err == nil || reads != 0 {
				t.Fatal("malformed operation reached observation", ok, err, reads)
			}
		})
	}
}
