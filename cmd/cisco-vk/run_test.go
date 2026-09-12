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

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientfake "k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func clearWorkerIdentityEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		envDeviceNamespace,
		envDeviceName,
		envDeviceUID,
		envNodeName,
		envManagedTopology,
		envWorkerRevision,
		legacyEnvNodeName,
		"POD_NAMESPACE",
	} {
		t.Setenv(name, "")
	}
}

func TestManagedRolloutControllerEnabledRequiresEverySafetyGate(t *testing.T) {
	tests := []struct {
		name            string
		managedTopology bool
		softwareUpgrade string
		gnoiDisabled    string
		want            bool
	}{
		{name: "all gates enabled", managedTopology: true, softwareUpgrade: "true", want: true},
		{name: "topology disabled", softwareUpgrade: "true"},
		{name: "software upgrade disabled", managedTopology: true},
		{name: "global gNOI kill switch", managedTopology: true, softwareUpgrade: "true", gnoiDisabled: "true"},
		{name: "explicit false values", managedTopology: true, softwareUpgrade: "false", gnoiDisabled: "false"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envEnableIOSXESoftwareUpgrade, tc.softwareUpgrade)
			t.Setenv(gNOIDisabledEnv, tc.gnoiDisabled)
			if got := managedRolloutControllerEnabled(tc.managedTopology); got != tc.want {
				t.Fatalf("managedRolloutControllerEnabled() = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestValidateManagedTopologyManagerOptions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		managed       bool
		leader        bool
		policyNS      string
		policyName    string
		wantErrorText string
	}{
		{name: "disabled remains compatible"},
		{name: "complete managed options", managed: true, leader: true, policyNS: "system", policyName: "topology-policy"},
		{name: "leader election required", managed: true, policyNS: "system", policyName: "topology-policy", wantErrorText: "--leader-elect"},
		{name: "policy identity required", managed: true, leader: true, policyNS: "system", wantErrorText: "--topology-policy-namespace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateManagedTopologyManagerOptions(tc.managed, tc.leader, tc.policyNS, tc.policyName)
			if tc.wantErrorText == "" {
				if err != nil {
					t.Fatalf("validateManagedTopologyManagerOptions() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErrorText) {
				t.Fatalf("validateManagedTopologyManagerOptions() error = %v, want %q", err, tc.wantErrorText)
			}
		})
	}
}

func TestVerifyManagedWorkerAdmissionRequiresPositiveAndNegativeDryRuns(t *testing.T) {
	const nodeName = "managed-edge-01"
	newClient := func(positiveErr, negativeErr error) *clientfake.Clientset {
		clientset := clientfake.NewSimpleClientset()
		clientset.PrependReactor("patch", "nodes", func(action clienttesting.Action) (bool, runtime.Object, error) {
			patchAction := action.(clienttesting.PatchAction)
			if action.GetSubresource() != "status" {
				t.Fatalf("probe subresource = %q, want status", action.GetSubresource())
			}
			if bytes.Contains(patchAction.GetPatch(), []byte("admission-probe")) {
				return true, nil, negativeErr
			}
			for _, annotation := range []string{
				managedprotocol.VirtualKubeletLastAppliedObjectMeta,
				managedprotocol.VirtualKubeletLastAppliedNodeStatus,
			} {
				if !bytes.Contains(patchAction.GetPatch(), []byte(annotation)) {
					t.Fatalf("positive status probe did not exercise %s: %s", annotation, patchAction.GetPatch())
				}
			}
			return true, &v1.Node{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}, positiveErr
		})
		return clientset
	}
	denied := apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, nodeName, errors.New("managed Node metadata is manager-owned"))

	t.Run("enforced", func(t *testing.T) {
		clientset := newClient(nil, denied)
		if err := verifyManagedWorkerAdmission(context.Background(), clientset, nodeName); err != nil {
			t.Fatalf("verifyManagedWorkerAdmission() error = %v", err)
		}
		if got := len(clientset.Actions()); got != 2 {
			t.Fatalf("API actions = %d, want positive and negative dry runs", got)
		}
	})

	t.Run("inactive policy", func(t *testing.T) {
		err := verifyManagedWorkerAdmission(context.Background(), newClient(nil, nil), nodeName)
		if err == nil || !strings.Contains(err.Error(), "was accepted") {
			t.Fatalf("error = %v, want unsafe acceptance failure", err)
		}
	})

	t.Run("blanket denial", func(t *testing.T) {
		err := verifyManagedWorkerAdmission(context.Background(), newClient(denied, denied), nodeName)
		if err == nil || !strings.Contains(err.Error(), "legitimate Node status") {
			t.Fatalf("error = %v, want positive-probe failure", err)
		}
	})

	t.Run("unexpected negative failure", func(t *testing.T) {
		conflict := apierrors.NewConflict(schema.GroupResource{Resource: "nodes"}, nodeName, errors.New("conflict"))
		err := verifyManagedWorkerAdmission(context.Background(), newClient(nil, conflict), nodeName)
		if err == nil || !strings.Contains(err.Error(), "unexpected reason") {
			t.Fatalf("error = %v, want unexpected-failure classification", err)
		}
	})
}

func TestResolveWorkerRuntimeIdentityStandaloneCompatibility(t *testing.T) {
	t.Run("legacy node identity remains the device identity", func(t *testing.T) {
		clearWorkerIdentityEnv(t)
		t.Setenv("POD_NAMESPACE", "edge")
		t.Setenv(legacyEnvNodeName, "legacy-node")

		got, err := resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{Address: "192.0.2.10"})
		if err != nil {
			t.Fatalf("resolveWorkerRuntimeIdentity() error = %v", err)
		}
		want := workerRuntimeIdentity{
			DeviceNamespace: "edge",
			DeviceName:      "legacy-node",
			NodeName:        "legacy-node",
		}
		if got != want {
			t.Fatalf("identity = %#v, want %#v", got, want)
		}
	})

	t.Run("device and node identity can be explicit and distinct", func(t *testing.T) {
		clearWorkerIdentityEnv(t)
		t.Setenv(envManagedTopology, "false")
		t.Setenv("POD_NAMESPACE", "edge")
		t.Setenv(envDeviceNamespace, "edge")
		t.Setenv(envDeviceName, "switch-01")
		t.Setenv(envDeviceUID, "22c81400-85ea-4ca8-91ee-07a7c7bd531c")
		t.Setenv(envNodeName, "cvk-edge-node-01")

		got, err := resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{})
		if err != nil {
			t.Fatalf("resolveWorkerRuntimeIdentity() error = %v", err)
		}
		if got.DeviceNamespace != "edge" || got.DeviceName != "switch-01" ||
			got.DeviceUID != "22c81400-85ea-4ca8-91ee-07a7c7bd531c" || got.NodeName != "cvk-edge-node-01" {
			t.Fatalf("identity = %#v", got)
		}
		if got.ManagedTopology {
			t.Fatal("standalone identity unexpectedly enabled managed topology")
		}
	})

	t.Run("node precedence preserves flag then new env then legacy env then config", func(t *testing.T) {
		clearWorkerIdentityEnv(t)
		t.Setenv("POD_NAMESPACE", "edge")
		t.Setenv(envNodeName, "new-env-node")
		t.Setenv(legacyEnvNodeName, "legacy-env-node")
		spec := &ciskov1.DeviceSpec{NodeName: "configured-node", Address: "192.0.2.10"}

		got, err := resolveWorkerRuntimeIdentity("flag-node", spec)
		if err != nil {
			t.Fatalf("flag identity error = %v", err)
		}
		if got.NodeName != "flag-node" {
			t.Fatalf("flag NodeName = %q", got.NodeName)
		}

		got, err = resolveWorkerRuntimeIdentity("", spec)
		if err != nil {
			t.Fatalf("new env identity error = %v", err)
		}
		if got.NodeName != "new-env-node" {
			t.Fatalf("new env NodeName = %q", got.NodeName)
		}

		t.Setenv(envNodeName, "")
		got, err = resolveWorkerRuntimeIdentity("", spec)
		if err != nil {
			t.Fatalf("legacy env identity error = %v", err)
		}
		if got.NodeName != "legacy-env-node" {
			t.Fatalf("legacy env NodeName = %q", got.NodeName)
		}

		t.Setenv(legacyEnvNodeName, "")
		got, err = resolveWorkerRuntimeIdentity("", spec)
		if err != nil {
			t.Fatalf("config identity error = %v", err)
		}
		if got.NodeName != "configured-node" {
			t.Fatalf("config NodeName = %q", got.NodeName)
		}
	})

	t.Run("legacy address and default fallbacks remain available", func(t *testing.T) {
		clearWorkerIdentityEnv(t)

		got, err := resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{Address: "192.0.2.10"})
		if err != nil {
			t.Fatalf("address identity error = %v", err)
		}
		if got.NodeName != "cisco-vk-192-0-2-10" || got.DeviceName != got.NodeName || got.DeviceNamespace != "default" {
			t.Fatalf("address identity = %#v", got)
		}

		got, err = resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{})
		if err != nil {
			t.Fatalf("default identity error = %v", err)
		}
		if got.NodeName != "cisco-virtual-kubelet" || got.DeviceName != got.NodeName || got.DeviceNamespace != "default" {
			t.Fatalf("default identity = %#v", got)
		}
	})
}

