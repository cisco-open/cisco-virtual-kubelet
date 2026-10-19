// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const templateOwnedTestUUID = "99999999-9999-4999-8999-999999999999"

func templateCreatorReceiptFixture(kind string) cvk.NetworkControllerConfigurationActionApplication {
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	submitted := metav1.NewTime(now.Add(-time.Second))
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, Section: "templates", Resource: templateCreatorResource(kind, "Lab", "base") + ":c", SourceUID: "source", SourceVersion: "1", Action: "Create", Phase: "Verified", TaskID: "task", SubmittedAt: &submitted}
	if kind != "p" {
		op.ParentID = "11111111-1111-4111-8111-111111111111"
	}
	if kind == "c" {
		floor := metav1.NewTime(now.Add(-time.Minute))
		op.PreparedAt = &floor
	}
	key, _ := templateCreateKey(op.Resource)
	return cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: templateCreatedPrefix + key + ":" + templateOwnedTestUUID, Operation: op, VerifiedAt: &now}
}
func TestTemplateCreatorTaskResult(t *testing.T) {
	for _, kind := range []string{"p", "r", "c"} {
		for _, mode := range []string{"valid", "missing-data", "prose", "json", "wrong-service", "wrong-task", "failed", "running", "stale", "future", "nil-uuid", "uppercase", "missing-source", "missing-source-pair"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				r := templateCreatorReceiptFixture(kind)
				op := r.Operation.DeepCopy()
				op.Phase = "Verifying"
				now := time.Now()
				body := map[string]any{"id": "task", "startTime": now.UnixMilli(), "endTime": now.UnixMilli(), "isError": false, "serviceType": "NCTP", "data": templateOwnedTestUUID}
				switch mode {
				case "missing-data":
					delete(body, "data")
				case "prose":
					body["data"] = "created " + templateOwnedTestUUID
				case "json":
					body["data"] = `{"id":"` + templateOwnedTestUUID + `"}`
				case "wrong-service":
					body["serviceType"] = "SWIM"
				case "wrong-task":
					body["id"] = "foreign"
				case "failed":
					body["isError"] = true
				case "running":
					delete(body, "endTime")
				case "stale":
					body["startTime"] = now.Add(-time.Hour).UnixMilli()
				case "future":
					body["endTime"] = now.Add(time.Hour).UnixMilli()
				case "nil-uuid":
					body["data"] = "00000000-0000-0000-0000-000000000000"
				case "uppercase":
					body["data"] = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA"
				case "missing-source":
					op.SourceUID = ""
				case "missing-source-pair":
					op.SourceUID = ""
					op.SourceVersion = ""
				}
				c := testClient(t, func(w http.ResponseWriter, req *http.Request) {
					if req.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if req.Method != "GET" || req.URL.Path != taskPath+"task" {
						t.Error("unexpected mutation or task")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": body})
				})
				id, done, err := c.templateCreateTaskResult(context.Background(), op, now)
				if mode == "valid" {
					if err != nil || !done || id != templateOwnedTestUUID {
						t.Fatal("valid result refused", err)
					}
				} else if mode == "running" {
					if err != nil || done || id != "" {
						t.Fatal("pending result completed")
					}
				} else if err == nil || done || id != "" {
					t.Fatal("untrusted task became ownership evidence")
				}
			})
		}
	}
}

