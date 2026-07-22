// Copyright © 2026 Cisco Systems Inc.
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

package nxos

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type nxosApp struct {
	ID            string
	State         string
	Image         string
	IPv4          string
	MAC           string
	RunOpts       []string
	ContainerName string
}

func (d *NXOSDriver) listApps(ctx context.Context) ([]nxosApp, error) {
	out, err := d.client.show(ctx, "show app-hosting list")
	if err != nil {
		return nil, err
	}
	return parseAppList(out), nil
}

func (d *NXOSDriver) appState(ctx context.Context, appID string) (string, error) {
	app, err := d.appDetail(ctx, appID)
	if err != nil {
		return "", err
	}
	return app.State, nil
}

func (d *NXOSDriver) appDetail(ctx context.Context, appID string) (nxosApp, error) {
	if err := validateNXOSAppID(appID); err != nil {
		return nxosApp{}, err
	}
	out, err := d.client.show(ctx, fmt.Sprintf("show app-hosting detail appid %s", appID))
	if err != nil {
		return nxosApp{}, err
	}
	app := parseAppDetail(out)
	if app.ID == "" {
		app.ID = appID
	} else if validateNXOSAppID(app.ID) != nil || app.ID != appID {
		return nxosApp{}, fmt.Errorf("NX-OS app detail returned an invalid or mismatched CVK app ID")
	}
	return app, nil
}

func validateNXOSAppID(appID string) error {
	if _, _, ok := common.ParseCVKAppName(appID); !ok {
		return fmt.Errorf("invalid NX-OS CVK app ID")
	}
	return nil
}

func parseAppList(out string) []nxosApp {
	var apps []nxosApp
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "app") && strings.Contains(lower, "state") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		state := strings.ToUpper(fields[len(fields)-1])
		if !looksLikeAppState(state) {
			continue
		}
		apps = append(apps, nxosApp{
			ID:    fields[0],
			State: state,
		})
	}
	return apps
}

func parseAppDetail(out string) nxosApp {
	app := nxosApp{}
	stateRe := regexp.MustCompile(`(?i)^\s*state\s*:\s*([A-Za-z0-9_-]+)`)
	appRe := regexp.MustCompile(`(?i)^\s*(?:app\s*id|appid)\s*:\s*([^\s]+)`)
	imageRe := regexp.MustCompile(`(?i)^\s*(?:package|image)\s*:\s*(.+)$`)
	ipRe := regexp.MustCompile(`(?i)(?:ipv4 address|ip address|guest-ipaddress)\s*:\s*([0-9.]+)`)
	macRe := regexp.MustCompile(`(?i)(?:mac address|mac)\s*:\s*([0-9a-f:.:-]+)`)
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if m := appRe.FindStringSubmatch(line); len(m) > 1 {
			app.ID = strings.TrimSpace(m[1])
		}
		if m := stateRe.FindStringSubmatch(line); len(m) > 1 {
			app.State = strings.ToUpper(strings.TrimSpace(m[1]))
		}
		if m := imageRe.FindStringSubmatch(line); len(m) > 1 && app.Image == "" {
			app.Image = strings.TrimSpace(m[1])
		}
		if m := ipRe.FindStringSubmatch(line); len(m) > 1 && app.IPv4 == "" {
			app.IPv4 = strings.TrimSpace(m[1])
		}
		if m := macRe.FindStringSubmatch(line); len(m) > 1 && app.MAC == "" {
			app.MAC = strings.TrimSpace(m[1])
		}
		if strings.Contains(trimmed, "--label ") || strings.Contains(trimmed, common.LabelPodUID) {
			app.RunOpts = append(app.RunOpts, trimmed)
		}
	}
	_, _, _, container := common.PodIdentityFromRunOpts(app.RunOpts)
	app.ContainerName = container
	return app
}

func setPodStatus(hostIP string, pod *v1.Pod, observed map[string]nxosApp) {
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
			ImageID:     container.Image,
			ContainerID: fmt.Sprintf("cisco-nxos://%s", app.ID),
			Ready:       false,
		}
		switch app.State {
		case "RUNNING":
			status.Ready = true
			status.State.Running = &v1.ContainerStateRunning{StartedAt: now}
			anyRunning = true
			if pod.Status.PodIP == "" {
				pod.Status.PodIP = app.IPv4
			}
		case "DEPLOYED", "ACTIVATED", "INSTALLING":
			status.State.Waiting = &v1.ContainerStateWaiting{Reason: "ContainerCreating", Message: "App state: " + app.State}
			allReady = false
		case "STOPPED", "UNINSTALLED":
			status.State.Terminated = &v1.ContainerStateTerminated{ExitCode: 0, Reason: "Completed", FinishedAt: now}
			allReady = false
		default:
			status.State.Waiting = &v1.ContainerStateWaiting{Reason: "ContainerCreating", Message: "Waiting for NX-OS app-hosting state"}
			allReady = false
		}
		pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, status)
	}
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

func looksLikeAppState(state string) bool {
	switch strings.ToUpper(state) {
	case "DEPLOYED", "ACTIVATED", "RUNNING", "STOPPED", "INSTALLING", "UNINSTALLED":
		return true
	default:
		return false
	}
}
