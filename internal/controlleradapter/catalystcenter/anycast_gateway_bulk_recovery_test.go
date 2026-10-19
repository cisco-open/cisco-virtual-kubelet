// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAnycastBulkRecoverySelection(t *testing.T) {
	wants := []fabricAnycastIntent{}
	for i := 0; i < 25; i++ {
		wants = append(wants, fabricAnycastIntent{path: "Global/Lab", anycastGatewayIntent: anycastGatewayIntent{pool: fmt.Sprint(i), network: "Corp", payload: map[string]any{"ipPoolName": fmt.Sprint(i), "virtualNetworkName": "Corp"}}})
	}
	digest, _ := anycastCohortDigest(wants)
	resource := fmt.Sprintf("%s0:25:20:ffff0f:%s:%s", anycastBulkChunkPrefix, strings.Repeat("a", 64), digest)
	scope := anycastBulkRecoveryScope{prefix: "fabric_sites/", cohorts: [][]fabricAnycastIntent{wants}}
	now := metav1.NewTime(time.Now().Add(-time.Second))
	base := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), Section: "fabric", Resource: scope.prefix + resource, Action: "Create", Phase: "Unknown", ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: 1, ParentID: "fabric", TaskID: "original-task", SubmittedAt: &now}
	for _, mode := range []string{"chunk", "legacy", "taskless", "foreign-scope", "source-digest", "count", "mask", "empty-binding", "phase", "action", "target", "parent", "physical", "prepared", "credentials", "source-pair"} {
		t.Run(mode, func(t *testing.T) {
			op := base.DeepCopy()
			want := mode == "chunk" || mode == "legacy"
			switch mode {
			case "legacy":
				op.Resource = scope.prefix + anycastBulkPrefix + "25:ffffff01:" + strings.Repeat("b", 64)
			case "taskless":
				op.TaskID = ""
			case "foreign-scope":
				op.Resource = "other/" + resource
			case "source-digest":
				op.Resource = strings.TrimSuffix(op.Resource, digest) + strings.Repeat("c", 64)
			case "count":
				op.Resource = strings.Replace(op.Resource, ":25:", ":26:", 1)
			case "mask":
				op.Resource = strings.Replace(op.Resource, ":ffff0f:", ":000000:", 1)
			case "empty-binding":
				op.Resource = scope.prefix + anycastBulkPrefix + "25:ffffff01:"
			case "phase":
				op.Phase = "Accepted"
			case "action":
				op.Action = "Update"
			case "target":
				op.TargetID = "target"
			case "parent":
				op.ParentID = ""
			case "physical":
				op.TargetSerial = "SERIAL"
			case "prepared":
				op.PreparedAt = &now
			case "credentials":
				op.CredentialVersions = []cvk.NetworkControllerConfigurationCredentialVersion{{Path: "private", Version: 1}}
			case "source-pair":
				op.SourceUID = "source"
			}
			before := op.DeepCopy()
			if scope.selects(op) != want || !reflect.DeepEqual(op, before) {
				t.Fatal("bulk task selector widened or mutated input")
			}
		})
	}
}

func TestAnycastBulkRecoveryPreservesOtherCallbacks(t *testing.T) {
	e := &anycastGatewayExecutor{desired: []fabricAnycastIntent{{path: "Global/Lab", anycastGatewayIntent: anycastGatewayIntent{pool: "pool", payload: map[string]any{}}}}}
	priorCalls := 0
	plan := configurationApplyPlan{section: "fabric", executor: &fabricGatewayDeployment{gateways: e}, recoverKnownTask: func(*cvk.NetworkControllerConfigurationOperation) bool { priorCalls++; return true }, observeCompletion: func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error) {
		priorCalls++
		return true, nil
	}}
	(&client{}).bindAnycastBulkTaskRecovery(&plan)
	op := &cvk.NetworkControllerConfigurationOperation{Resource: "network_settings/pool"}
	if !plan.recoverKnownTask(op) {
		t.Fatal("unrelated observer lost")
	}
	if done, err := plan.observeCompletion(context.Background(), op, time.Now()); err != nil || !done || priorCalls != 2 {
		t.Fatal("unrelated callback altered")
	}
	op.Resource = anycastBulkPrefix + "bad"
	if plan.recoverKnownTask(op) {
		t.Fatal("invalid recognized bulk receipt fell through")
	}
	if _, err := plan.observeCompletion(context.Background(), op, time.Now()); err == nil || priorCalls != 2 {
		t.Fatal("invalid recognized receipt used fallback")
	}
}

func TestAnycastBulkReceiptCanonicalRoundTrip(t *testing.T) {
	for _, resource := range []string{anycastBulkPrefix + "45:ffffffffff1f:" + strings.Repeat("a", 64), anycastBulkChunkPrefix + "40:45:5:1f:" + strings.Repeat("b", 64) + ":" + strings.Repeat("c", 64)} {
		receipt, err := decodeAnycastBulkReceipt(resource)
		if err != nil {
			t.Fatal(err)
		}
		legacy, err := decodeAnycastBulkReceipt(receipt.legacy())
		if err != nil || legacy.count != receipt.count || legacy.binding != receipt.binding {
			t.Fatal("legacy footprint changed")
		}
		a, _ := json.Marshal(receipt.mask)
		b, _ := json.Marshal(legacy.mask)
		if string(a) != string(b) {
			t.Fatal("original mask lost")
		}
	}
}

// A successful root must not complete an ordinary Accepted bulk operation
// while a child failed or remains pending, even when all native rows exist.
func TestAnycastBulkAcceptedChildrenHoldOriginalOperation(t *testing.T) {
	for _, mode := range []string{"public-accepted-child-failed", "public-accepted-child-pending"} {
		t.Run(mode, func(t *testing.T) { exerciseAnycastBulkChunks(t, mode, nil) })
	}
}