func TestResolveWorkerRuntimeIdentityManaged(t *testing.T) {
	setComplete := func(t *testing.T) {
		t.Helper()
		clearWorkerIdentityEnv(t)
		t.Setenv(envManagedTopology, "true")
		t.Setenv(envDeviceNamespace, "edge")
		t.Setenv(envDeviceName, "switch-01")
		t.Setenv(envDeviceUID, "22c81400-85ea-4ca8-91ee-07a7c7bd531c")
		t.Setenv(envNodeName, "cvk-edge-node-01")
		t.Setenv(envWorkerRevision, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		t.Setenv("POD_NAMESPACE", "edge")
	}

	t.Run("complete distinct identity", func(t *testing.T) {
		setComplete(t)
		t.Setenv(legacyEnvNodeName, "cvk-edge-node-01")
		got, err := resolveWorkerRuntimeIdentity("cvk-edge-node-01", &ciskov1.DeviceSpec{NodeName: "cvk-edge-node-01"})
		if err != nil {
			t.Fatalf("resolveWorkerRuntimeIdentity() error = %v", err)
		}
		if !got.ManagedTopology || got.DeviceName != "switch-01" || got.NodeName != "cvk-edge-node-01" {
			t.Fatalf("identity = %#v", got)
		}
	})

	for _, missing := range []string{envDeviceNamespace, envDeviceName, envDeviceUID, envNodeName, envWorkerRevision} {
		t.Run("missing "+missing, func(t *testing.T) {
			setComplete(t)
			t.Setenv(missing, "")
			_, err := resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{})
			if err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("error = %v, want missing %s", err, missing)
			}
		})
	}

	for _, conflict := range []struct {
		name       string
		flag       string
		legacy     string
		configured string
	}{
		{name: "flag", flag: "other-node"},
		{name: "legacy env", legacy: "other-node"},
		{name: "config", configured: "other-node"},
	} {
		t.Run("rejects conflicting "+conflict.name, func(t *testing.T) {
			setComplete(t)
			t.Setenv(legacyEnvNodeName, conflict.legacy)
			_, err := resolveWorkerRuntimeIdentity(conflict.flag, &ciskov1.DeviceSpec{NodeName: conflict.configured})
			if err == nil || !strings.Contains(err.Error(), "Node identity conflict") {
				t.Fatalf("error = %v, want Node identity conflict", err)
			}
		})
	}

	t.Run("rejects invalid mode", func(t *testing.T) {
		clearWorkerIdentityEnv(t)
		t.Setenv(envManagedTopology, "sometimes")
		_, err := resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{})
		if err == nil || !strings.Contains(err.Error(), envManagedTopology) {
			t.Fatalf("error = %v, want %s parse failure", err, envManagedTopology)
		}
	})

	t.Run("requires the reconciler namespace binding", func(t *testing.T) {
		setComplete(t)
		t.Setenv("POD_NAMESPACE", "other")
		_, err := resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{})
		if err == nil || !strings.Contains(err.Error(), "namespace conflict") {
			t.Fatalf("error = %v, want namespace conflict", err)
		}
	})

	t.Run("requires an explicit reconciler namespace", func(t *testing.T) {
		setComplete(t)
		t.Setenv("POD_NAMESPACE", "")
		_, err := resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{})
		if err == nil || !strings.Contains(err.Error(), "requires POD_NAMESPACE") {
			t.Fatalf("error = %v, want missing POD_NAMESPACE", err)
		}
	})

	t.Run("rejects Node name that cannot be the hostname label", func(t *testing.T) {
		setComplete(t)
		longNodeName := strings.Repeat("a", 32) + "." + strings.Repeat("b", 32)
		t.Setenv(envNodeName, longNodeName)
		_, err := resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{})
		if err == nil || !strings.Contains(err.Error(), "hostname label") {
			t.Fatalf("error = %v, want hostname label failure", err)
		}
	})
}

