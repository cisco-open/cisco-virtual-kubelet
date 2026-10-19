// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"
)

// Diagnostic output contains only known non-secret identities, booleans and
// task terminal metadata. Inherited profile contents never leave memory.
func TestLiveSwitchingProfileRetainedReadOnlyDiagnostic(t *testing.T) {
	id := os.Getenv("CATC_SWITCHING_PROFILE_DIAGNOSTIC_ID")
	if !validAPIID(id) {
		t.Skip("exact retained diagnostic UUID not provided")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	profiles, err := c.listSwitchingProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := ""
	for _, profile := range profiles {
		if profile.ID == id {
			name = profile.Name
		}
	}
	if name == "" {
		t.Fatal("captured profile UUID is absent")
	}
	e := &switchingProfileExecutor{client: c, desired: []switchingProfileIntent{{name: name}}}
	before, err := e.observe(ctx)
	if err != nil || len(before) != 1 {
		t.Fatal("retained profile read failed", err)
	}
	sets, _ := profileTemplateSets(before[0].actual)
	t.Log("retained profile membership counts", len(sets["cli.templates"]), len(sets["day0.templates"]))
	for i := 0; i < 6; i++ {
		after, err := e.observe(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("consecutive private observations equal", sameSwitchingProfileObservation(before, after), "selected payload equal", reflect.DeepEqual(before[0].desired, after[0].desired), "binding equal", before[0].binding == after[0].binding)
		before = after
	}
	taskID := os.Getenv("CATC_SWITCHING_PROFILE_DIAGNOSTIC_TASK")
	if validAPIID(taskID) {
		raw, err := c.get(ctx, taskPath+taskID, nil, http.StatusOK)
		if err != nil {
			t.Fatal(err)
		}
		var task struct {
			Response struct {
				ID    string `json:"id"`
				End   *int64 `json:"endTime"`
				Error *bool  `json:"isError"`
				Code  string `json:"errorCode"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &task) != nil || task.Response.ID != taskID {
			t.Fatal("task diagnostic response invalid")
		}
		t.Log("captured task terminal", task.Response.End != nil, "failed", task.Response.Error != nil && *task.Response.Error)
	}
}
