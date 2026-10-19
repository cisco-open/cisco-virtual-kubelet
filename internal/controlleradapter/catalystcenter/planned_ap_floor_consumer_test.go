// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Seed a synthetic already-verified floor checkpoint. This tests the consumer,
// not the still-disabled original-task floor effect publisher.
func seedPlannedAPFloorConsumer(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, epoch string) {
	t.Helper()
	ctx := context.Background()
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		t.Fatal(err)
	}
	_, desired, err := parseSitesWithPlannedAPs(source.raw)
	if err != nil || len(desired) != 1 {
		t.Fatal("consumer source fixture", err)
	}
	want := desired[0]
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := a.resolveTemplateComparison(nc, config, source, "sites", siteComparisonScope, "cvk-site-native-key-v1")
	if err != nil {
		t.Fatal(err)
	}
	creators, err := siteCreatedRecords(nc.Status.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	creator, ok := siteCreatorForResource(creators, want.floor)
	if !ok {
		t.Fatal("consumer floor creator missing")
	}
	anchors, err := plannedAPIdentityRecords(nc.Status.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	anchor := anchors[plannedAPResource(want)]
	rows, err := a.client.plannedAPNativePositions(ctx, creator.id)
	if err != nil {
		t.Fatal(err)
	}
	native := rows[want.payload["name"].(string)]
	view, err := plannedAPBaselineView(plannedAPState{want: want, floorID: creator.id, actual: native.actual, native: native.native})
	if err != nil {
		t.Fatal(err)
	}
	mac, err := plannedAPNativeMAC(comparison, anchor.receipt.Operation.Resource, view)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := plannedAPAppliedRecords(nc.Status.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	floors, err := plannedAPFloorAuthorities(nc.Status.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	_, priorAP, _, err := plannedAPCurrentAuthority(plannedAPResource(want), anchor, applied, floors)
	if err != nil {
		t.Fatal(err)
	}
	effect := plannedAPFloorEffectPrefix + strings.TrimPrefix(plannedAPResource(want), "planned-ap:") + ":" + anchor.id + ":" + comparison.keyID + ":" + mac + ":" + priorAP
	root, err := plannedAPFloorEffectRoot([]string{effect})
	if err != nil {
		t.Fatal(err)
	}
	siteRows, err := siteAppliedRecords(nc.Status.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	priorFloor := creator.receipt
	if row, exists := siteRows[sitePathDigest(want.floor)]; exists {
		priorFloor = row
	}
	base, _ := sitePrivateCreateParts(creator.receipt.Operation.Resource)
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	for _, row := range nc.Status.Configuration.ActionApplications {
		if row.VerifiedAt != nil && row.VerifiedAt.After(at.Time) {
			at = *row.VerifiedAt.DeepCopy()
		}
	}
	op := &cvk.NetworkControllerConfigurationOperation{ID: epoch, Section: "sites", Resource: sitePrivateFloorUpdatePrefix + strings.Join(base, ":") + ":" + siteReceiptDigest(priorFloor) + ":" + root, Action: "Update", Phase: "Verified", ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, SourceUID: string(source.uid), SourceVersion: source.version, ParentID: creator.receipt.Operation.ParentID, TargetID: creator.id, TaskID: "synthetic-floor-task", SubmittedAt: &at}
	sites, err := a.client.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	floorView, err := a.client.siteNativeFootprint(ctx, sites, want.floor, false)
	if err != nil {
		t.Fatal(err)
	}
	floorMAC, err := siteNativeMAC(comparison, op.Resource, floorView)
	if err != nil {
		t.Fatal(err)
	}
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: siteAppliedResult(op, creator, floorMAC), Operation: *op, VerifiedAt: &at, EffectReceipts: []string{effect}}
	if err := siteReplaceApplied(nc.Status.Configuration, row); err != nil {
		t.Fatal(err)
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
}

func TestPlannedAPFloorConsumerManifest(t *testing.T) {
	for _, mode := range []string{"keyed-floor-consumer-private", "keyed-floor-consumer-recover-private", "keyed-floor-consumer-drift-private", "keyed-floor-consumer-missing-private", "keyed-floor-consumer-keyed-result-drift-private"} {
		t.Run(mode, func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}

func testPlannedAPFloorConsumerStages(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig, sites []Site, placements *[]plannedAPObservation, writes *[]string, mode string, tick, converge func()) {
	t.Helper()
	ctx := context.Background()
	before := len(*writes)
	anchorsBefore, err := plannedAPIdentityRecords(journalState(t, a))
	if err != nil {
		t.Fatal(err)
	}
	setModel := func(width, x float64) {
		t.Helper()
		var root map[string]any
		if json.Unmarshal(config.Spec.Source.Inline.Raw, &root) != nil {
			t.Fatal("consumer inline fixture")
		}
		floor := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
		floor["width"] = width
		floor["planned_access_points"].([]any)[0].(map[string]any)["position_x"] = x
		config.Spec.Source.Inline.Raw, _ = json.Marshal(root)
		config.Generation++
		if err := a.statusWriter.Update(ctx, config); err != nil {
			t.Fatal(err)
		}
	}
	checkEpoch := func(epoch string, count int) cvk.NetworkControllerConfigurationActionApplication {
		t.Helper()
		rows, err := plannedAPAppliedRecords(journalState(t, a))
		if err != nil || len(rows) != 1 || len(*writes) != count {
			t.Fatal("checkpoint correction failed or replayed", len(rows), len(*writes), count, err)
		}
		for _, row := range rows {
			parts, ok := plannedAPPrivateParts(row.Operation.Resource)
			if !ok || len(parts) != 6 || parts[5] != epoch {
				t.Fatal("correction lost its explicit floor epoch")
			}
			return row
		}
		t.Fatal("correction receipt missing")
		return cvk.NetworkControllerConfigurationActionApplication{}
	}
	width := 12.0
	sites[len(sites)-1].Width = &width
	scaled := 5.4
	(*placements)[0].Position.X = &scaled
	setModel(width, 4.5)
	firstEpoch := strings.Repeat("d", 32)
	seedPlannedAPFloorConsumer(t, a, config, firstEpoch)
	if strings.Contains(mode, "consumer-drift") || strings.Contains(mode, "consumer-missing") {
		if strings.Contains(mode, "consumer-drift") {
			n := 5.5
			(*placements)[0].Position.X = &n
		} else {
			nc, _ := a.currentController(ctx)
			for i := range nc.Status.Configuration.ActionApplications {
				if len(nc.Status.Configuration.ActionApplications[i].EffectReceipts) > 0 {
					nc.Status.Configuration.ActionApplications[i].EffectReceipts = nil
				}
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < 4; i++ {
			tick()
		}
		if len(*writes) != before || journalState(t, a).Operation != nil || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("drifted/missing floor authority caused a mutation or false convergence")
		}
		return
	}
	if strings.Contains(mode, "keyed-result-drift") {
		for i := 0; i < 8; i++ {
			tick()
		}
		state := journalState(t, a)
		rows, err := plannedAPAppliedRecords(state)
		if err != nil || len(rows) != 0 || len(*writes) != before+1 || state.Operation == nil || state.Operation.Phase != "Verifying" || config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			t.Fatal("unrelated post-task native change was published or replayed", err)
		}
		return
	}
	converge()
	first := checkEpoch(firstEpoch, before+1)
	setModel(width, 6.5)
	converge()
	second := checkEpoch(firstEpoch, before+2)
	parts, _ := plannedAPPrivateParts(second.Operation.Resource)
	if parts[3] != siteReceiptDigest(first) {
		t.Fatal("second correction did not advance its own latest receipt")
	}
	width = 14.0
	sites[len(sites)-1].Width = &width
	scaled, err = plannedAPFloorCoordinate(6.5 * 14 / 12)
	if err != nil {
		t.Fatal(err)
	}
	(*placements)[0].Position.X = &scaled
	setModel(width, 6.5)
	secondEpoch := strings.Repeat("e", 32)
	seedPlannedAPFloorConsumer(t, a, config, secondEpoch)
	converge()
	checkEpoch(secondEpoch, before+3)
	for i := 0; i < 3; i++ {
		tick()
	}
	anchorsAfter, err := plannedAPIdentityRecords(journalState(t, a))
	if err != nil || !reflect.DeepEqual(anchorsBefore, anchorsAfter) || len(*writes) != before+3 || len(journalState(t, a).ActionApplications) != 4 {
		t.Fatal("checkpoint consumption grew receipts, changed anchors or replayed", err)
	}
}
