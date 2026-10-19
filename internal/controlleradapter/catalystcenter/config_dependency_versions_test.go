// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"testing"
)

func dependencyJournalIdentity() configurationResourceIdentity {
	return configurationResourceIdentity{target: "device", parent: "site", serial: "serial", dependencies: &cvk.NetworkControllerConfigurationDependencyVersions{Kind: "template-graph-v1", Entries: []cvk.NetworkControllerConfigurationDependencyVersion{{ID: "parent", VersionID: "parent-v2", Version: 2}, {ID: "member", VersionID: "member-v4", Version: 4}}}}
}
func TestDependencyVersionJournalBinding(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	identity := dependencyJournalIdentity()
	op, err := a.prepareConfiguration(ctx, config, nil, "inventory", "template-composite-deploy:fixture", "Update", identity)
	if err != nil {
		t.Fatal(err)
	}
	identity.dependencies.Entries[1].Version = 5
	if op.DependencyVersions.Entries[1].Version != 4 || journalState(t, a).Operation.DependencyVersions.Entries[1].Version != 4 {
		t.Fatal("journal aliases caller graph")
	}
	if err := a.startConfiguration(ctx, config, nil, op.ID); err != errConfigurationStale {
		t.Fatal("missing graph admitted", err)
	}
	if err := a.startConfiguration(ctx, config, nil, op.ID, identity); err != errConfigurationStale {
		t.Fatal("changed graph admitted", err)
	}
	identity.dependencies.Entries[1].Version = 4
	if err := a.startConfiguration(ctx, config, nil, op.ID, identity); err != nil {
		t.Fatal(err)
	}
	if journalState(t, a).Operation.DependencyVersions.Entries[1].Version != 4 {
		t.Fatal("submission record lost graph")
	}
}
func TestDependencyVersionJournalScope(t *testing.T) {
	for _, mode := range []string{"global", "regular", "missing", "duplicate", "no-device", "no-site", "no-serial", "credentials", "create"} {
		t.Run(mode, func(t *testing.T) {
			identity := dependencyJournalIdentity()
			section, resource, action := "inventory", "template-composite-deploy:fixture", "Update"
			switch mode {
			case "global":
				section = "templates"
			case "regular":
				resource = "template-deploy:fixture"
			case "missing":
				identity.dependencies = nil
			case "duplicate":
				identity.dependencies.Entries[1] = identity.dependencies.Entries[0]
			case "no-device":
				identity.target = ""
			case "no-site":
				identity.parent = ""
			case "no-serial":
				identity.serial = ""
			case "credentials":
				identity.credentials = []cvk.NetworkControllerConfigurationCredentialVersion{{}}
			case "create":
				action = "Create"
			}
			if err := validateConfigurationDependencyVersions(section, resource, action, identity); err == nil {
				t.Fatal("invalid scope accepted")
			}
		})
	}
}

func TestDependencyVersionPreparedSubmission(t *testing.T) {
	for _, mode := range []string{"accepted", "missing-expected", "changed-expected", "late-change", "late-removal"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			identity := dependencyJournalIdentity()
			op, err := a.prepareConfiguration(ctx, config, nil, "inventory", "template-composite-deploy:fixture", "Update", identity)
			if err != nil {
				t.Fatal(err)
			}
			expected := identity
			expected.dependencies = identity.dependencies.DeepCopy()
			if mode == "missing-expected" {
				expected.dependencies = nil
			}
			if mode == "changed-expected" {
				expected.dependencies.Entries[1].Version++
			}
			admit := func(ctx context.Context) (configurationDispatchCheck, error) {
				if mode == "late-change" || mode == "late-removal" {
					nc, err := a.currentController(ctx)
					if err != nil {
						return nil, err
					}
					if mode == "late-change" {
						nc.Status.Configuration.Operation.DependencyVersions.Entries[1].Version++
					} else {
						nc.Status.Configuration.Operation.DependencyVersions = nil
					}
					if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
						return nil, err
					}
				}
				return journalTestAdmission(ctx)
			}
			calls := 0
			err = a.submitPreparedConfigurationReceipt(ctx, config, nil, op, "inventory", op.Resource, "Update", func(context.Context) (configurationReceipt, error) {
				calls++
				return configurationReceipt{task: Task{ID: deploymentFixtureID}}, nil
			}, admit, expected)
			if mode == "accepted" {
				if err != nil || calls != 1 || journalState(t, a).Operation.Phase != "Accepted" || journalState(t, a).Operation.DependencyVersions.Entries[1].Version != 4 {
					t.Fatal("accepted receipt lost graph", err, calls)
				}
			} else if err == nil || calls != 0 {
				t.Fatal("changed graph submitted", err, calls)
			}
		})
	}
}

func TestDependencyVersionJournalRejectsPrunedSchema(t *testing.T) {
	for _, phase := range []string{"Prepared", "Submitting"} {
		t.Run(phase, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			if err := a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			original := a.statusWriter
			a.statusWriter = rotatingSecretJournalClient{Client: original, phase: phase, rotate: func() error {
				nc, err := a.currentController(ctx)
				if err != nil {
					return err
				}
				nc.Status.Configuration.Operation.DependencyVersions = nil
				return original.Status().Update(ctx, nc)
			}}
			identity := dependencyJournalIdentity()
			op, err := a.prepareConfiguration(ctx, config, nil, "inventory", "template-composite-deploy:fixture", "Update", identity)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			err = a.submitPreparedConfigurationReceipt(ctx, config, nil, op, "inventory", op.Resource, "Update", func(context.Context) (configurationReceipt, error) {
				calls++
				return configurationReceipt{task: Task{ID: deploymentFixtureID}}, nil
			}, journalTestAdmission, identity)
			if err != errConfigurationStale || calls != 0 {
				t.Fatal("pruned graph permitted native write", err, calls)
			}
			if journalState(t, a).Operation.Phase != phase {
				t.Fatal("pruned graph lost operation fence")
			}
		})
	}
}
