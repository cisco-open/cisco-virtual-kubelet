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
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

func TestDeployPodInitiatesInstallWithoutWaitingForRunning(t *testing.T) {
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
	assertNXAPILifecycleCommandAbsent(t, requests, "app-hosting activate appid "+appID)
	assertNXAPILifecycleCommandAbsent(t, requests, "app-hosting start appid "+appID)
	assertNoLifecycleShow(t, requests)
}

func TestDeletePodUsesCLIConfForLifecycleCommands(t *testing.T) {
	pod := nxosTestPod()
	appID := common.GenerateContainerAppIDs(pod)["app"]
	var mu sync.Mutex
	var requests []nxapiRequest
	state := "RUNNING"
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
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting stop "):
			state = "STOPPED"
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting deactivate "):
			state = "DEPLOYED"
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting uninstall "):
			state = ""
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
	if err := driver.DeletePod(context.Background(), pod); err != nil {
		t.Fatalf("DeletePod: %v", err)
	}
	assertNXAPILifecycleCommand(t, requests, "app-hosting stop appid "+appID, "cli_conf")
	assertNXAPILifecycleCommand(t, requests, "app-hosting deactivate appid "+appID, "cli_conf")
	assertNXAPILifecycleCommand(t, requests, "app-hosting uninstall appid "+appID, "cli_conf")
	assertNoLifecycleShow(t, requests)
}

func TestDeletePodRemovesConfigWhenAppStateIsUnavailable(t *testing.T) {
	pod := nxosTestPod()
	appID := common.GenerateContainerAppIDs(pod)["app"]
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, req)
		body := ""
		if req.InsAPI.Type == "cli_show_ascii" && strings.HasPrefix(req.InsAPI.Input, "show app-hosting detail ") {
			body = ""
		}
		writeNXAPISuccess(t, w, req.InsAPI.Input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	if err := driver.DeletePod(context.Background(), pod); err != nil {
		t.Fatalf("DeletePod: %v", err)
	}
	assertNXAPILifecycleCommand(t, requests, "configure terminal ; no app-hosting appid "+appID, "cli_conf")
	for _, req := range requests {
		for _, prefix := range []string{"app-hosting stop ", "app-hosting deactivate ", "app-hosting uninstall "} {
			if strings.HasPrefix(req.InsAPI.Input, prefix) {
				t.Fatalf("unexpected lifecycle command for unknown state: %q", req.InsAPI.Input)
			}
		}
	}
}

func TestDeleteAppFinalTimeoutCheckAcceptsLateRemoval(t *testing.T) {
	appID := "cvk0000_late"
	state := "STOPPED"
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, req)
		input := req.InsAPI.Input
		body := ""
		switch {
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting deactivate "):
			state = ""
		case req.InsAPI.Type == "cli_show_ascii" && strings.HasPrefix(input, "show app-hosting detail "):
			if state != "" {
				body = nxosAppDetailBody(appID, state)
			}
		}
		writeNXAPISuccess(t, w, input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	if err := driver.deleteApp(context.Background(), appID, time.Nanosecond); err != nil {
		t.Fatalf("deleteApp: %v", err)
	}
	assertNXAPILifecycleCommand(t, requests, "app-hosting deactivate appid "+appID, "cli_conf")
	assertNXAPILifecycleCommand(t, requests, "configure terminal ; no app-hosting appid "+appID, "cli_conf")
}

