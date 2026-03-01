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
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// helper to build oper data with a given state string.
func operDataWithState(state string) *Cisco_IOS_XEAppHostingOper_AppHostingOperData_App {
	s := state
	return &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		Details: &Cisco_IOS_XEAppHostingOper_AppHostingOperData_App_Details{
			State: &s,
		},
	}
}

// helper to build a minimal pod with N containers.
func statusTestPod(containers ...string) *v1.Pod {
	pod := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
	}
	for _, name := range containers {
		pod.Spec.Containers = append(pod.Spec.Containers, v1.Container{
			Name:  name,
			Image: name + ":latest",
		})
	}
	return pod
}

// ─────────────────────────────────────────────────────────────────────────────
// GetContainerStatus — single container, all possible states
// ─────────────────────────────────────────────────────────────────────────────

func TestGetContainerStatus_Running(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("app")
	containers := map[string]string{"app": "app-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"app-id": operDataWithState("RUNNING"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pod.Status.Phase != v1.PodRunning {
		t.Errorf("Phase = %v, want PodRunning", pod.Status.Phase)
	}
	if len(pod.Status.ContainerStatuses) != 1 {
		t.Fatalf("expected 1 container status, got %d", len(pod.Status.ContainerStatuses))
	}
	cs := pod.Status.ContainerStatuses[0]
	if !cs.Ready {
		t.Error("container should be Ready")
	}
	if cs.State.Running == nil {
		t.Error("container state should be Running")
	}
	if cs.ContainerID != "cisco://app-id" {
		t.Errorf("ContainerID = %q, want cisco://app-id", cs.ContainerID)
	}

	// PodReady and PodInitialized conditions should be True
	for _, cond := range pod.Status.Conditions {
		if cond.Type == v1.PodReady || cond.Type == v1.PodInitialized {
			if cond.Status != v1.ConditionTrue {
				t.Errorf("condition %s = %v, want True", cond.Type, cond.Status)
			}
		}
	}
}

func TestGetContainerStatus_Deployed(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("app")
	containers := map[string]string{"app": "app-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"app-id": operDataWithState("DEPLOYED"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pod.Status.Phase != v1.PodPending {
		t.Errorf("Phase = %v, want PodPending", pod.Status.Phase)
	}
	cs := pod.Status.ContainerStatuses[0]
	if cs.Ready {
		t.Error("container should not be Ready when DEPLOYED")
	}
	if cs.State.Waiting == nil {
		t.Fatal("container state should be Waiting")
	}
	if cs.State.Waiting.Reason != "ContainerCreating" {
		t.Errorf("Reason = %q, want ContainerCreating", cs.State.Waiting.Reason)
	}
}

func TestGetContainerStatus_Activated(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("app")
	containers := map[string]string{"app": "app-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"app-id": operDataWithState("ACTIVATED"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cs := pod.Status.ContainerStatuses[0]
	if cs.Ready {
		t.Error("container should not be Ready when ACTIVATED")
	}
	if cs.State.Waiting == nil || cs.State.Waiting.Reason != "ContainerCreating" {
		t.Error("expected Waiting/ContainerCreating for ACTIVATED state")
	}
}

func TestGetContainerStatus_Stopped(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("app")
	containers := map[string]string{"app": "app-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"app-id": operDataWithState("STOPPED"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cs := pod.Status.ContainerStatuses[0]
	if cs.Ready {
		t.Error("container should not be Ready when STOPPED")
	}
	if cs.State.Terminated == nil {
		t.Fatal("container state should be Terminated")
	}
	if cs.State.Terminated.Reason != "Completed" {
		t.Errorf("Reason = %q, want Completed", cs.State.Terminated.Reason)
	}
}

func TestGetContainerStatus_UnknownState(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("app")
	containers := map[string]string{"app": "app-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"app-id": operDataWithState("INSTALLING"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cs := pod.Status.ContainerStatuses[0]
	if cs.State.Waiting == nil || cs.State.Waiting.Reason != "Unknown" {
		t.Error("expected Waiting/Unknown for unrecognised state")
	}
}

func TestGetContainerStatus_NilOperData(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("app")
	containers := map[string]string{"app": "app-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{} // no entry

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pod.Status.Phase != v1.PodPending {
		t.Errorf("Phase = %v, want PodPending", pod.Status.Phase)
	}
	cs := pod.Status.ContainerStatuses[0]
	if cs.State.Waiting == nil || cs.State.Waiting.Reason != "ContainerCreating" {
		t.Error("expected Waiting/ContainerCreating when no oper data")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GetContainerStatus — multi-container pods
// ─────────────────────────────────────────────────────────────────────────────

func TestGetContainerStatus_AllRunning(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("web", "sidecar")
	containers := map[string]string{"web": "web-id", "sidecar": "sidecar-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"web-id":     operDataWithState("RUNNING"),
		"sidecar-id": operDataWithState("RUNNING"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pod.Status.Phase != v1.PodRunning {
		t.Errorf("Phase = %v, want PodRunning", pod.Status.Phase)
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if !cs.Ready {
			t.Errorf("container %s should be Ready", cs.Name)
		}
	}
}

func TestGetContainerStatus_MixedStates(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("web", "sidecar")
	containers := map[string]string{"web": "web-id", "sidecar": "sidecar-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"web-id":     operDataWithState("RUNNING"),
		"sidecar-id": operDataWithState("DEPLOYED"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// At least one running, but not all ready → PodRunning but conditions not True
	if pod.Status.Phase != v1.PodRunning {
		t.Errorf("Phase = %v, want PodRunning (partial)", pod.Status.Phase)
	}

	// PodReady should still be False since not all containers are ready
	for _, cond := range pod.Status.Conditions {
		if cond.Type == v1.PodReady && cond.Status == v1.ConditionTrue {
			t.Error("PodReady should be False when not all containers are running")
		}
	}
}

func TestGetContainerStatus_HostIP(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "192.168.1.50"}}
	pod := statusTestPod("app")
	containers := map[string]string{"app": "app-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"app-id": operDataWithState("RUNNING"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pod.Status.HostIP != "192.168.1.50" {
		t.Errorf("HostIP = %q, want 192.168.1.50", pod.Status.HostIP)
	}
}

func TestGetContainerStatus_StartTimeSet(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("app")
	containers := map[string]string{"app": "app-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"app-id": operDataWithState("RUNNING"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if pod.Status.StartTime == nil {
		t.Error("StartTime should be set")
	}
}

func TestGetContainerStatus_ScheduledConditionTrue(t *testing.T) {
	d := &XEDriver{config: &v1alpha1.DeviceSpec{Address: "10.0.0.1"}}
	pod := statusTestPod("app")
	containers := map[string]string{"app": "app-id"}
	operData := map[string]*Cisco_IOS_XEAppHostingOper_AppHostingOperData_App{
		"app-id": operDataWithState("DEPLOYED"),
	}

	if err := d.GetContainerStatus(testCtx(), pod, containers, operData); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, cond := range pod.Status.Conditions {
		if cond.Type == v1.PodScheduled && cond.Status != v1.ConditionTrue {
			t.Errorf("PodScheduled = %v, want True", cond.Status)
		}
	}
}
