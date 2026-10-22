// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestLiveActivationLifecycle(t *testing.T) {
	if os.Getenv("CVK_SDWAN_MUTATE") != "1" {
		t.Skip("requires fixture mutation opt-in")
	}
	fixture := os.Getenv("CVK_SDWAN_ACTIVATION_FIXTURE")
	if fixture != "classic-centralized-activation" && fixture != "topology-group-activation" {
		t.Skip("select an activation fixture")
	}
	c := liveClient(t)
	ctx := context.Background()
	resources := parcelFixtures(t, fixture)
	var activation Resource
	for _, r := range resources {
		if r.Kind == "activation" {
			activation = r
			break
		}
	}
	if activation.Name == "" {
		t.Fatal("fixture has no activation")
	}
	if activation.Type == "centralized-policy" {
		var list struct {
			Data []map[string]any `json:"data"`
		}
		if err := c.Request(ctx, "GET", centralizedPolicyCollection, nil, &list); err != nil || len(list.Data) != 0 {
			t.Fatal("centralized policy baseline is not empty", err)
		}
	} else {
		var list []map[string]any
		if err := c.Request(ctx, "GET", topologyGroupCollection, nil, &list); err != nil || len(list) != 0 {
			t.Fatal("topology group baseline is not empty", err)
		}
	}
	for _, r := range resources {
		if r.Kind == "activation" {
			continue
		}
		if observed, err := c.Observe(ctx, r); err != nil || observed != nil {
			t.Fatal("fixture must be absent", r.Type, r.Name, err)
		}
		id, err := c.Create(ctx, r)
		if err != nil {
			t.Fatal("create prerequisite", r.Type, r.Name, err)
		}
		t.Cleanup(func() {
			if err := c.DeleteResourceFixture(context.Background(), r, id); err != nil {
				t.Error("prerequisite cleanup", r.Type, r.Name, err)
			}
		})
	}
	if observed, err := c.Observe(ctx, activation); err != nil || observed != nil {
		t.Fatal("activation must be absent", err)
	}
	var id string
	var err error
	if activation.Type == "centralized-policy" {
		id, err = c.centralizedPolicyID(ctx, activation.Name)
	} else {
		id, err = c.topologyGroupID(ctx, activation.Name)
	}
	if err != nil || id == "" {
		t.Fatal("activation target ID", err)
	}
	// Register restoration before sending the activation request: a task polling
	// error can otherwise leave an activation whose POST actually succeeded.
	t.Cleanup(func() {
		observed, err := c.Observe(context.Background(), activation)
		if err != nil {
			t.Error("restoration read", err)
			return
		}
		if observed != nil {
			if err := deactivateLiveFixture(c, activation.Type, id); err != nil {
				t.Error("activation restoration", err)
				return
			}
		}
		if observed, err := c.Observe(context.Background(), activation); err != nil || observed != nil {
			t.Error("activation remained after restoration", err)
		}
	})
	createdID := ""
	if os.Getenv("CVK_SDWAN_ACTIVATION_DIAG") == "1" {
		createdID, err = activateWithTaskDiagnostics(t, c, activation, id)
	} else {
		createdID, err = c.Create(ctx, activation)
	}
	if err != nil || createdID != id {
		t.Fatal("activate", err)
	}
	observed, err := c.Observe(ctx, activation)
	if err != nil || observed == nil {
		t.Fatal("activation readback", err)
	}
	same, err := Equal(activation, observed.Body)
	if err != nil || !same {
		t.Fatal("activation mismatch", err)
	}
}

func activateWithTaskDiagnostics(t *testing.T, c *Client, r Resource, id string) (string, error) {
	t.Helper()
	path := centralizedPolicyCollection + "activate/" + id + "?confirm=true"
	if r.Type == "topology-group" {
		path = topologyGroupCollection + "/" + id + "/device/deploy"
	}
	var result map[string]any
	if err := diagnosticActivationRequest(t, c, path, &result); err != nil {
		return "", err
	}
	taskID, _ := result["id"].(string)
	if r.Type == "topology-group" {
		taskID, _ = result["parentTaskId"].(string)
	}
	if err := c.waitCustomApplicationTask(context.Background(), taskID); err != nil {
		var status struct {
			Summary    map[string]any   `json:"summary"`
			Validation map[string]any   `json:"validation"`
			Data       []map[string]any `json:"data"`
		}
		if readErr := c.Request(context.Background(), "GET", "/dataservice/device/action/status/"+taskID, nil, &status); readErr == nil {
			t.Logf("task summary status=%v validation=%v", status.Summary["status"], status.Validation["status"])
			for _, row := range status.Data {
				t.Logf("task row status=%v activity=%v message=%v", row["statusId"], row["activity"], row["statusMessage"])
			}
		}
		return "", err
	}
	return id, nil
}

func diagnosticActivationRequest(t *testing.T, c *Client, path string, result *map[string]any) error {
	t.Helper()
	ctx := context.Background()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.connect(ctx); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(c.Endpoint, "/")+path, strings.NewReader("{}"))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-XSRF-TOKEN", c.token)
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Logf("activation rejection body bytes=%d content-type=%s", len(data), response.Header.Get("Content-Type"))
		var failure map[string]any
		if json.Unmarshal(data, &failure) == nil {
			keys := make([]string, 0, len(failure))
			for key := range failure {
				keys = append(keys, key)
			}
			t.Logf("activation rejection fields=%v", keys)
			for _, key := range []string{"errorCode", "code", "message", "errorMessage", "error"} {
				if value, ok := failure[key].(string); ok {
					t.Logf("activation error %s: %.400s", key, value)
				}
			}
			if nested, ok := failure["error"].(map[string]any); ok {
				keys := make([]string, 0, len(nested))
				for key := range nested {
					keys = append(keys, key)
				}
				t.Logf("activation error fields=%v", keys)
				for _, key := range []string{"code", "message", "errorCode", "errorMessage", "details", "detail"} {
					if value, ok := nested[key].(string); ok {
						t.Logf("activation error.%s: %.400s", key, value)
					}
				}
			}
		}
		return &HTTPError{Status: response.StatusCode}
	}
	return json.Unmarshal(data, result)
}

func deactivateLiveFixture(c *Client, kind, id string) error {
	ctx := context.Background()
	if kind == "topology-group" {
		var result struct {
			TaskID string `json:"parentTaskId"`
		}
		if err := c.Request(ctx, "POST", topologyGroupCollection+"/"+id+"/device/deploy", map[string]any{"deactivateTopology": true}, &result); err != nil {
			return err
		}
		return c.waitCustomApplicationTask(ctx, result.TaskID)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := c.Request(ctx, "POST", centralizedPolicyCollection+"deactivate/"+id, map[string]any{}, &result); err != nil {
		return err
	}
	return c.waitCustomApplicationTask(ctx, result.ID)
}