func TestDeployPodReturnsWhileAppIsOnlyDeployed(t *testing.T) {
	pod := nxosTestPod()
	appID := common.GenerateContainerAppIDs(pod)["app"]
	state := ""
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, req)
		body := ""
		switch {
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(req.InsAPI.Input, "app-hosting install "):
			state = "DEPLOYED"
		case req.InsAPI.Type == "cli_show_ascii" && strings.HasPrefix(req.InsAPI.Input, "show app-hosting detail "):
			if state != "" {
				body = nxosAppDetailBody(appID, state)
			}
		}
		writeNXAPISuccess(t, w, req.InsAPI.Input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	start := time.Now()
	if err := driver.DeployPod(context.Background(), pod, nil, nil); err != nil {
		t.Fatalf("DeployPod: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("DeployPod blocked for %s, want quick initiation", elapsed)
	}
	assertNXAPILifecycleCommand(t, requests, "app-hosting install appid "+appID, "cli_conf")
	assertNXAPILifecycleCommandAbsent(t, requests, "app-hosting activate appid "+appID)
	assertNXAPILifecycleCommandAbsent(t, requests, "app-hosting start appid "+appID)
}

func TestDeployPodDoesNotBlockOnLongNXAPIInstall(t *testing.T) {
	pod := nxosTestPod()
	appID := common.GenerateContainerAppIDs(pod)["app"]
	installEntered := make(chan struct{})
	releaseInstall := make(chan struct{})
	var closeOnce sync.Once
	var mu sync.Mutex
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		mu.Lock()
		requests = append(requests, req)
		mu.Unlock()
		input := req.InsAPI.Input
		if req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting install ") {
			closeOnce.Do(func() { close(installEntered) })
			<-releaseInstall
		}
		writeNXAPISuccess(t, w, input, "")
	}))
	defer server.Close()
	defer close(releaseInstall)

	driver := nxosTestDriver(server)
	driver.asyncActions = true
	start := time.Now()
	if err := driver.DeployPod(context.Background(), pod, nil, nil); err != nil {
		t.Fatalf("DeployPod: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("DeployPod blocked for %s while install command was still running", elapsed)
	}
	select {
	case <-installEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("async install action did not start")
	}
	mu.Lock()
	assertNXAPILifecycleCommand(t, append([]nxapiRequest(nil), requests...), "app-hosting install appid "+appID, "cli_conf")
	mu.Unlock()
}

func TestDockerLabelRunOptsFiltersKubernetesServiceLinks(t *testing.T) {
	pod := nxosTestPod()
	container := pod.Spec.Containers[0]
	container.Env = []v1.EnvVar{
		{Name: "APP_MODE", Value: "smoke"},
		{Name: "APP_PORT", Value: "8080"},
		{Name: "EMPTY", Value: ""},
		{Name: "KUBERNETES_SERVICE_HOST", Value: "10.43.0.1"},
		{Name: "KUBERNETES_SERVICE_PORT", Value: "443"},
		{Name: "KUBERNETES_PORT", Value: "tcp://10.43.0.1:443"},
		{Name: "KUBERNETES_PORT_443_TCP_ADDR", Value: "10.43.0.1"},
		{Name: "SPLUNK_OTEL_COLLECTOR_AGENT_SERVICE_HOST", Value: "10.43.49.232"},
		{Name: "SPLUNK_OTEL_COLLECTOR_AGENT_SERVICE_PORT_OTLP", Value: "4317"},
		{Name: "SPLUNK_OTEL_COLLECTOR_AGENT_PORT_4317_TCP", Value: "tcp://10.43.49.232:4317"},
	}
	driver := &NXOSDriver{config: &v1alpha1.DeviceSpec{}}
	opts, err := driver.buildRunOptions(pod, container)
	if err != nil {
		t.Fatalf("buildRunOptions: %v", err)
	}
	joined := strings.Join(opts, "\n")
	for _, want := range []string{
		"--hostname=app",
		"--env APP_MODE='smoke'",
		"--env APP_PORT='8080'",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("run opts missing %q in:\n%s", want, joined)
		}
	}
	for _, blocked := range []string{
		"KUBERNETES_SERVICE_HOST",
		"KUBERNETES_PORT",
		"SPLUNK_OTEL_COLLECTOR_AGENT_SERVICE_HOST",
		"SPLUNK_OTEL_COLLECTOR_AGENT_PORT_4317_TCP",
		"EMPTY",
	} {
		if strings.Contains(joined, blocked) {
			t.Fatalf("run opts unexpectedly include %q in:\n%s", blocked, joined)
		}
	}
}

