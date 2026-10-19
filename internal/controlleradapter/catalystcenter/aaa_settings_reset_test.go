// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func aaaResetFixture() (*configapi.NetworkControllerConfig, aaaResetIntent, ciskov1.NetworkControllerConfigurationCredentialApplication) {
	config := &configapi.NetworkControllerConfig{ObjectMeta: metav1.ObjectMeta{Name: "aaa", Namespace: "lab", UID: "owner", Generation: 1}, Spec: configapi.NetworkControllerConfigSpec{Mode: configapi.NetworkControllerApplyModeApply, PrunePolicy: configapi.NetworkControllerRetentionPolicyDelete, ManagedSections: []string{"network_settings"}}}
	want := aaaResetIntent{site: "Global/Lab", network: aaaResetInherit, client: aaaResetInherit}
	receipt := ciskov1.NetworkControllerConfigurationCredentialApplication{Section: "network_settings", Resource: "Global/Lab#aaa", ConfigUID: "owner", TargetID: "area", OperationID: strings.Repeat("a", 32)}
	for _, group := range aaaGroups {
		receipt.Versions = append(receipt.Versions, ciskov1.NetworkControllerConfigurationCredentialVersion{Path: "/aaa_servers/0/" + group.model + "/shared_secret", Version: 2, SourceAlias: group.api, SecretName: "projected", SecretKey: group.api})
	}
	return config, want, receipt
}

func TestAAAResetShapesRemainDistinct(t *testing.T) {
	for _, mode := range []string{aaaResetInherit, aaaResetUnset} {
		want := aaaResetIntent{site: "Global/Lab", network: aaaResetInherit, client: mode}
		payload, err := want.payload()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(payload)
		if err != nil || strings.Contains(string(raw), "sharedSecret") {
			t.Fatal("invalid nonsecret reset projection", err)
		}
		var response map[string]json.RawMessage
		if json.Unmarshal(raw, &response) != nil {
			t.Fatal("reset JSON")
		}
		groups, err := parseAAAObservation(response)
		if err != nil || !aaaResetMatches(want, aaaResetObservation{groups: groups}) {
			t.Fatal("reset projection/readback semantics diverged", err)
		}
		other := aaaResetInherit
		if mode == aaaResetInherit {
			other = aaaResetUnset
		}
		want.client = other
		if aaaResetMatches(want, aaaResetObservation{groups: groups}) {
			t.Fatal("null inheritance and empty unset collapsed")
		}
	}
}

func TestOwnedAAAResetAuthority(t *testing.T) {
	for _, mode := range []string{"valid", "retain", "report", "owner", "target", "receipt-id", "duplicate-version", "bad-mode", "unset-gated", "no-guard"} {
		t.Run(mode, func(t *testing.T) {
			config, want, receipt := aaaResetFixture()
			switch mode {
			case "retain":
				config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
			case "report":
				config.Spec.Mode = configapi.NetworkControllerApplyModeReport
			case "owner":
				receipt.ConfigUID = "foreign"
			case "target":
				receipt.TargetID = ""
			case "receipt-id":
				receipt.OperationID = "invalid"
			case "duplicate-version":
				receipt.Versions[1] = receipt.Versions[0]
			case "bad-mode":
				want.client = "omitted"
			case "unset-gated":
				want.client = aaaResetUnset
			}
			if mode == "valid" {
				if err := validateAAAResetOwnership(config, want, receipt); err != nil {
					t.Fatal(err)
				}
				return
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				t.Error("invalid reset authority reached the API")
				w.WriteHeader(400)
			})
			guard := func(context.Context) error { return nil }
			if mode == "no-guard" {
				guard = nil
			}
			if write, err := c.planOwnedAAAReset(context.Background(), config, want, receipt, guard); err == nil || write != nil {
				t.Fatal("invalid reset admitted")
			}
		})
	}
}

