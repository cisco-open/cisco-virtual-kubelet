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

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

func TestTemplateMembershipRecoveryReadsOnlyAcknowledgedAssociation(t *testing.T) {
	intent, err := parseTemplateIntent([]byte(strings.Replace(templateModel, "- name: base", "- name: base\n            tags: [selected, later]", 1)), map[string]string{"base.j2": "body"})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := templateMembershipBoundResource(tagMembership{resource: tagMembershipIdentity(intent.definitions[0].identity.Key(), "selected"), projectID: "project"})
	if err != nil {
		t.Fatal(err)
	}
	selectedReads := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.URL.Path != authPath {
			t.Error("recovery attempted a mutation")
		}
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case tagsPath:
			fmt.Fprint(w, `{"response":[{"id":"selected-tag","name":"selected","systemTag":false},{"id":"later-tag","name":"later","systemTag":false}]}`)
		case templateProjectsPath:
			fmt.Fprint(w, `[{"id":"project","name":"Lab"}]`)
		case templatesPath:
			fmt.Fprint(w, `[{"templateId":"template","projectId":"project","name":"base"}]`)
		case tagsPath + "/selected-tag/member":
			selectedReads++
			fmt.Fprint(w, `{"response":[{"instanceUuid":"template"}]}`)
		default:
			t.Errorf("recovery read unrelated association %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	op := &cvk.NetworkControllerConfigurationOperation{Action: "Update", Resource: resource, TargetID: "template", ParentID: "selected-tag"}
	matched, err := (&templateMembershipExecutor{client: c, intent: intent}).verify(context.Background(), op)
	if err != nil || !matched || selectedReads != 4 {
		t.Fatal("recovery did not bound its repeated association reads", matched, err, selectedReads)
	}
}

func TestTemplateMembershipProjectBindingJournalRestart(t *testing.T) {
	for _, mode := range []string{"stable", "project-replaced", "late-project-replaced", "template-replaced", "tag-replaced", "membership-missing", "legacy", "malformed", "lost-ack"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			raw := []byte(strings.Replace(templateModel, "- name: base", "- name: base\n            tags: [lab]", 1))
			intent, err := parseTemplateIntent(raw, map[string]string{"base.j2": "body"})
			if err != nil {
				t.Fatal(err)
			}
			jsonSource, err := yaml.YAMLToJSON(raw)
			if err != nil {
				t.Fatal(err)
			}
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"templates"}
			config.Spec.Source.Inline = &runtime.RawExtension{Raw: jsonSource}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			project, template, tag := "original-project", "original-template", "original-tag"
			present, verifying, projectFlip := false, false, false
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				switch r.URL.Path {
				case tagsPath:
					_ = json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": tag, "name": "lab", "systemTag": false}}})
				case templateProjectsPath:
					_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": project, "name": "Lab"}})
				case templatesPath:
					_ = json.NewEncoder(w).Encode([]any{map[string]any{"templateId": template, "name": "base", "projectId": project}})
				default:
					if r.URL.Path != tagsPath+"/"+tag+"/member" {
						t.Errorf("unexpected membership endpoint %s", r.URL.Path)
						http.NotFound(w, r)
						return
					}
					if r.Method == http.MethodPost {
						writes++
						present = true
						if mode == "lost-ack" {
							fmt.Fprint(w, `{"response":{}}`)
						} else {
							fmt.Fprint(w, `{"response":{"taskId":"membership-task"}}`)
						}
						return
					}
					members := []any{map[string]any{"instanceUuid": "foreign-member"}}
					if present {
						members = append(members, map[string]any{"instanceUuid": template})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": members})
					if verifying && mode == "late-project-replaced" && !projectFlip {
						project, projectFlip = "replacement-project", true
					}
				}
			})
			a.client = c
			executor := &configurationGroup{parts: []configurationPart{{name: "members", executor: &templateMembershipExecutor{client: c, intent: intent}}}}
			write, err := executor.next(ctx)
			if err != nil || write == nil || !strings.HasPrefix(write.resource, "members/"+templateMembershipBindingPrefix) {
				t.Fatal("project-bound membership was not planned", err)
			}
			if mode == "legacy" {
				write.resource = "members/" + tagMembershipIdentity(intent.definitions[0].identity.Key(), "lab")
			} else if mode == "malformed" {
				write.resource = "members/" + templateMembershipBindingPrefix + "missing-binding"
			}
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			err = a.submitConfigurationWrite(ctx, config, source, "templates", write)
			if mode == "lost-ack" {
				state := journalState(t, a)
				if err == nil || state.Operation == nil || state.Operation.Phase != "Unknown" || writes != 1 {
					t.Fatal("uncertain membership was not retained", err)
				}
				if err := a.finishConfiguration(ctx, state.Operation.ID); err == nil {
					t.Fatal("uncertain membership released")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			op := journalState(t, a).Operation.DeepCopy()
			if op.Phase != "Accepted" || op.Resource != write.resource || op.TargetID != "original-template" || op.ParentID != "original-tag" {
				t.Fatal("journal lost the selected identity")
			}
			// Reconstruct only persisted operation/source data. The new executor
			// has no memory of the project observation used before submission.
			stored, _ := json.Marshal(op)
			var recovered cvk.NetworkControllerConfigurationOperation
			if err := json.Unmarshal(stored, &recovered); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "project-replaced":
				project = "replacement-project" // Same names and template UUID.
			case "template-replaced":
				template = "replacement-template"
			case "tag-replaced":
				tag = "replacement-tag"
			case "membership-missing":
				present = false
			}
			if err := a.advanceConfiguration(ctx, recovered.ID, "Verifying", ""); err != nil {
				t.Fatal(err)
			}
			verifying = true
			before := journalState(t, a).DeepCopy()
			restarted := &configurationGroup{parts: []configurationPart{{name: "members", executor: &templateMembershipExecutor{client: c, intent: intent}}}}
			matched, err := restarted.verify(ctx, &recovered)
			if !reflect.DeepEqual(before, journalState(t, a)) || writes != 1 {
				t.Fatal("verification changed the journal or replayed membership")
			}
			if mode == "stable" {
				if err != nil || !matched {
					t.Fatal("original project did not verify after restart", err)
				}
				if err := a.advanceConfiguration(ctx, recovered.ID, "Verified", ""); err != nil {
					t.Fatal(err)
				}
				if err := a.finishConfiguration(ctx, recovered.ID); err != nil {
					t.Fatal("project-bound operation could not release", err)
				}
			} else if matched {
				t.Fatal("changed or unbound project verified")
			} else if err := a.finishConfiguration(ctx, recovered.ID); err == nil {
				t.Fatal("unverified operation released")
			}
		})
	}
}

