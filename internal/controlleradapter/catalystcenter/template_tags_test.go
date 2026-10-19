// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"testing"
	"time"
)

func TestTagExecution(t *testing.T) {
	tags := []observedTag{}
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path == taskPath+"tag-task" {
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "tag-task", "isError": false, "startTime": time.Now().UnixMilli(), "endTime": time.Now().UnixMilli(), "serviceType": "Grouping Service", "data": "22f33fa0-e275-41c9-a50d-d7e5135adb8c"}})
			return
		}
		if r.URL.Path != tagsPath {
			t.Error("unexpected API")
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"response": tags})
			return
		}
		var tag observedTag
		if err := json.NewDecoder(r.Body).Decode(&tag); err != nil {
			t.Error(err)
		}
		if writes == 0 && r.Method != "POST" {
			t.Error("expected create")
		}
		if writes > 0 && (r.Method != "PUT" || tag.ID != "22f33fa0-e275-41c9-a50d-d7e5135adb8c") {
			t.Error("update identity not pinned")
		}
		tag.ID = "22f33fa0-e275-41c9-a50d-d7e5135adb8c"
		tags = []observedTag{tag}
		writes++
		fmt.Fprint(w, `{"response":{"taskId":"tag-task"}}`)
	})
	for _, description := range []string{"first", "second"} {
		raw := []byte(fmt.Sprintf(`{"catalyst_center":{"templates":{"tags":[{"name":"lab","description":%q,"system_tag":false}]}}}`, description))
		executor, err := c.compileConfigurationExecutor(raw, "templates")
		if err != nil {
			t.Fatal(err)
		}
		write, err := executor.next(context.Background())
		if err != nil || write == nil {
			t.Fatal("no tag write", err)
		}
		if _, err = write.submit(context.Background()); err != nil {
			t.Fatal(err)
		}
		executor, err = c.compileConfigurationExecutor(raw, "templates")
		if err != nil {
			t.Fatal(err)
		}
		at := metav1.Now()
		if ok, err := executor.verify(context.Background(), &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, TaskID: "tag-task", SubmittedAt: &at}); err != nil || !ok {
			t.Fatal("tag verify", err)
		}
		if next, err := executor.next(context.Background()); err != nil || next != nil {
			t.Fatal("tag did not converge", err)
		}
	}
	if writes != 2 {
		t.Fatal("tag replayed")
	}
}
func TestTagRulesAndProtectedPreflight(t *testing.T) {
	raw := []byte(`{"catalyst_center":{"templates":{"tags":[{"name":"new"},{"name":"protected","description":"change","dynamic_rules":[{"member_type":"networkdevice","operation":"AND","items":[{"operation":"EQ","name":"family","value":"Switches"},{"operation":"EQ","name":"hostname","value":"isolated"}]}]}]}}}`)
	intent, err := parseTemplateIntent(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	rules := intent.tags[1].payload["dynamicRules"].([]any)
	if rules[0].(map[string]any)["rules"] == nil {
		t.Fatal("rule nesting missing")
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Fatal("partial mutation")
		}
		fmt.Fprint(w, `{"response":[{"id":"protected","name":"protected","systemTag":true}]}`)
	})
	executor, err := c.compileConfigurationExecutor(raw, "templates")
	if err != nil {
		t.Fatal(err)
	}
	if write, err := executor.next(context.Background()); err == nil || write != nil {
		t.Fatal("system tag conflict did not block earlier write")
	}
}