func TestNXOSRunOptionsKeepIdentityLabelsOnSeparateLines(t *testing.T) {
	pod := nxosTestPod()
	pod.Name = "cvk-nxos-branch-smoke-7"
	pod.UID = types.UID("d79ecd5a-768c-4f2d-9b1b-9b46649fd0b6")
	container := pod.Spec.Containers[0]
	container.Name = "smoke"
	container.Env = []v1.EnvVar{{Name: "BRANCH_SMOKE", Value: "latest"}}
	driver := &NXOSDriver{config: &v1alpha1.DeviceSpec{}}

	opts, err := driver.buildRunOptions(pod, container)
	if err != nil {
		t.Fatalf("buildRunOptions: %v", err)
	}

	wantLines := []string{
		fmt.Sprintf("--label %s=%s", common.LabelPodName, pod.Name),
		fmt.Sprintf("--label %s=%s", common.LabelPodNamespace, pod.Namespace),
		fmt.Sprintf("--label %s=%s", common.LabelPodUID, pod.UID),
		fmt.Sprintf("--label %s=%s", common.LabelContainerName, container.Name),
		"--hostname=smoke",
	}
	for i, want := range wantLines {
		if len(opts) <= i || opts[i] != want {
			t.Fatalf("run opt line %d=%q, want %q; all opts=%#v", i, optsAt(opts, i), want, opts)
		}
	}
	if !strings.Contains(strings.Join(opts, "\n"), "--env BRANCH_SMOKE='latest'") {
		t.Fatalf("run opts missing BRANCH_SMOKE env: %#v", opts)
	}
}

func TestNXOSRunOptionsResolveSecretAndConfigMapRefs(t *testing.T) {
	pod := nxosTestPod()
	container := pod.Spec.Containers[0]
	container.Env = []v1.EnvVar{
		{Name: "SECRET_VALUE", ValueFrom: &v1.EnvVarSource{SecretKeyRef: &v1.SecretKeySelector{
			LocalObjectReference: v1.LocalObjectReference{Name: "app-secret"},
			Key:                  "token",
		}}},
		{Name: "CONFIG_VALUE", ValueFrom: &v1.EnvVarSource{ConfigMapKeyRef: &v1.ConfigMapKeySelector{
			LocalObjectReference: v1.LocalObjectReference{Name: "app-config"},
			Key:                  "mode",
		}}},
	}
	secretLister, configMapLister := nxosEnvListers(t)
	driver := &NXOSDriver{
		config:       &v1alpha1.DeviceSpec{},
		secretLister: secretLister,
		configLister: configMapLister,
	}
	opts, err := driver.buildRunOptions(pod, container)
	if err != nil {
		t.Fatalf("buildRunOptions: %v", err)
	}
	joined := strings.Join(opts, "\n")
	for _, want := range []string{
		"--env SECRET_VALUE='s3cr3t token'",
		"--env CONFIG_VALUE='debug'",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("run opts missing %q in:\n%s", want, joined)
		}
	}
}