func TestTemplateCreatorReceiptEnvelope(t *testing.T) {
	for _, kind := range []string{"p", "r", "c"} {
		for _, mode := range []string{"valid", "source-loss", "source-pair-loss", "wrong-key", "missing-owner", "future", "wrong-phase", "wrong-action", "duplicate", "duplicate-id"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				r := templateCreatorReceiptFixture(kind)
				state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
				record := &state.ActionApplications[0]
				switch mode {
				case "source-loss":
					record.Operation.SourceUID = ""
				case "source-pair-loss":
					record.Operation.SourceUID = ""
					record.Operation.SourceVersion = ""
				case "wrong-key":
					record.Resource = templateCreatedPrefix + "p:" + strings.Repeat("f", 64) + ":" + templateOwnedTestUUID
				case "missing-owner":
					record.Operation.ConfigUID = ""
				case "future":
					later := metav1.NewTime(time.Now().Add(time.Hour))
					record.VerifiedAt = &later
				case "wrong-phase":
					record.Operation.Phase = "Verifying"
				case "wrong-action":
					record.Operation.Action = "Update"
				case "duplicate":
					state.ActionApplications = append(state.ActionApplications, r)
				case "duplicate-id":
					other := templateCreatorReceiptFixture(kind)
					other.Operation.Resource = templateCreatorResource(kind, "Other", "different") + ":c"
					key, _ := templateCreateKey(other.Operation.Resource)
					other.Resource = templateCreatedPrefix + key + ":" + templateOwnedTestUUID
					state.ActionApplications = append(state.ActionApplications, other)
				}
				_, err := templateCreatedRecords(state)
				if (err == nil) != (mode == "valid") {
					t.Fatal("incorrect receipt eligibility", err)
				}
				if got := templateCreatedReceiptPresent(state, &r.Operation); got != (mode == "valid") {
					t.Fatal("incorrect finish eligibility", got)
				}
			})
		}
	}
}

type templateCreatorReceiptClient struct {
	ctrlclient.Client
	mode     string
	attempts atomic.Int32
}

func (c *templateCreatorReceiptClient) Status() ctrlclient.SubResourceWriter {
	return &templateCreatorReceiptWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type templateCreatorReceiptWriter struct {
	ctrlclient.SubResourceWriter
	c *templateCreatorReceiptClient
}

func (w *templateCreatorReceiptWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	nc, ok := obj.(*cvk.NetworkController)
	if !ok || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Verified" || (!strings.HasPrefix(nc.Status.Configuration.Operation.Resource, templateCreatePrefix) && !strings.HasPrefix(nc.Status.Configuration.Operation.Resource, templateApplyPrefix)) {
		return w.SubResourceWriter.Update(ctx, obj, opts...)
	}
	prefix := templateCreatedPrefix
	if strings.HasPrefix(nc.Status.Configuration.Operation.Resource, templateApplyPrefix) {
		prefix = templateAppliedPrefix
	}
	attempt := w.c.attempts.Add(1)
	if w.c.mode == "receipt-conflict" && attempt == 1 {
		return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "networkcontrollers"}, nc.Name, fmt.Errorf("controlled creator CAS conflict"))
	}
	if w.c.mode == "receipt-pruned" {
		retained := nc.Status.Configuration.ActionApplications[:0]
		for _, r := range nc.Status.Configuration.ActionApplications {
			if !strings.HasPrefix(r.Resource, prefix) {
				retained = append(retained, r)
			}
		}
		nc.Status.Configuration.ActionApplications = retained
	}
	if w.c.mode == "source-fields-pruned" {
		nc.Status.Configuration.Operation.SourceUID = ""
		nc.Status.Configuration.Operation.SourceVersion = ""
		for i := range nc.Status.Configuration.ActionApplications {
			r := &nc.Status.Configuration.ActionApplications[i]
			if strings.HasPrefix(r.Resource, prefix) {
				r.Operation.SourceUID = ""
				r.Operation.SourceVersion = ""
			}
		}
	}
	if w.c.mode == "baseline-pruned" || w.c.mode == "baseline-wrong-key" {
		for i := range nc.Status.Configuration.ActionApplications {
			r := &nc.Status.Configuration.ActionApplications[i]
			if !strings.HasPrefix(r.Resource, prefix) {
				continue
			}
			if w.c.mode == "baseline-pruned" {
				r.Resource = strings.Split(r.Resource, ":"+templateComparisonTag+":")[0]
			} else {
				parts := strings.Split(r.Resource, ":")
				for index, part := range parts {
					if part == templateComparisonTag && index+1 < len(parts) {
						parts[index+1] = strings.Repeat("f", 64)
						break
					}
				}
				r.Resource = strings.Join(parts, ":")
			}
		}
	}
	if prefix == templateAppliedPrefix && (w.c.mode == "receipt-version-pruned" || w.c.mode == "receipt-version-noncanonical") {
		for i := range nc.Status.Configuration.ActionApplications {
			row := &nc.Status.Configuration.ActionApplications[i]
			if !strings.HasPrefix(row.Resource, prefix) || !templateVersionOperation(&row.Operation) {
				continue
			}
			if w.c.mode == "receipt-version-pruned" {
				row.Resource = strings.Split(row.Resource, ":"+templateVersionOwnershipTag+":")[0]
			} else {
				parts := strings.Split(row.Resource, ":")
				parts[len(parts)-2] = "noncanonical"
				row.Resource = strings.Join(parts, ":")
			}
		}
	}
	err := w.SubResourceWriter.Update(ctx, obj, opts...)
	if err == nil && w.c.mode == "receipt-lost-ack" && attempt == 1 {
		return apierrors.NewTimeoutError("controlled creator acknowledgement loss", 1)
	}
	return err
}