func TestTemplateMembershipLegacyVerifiedCannotRelease(t *testing.T) {
	for _, wrapper := range []string{"members/", "templates/members/"} {
		t.Run(wrapper, func(t *testing.T) {
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"templates"}
			if err := a.statusWriter.Update(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			identity := configurationResourceIdentity{target: "template", parent: "tag"}
			op, err := a.prepareConfiguration(context.Background(), config, nil, "templates", wrapper+"legacy-name/hash", "Update", identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(context.Background(), config, nil, op.ID, identity); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(context.Background(), op.ID, "Accepted", "legacy-task"); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(context.Background(), op.ID, "Verifying", ""); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(context.Background(), op.ID, "Verified", ""); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(context.Background(), op.ID); err == nil {
				t.Fatal("legacy Verified association silently released without project evidence")
			}
		})
	}
}

func TestTemplateMembershipLegacyPublicRecoveryStatus(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"templates"}
	model := strings.Replace(templateModel, "- name: base", "- name: base\n            tags: [lab]", 1)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "model", UID: "model-source"}, Data: map[string]string{"model.yaml": model, nacmodel.TemplateAssetsKey: `{"base.j2":"body"}`}}
	if err := a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.yaml"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	identity := configurationResourceIdentity{target: "template", parent: "tag"}
	op, err := a.prepareConfiguration(ctx, config, source, "templates", "members/legacy/hash", "Update", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.startConfiguration(ctx, config, source, op.ID, identity); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"Accepted", "Verifying", "Verified"} {
		task := ""
		if phase == "Accepted" {
			task = "old-task"
		}
		if err := a.advanceConfiguration(ctx, op.ID, phase, task); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		t.Error("legacy recovery made a native request")
		http.NotFound(w, r)
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(ctx, key, config); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, condition := range config.Status.Conditions {
		if condition.Reason == "OperationRecoveryRequired" && strings.Contains(condition.Message, "original durable project binding") {
			found = true
		}
	}
	if !found || calls != 0 || journalState(t, a).Operation == nil || journalState(t, a).Operation.ID != op.ID {
		t.Fatal("legacy operation lacked actionable retained recovery status", config.Status.Conditions)
	}
}

func TestTemplateMembershipLegacyPreparedCanCancel(t *testing.T) {
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"templates"}
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareConfiguration(context.Background(), config, nil, "templates", "members/legacy/hash", "Update", configurationResourceIdentity{target: "template", parent: "tag"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(context.Background(), op.ID); err != nil || journalState(t, a).Operation != nil {
		t.Fatal("unsubmitted legacy preparation could not cancel", err)
	}
}
