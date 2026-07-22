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

package iosxe

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
	siruplogrus "github.com/sirupsen/logrus"
	vklog "github.com/virtual-kubelet/virtual-kubelet/log"
	vklogrus "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func lifecycleTestPod() *v1.Pod {
	return &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "multi-container",
			UID:       types.UID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"),
		},
		Spec: v1.PodSpec{
			Containers: []v1.Container{
				{Name: "alpha", Image: "flash:alpha.tar"},
				{Name: "beta", Image: "flash:beta.tar"},
			},
		},
	}
}

func TestPodDeletionTargetsIncludesExpectedAppsWhenDiscoveryIsEmpty(t *testing.T) {
	pod := lifecycleTestPod()

	got := podDeletionTargets(testCtx(), pod, map[string]string{})
	want := common.GenerateContainerAppIDs(pod)

	for containerName, appID := range want {
		if got[containerName] != appID {
			t.Fatalf("target for container %s = %q, want %q", containerName, got[containerName], appID)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got %d deletion targets, want %d: %#v", len(got), len(want), got)
	}
}

func TestPodDeletionTargetsDedupesSyntheticDiscoveredApps(t *testing.T) {
	pod := lifecycleTestPod()
	expected := common.GenerateContainerAppIDs(pod)

	got := podDeletionTargets(testCtx(), pod, map[string]string{
		"container-0": expected["alpha"],
	})

	if got["container-0"] != expected["alpha"] {
		t.Fatalf("synthetic discovered app was not preserved: %#v", got)
	}
	if _, duplicated := got["alpha"]; duplicated {
		t.Fatalf("expected app %s should not be duplicated under alpha when already discovered synthetically: %#v", expected["alpha"], got)
	}
	if got["beta"] != expected["beta"] {
		t.Fatalf("target for missing beta = %q, want %q", got["beta"], expected["beta"])
	}
	if len(got) != 2 {
		t.Fatalf("got %d deletion targets, want 2: %#v", len(got), got)
	}
}

func TestUpdatePodDeletingCleansExpectedAppsInsteadOfRedeploying(t *testing.T) {
	pod := lifecycleTestPod()
	now := metav1.Now()
	pod.DeletionTimestamp = &now

	var deletePaths []string
	fc := &fakeNetworkClient{
		getHook: func(path string, result any) error {
			switch result.(type) {
			case *Cisco_IOS_XEAppHostingCfg_AppHostingCfgData:
			case *Cisco_IOS_XEAppHostingOper_AppHostingOperData:
			default:
				t.Fatalf("unexpected GET result type %T for path %s", result, path)
			}
			return nil
		},
		deleteHook: func(path string) error {
			deletePaths = append(deletePaths, path)
			return nil
		},
	}

	if err := newTestDriver(fc).UpdatePod(testCtx(), pod); err != nil {
		t.Fatalf("UpdatePod deleting pod: %v", err)
	}

	for _, appID := range common.GenerateContainerAppIDs(pod) {
		found := false
		for _, path := range deletePaths {
			if strings.Contains(path, appID) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected config delete for app %s, got paths %#v", appID, deletePaths)
		}
	}
}

func TestPodLifecycleDebugLogsOmitPodSecrets(t *testing.T) {
	const (
		imageUser   = "pod-image-user-sentinel"
		imagePass   = "pod-image-password-sentinel"
		imageToken  = "pod-image-query-sentinel"
		envSecret   = "pod-environment-secret-sentinel"
		destination = "destination-injection-sentinel"
	)
	pod := lifecycleTestPod()
	pod.Spec.Containers[0].Image = "https://" + imageUser + ":" + imagePass + "@registry.example.com/app.tar?token=" + imageToken
	pod.Spec.Containers[0].Env = []v1.EnvVar{{Name: "TOKEN", Value: envSecret}}
	pod.Annotations = map[string]string{annotationPackageDest: "flash:/app.tar\r" + destination}

	driver := &XEDriver{
		config: &v1alpha1.DeviceSpec{XE: &v1alpha1.XEConfig{}},
		client: &fakeNetworkClient{getHook: func(_ string, _ any) error {
			return nil
		}},
		recoveringPods: make(map[string]bool),
	}
	var logs bytes.Buffer
	backend := siruplogrus.New()
	backend.SetOutput(&logs)
	backend.SetLevel(siruplogrus.DebugLevel)
	ctx := vklog.WithLogger(context.Background(), vklogrus.FromLogrus(siruplogrus.NewEntry(backend)))

	if err := driver.DeployPod(ctx, pod, nil, nil); err == nil {
		t.Fatal("DeployPod succeeded with an invalid package destination")
	}
	if err := driver.DeletePod(ctx, pod); err != nil {
		t.Fatalf("DeletePod: %v", err)
	}
	for _, secret := range []string{imageUser, imagePass, imageToken, envSecret, destination} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("pod lifecycle logs exposed %q: %s", secret, logs.String())
		}
	}
}

func TestGetPodContainersWarningOmitsRunOptions(t *testing.T) {
	const sentinel = "RUN_OPTIONS_SECRET_SENTINEL_DO_NOT_EXPOSE"
	pod := lifecycleTestPod()
	appID := common.GenerateContainerAppIDs(pod)["alpha"]
	lineIndex := uint16(1)
	runOpts := "--label " + common.LabelPodNamespace + "=" + pod.Namespace +
		" --label " + common.LabelPodName + "=" + pod.Name +
		" --label " + common.LabelPodUID + "=" + string(pod.UID) +
		" --env TOKEN='" + sentinel + "'"
	driver := &XEDriver{client: &fakeNetworkClient{getHook: func(_ string, result any) error {
		root, ok := result.(*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData)
		if !ok {
			t.Fatalf("unexpected GET result type %T", result)
		}
		root.Apps = &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps{App: map[string]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App{
			appID: {
				ApplicationName: &appID,
				RunOptss: &Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss{RunOpts: map[uint16]*Cisco_IOS_XEAppHostingCfg_AppHostingCfgData_Apps_App_RunOptss_RunOpts{
					lineIndex: {LineIndex: &lineIndex, LineRunOpts: &runOpts},
				}},
			},
		}}
		return nil
	}}}

	var logs bytes.Buffer
	backend := siruplogrus.New()
	backend.SetOutput(&logs)
	backend.SetLevel(siruplogrus.DebugLevel)
	ctx := vklog.WithLogger(context.Background(), vklogrus.FromLogrus(siruplogrus.NewEntry(backend)))
	_, _ = driver.GetPodContainers(ctx, pod)
	if strings.Contains(logs.String(), sentinel) || strings.Contains(logs.String(), "--env") {
		t.Fatalf("container-discovery warning exposed run options: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "no container name label across 1 RunOpts lines") {
		t.Fatalf("expected sanitized missing-label warning, got: %s", logs.String())
	}
}
