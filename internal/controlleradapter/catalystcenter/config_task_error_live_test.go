// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestLiveConfigurationFailedTaskRead(t *testing.T) {
	id := os.Getenv("CATC_FAILED_TASK_READ")
	if id == "" {
		t.Skip("explicit existing task not supplied")
	}
	if !validAPIID(id) {
		t.Fatal("invalid task ID")
	}
	expected := os.Getenv("CATC_FAILED_TASK_EXPECTED_CODE")
	if expected == "" {
		expected = "NCSP11000"
	}
	if !configurationTaskCode.MatchString(expected) {
		t.Fatal("expected task code is not a bounded machine code")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := liveClient(t)
	raw, err := c.get(ctx, taskPath+id, nil, 200)
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Response struct {
			ID    string `json:"id"`
			Start int64  `json:"startTime"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &task) != nil || task.Response.ID != id || task.Response.Start <= 0 {
		t.Fatal("task correlation missing")
	}
	done, err := c.configurationTaskComplete(ctx, id, time.UnixMilli(task.Response.Start), time.Now())
	var failure *configurationTaskFailure
	if done || !errors.As(err, &failure) || failure.code != expected {
		t.Fatal("expected qualified failed-task response", err)
	}
	done, err = c.configurationTaskTreeComplete(ctx, id, time.UnixMilli(task.Response.Start), time.Now())
	if done || !errors.As(err, &failure) || failure.code != expected {
		t.Fatal("full rooted task tree did not retain correlated failure", err)
	}
	t.Log(failure.Error())
}
