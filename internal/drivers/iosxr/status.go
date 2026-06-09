// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package iosxr

import (
	"context"
	"fmt"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type iosxrApp struct {
	ID            string
	Type          string
	ConfigState   string
	Status        string
	Source        string
	Image         string
	ContainerID   string
	ContainerName string
	Network       string
	RunOpts       []string
}

func (d *IOSXRDriver) sourceTable(ctx context.Context) (map[string]bool, error) {
	out, err := d.client.Run(ctx, "show appmgr source-table")
	if err != nil {
		return nil, err
	}
	return parseSourceTable(out), nil
}

func (d *IOSXRDriver) listApps(ctx context.Context) ([]iosxrApp, error) {
	out, err := d.client.Run(ctx, "show appmgr application-table")
	if err != nil {
		return nil, err
	}
	return parseApplicationTable(out), nil
}

func (d *IOSXRDriver) appState(ctx context.Context, appID string) (string, error) {
	app, err := d.appDetail(ctx, appID)
	if err != nil {
		return "", err
	}
	return app.State(), nil
}

func (d *IOSXRDriver) appDetail(ctx context.Context, appID string) (iosxrApp, error) {
	out, err := d.client.Run(ctx, fmt.Sprintf("show appmgr application name %s info detail", appID))
	if err != nil {
		return iosxrApp{}, err
	}
	if strings.Contains(out, "Application not found") {
		return iosxrApp{}, nil
	}
	app := parseApplicationDetail(out)
	if app.ID == "" {
		app.ID = appID
	}
	return app, nil
}

func (a iosxrApp) State() string {
	switch {
	case strings.EqualFold(a.ConfigState, "Error"):
		return "ERROR"
	case strings.HasPrefix(strings.ToLower(a.Status), "up"):
		return "RUNNING"
	case strings.EqualFold(a.ConfigState, "Activated"):
		return "ACTIVATED"
	case strings.EqualFold(a.ConfigState, "Deactivated"):
		return "DEPLOYED"
	case a.ConfigState != "":
		return strings.ToUpper(a.ConfigState)
	default:
		return ""
	}
}

func setPodStatus(hostIP string, pod *v1.Pod, observed map[string]iosxrApp) {
	now := metav1.Now()
	pod.Status = v1.PodStatus{
		Phase:     v1.PodPending,
		HostIP:    hostIP,
		StartTime: &now,
		Conditions: []v1.PodCondition{
			{Type: v1.PodInitialized, Status: v1.ConditionFalse, LastTransitionTime: now},
			{Type: v1.PodReady, Status: v1.ConditionFalse, LastTransitionTime: now},
			{Type: v1.PodScheduled, Status: v1.ConditionTrue, LastTransitionTime: now},
		},
	}
	allReady := true
	anyRunning := false
	for _, container := range pod.Spec.Containers {
		app := observed[container.Name]
		status := v1.ContainerStatus{
			Name:        container.Name,
			Image:       container.Image,
			ImageID:     app.Image,
			ContainerID: fmt.Sprintf("cisco-iosxr://%s", app.ID),
			Ready:       false,
		}
		switch app.State() {
		case "RUNNING":
			status.Ready = true
			status.State.Running = &v1.ContainerStateRunning{StartedAt: now}
			anyRunning = true
		case "ACTIVATED", "DEPLOYED":
			status.State.Waiting = &v1.ContainerStateWaiting{Reason: "ContainerCreating", Message: "App state: " + app.State()}
			allReady = false
		case "ERROR":
			status.State.Waiting = &v1.ContainerStateWaiting{Reason: "AppMgrError", Message: app.Status}
			allReady = false
		default:
			status.State.Waiting = &v1.ContainerStateWaiting{Reason: "ContainerCreating", Message: "Waiting for IOS XR appmgr state"}
			allReady = false
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, status)
	}
	pod.Status.PodIP = hostIP
	if pod.Status.PodIP == "" {
		pod.Status.PodIP = "0.0.0.0"
	}
	if anyRunning {
		pod.Status.Phase = v1.PodRunning
	}
	if anyRunning && allReady {
		for i := range pod.Status.Conditions {
			if pod.Status.Conditions[i].Type == v1.PodInitialized || pod.Status.Conditions[i].Type == v1.PodReady {
				pod.Status.Conditions[i].Status = v1.ConditionTrue
			}
		}
	}
}

func hostedAppFromXR(app iosxrApp) common.HostedApp {
	ns, name, uid, container := common.PodIdentityFromRunOpts(app.RunOpts)
	return common.HostedApp{
		AppID:         app.ID,
		State:         app.State(),
		PodName:       name,
		PodNamespace:  ns,
		PodUID:        uid,
		ContainerName: firstNonEmpty(container, app.ContainerName),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