func TestManagedWorkerInitialNodeKeepsOnlyIdentityAndStatusSeed(t *testing.T) {
	input := v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "edge-01",
			Labels:      map[string]string{"topology.kubernetes.io/zone": "zone-a"},
			Annotations: map[string]string{"cisco.io/hostname": "edge-01"},
		},
		Spec:   v1.NodeSpec{Taints: []v1.Taint{{Key: "managed", Effect: v1.TaintEffectNoSchedule}}},
		Status: v1.NodeStatus{Phase: v1.NodeRunning},
	}

	got := managedWorkerInitialNode(input)
	if got.Name != input.Name || got.Status.Phase != v1.NodeRunning {
		t.Fatalf("identity/status seed changed: %#v", got)
	}
	if len(got.Labels) != 0 || len(got.Annotations) != 0 || len(got.Spec.Taints) != 0 {
		t.Fatalf("manager-owned fields remained: labels=%v annotations=%v taints=%v", got.Labels, got.Annotations, got.Spec.Taints)
	}
}

func TestResolveWorkerRuntimeIdentityRejectsNilSpec(t *testing.T) {
	clearWorkerIdentityEnv(t)
	_, err := resolveWorkerRuntimeIdentity("", nil)
	if err == nil || !strings.Contains(err.Error(), "nil DeviceSpec") {
		t.Fatalf("error = %v, want nil DeviceSpec", err)
	}
}

