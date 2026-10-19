// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestWirelessSSIDUnknownTaskObservationRemainsScoped(t *testing.T) {
	for _, mode := range []string{"known", "legacy", "pending", "failed", "foreign-task", "stale-time", "missing-task", "pruned-history", "other-resource", "custom-observer"} {
		t.Run(mode, func(t *testing.T) {
			_, state, _ := ssidQualificationProof(t, 2)
			op := state.Operation
			op.Phase = "Unknown"
			plan := configurationApplyPlan{section: "wireless"}
			expectedRead := mode == "known" || mode == "legacy" || mode == "pending" || mode == "failed" || mode == "foreign-task" || mode == "stale-time"
			observer, reads := 0, 0
			switch mode {
			case "legacy":
				op.SSIDCredentialHistory = nil
				op.Resource = "ssid-credential:global:CVK_PSK_fixture"
			case "missing-task":
				op.TaskID = ""
			case "pruned-history":
				op.SSIDCredentialHistory = nil
			case "other-resource":
				op.Resource = "ap-credential-profile:example"
			case "custom-observer":
				plan.observeCompletion = func(context.Context, *cvk.NetworkControllerConfigurationOperation, time.Time) (bool, error) {
					observer++
					return true, nil
				}
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != taskPath+"task-id" {
					t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				reads++
				id := "task-id"
				start := op.SubmittedAt.UnixMilli()
				end := time.Now().UnixMilli()
				if mode == "foreign-task" {
					id = "other-task"
				}
				if mode == "stale-time" {
					start -= int64(time.Hour / time.Millisecond)
				}
				if mode == "pending" {
					end = 0
				}
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "startTime": start, "endTime": end, "isError": mode == "failed", "errorCode": "NCSP11008"}})
			})
			done, err := plan.taskComplete(context.Background(), c, op, time.Now())
			if (mode == "known" || mode == "legacy") && (!done || err != nil) {
				t.Fatal("known task refused", err)
			}
			if mode == "pending" && (done || err != nil) {
				t.Fatal("pending outcome changed", err)
			}
			if mode != "known" && mode != "legacy" && mode != "pending" && (done || err == nil) {
				t.Fatal("uncorroborated outcome accepted", err)
			}
			if expectedRead && reads != 1 || !expectedRead && reads != 0 || observer != 0 {
				t.Fatal("observation escaped supported scope", reads, observer)
			}
			if op.Phase != "Unknown" {
				t.Fatal("task observation fabricated accepted phase")
			}
		})
	}
}