func TestDeployPodRendersResourceProfileAndRunOpts(t *testing.T) {
	pod := nxosTestPod()
	pod.Spec.Containers[0].Resources = v1.ResourceRequirements{
		Requests: v1.ResourceList{
			v1.ResourceCPU:    resource.MustParse("50m"),
			v1.ResourceMemory: resource.MustParse("4Mi"),
		},
		Limits: v1.ResourceList{
			v1.ResourceCPU: resource.MustParse("500m"),
		},
	}
	appID := common.GenerateContainerAppIDs(pod)["app"]
	var requests []nxapiRequest
	state := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
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
		writeNXAPISuccess(t, w, input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	if err := driver.DeployPod(context.Background(), pod, nil, nil); err != nil {
		t.Fatalf("DeployPod: %v", err)
	}
	assertNXAPICommandContains(t, requests, "app-resource profile custom ; cpu 50 ; memory 4")
	assertNXAPICommandContains(t, requests, "no app-resource docker")
	assertNXAPICommandContains(t, requests, "--hostname=app")
	for _, req := range requests {
		if strings.Contains(req.InsAPI.Input, " vcpu ") {
			t.Fatalf("unexpected unsupported vcpu command: %q", req.InsAPI.Input)
		}
	}
}

func TestDeployPodManyContainersInitiatesAllApps(t *testing.T) {
	const containerCount = 32
	pod := nxosTestPod()
	pod.Name = "scale"
	pod.UID = types.UID("11111111-2222-3333-4444-555555555555")
	pod.Spec.Containers = make([]v1.Container, 0, containerCount)
	for i := 0; i < containerCount; i++ {
		pod.Spec.Containers = append(pod.Spec.Containers, v1.Container{
			Name:  fmt.Sprintf("app-%02d", i),
			Image: "bootflash:/hello.tar",
			Env: []v1.EnvVar{
				{Name: "APP_INDEX", Value: fmt.Sprintf("%02d", i)},
			},
			Resources: v1.ResourceRequirements{
				Requests: v1.ResourceList{
					v1.ResourceCPU:    resource.MustParse("25m"),
					v1.ResourceMemory: resource.MustParse("8Mi"),
				},
			},
		})
	}
	appIDs := common.GenerateContainerAppIDs(pod)
	if len(appIDs) != containerCount {
		t.Fatalf("appIDs=%d, want %d", len(appIDs), containerCount)
	}
	seenAppIDs := map[string]string{}
	for container, appID := range appIDs {
		if previous, exists := seenAppIDs[appID]; exists {
			t.Fatalf("duplicate appID %s for containers %s and %s", appID, previous, container)
		}
		seenAppIDs[appID] = container
	}

	var mu sync.Mutex
	var requests []nxapiRequest
	stateByApp := map[string]string{}
	configByApp := map[string]string{}
	installs := map[string]int{}
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
		case req.InsAPI.Type == "cli_conf" && strings.Contains(input, "app-resource profile custom"):
			appID := appIDFromCommand(input)
			configByApp[appID] = input
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting install "):
			appID := appIDFromCommand(input)
			installs[appID]++
			stateByApp[appID] = "DEPLOYED"
		case req.InsAPI.Type == "cli_show_ascii" && strings.HasPrefix(input, "show app-hosting detail "):
			appID := appIDFromCommand(input)
			if state := stateByApp[appID]; state != "" {
				body = nxosAppDetailBody(appID, state)
			}
		}
		mu.Unlock()
		writeNXAPISuccess(t, w, input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	driver.config.ResourceLimits.Others = map[string]string{nxosMaxAppsResourceKey: "64"}
	if err := driver.DeployPod(context.Background(), pod, nil, nil); err != nil {
		t.Fatalf("DeployPod: %v", err)
	}

	for _, container := range pod.Spec.Containers {
		appID := appIDs[container.Name]
		if installs[appID] != 1 {
			t.Fatalf("container %s app %s installs=%d, want 1", container.Name, appID, installs[appID])
		}
		cfg := configByApp[appID]
		for _, want := range []string{
			"app-resource profile custom ; cpu 25 ; memory 8",
			fmt.Sprintf("--hostname=%s", container.Name),
			fmt.Sprintf("--label %s=%s", common.LabelContainerName, container.Name),
			"--env APP_INDEX='",
		} {
			if !strings.Contains(cfg, want) {
				t.Fatalf("container %s app %s config missing %q in:\n%s", container.Name, appID, want, cfg)
			}
		}
		assertNXAPILifecycleCommandAbsent(t, requests, "app-hosting activate appid "+appID)
		assertNXAPILifecycleCommandAbsent(t, requests, "app-hosting start appid "+appID)
	}
	assertNoLifecycleShow(t, requests)
}

func TestGetPodStatusAdvancesMixedStatesWithoutBlocking(t *testing.T) {
	pod := nxosTestPod()
	pod.Spec.Containers = []v1.Container{
		{Name: "running", Image: "bootflash:/hello.tar"},
		{Name: "deployed", Image: "bootflash:/hello.tar"},
		{Name: "activated", Image: "bootflash:/hello.tar"},
	}
	appIDs := common.GenerateContainerAppIDs(pod)
	stateByApp := map[string]string{
		appIDs["running"]:   "RUNNING",
		appIDs["deployed"]:  "DEPLOYED",
		appIDs["activated"]: "ACTIVATED",
	}
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, req)
		input := req.InsAPI.Input
		body := ""
		switch {
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting activate "):
			stateByApp[appIDFromCommand(input)] = "ACTIVATED"
		case req.InsAPI.Type == "cli_conf" && strings.HasPrefix(input, "app-hosting start "):
			stateByApp[appIDFromCommand(input)] = "RUNNING"
		case req.InsAPI.Type == "cli_show_ascii" && strings.HasPrefix(input, "show app-hosting detail "):
			appID := appIDFromCommand(input)
			body = nxosAppDetailBody(appID, stateByApp[appID])
		}
		writeNXAPISuccess(t, w, input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	statusPod, err := driver.GetPodStatus(context.Background(), pod)
	if err != nil {
		t.Fatalf("GetPodStatus: %v", err)
	}
	if statusPod.Status.Phase != v1.PodRunning {
		t.Fatalf("phase=%s, want Running because one container is running", statusPod.Status.Phase)
	}
	ready := map[string]bool{}
	for _, status := range statusPod.Status.ContainerStatuses {
		ready[status.Name] = status.Ready
	}
	if !ready["running"] || ready["deployed"] || ready["activated"] {
		t.Fatalf("ready statuses=%v, want only running ready", ready)
	}
	assertNXAPILifecycleCommand(t, requests, "app-hosting activate appid "+appIDs["deployed"], "cli_conf")
	assertNXAPILifecycleCommand(t, requests, "app-hosting start appid "+appIDs["activated"], "cli_conf")
	assertNXAPILifecycleCommandAbsent(t, requests, "app-hosting activate appid "+appIDs["running"])
	assertNXAPILifecycleCommandAbsent(t, requests, "app-hosting start appid "+appIDs["running"])
}

func TestDeployPodRejectsContainerCountAboveNXOSAppSlotsBeforeConfig(t *testing.T) {
	pod := nxosTestPod()
	pod.Name = "too-many"
	pod.Spec.Containers = nil
	for i := 0; i < defaultNXOSMaxAppSlots+1; i++ {
		pod.Spec.Containers = append(pod.Spec.Containers, v1.Container{
			Name:  fmt.Sprintf("app-%02d", i),
			Image: "bootflash:/hello.tar",
		})
	}
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, req)
		writeNXAPISuccess(t, w, req.InsAPI.Input, "")
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	err := driver.DeployPod(context.Background(), pod, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "app-hosting capacity exceeded") {
		t.Fatalf("DeployPod err=%v, want app-hosting capacity rejection", err)
	}
	if !strings.Contains(err.Error(), "requires 5 new app slots, 4 available") {
		t.Fatalf("DeployPod err=%q, want precise slot accounting", err)
	}
	for _, req := range requests {
		if req.InsAPI.Type == "cli_conf" {
			t.Fatalf("unexpected config command before capacity rejection: %q", req.InsAPI.Input)
		}
	}
}