func TestOwnedAAAResetSubmission(t *testing.T) {
	for _, mode := range []string{"valid", "changed-metadata", "replaced-site", "denied", "late-denied", "missing-receipt", "already-matching", "frozen-receipt"} {
		t.Run(mode, func(t *testing.T) {
			config, want, receipt := aaaResetFixture()
			actual, id := aaaResponse, "area"
			if mode == "already-matching" {
				actual = `{"aaaNetwork":null,"aaaClient":null}`
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == sitesPath:
					sites := sampleSites()
					sites[1].ID = id
					sites[2].ParentID = id
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case r.URL.Path == sitesPath+"/root/aaaSettings":
					fmt.Fprint(w, `{"response":`+aaaResponse+`}`)
				case r.Method == http.MethodGet && r.URL.Path == sitesPath+"/area/aaaSettings":
					fmt.Fprint(w, `{"response":`+actual+`}`)
				case r.Method == http.MethodPut && r.URL.Path == sitesPath+"/area/aaaSettings":
					writes++
					var body map[string]any
					if json.NewDecoder(r.Body).Decode(&body) != nil {
						t.Error("bad cleanup body")
					}
					expected, _ := want.payload()
					if !reflect.DeepEqual(body, expected) {
						t.Error("reset lost explicit null/empty distinction or added credential fields")
					}
					if mode == "missing-receipt" {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"reset-task"}}`)
					}
				default:
					t.Error("unexpected cleanup API")
					w.WriteHeader(400)
				}
			})
			denied := false
			guardCalls := 0
			guard := func(context.Context) error {
				guardCalls++
				if mode == "late-denied" && guardCalls == 4 {
					return errConfigurationStale
				}
				if denied {
					return errConfigurationStale
				}
				return nil
			}
			write, err := c.planOwnedAAAReset(context.Background(), config, want, receipt, guard)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "already-matching" {
				if write != nil || writes != 0 {
					t.Fatal("converged cleanup wrote")
				}
				return
			}
			if write == nil || len(write.identity.credentials) != 0 {
				t.Fatal("reset missing or invented credential versions")
			}
			switch mode {
			case "changed-metadata":
				actual = strings.Replace(actual, "192.0.2.10", "192.0.2.11", 1)
			case "replaced-site":
				id = "replacement"
			case "denied":
				denied = true
			case "frozen-receipt":
				receipt.Versions[0].Version = 99
			}
			_, err = write.submit(context.Background())
			valid := mode == "valid" || mode == "frozen-receipt"
			if (err == nil) != valid {
				t.Fatal("unexpected dispatch", err)
			}
			expected := 0
			if valid || mode == "missing-receipt" {
				expected = 1
			}
			if writes != expected {
				t.Fatal("stale/denied reset wrote")
			}
			if _, err := write.submit(context.Background()); err == nil || writes != expected {
				t.Fatal("attempted reset replayed")
			}
		})
	}
}

func TestOwnedAAAResetVerification(t *testing.T) {
	for _, mode := range []string{"valid", "pending-readback", "parent-valued-without-provenance", "collapsed-inheritance", "failed-task", "running-task", "late-task-failure", "late-task-boundary", "future-task", "changed-readback", "wrong-resource", "invented-version", "wrong-generation", "denied"} {
		t.Run(mode, func(t *testing.T) {
			config, want, receipt := aaaResetFixture()
			now := time.Now().UTC()
			submitted := metav1.NewTime(now.Add(-time.Second))
			op := &ciskov1.NetworkControllerConfigurationOperation{Section: "network_settings", ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, Phase: "Accepted", Resource: aaaResetResource(want, receipt), Action: "Update", TargetID: "area", TaskID: "reset-task", SubmittedAt: &submitted}
			if mode == "wrong-resource" {
				op.Resource = "other"
			}
			if mode == "wrong-generation" {
				op.ConfigGeneration++
			}
			if mode == "invented-version" {
				op.CredentialVersions = receipt.Versions
			}
			reads, trees := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case sitesPath + "/root/aaaSettings":
					fmt.Fprint(w, `{"response":`+aaaResponse+`}`)
				case sitesPath + "/area/aaaSettings":
					reads++
					body := `{"aaaNetwork":null,"aaaClient":null}`
					if mode == "pending-readback" || mode == "parent-valued-without-provenance" || mode == "changed-readback" && reads == 2 {
						body = aaaResponse
					}
					if mode == "collapsed-inheritance" {
						body = `{"aaaNetwork":{},"aaaClient":{}}`
					}
					fmt.Fprint(w, `{"response":`+body+`}`)
				case taskPath + "reset-task/tree":
					trees++
					end := now.Add(-time.Millisecond).UnixMilli()
					if mode == "future-task" {
						end = now.Add(time.Second).UnixMilli()
					}
					if mode == "late-task-boundary" && trees == 2 {
						end = now.Add(-2 * time.Millisecond).UnixMilli()
					}
					if mode == "running-task" {
						end = 0
					}
					failed := mode == "failed-task" || mode == "late-task-failure" && trees == 2
					fmt.Fprintf(w, `{"response":[{"id":"reset-task","isError":%t,"startTime":%d,"endTime":%d}]}`, failed, submitted.UnixMilli(), end)
				default:
					t.Error("unexpected verification API")
					w.WriteHeader(400)
				}
			})
			guard := func(context.Context) error {
				if mode == "denied" {
					return errConfigurationStale
				}
				return nil
			}
			ok, err := c.verifyOwnedAAAReset(context.Background(), config, want, receipt, op, guard, now)
			pending := mode == "pending-readback" || mode == "parent-valued-without-provenance" || mode == "collapsed-inheritance" || mode == "running-task" || mode == "future-task"
			if mode == "valid" {
				if err != nil || !ok {
					t.Fatal("valid cleanup", err)
				}
			} else if pending {
				if err != nil || ok {
					t.Fatal("pending native state certified", err)
				}
			} else if err == nil || ok {
				t.Fatal("invalid cleanup certified")
			}
		})
	}
}
