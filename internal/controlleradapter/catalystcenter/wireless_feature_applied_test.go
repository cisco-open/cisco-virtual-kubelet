// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func featureAppliedFixture(config *configapi.NetworkControllerConfig) cvk.NetworkControllerConfigurationActionApplication {
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-time.Second))
	op := cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: string(config.UID), ConfigName: config.Name, ConfigGeneration: 1, ControllerGeneration: 1, Section: "feature_templates", Resource: featureOwnedTestResource(), Action: "Create", Phase: "Verified", TaskID: "creator-task", SubmittedAt: &at}
	key, _ := featureCreateKey(op.Resource)
	return cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: featureCreatedPrefix + key + ":" + featureTestFRAUUID, Operation: op, VerifiedAt: &at}
}
func TestFeatureAppliedReceiptRequiredForRelease(t *testing.T) {
	for _, mode := range []string{"known", "missing", "duplicate", "pruned", "creator-missing", "foreign-owner"} {
		t.Run(mode, func(t *testing.T) { exerciseFeatureAppliedRelease(t, mode, nil) })
	}
}
func exerciseFeatureAppliedRelease(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"feature_templates"}
	config.Spec.Source.Inline.Raw = []byte(wirelessFRAModel)
	if install != nil {
		install(a, config)
	}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	creator := featureAppliedFixture(config)
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}}
	if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	creator = nc.Status.Configuration.ActionApplications[0]
	resource := featureUpdatePrefix + strings.TrimPrefix(featureOwnedTestResource(), featureCreatePrefix)
	op, err := a.prepareConfiguration(ctx, config, nil, "feature_templates", resource, "Update", configurationResourceIdentity{target: featureTestFRAUUID})
	if err != nil {
		t.Fatal(err)
	}
	if err = a.startConfiguration(ctx, config, nil, op.ID); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Accepted", "update-task"); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
		t.Fatal(err)
	}
	if err = a.advanceConfiguration(ctx, op.ID, "Verified", ""); err != errConfigurationTransition {
		t.Fatal("generic verification bypassed applied proof", err)
	}
	nc, err = a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Operation.Phase = "Verified"
	verified := nc.Status.Configuration.Operation.DeepCopy()
	key, _ := featureUpdateKey(verified)
	now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	r := cvk.NetworkControllerConfigurationActionApplication{Section: verified.Section, Resource: featureAppliedPrefix + key + ":" + featureTestFRAUUID, Operation: *verified, VerifiedAt: &now}
	nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, r)
	if mode == "pruned" {
		a.statusWriter = pruningActionReceiptClient{Client: a.statusWriter}
	}
	err = a.persistConfigurationActionReceipt(ctx, nc)
	if mode == "pruned" {
		if err == nil {
			t.Fatal("pruned publication accepted")
		}
	} else if err != nil {
		t.Fatal(err)
	}
	if mode != "known" && mode != "pruned" {
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		switch mode {
		case "missing":
			nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[:1]
		case "duplicate":
			duplicate := r
			duplicate.Operation = *r.Operation.DeepCopy()
			duplicate.Operation.ID = strings.Repeat("b", 32)
			duplicate.Resource += ":duplicate"
			nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, duplicate)
		case "creator-missing":
			nc.Status.Configuration.ActionApplications = nc.Status.Configuration.ActionApplications[1:]
		case "foreign-owner":
			nc.Status.Configuration.ActionApplications[1].Operation.ConfigUID = "foreign"
		}
		if err = a.statusWriter.Status().Update(ctx, nc); err != nil {
			t.Fatal(err)
		}
	}
	err = a.finishConfiguration(ctx, op.ID)
	state := journalState(t, a)
	if mode == "known" {
		if err != nil || state.Operation != nil || len(state.ActionApplications) != 2 || !reflect.DeepEqual(state.ActionApplications[0], creator) {
			t.Fatal("applied record changed creator or failed release", err, state)
		}
	} else if err == nil || state.Operation == nil {
		t.Fatal("invalid applied receipt released", mode, err)
	}
}
func TestFeatureAppliedCapacityAndValidation(t *testing.T) {
	_, config := journalFixture(t)
	creator := featureAppliedFixture(config)
	op := creator.Operation.DeepCopy()
	op.Action = "Update"
	op.TargetID = featureTestFRAUUID
	op.Resource = featureUpdatePrefix + strings.TrimPrefix(op.Resource, featureCreatePrefix)
	op.ID = strings.Repeat("b", 32)
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{creator}}
	for i := 0; i < 255; i++ {
		state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: fmt.Sprint("other-", i)})
	}
	if err := featureUpdateCapacity(state, op); err != errConfigurationBusy {
		t.Fatal("missing applied row exceeded capacity", err)
	}
	key, _ := featureUpdateKey(op)
	row := cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: featureAppliedPrefix + key + ":" + op.TargetID, Operation: *op, VerifiedAt: creator.VerifiedAt.DeepCopy()}
	state.ActionApplications[255] = row
	if err := featureUpdateCapacity(state, op); err != nil {
		t.Fatal("replacing existing applied row needs no additional slot", err)
	}
	for _, mode := range []string{"foreign-owner", "replacement", "duplicate", "creator-missing", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			copy := state.DeepCopy()
			switch mode {
			case "foreign-owner":
				copy.ActionApplications[255].Operation.ConfigUID = "foreign"
			case "replacement":
				copy.ActionApplications[255].Operation.TargetID = featureTestCAUUID
			case "duplicate":
				copy.ActionApplications = append(copy.ActionApplications, row)
			case "creator-missing":
				copy.ActionApplications = copy.ActionApplications[1:]
			case "malformed":
				copy.ActionApplications[255].Operation.Resource = featureUpdatePrefix + "bad"
			}
			before := copy.DeepCopy()
			if _, err := featureAppliedRecords(copy); err == nil || !reflect.DeepEqual(copy, before) {
				t.Fatal("invalid applied record accepted or mutated", err)
			}
		})
	}
}