func TestDeployPodCountsExistingAppsAgainstNXOSAppSlots(t *testing.T) {
	pod := nxosTestPod()
	pod.Name = "four-new"
	pod.Spec.Containers = []v1.Container{
		{Name: "app-a", Image: "bootflash:/hello.tar"},
		{Name: "app-b", Image: "bootflash:/hello.tar"},
		{Name: "app-c", Image: "bootflash:/hello.tar"},
		{Name: "app-d", Image: "bootflash:/hello.tar"},
	}
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, req)
		body := ""
		if req.InsAPI.Type == "cli_show_ascii" && req.InsAPI.Input == "show app-hosting list" {
			body = "existing RUNNING\n"
		}
		writeNXAPISuccess(t, w, req.InsAPI.Input, body)
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	err := driver.DeployPod(context.Background(), pod, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "requires 4 new app slots, 3 available") {
		t.Fatalf("DeployPod err=%v, want existing app to consume one slot", err)
	}
	for _, req := range requests {
		if req.InsAPI.Type == "cli_conf" {
			t.Fatalf("unexpected config command before capacity rejection: %q", req.InsAPI.Input)
		}
	}
}

func TestUpdatePodRejectsContainerCountAboveNXOSAppSlotsBeforeConfig(t *testing.T) {
	pod := nxosTestPod()
	pod.Name = "update-too-many"
	pod.Spec.Containers = nil
	for i := 0; i < defaultNXOSMaxAppSlots+1; i++ {
		pod.Spec.Containers = append(pod.Spec.Containers, v1.Container{
			Name:  fmt.Sprintf("app-%02d", i),
			Image: "bootflash:/hello.tar",
		})
	}
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, req)
		writeNXAPISuccess(t, w, req.InsAPI.Input, "")
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	err := driver.UpdatePod(context.Background(), pod)
	if err == nil || !strings.Contains(err.Error(), "app-hosting capacity exceeded") {
		t.Fatalf("UpdatePod err=%v, want app-hosting capacity rejection", err)
	}
	for _, req := range requests {
		if req.InsAPI.Type == "cli_conf" {
			t.Fatalf("unexpected config command before capacity rejection: %q", req.InsAPI.Input)
		}
	}
}

