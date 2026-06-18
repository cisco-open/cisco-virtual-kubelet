// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package nxos

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestDeployPodUsesCLIConfForLifecycleCommands(t *testing.T) {
	pod := nxosTestPod()
	appID := common.GenerateContainerAppIDs(pod)["app"]
	var mu sync.Mutex
	var requests []nxapiRequest
	state := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, req)
		input := req.InsAPI.Input
		body := ""
		switch {
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting install "):
			state = "DEPLOYED"
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting activate "):
			state = "ACTIVATED"
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting start "):
			state = "RUNNING"
		case req.InsAPI.Type == "cli_show_ascii" && strings.HasPrefix(input, "show app-hosting detail "):
			if state != "" {
				body = nxosAppDetailBody(appID, state)
			}
		}
		mu.Unlock()
		writeNXAPISuccess(t, w, input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	if err := driver.DeployPod(context.Background(), pod, nil, nil); err != nil {
		t.Fatalf("DeployPod: %v", err)
	}
	assertNXAPILifecycleCommand(t, requests, "app-hosting install appid "+appID, "cli_conf")
	assertNXAPILifecycleCommand(t, requests, "app-hosting activate appid "+appID, "cli_conf")
	assertNXAPILifecycleCommand(t, requests, "app-hosting start appid "+appID, "cli_conf")
	assertNoLifecycleShow(t, requests)
}

func TestDeletePodUsesCLIConfForLifecycleCommands(t *testing.T) {
	pod := nxosTestPod()
	appID := common.GenerateContainerAppIDs(pod)["app"]
	var mu sync.Mutex
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, req)
		input := req.InsAPI.Input
		body := ""
		if req.InsAPI.Type == "cli_show_ascii" && strings.HasPrefix(input, "show app-hosting detail ") {
			body = nxosAppDetailBody(appID, "RUNNING")
		}
		mu.Unlock()
		writeNXAPISuccess(t, w, input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	if err := driver.DeletePod(context.Background(), pod); err != nil {
		t.Fatalf("DeletePod: %v", err)
	}
	assertNXAPILifecycleCommand(t, requests, "app-hosting stop appid "+appID, "cli_conf")
	assertNXAPILifecycleCommand(t, requests, "app-hosting deactivate appid "+appID, "cli_conf")
	assertNXAPILifecycleCommand(t, requests, "app-hosting uninstall appid "+appID, "cli_conf")
	assertNoLifecycleShow(t, requests)
}

func TestDeployPodDoesNotHoldDriverMutexWhilePolling(t *testing.T) {
	pod := nxosTestPod()
	appID := common.GenerateContainerAppIDs(pod)["app"]
	pollEntered := make(chan struct{})
	releasePoll := make(chan struct{})
	var detailCalls int
	state := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		body := ""
		if req.InsAPI.Type == "cli_show_ascii" && strings.HasPrefix(req.InsAPI.Input, "show app-hosting detail ") {
			detailCalls++
			if detailCalls == 2 {
				close(pollEntered)
				<-releasePoll
				state = "RUNNING"
			}
			if state != "" {
				body = nxosAppDetailBody(appID, state)
			}
		}
		writeNXAPISuccess(t, w, req.InsAPI.Input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	done := make(chan error, 1)
	go func() {
		done <- driver.DeployPod(context.Background(), pod, nil, nil)
	}()

	select {
	case <-pollEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("DeployPod did not enter wait polling")
	}
	if !driver.mu.TryLock() {
		close(releasePoll)
		t.Fatal("driver mutex is held while DeployPod waits for app state")
	}
	driver.mu.Unlock()
	close(releasePoll)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("DeployPod: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DeployPod did not finish after polling was released")
	}
}

func nxosTestPod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "hello",
			UID:       types.UID("01234567-89ab-cdef-0123-456789abcdef"),
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{{
				Name:  "app",
				Image: "bootflash:/hello.tar",
			}},
		},
	}
}

func nxosTestDriver(server *httptest.Server) *NXOSDriver {
	return &NXOSDriver{
		config: &v1alpha1.DeviceSpec{Address: "192.0.2.10"},
		client: &nxapiClient{
			baseURL: server.URL,
			client:  server.Client(),
		},
	}
}

func nxosAppDetailBody(appID, state string) string {
	return fmt.Sprintf("App id                 : %s\nState                  : %s\n", appID, state)
}

func writeNXAPISuccess(t *testing.T, w http.ResponseWriter, input, body string) {
	t.Helper()
	inputJSON, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	_, _ = fmt.Fprintf(w, `{"ins_api":{"outputs":{"output":{"input":%s,"code":"200","msg":"Success","body":%s}}}}`, inputJSON, bodyJSON)
}

func assertNXAPILifecycleCommand(t *testing.T, requests []nxapiRequest, prefix, wantType string) {
	t.Helper()
	for _, req := range requests {
		if strings.HasPrefix(req.InsAPI.Input, prefix) {
			if req.InsAPI.Type != wantType {
				t.Fatalf("%q type=%q, want %q", req.InsAPI.Input, req.InsAPI.Type, wantType)
			}
			return
		}
	}
	t.Fatalf("did not see lifecycle command prefix %q in %#v", prefix, requests)
}

func assertNoLifecycleShow(t *testing.T, requests []nxapiRequest) {
	t.Helper()
	for _, req := range requests {
		if req.InsAPI.Type == "cli_show_ascii" && isAppLifecycleCommand(req.InsAPI.Input) {
			t.Fatalf("state-changing app-hosting command used cli_show_ascii: %q", req.InsAPI.Input)
		}
	}
}

func isAppLifecycleCommand(input string) bool {
	for _, prefix := range []string{
		"app-hosting install ",
		"app-hosting activate ",
		"app-hosting start ",
		"app-hosting stop ",
		"app-hosting deactivate ",
		"app-hosting uninstall ",
	} {
		if strings.HasPrefix(input, prefix) {
			return true
		}
	}
	return false
}
