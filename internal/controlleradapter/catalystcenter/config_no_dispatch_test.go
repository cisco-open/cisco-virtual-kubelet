// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type noDispatchConflictClient struct {
	ctrlclient.Client
	attempts int
	always   bool
}

func (c *noDispatchConflictClient) Status() ctrlclient.SubResourceWriter {
	return &noDispatchConflictWriter{SubResourceWriter: c.Client.Status(), c: c}
}

type noDispatchConflictWriter struct {
	ctrlclient.SubResourceWriter
	c *noDispatchConflictClient
}

func (w *noDispatchConflictWriter) Update(ctx context.Context, obj ctrlclient.Object, opts ...ctrlclient.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok && nc.Status.Configuration != nil && nc.Status.Configuration.Operation == nil {
		w.c.attempts++
		if w.c.always || w.c.attempts == 1 {
			return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "networkcontrollers"}, nc.Name, errors.New("test concurrent status update"))
		}
	}
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func TestConfigurationNoDispatchProof(t *testing.T) {
	for _, scope := range []struct {
		name, section, resource, action, target, parent string
		prepared                                        bool
	}{
		{"tag-delete", "inventory", tagDeletePrefix + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":i", "Delete", "22f33fa0-e275-41c9-a50d-d7e5135adb8c", "", false},
		{"tag-create", "inventory", tagOwnedPrefix + "c:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":i", "Create", "", "", false},
		{"tag-update", "templates", tagOwnedPrefix + "u:" + strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64) + ":i", "Update", "22f33fa0-e275-41c9-a50d-d7e5135adb8c", "", false},
		{"wireless", "wireless", "anchor-group:CVK_LAB", "Create", "", "", false},
		{"network-ntp", "network_settings", "Global/Lab#ntp", "Update", "area", "", false},
		{"network-dhcp", "network_settings", "Global/Lab#dhcp", "Update", "area", "", false},
		{"network-dns", "network_settings", "Global/Lab#dns", "Update", "area", "", false},
		{"network-timezone", "network_settings", "Global/Lab#timezone", "Update", "area", "", false},
		{"network-banner", "network_settings", "Global/Lab#banner", "Update", "area", "", false},
		{"network-telemetry", "network_settings", "Global/Lab#telemetry", "Update", "area", "", false},
		{"switching-profile-create", "network_profiles", "profiles/Lab", "Create", "", "sp2-" + strings.Repeat("a", 64), false},
		{"switching-custody-create", "network_profiles", switchingCreatePrefix + switchingNameDigest("Lab"), "Create", "", "sp2-" + strings.Repeat("a", 64), false},
		{"switching-profile-update", "network_profiles", "profiles/Lab", "Update", "profile", "sp2-" + strings.Repeat("a", 64), false},
		{"pnp-registration", "inventory", "pnp-register:" + strings.Repeat("a", 64), "Create", "", "", false},
		{"project-create", "templates", "Lab", "Create", "", "", false},
		{"project-update", "templates", "objects/Lab", "Update", "project", "", false},
		{"draft-create", "templates", "draft:Lab#base", "Create", "", "project", false},
		{"draft-update", "templates", "objects/draft:Lab#base", "Update", "base", "project", false},
		{"commit", "templates", "commit:Lab#base", "Update", "base", "project", false},
		{"prepared-draft", "templates", "objects/draft:Lab#combined", "Create", "", "project", true},
		{"prepared-commit", "templates", "commit:Lab#combined", "Update", "combined", "project", true},
	} {
		for _, mode := range []string{"refuse", "read-error", "unknown-phase", "cancel", "conflict", "conflict-exhausted", "ordinary-error", "lost-ack", "foreign-proof", "replayed-proof", "changed-operation", "changed-owner", "changed-generation", "recorded-task", "recorded-receipt", "feature-scope", "changed-parent", "changed-preparation"} {
			if mode == "feature-scope" && scope.section != "wireless" {
				continue
			}
			t.Run(scope.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				a, config := journalFixture(t)
				config.Spec.ManagedSections = []string{scope.section}
				config.Spec.Source.Inline.Raw = []byte(wirelessAnchorModel)
				section := scope.section
				resource := scope.resource
				if section == "templates" {
					config.Spec.Source.Inline.Raw = []byte(`{"catalyst_center":{"templates":{"projects":[{"name":"Lab"}]}}}`)
				}
				if mode == "feature-scope" {
					section = "feature_templates"
					config.Spec.ManagedSections = []string{section}
					config.Spec.Source.Inline.Raw = []byte(wirelessCleanAirModel)
					resource = "cleanair:CVK_LAB"
				}
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				var conflict *noDispatchConflictClient
				if mode == "conflict" || mode == "conflict-exhausted" {
					conflict = &noDispatchConflictClient{Client: a.statusWriter, always: mode == "conflict-exhausted"}
					a.statusWriter = conflict
				}
				calls := 0
				write := &configurationWrite{resource: resource, action: scope.action, identity: configurationResourceIdentity{target: scope.target, parent: scope.parent, preparationWait: scope.prepared}, submit: func(context.Context) (Task, error) { calls++; return Task{}, errInvalidResponse }}
				var before *cvk.NetworkControllerConfigurationOperation
				check := func(ctx context.Context) error {
					nc, err := a.currentController(ctx)
					if err != nil {
						return err
					}
					before = nc.Status.Configuration.Operation.DeepCopy()
					switch mode {
					case "lost-ack", "foreign-proof":
						return nil
					case "unknown-phase":
						if err := a.advanceConfiguration(ctx, before.ID, "Unknown", ""); err != nil {
							t.Fatal(err)
						}
					case "cancel":
						cancel()
						return context.Canceled
					case "read-error":
						return errInvalidResponse
					case "changed-operation":
						nc.Status.Configuration.Operation.SourceVersion = "changed"
					case "changed-parent":
						nc.Status.Configuration.Operation.ParentID = "replacement"
					case "changed-preparation":
						nc.Status.Configuration.Operation.PreparedAt = &metav1.Time{Time: time.Now().Add(-time.Hour)}
					case "changed-owner":
						nc.Status.Configuration.Owners[0].ConfigUID = "another-owner"
					case "changed-generation":
						nc.Generation++
						nc.Spec.Paused = true
						if err := a.statusWriter.Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
						return errConfigurationStale
					case "recorded-task":
						nc.Status.Configuration.Operation.TaskID = "unexpected-task"
					case "recorded-receipt":
						op := before.DeepCopy()
						op.Phase = "Verified"
						op.TaskID = "unexpected-task"
						nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: section, Resource: "late-receipt", Operation: *op})
					}
					if mode == "changed-operation" || mode == "changed-owner" || mode == "recorded-task" || mode == "recorded-receipt" || mode == "changed-parent" || mode == "changed-preparation" {
						if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
							t.Fatal(err)
						}
					}
					return errConfigurationStale
				}
				guardWirelessDefinitionPreflight(write, check)
				var proof *configurationPreSubmitRefusal
				claim := write.noDispatch
				write.noDispatch = func(err error) *configurationPreSubmitRefusal {
					p := claim(err)
					if p != nil {
						proof = p
					}
					return p
				}
				if mode == "ordinary-error" {
					write.noDispatch = nil
					write.submit = func(context.Context) (Task, error) { calls++; return Task{}, errConfigurationStale }
				}
				if mode == "foreign-proof" {
					foreign := &configurationWrite{submit: func(context.Context) (Task, error) { t.Fatal("foreign native closure invoked"); return Task{}, nil }}
					guardWirelessDefinitionPreflight(foreign, func(context.Context) error { return errConfigurationStale })
					_, foreignErr := foreign.submit(ctx)
					write.submit = func(context.Context) (Task, error) { calls++; return Task{}, foreignErr }
				}
				if mode == "replayed-proof" {
					foreign := &configurationWrite{submit: func(context.Context) (Task, error) { return Task{}, nil }}
					guardWirelessDefinitionPreflight(foreign, func(context.Context) error { return errConfigurationStale })
					_, oldErr := foreign.submit(ctx)
					oldProof := foreign.noDispatch(oldErr)
					if oldProof == nil {
						t.Fatal("missing initial refusal")
					}
					write.noDispatch = foreign.noDispatch
					write.submit = func(context.Context) (Task, error) { calls++; return Task{}, oldErr }
				}
				var err error
				if section == "inventory" && !strings.HasPrefix(resource, tagOwnedPrefix) && !strings.HasPrefix(resource, tagDeletePrefix) {
					guarded := write.submit
					write.submit = nil
					write.submitCreated = func(ctx context.Context) (string, error) {
						_, err := guarded(ctx)
						return "", err
					}
				}
				if scope.prepared {
					var op *cvk.NetworkControllerConfigurationOperation
					op, err = a.prepareConfiguration(ctx, config, nil, section, write.resource, write.action, write.identity)
					if err == nil {
						// Resume the persisted API representation, as reconciliation does.
						op = journalState(t, a).Operation.DeepCopy()
						err = a.submitPreparedConfigurationReceipt(ctx, config, nil, op, section, write.resource, write.action, configurationTaskWriteReceipt(section, write), nil, write.identity)
					}
				} else {
					err = a.submitConfigurationWrite(ctx, config, nil, section, write)
				}
				retired := mode == "refuse" || mode == "read-error" || mode == "unknown-phase" || mode == "cancel" || mode == "conflict"
				if errors.Is(err, errConfigurationNotDispatched) != retired {
					t.Fatal("incorrect no-dispatch outcome", err)
				}
				state := journalState(t, a)
				if (state.Operation == nil) != retired {
					t.Fatal("incorrect journal retirement", state.Operation)
				}
				expected := 0
				if mode == "ordinary-error" || mode == "lost-ack" || mode == "foreign-proof" || mode == "replayed-proof" {
					expected = 1
				}
				if calls != expected {
					t.Fatal("native submission replayed", calls, expected)
				}
				if len(state.Owners) != 1 {
					t.Fatal("ownership lost")
				}
				if retired && proof != nil {
					if err := a.retireProvenNoDispatch(context.Background(), before, proof); !errors.Is(err, errConfigurationTransition) {
						t.Fatal("proof replay allowed", err)
					}
				}
				if conflict != nil && conflict.attempts < 2 {
					t.Fatal("conflict not retried")
				}
			})
		}
	}
}