func TestDeployPodRejectsPullNeverHTTPBeforeConfig(t *testing.T) {
	pod := nxosTestPod()
	pod.Spec.Containers[0].Image = "https://registry.example.com/hello.tar"
	pod.Spec.Containers[0].ImagePullPolicy = v1.PullNever
	var requests []nxapiRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req nxapiRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, req)
		writeNXAPISuccess(t, w, req.InsAPI.Input, "")
	}))
	defer server.Close()

	driver := nxosTestDriver(server)
	err := driver.DeployPod(context.Background(), pod, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "imagePullPolicy is Never") {
		t.Fatalf("DeployPod err=%v, want PullNever HTTP rejection", err)
	}
	for _, req := range requests {
		if req.InsAPI.Type == "cli_conf" {
			t.Fatalf("unexpected config command before PullNever rejection: %q", req.InsAPI.Input)
		}
	}
}

func TestConvertPodRejectsEphemeralStorageReservation(t *testing.T) {
	pod := nxosTestPod()
	pod.Spec.Containers[0].Resources = v1.ResourceRequirements{
		Requests: v1.ResourceList{
			v1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
		},
	}
	driver := &NXOSDriver{config: &v1alpha1.DeviceSpec{}}
	_, err := driver.convertPodToAppConfigs(pod)
	if err == nil || !strings.Contains(err.Error(), "ephemeral-storage reservation is not supported") {
		t.Fatalf("convertPodToAppConfigs err=%v, want unsupported storage rejection", err)
	}
}

func TestConvertPodRejectsMultiVCPUReservation(t *testing.T) {
	pod := nxosTestPod()
	pod.Spec.Containers[0].Resources = v1.ResourceRequirements{
		Limits: v1.ResourceList{
			v1.ResourceCPU: resource.MustParse("1500m"),
		},
	}
	driver := &NXOSDriver{config: &v1alpha1.DeviceSpec{}}
	_, err := driver.convertPodToAppConfigs(pod)
	if err == nil || !strings.Contains(err.Error(), "does not support vcpu reservation") {
		t.Fatalf("convertPodToAppConfigs err=%v, want unsupported vcpu rejection", err)
	}
}

func TestConvertPodRejectsOversizedNXOSResourceReservations(t *testing.T) {
	tests := []struct {
		name       string
		config     v1alpha1.ResourceConfig
		resources  v1.ResourceRequirements
		wantErrSub string
	}{
		{
			name: "cpu request overflow",
			resources: v1.ResourceRequirements{Requests: v1.ResourceList{
				v1.ResourceCPU: resource.MustParse("70000m"),
			}},
			wantErrSub: "cpu request milli-units",
		},
		{
			name: "memory request overflow",
			resources: v1.ResourceRequirements{Requests: v1.ResourceList{
				v1.ResourceMemory: resource.MustParse("70000Mi"),
			}},
			wantErrSub: "memory request MiB",
		},
		{
			name: "cpu limit rounded vcpu overflow",
			resources: v1.ResourceRequirements{Limits: v1.ResourceList{
				v1.ResourceCPU: resource.MustParse("70000"),
			}},
			wantErrSub: "cpu limit rounded vcpu",
		},
		{
			name:       "default cpu overflow",
			config:     v1alpha1.ResourceConfig{DefaultCPU: "70000m"},
			wantErrSub: "defaultCPU",
		},
		{
			name:       "default memory overflow",
			config:     v1alpha1.ResourceConfig{DefaultMemory: "70000Mi"},
			wantErrSub: "defaultMemory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := nxosTestPod()
			pod.Spec.Containers[0].Resources = tt.resources
			driver := &NXOSDriver{config: &v1alpha1.DeviceSpec{ResourceLimits: tt.config}}
			_, err := driver.convertPodToAppConfigs(pod)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) || !strings.Contains(err.Error(), "exceeds NX-OS app-hosting maximum") {
				t.Fatalf("convertPodToAppConfigs err=%v, want %q overflow", err, tt.wantErrSub)
			}
		})
	}
}

