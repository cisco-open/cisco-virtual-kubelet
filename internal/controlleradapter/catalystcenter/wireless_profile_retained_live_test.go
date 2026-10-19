// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"os"
	"reflect"
	"testing"
	"time"
)

// A retained nonsecret model and operation receipt may qualify readback without
// repeating a mutation. The fixture must still exist; deleted examples are not
// reusable evidence. This test has no submit, cleanup or task-following path.
func TestLiveWirelessProfileTagRetainedReadback(t *testing.T) {
	path := os.Getenv("CATC_WIRELESS_PROFILE_TAG_RECEIPT")
	if path == "" {
		t.Skip("provide the retained model and operation receipt")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Model     json.RawMessage                              `json:"model"`
		Operation *cvk.NetworkControllerConfigurationOperation `json:"operation"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.Operation == nil || !validAPIID(receipt.Operation.ParentID) || !validAPIID(receipt.Operation.TaskID) {
		t.Fatal("invalid retained profile tag receipt")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := c.compileNetworkProfileConfiguration(receipt.Model)
	if err != nil {
		t.Fatal(err)
	}
	qualifyLiveWirelessProfileTagRetainedReadback(t, ctx, c, e, receipt.Operation)
}

func qualifyLiveWirelessProfileTagRetainedReadback(t *testing.T, ctx context.Context, c *client, e *networkProfileConfiguration, op *cvk.NetworkControllerConfigurationOperation) {
	t.Helper()
	var err error
	before := map[bool]map[string]wirelessProfileTagMetadata{}
	for _, policy := range []bool{false, true} {
		before[policy], err = c.wirelessProfileTagInventory(ctx, op.ParentID, policy)
		if err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := e.verify(ctx, op); err != nil || !ok {
		t.Fatal("retained operation readback incomplete", err)
	}
	if next, err := e.next(ctx); err != nil || next != nil {
		t.Fatal("retained model did not converge without replay", err)
	}
	for _, policy := range []bool{false, true} {
		after, err := c.wirelessProfileTagInventory(ctx, op.ParentID, policy)
		if err != nil || !reflect.DeepEqual(before[policy], after) {
			t.Fatal("tag inventory changed during observation", err)
		}
	}
	t.Log("original accepted operation verified with retained resource identity and unchanged tag inventory; no request replayed")
}
