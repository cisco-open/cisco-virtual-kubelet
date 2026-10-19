// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTemplateRegularSettlingUsesDurableEvidence(t *testing.T) {
	for _, mode := range []string{"settled", "recent", "boundary", "planning", "fallback", "fallback-recent", "legacy", "future-prepared", "no-owner", "duplicate", "foreign", "source-changed", "wrong-native-id", "future-verified", "uncommitted", "wrong-journal", "old-generation"} {
		t.Run(mode, func(t *testing.T) {
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"templates"}
			now := time.Now().UTC().Truncate(time.Second)
			nc, err := a.currentController(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			definition := desiredTemplate{identity: nacmodel.TemplateIdentity{Project: "Lab", Name: "base"}, payload: map[string]any{"name": "base"}}
			intent := templateIntent{definitions: []desiredTemplate{definition}}
			observed := map[string]templateObservation{definition.identity.Key(): {project: templateProject{ID: "project"}, summary: templateSummary{ID: "base"}, draft: definition.payload, committed: definition.payload, version: &templateVersion{ID: "version"}}}
			source := &configSourceSnapshot{uid: "source", version: "1"}
			op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: string(config.UID), ConfigName: config.Name, ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, SourceUID: "source", SourceVersion: "1", Section: "templates", Resource: "objects/commit:Lab#base", Action: "Update", TargetID: "base", ParentID: "project", Phase: "Verified", TaskID: "task", SubmittedAt: &metav1.Time{Time: now.Add(-time.Minute)}}
			receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: op.Resource, Operation: op, VerifiedAt: &metav1.Time{Time: now.Add(-11 * time.Second)}}
			journal := op.DeepCopy()
			journal.Resource = "objects/draft:Lab#combined"
			journal.Phase = "Prepared"
			journal.TaskID = ""
			journal.SubmittedAt = nil
			journal.PreparedAt = &metav1.Time{Time: now.Add(-11 * time.Second)}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "templates", ConfigUID: string(config.UID), ConfigName: config.Name}}, ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{receipt}, Operation: journal}
			switch mode {
			case "recent":
				nc.Status.Configuration.ActionApplications[0].VerifiedAt = &metav1.Time{Time: now.Add(-9 * time.Second)}
			case "boundary":
				nc.Status.Configuration.ActionApplications[0].VerifiedAt = &metav1.Time{Time: now.Add(-10 * time.Second)}
			case "planning":
				nc.Status.Configuration.ActionApplications = nil
				nc.Status.Configuration.Operation = nil
			case "fallback":
				nc.Status.Configuration.ActionApplications = nil
			case "fallback-recent":
				nc.Status.Configuration.ActionApplications = nil
				journal.PreparedAt = &metav1.Time{Time: now.Add(-9 * time.Second)}
			case "legacy":
				nc.Status.Configuration.ActionApplications = nil
				journal.PreparedAt = nil
			case "future-prepared":
				nc.Status.Configuration.ActionApplications = nil
				journal.PreparedAt = &metav1.Time{Time: now.Add(time.Second)}
			case "no-owner":
				nc.Status.Configuration.Owners = nil
			case "duplicate":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, receipt)
			case "foreign":
				nc.Status.Configuration.ActionApplications[0].Operation.ConfigUID = "foreign"
			case "source-changed":
				journal.SourceVersion = "2"
			case "wrong-native-id":
				nc.Status.Configuration.ActionApplications[0].Operation.TargetID = "replacement"
			case "future-verified":
				nc.Status.Configuration.ActionApplications[0].VerifiedAt = &metav1.Time{Time: now.Add(time.Second)}
			case "uncommitted":
				got := observed[definition.identity.Key()]
				got.committed = nil
				observed[definition.identity.Key()] = got
			case "wrong-journal":
				journal.Resource = "objects/draft:Lab#other"
			case "old-generation":
				nc.Status.Configuration.ActionApplications[0].Operation.ConfigGeneration--
			}
			err = templateRegularDependenciesSettled(nc, config, source, intent, "objects/", "draft:Lab#combined", observed, now)
			allowed := mode == "settled" || mode == "fallback" || mode == "planning" || mode == "old-generation"
			if allowed && err != nil {
				t.Fatal("durable evidence rejected", err)
			}
			if !allowed && err == nil {
				t.Fatal("unsafe composite dependency allowed")
			}
			if (mode == "recent" || mode == "boundary" || mode == "fallback-recent" || mode == "legacy") && !errors.Is(err, errConfigurationBusy) {
				t.Fatal("wait did not remain pending", err)
			}
		})
	}
}