func TestNXOSResourceConversionRoundsSubMiBMemoryAndAllowsNilDeviceSpec(t *testing.T) {
	driver := &NXOSDriver{}
	container := v1.Container{
		Name: "app",
		Resources: v1.ResourceRequirements{
			Requests: v1.ResourceList{
				v1.ResourceMemory: resource.MustParse("512Ki"),
			},
		},
	}

	cfg, err := driver.getResourceConfig(&container)
	if err != nil {
		t.Fatalf("getResourceConfig: %v", err)
	}
	if cfg.memoryMB != 1 {
		t.Fatalf("memoryMB=%d, want sub-MiB request rounded up to 1MiB", cfg.memoryMB)
	}
}

func TestParseAppHostingResources(t *testing.T) {
	res := parseAppHostingResources(`
Resource       Total  Used  Available
CPU-percent    7400   200  7200
VCPU-count        1     0     1
Memory (MB)    3840    64  3776
Disk (MB)      4257  2551  1706
`)
	if res.CPUTotalMilli != 7400 || res.CPUAvailableMilli != 7200 {
		t.Fatalf("cpu resources=%+v", res)
	}
	if res.MemoryTotalMB != 3840 || res.MemoryAvailableMB != 3776 {
		t.Fatalf("memory resources=%+v", res)
	}
	if res.StorageTotalMB != 4257 || res.StorageAvailableMB != 1706 {
		t.Fatalf("storage resources=%+v", res)
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
		config:     &v1alpha1.DeviceSpec{Address: "192.0.2.10"},
		appActions: make(map[string]nxosAppAction),
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

func assertNXAPILifecycleCommandAbsent(t *testing.T, requests []nxapiRequest, prefix string) {
	t.Helper()
	for _, req := range requests {
		if strings.HasPrefix(req.InsAPI.Input, prefix) {
			t.Fatalf("unexpected lifecycle command prefix %q in %#v", prefix, requests)
		}
	}
}

func assertNXAPICommandContains(t *testing.T, requests []nxapiRequest, want string) {
	t.Helper()
	for _, req := range requests {
		if strings.Contains(req.InsAPI.Input, want) {
			return
		}
	}
	t.Fatalf("did not see command containing %q in %#v", want, requests)
}

func optsAt(opts []string, i int) string {
	if i < 0 || i >= len(opts) {
		return ""
	}
	return opts[i]
}

func assertNoLifecycleShow(t *testing.T, requests []nxapiRequest) {
	t.Helper()
	for _, req := range requests {
		if req.InsAPI.Type == "cli_show_ascii" && isAppLifecycleCommand(req.InsAPI.Input) {
			t.Fatalf("state-changing app-hosting command used cli_show_ascii: %q", req.InsAPI.Input)
		}
	}
}

func appIDFromCommand(input string) string {
	fields := strings.Fields(input)
	for i, field := range fields {
		if field == "appid" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	if len(fields) > 3 && fields[0] == "show" && fields[1] == "app-hosting" && fields[2] == "detail" {
		return fields[len(fields)-1]
	}
	return ""
}

func nxosEnvListers(t *testing.T) (corev1listers.SecretNamespaceLister, corev1listers.ConfigMapNamespaceLister) {
	t.Helper()
	secretIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if err := secretIndexer.Add(&v1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "app-secret", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("s3cr3t token")},
	}); err != nil {
		t.Fatalf("add secret: %v", err)
	}
	configIndexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	if err := configIndexer.Add(&v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "default"},
		Data:       map[string]string{"mode": "debug"},
	}); err != nil {
		t.Fatalf("add configmap: %v", err)
	}
	return corev1listers.NewSecretLister(secretIndexer).Secrets("default"),
		corev1listers.NewConfigMapLister(configIndexer).ConfigMaps("default")
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
