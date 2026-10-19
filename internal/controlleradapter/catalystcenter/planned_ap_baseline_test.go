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

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func plannedAPKeyedFixture(t *testing.T) (*plannedAPExecutor, plannedAPState, cvk.NetworkControllerConfigurationActionApplication) {
	t.Helper()
	_, desired, err := parseSitesWithPlannedAPs([]byte(plannedAPFixture))
	if err != nil {
		t.Fatal(err)
	}
	native := plannedAPNativeTestObject(t)
	native["id"] = plannedAPIdentityTestUUID
	native["opaque"] = map[string]any{"counter": json.Number("9007199254740992"), "nullable": nil}
	raw, _ := json.Marshal(native)
	var actual plannedAPObservation
	if json.Unmarshal(raw, &actual) != nil || validatePlannedAPObservation(&actual) != nil {
		t.Fatal("fixture")
	}
	o := plannedAPState{want: desired[0], floorID: siteOwnedTestUUID, actual: actual, native: native}
	e := &plannedAPExecutor{desired: desired, identityOwner: "owner", sourceMode: "i", comparison: &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("e", 64), check: func(context.Context) error { return nil }}, applied: map[string]cvk.NetworkControllerConfigurationActionApplication{}}
	r := plannedAPIdentityTestRecord()
	r.Operation.Resource = plannedAPPrivateObservePrefix + strings.TrimPrefix(plannedAPResource(desired[0]), "planned-ap:") + ":i:" + e.comparison.keyID
	view, err := plannedAPBaselineView(o)
	if err != nil {
		t.Fatal(err)
	}
	mac, err := plannedAPNativeMAC(e.comparison, r.Operation.Resource, view)
	if err != nil {
		t.Fatal(err)
	}
	r.Resource = plannedAPIdentityResource(&r.Operation, plannedAPIdentityTestUUID, mac)
	e.identities, err = plannedAPIdentityRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}})
	if err != nil {
		t.Fatal(err)
	}
	return e, o, r
}
func TestPlannedAPKeyedNativeBaseline(t *testing.T) {
	for _, mode := range []string{"same", "opaque-number", "opaque-null", "managed-coordinate", "MAC-representation", "key-removed", "key-changed", "key-id", "source-mode", "scope", "missing-check"} {
		t.Run(mode, func(t *testing.T) {
			e, o, r := plannedAPKeyedFixture(t)
			switch mode {
			case "opaque-number":
				o.native["opaque"].(map[string]any)["counter"] = json.Number("9007199254740993")
			case "opaque-null":
				delete(o.native["opaque"].(map[string]any), "nullable")
			case "managed-coordinate":
				v := 5.5
				o.actual.Position.X = &v
				o.native["position"].(map[string]any)["x"] = json.Number("5.5")
			case "MAC-representation":
				o.native["macAddress"] = "AA-BB-CC-DD-EE-FF"
			case "key-removed":
				e.comparison = nil
			case "key-changed":
				e.comparison.key = []byte(strings.Repeat("z", 32))
			case "key-id":
				e.comparison.keyID = strings.Repeat("f", 64)
			case "source-mode":
				e.sourceMode = "c"
			case "scope":
				o.floorID = plannedAPIdentityTestUUID
			case "missing-check":
				e.comparison.check = nil
			}
			err := e.checkPrivateBaseline(context.Background(), o)
			if (err == nil) != (mode == "same" || mode == "MAC-representation") {
				t.Fatal("keyed native drift or authority loss was accepted", err)
			}
			if mode == "same" {
				value := e.identities[plannedAPResource(o.want)]
				if value.keyID == "" || value.mac == "" || r.Resource == "" {
					t.Fatal("keyed baseline missing")
				}
			}
		})
	}
}
func TestPlannedAPPrivateIdentityReceiptBoundaries(t *testing.T) {
	for _, mode := range []string{"valid", "key-pruned", "MAC-pruned", "mode-pruned", "producer-key", "result-prefix", "result-pruned", "task-pruned"} {
		t.Run(mode, func(t *testing.T) {
			_, _, r := plannedAPKeyedFixture(t)
			switch mode {
			case "key-pruned":
				r.Resource = strings.Replace(r.Resource, strings.Repeat("e", 64), "", 1)
			case "MAC-pruned":
				r.Resource = r.Resource[:len(r.Resource)-64]
			case "mode-pruned":
				r.Resource = strings.Replace(r.Resource, ":i:", ":c:", 1)
			case "producer-key":
				r.Operation.Resource = strings.Replace(r.Operation.Resource, strings.Repeat("e", 64), strings.Repeat("f", 64), 1)
			case "result-prefix":
				r.Resource = strings.Replace(r.Resource, plannedAPPrivateIdentityPrefix, plannedAPIdentityPrefix, 1)
			case "result-pruned":
				r.Resource = "pruned"
			case "task-pruned":
				r.Operation.TaskID = ""
			}
			state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
			_, err := plannedAPIdentityRecords(state)
			if (err == nil) != (mode == "valid") || plannedAPIdentityReceiptPresent(state, &r.Operation) != (mode == "valid") {
				t.Fatal("private result/schema/key binding was not enforced", err)
			}
		})
	}
}
func TestPlannedAPPrivateUpdateResultAndCapacity(t *testing.T) {
	e, o, r := plannedAPKeyedFixture(t)
	// Unknown native attributes stay in the expected MAC even though a real
	// writer refuses unmapped fields. This tests result binding independently.
	body, _, err := plannedAPUpdatePayload(o.want, o.actual)
	if err != nil {
		t.Fatal(err)
	}
	body["position"].(map[string]any)["x"] = 8.0
	resource, err := e.privateUpdateResource(o, body)
	if err != nil {
		t.Fatal(err)
	}
	parts, ok := plannedAPPrivateParts(resource)
	if !ok || len(parts) != 5 || parts[3] != siteReceiptDigest(r) {
		t.Fatal("expected result/prior identity missing")
	}
	op := r.Operation.DeepCopy()
	op.ID = strings.Repeat("b", 32)
	op.Resource = resource
	op.Action = "Update"
	op.TargetID = plannedAPIdentityTestUUID
	op.Phase = "Prepared"
	op.TaskID = ""
	op.SubmittedAt = nil
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{r}}
	if err := plannedAPApplyCapacity(state, op); err != nil {
		t.Fatal(err)
	}
	forged := op.DeepCopy()
	forged.Resource = strings.Replace(resource, parts[3], strings.Repeat("f", 64), 1)
	if err := plannedAPApplyCapacity(state, forged); err == nil {
		t.Fatal("changed prior receipt allowed dispatch")
	}
	at := metav1.NewTime(time.Now())
	op.Phase, op.TaskID, op.SubmittedAt = "Verified", "task-update", &at
	anchor := e.identities[plannedAPResource(o.want)]
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "sites", Resource: plannedAPAppliedResult(op, anchor), Operation: *op, VerifiedAt: &at}
	if err := plannedAPReplaceApplied(state, row); err != nil {
		t.Fatal(err)
	}
	if !plannedAPAppliedReceiptPresent(state, op) {
		t.Fatal("atomic applied receipt missing")
	}
	changed := row
	changed.Resource = strings.Replace(row.Resource, parts[4], strings.Repeat("d", 64), 1)
	broken := state.DeepCopy()
	broken.ActionApplications[1] = changed
	if _, err := plannedAPAppliedRecords(broken); err == nil {
		t.Fatal("result disagreed with pre-dispatch expected MAC")
	}
	partial := op.DeepCopy()
	partial.Resource = "pruned"
	if !plannedAPAppliedSettlementRequired(state, partial) || plannedAPAppliedReceiptPresent(state, partial) {
		t.Fatal("partial current operation bypassed retained proof")
	}
	after, err := plannedAPIdentityRecords(state)
	if err != nil || !reflect.DeepEqual(after, e.identities) {
		t.Fatal("latest state changed initial keyed observation", err)
	}
	second := op.DeepCopy()
	second.ID = strings.Repeat("c", 32)
	second.Phase = "Prepared"
	second.TaskID = ""
	second.SubmittedAt = nil
	second.Resource = plannedAPPrivateUpdatePrefix + strings.Join(parts[:3], ":") + ":" + siteReceiptDigest(row) + ":" + parts[4]
	if err := plannedAPApplyCapacity(state, second); err != nil {
		t.Fatal("bounded next update not admitted", err)
	}
	second.Phase, second.TaskID, second.SubmittedAt = "Verified", "task-second", &at
	row.Operation = *second
	row.Resource = plannedAPAppliedResult(second, anchor)
	if err := plannedAPReplaceApplied(state, row); err != nil || len(state.ActionApplications) != 2 {
		t.Fatal("latest rows grew or lost lineage", err)
	}
}