func TestResolveWorkerRuntimeIdentityValidation(t *testing.T) {
	tests := []struct {
		name   string
		env    string
		value  string
		needle string
	}{
		{name: "namespace", env: envDeviceNamespace, value: "Not-A-Namespace", needle: "invalid CiscoDevice namespace"},
		{name: "device name", env: envDeviceName, value: "switch_01", needle: "CiscoDevice name"},
		{name: "device UID", env: envDeviceUID, value: "uid with spaces", needle: "CiscoDevice UID"},
		{name: "long device UID", env: envDeviceUID, value: strings.Repeat("u", 129), needle: "CiscoDevice UID"},
		{name: "Node name", env: envNodeName, value: "node_01", needle: "Kubernetes Node name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearWorkerIdentityEnv(t)
			t.Setenv("POD_NAMESPACE", "edge")
			t.Setenv(envDeviceNamespace, "edge")
			t.Setenv(envDeviceName, "switch-01")
			t.Setenv(envDeviceUID, "valid-uid")
			t.Setenv(envNodeName, "node-01")
			t.Setenv(tt.env, tt.value)
			if tt.env == envDeviceNamespace {
				t.Setenv("POD_NAMESPACE", tt.value)
			}
			_, err := resolveWorkerRuntimeIdentity("", &ciskov1.DeviceSpec{})
			if err == nil || !strings.Contains(err.Error(), tt.needle) {
				t.Fatalf("error = %v, want %q", err, tt.needle)
			}
		})
	}
}

