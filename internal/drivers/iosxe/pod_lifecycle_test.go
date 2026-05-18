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
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
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