var templateOwnershipModes = []string{"known", "missing-data", "prose-data", "json-data", "wrong-service", "foreign-task", "wrong-uuid", "same-name-replacement", "kind-changed", "source-during-proof", "owner-during-proof", "receipt-conflict", "receipt-lost-ack", "receipt-pruned", "source-fields-pruned", "noop-replacement", "update-replacement"}

func TestTemplateCreatorPublicFlow(t *testing.T) {
	for _, stage := range []string{"project-create", "draft-create", "composite-draft"} {
		for _, mode := range templateOwnershipModes {
			if stage == "project-create" && mode == "kind-changed" {
				continue
			}
			t.Run(stage+"/"+mode, func(t *testing.T) { exerciseTemplateNoDispatchReplan(t, stage, "ownership-"+mode, nil) })
		}
	}
}

func TestTemplateCreatorInlineSource(t *testing.T) {
	exerciseTemplateNoDispatchReplan(t, "project-create", "ownership-inline", nil)
}

func TestTemplateCreatorWholeSetCapacity(t *testing.T) {
	for _, mode := range []string{"count", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			var mutations atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					mutations.Add(1)
					t.Error("capacity refusal followed a native mutation")
					w.WriteHeader(500)
					return
				}
				if r.URL.Path != templateProjectsPath {
					t.Error("unexpected catalog read")
				}
				fmt.Fprint(w, `[]`)
			})
			e := &templateOwnedExecutor{client: c, owner: "11111111-1111-4111-8111-111111111111", ownerName: "config", configGeneration: 1, controllerGeneration: 1, bindings: map[string]templateCreateBinding{}}
			count := 257
			if mode == "bytes" {
				count = 5
				now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
				submitted := metav1.NewTime(now.Add(-time.Second))
				for i := 0; ; i++ {
					resource := "commit:" + strings.Repeat("p", 230) + "#" + strings.Repeat("t", 230) + fmt.Sprint(i)
					op := cvk.NetworkControllerConfigurationOperation{ID: fmt.Sprintf("%032x", i+1), ConfigUID: e.owner, ConfigName: strings.Repeat("c", 230), ConfigGeneration: 1, ControllerGeneration: 1, Section: "templates", Resource: resource, Action: "Update", TargetID: templateOwnedTestUUID, ParentID: templateOwnedTestUUID, Phase: "Verified", TaskID: strings.Repeat("t", 128), SubmittedAt: &submitted}
					r := cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: resource, Operation: op, VerifiedAt: &now}
					trial := append(append([]cvk.NetworkControllerConfigurationActionApplication(nil), e.receipts...), r)
					raw, _ := json.Marshal(trial)
					if len(raw) > 256<<10 {
						break
					}
					e.receipts = trial
				}
				raw, _ := json.Marshal(e.receipts)
				if len(raw) > 256<<10 || len(e.receipts)+count > 256 {
					t.Fatal("invalid preexisting capacity fixture")
				}
			}
			desired := []templateProject{}
			for i := 0; i < count; i++ {
				name := fmt.Sprintf("Project-%d", i)
				resource := templateCreatorResource("p", "", name) + ":i"
				e.bindings[resource] = templateCreateBinding{resource: resource, legacy: name, kind: "p", name: name}
				desired = append(desired, templateProject{Name: name})
			}
			e.base = &templateProjectExecutor{client: c, desired: desired}
			if _, err := e.next(context.Background()); err != errConfigurationBusy || mutations.Load() != 0 {
				t.Fatal("whole-set capacity was not reserved before effects", err, mutations.Load())
			}
		})
	}
}