func TestLogLevelValidation(t *testing.T) {
	tests := []struct {
		name        string
		logLevel    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "valid level - debug",
			logLevel: "debug",
			wantErr:  false,
		},
		{
			name:     "valid level - info",
			logLevel: "info",
			wantErr:  false,
		},
		{
			name:     "valid level - warn",
			logLevel: "warn",
			wantErr:  false,
		},
		{
			name:     "valid level - warning",
			logLevel: "warning",
			wantErr:  false,
		},
		{
			name:     "valid level - error",
			logLevel: "error",
			wantErr:  false,
		},
		{
			name:     "valid level - empty (defaults to info)",
			logLevel: "",
			wantErr:  false,
		},
		{
			name:        "invalid level - verbose",
			logLevel:    "verbose",
			wantErr:     true,
			errContains: "invalid log level",
		},
		{
			name:        "invalid level - trace",
			logLevel:    "trace",
			wantErr:     true,
			errContains: "invalid log level",
		},
		{
			name:        "invalid level - DEBUG (case sensitive)",
			logLevel:    "DEBUG",
			wantErr:     true,
			errContains: "invalid log level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateLogLevel(tt.logLevel)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for log level %q, got nil", tt.logLevel)
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for log level %q: %v", tt.logLevel, err)
				}
			}
		})
	}
}

func TestValidateConfig(t *testing.T) {
	tmpDir := t.TempDir()
	validConfigPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(validConfigPath, []byte("test: config"), 0644); err != nil {
		t.Fatalf("failed to create test config: %v", err)
	}

	tests := []struct {
		name        string
		configPath  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "valid config",
			configPath: validConfigPath,
			wantErr:    false,
		},
		{
			name:        "missing config",
			configPath:  "/nonexistent/path/config.yaml",
			wantErr:     true,
			errContains: "config file not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.configPath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for config %q, got nil", tt.configPath)
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for config %q: %v", tt.configPath, err)
				}
			}
		})
	}
}

func TestGetKubeConfig(t *testing.T) {
	originalKubeconfig := os.Getenv("KUBECONFIG")
	defer os.Setenv("KUBECONFIG", originalKubeconfig)

	tests := []struct {
		name        string
		flagValue   string
		envValue    string
		wantErr     bool
		errContains string
		setupEnv    bool
	}{
		{
			name:        "flag provided with invalid path",
			flagValue:   "/nonexistent/kubeconfig",
			wantErr:     true,
			errContains: "kubeconfig file not found",
		},
		{
			name:        "no flag, env var with invalid path",
			flagValue:   "",
			envValue:    "/nonexistent/kubeconfig",
			setupEnv:    true,
			wantErr:     true,
			errContains: "kubeconfig file from KUBECONFIG env not found",
		},
		{
			name:        "no flag, no env - in-cluster expected",
			flagValue:   "",
			envValue:    "",
			setupEnv:    true,
			wantErr:     true,
			errContains: "failed to load in-cluster config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setupEnv {
				os.Setenv("KUBECONFIG", tt.envValue)
				defer os.Unsetenv("KUBECONFIG")
			}

			_, err := GetKubeConfig(tt.flagValue)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %q", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
