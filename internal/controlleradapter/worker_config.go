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

package controlleradapter

import (
	"fmt"
	"path/filepath"
	"strings"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	// WorkerConfigAPIVersion versions the private manager-to-worker bootstrap
	// contract independently from public CRDs.
	WorkerConfigAPIVersion = "controlleradapter.cisco.vk/v1alpha1"
	WorkerConfigKind       = "NetworkControllerWorkerConfig"
)

// WorkerControllerReference identifies the single namespaced endpoint owned
// by a worker. It intentionally contains no Secret or ConfigMap reference.
type WorkerControllerReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	UID       string `json:"uid"`
}

// WorkerConfig is the non-sensitive bootstrap document projected into a
// controller worker. The worker fetches the current NetworkController from the
// API before constructing its adapter, while credentials and optional CA data
// arrive through separate read-only volumes.
type WorkerConfig struct {
	APIVersion    string                    `json:"apiVersion"`
	Kind          string                    `json:"kind"`
	ControllerRef WorkerControllerReference `json:"controllerRef"`
	Type          string                    `json:"type"`
}

// NewWorkerConfig creates the private bootstrap contract for controller.
// The document is invariant across mutable endpoint settings. Optional CA
// presence is derived from the live NetworkController after the worker binds
// this document to the immutable identity supplied on its command line.
func NewWorkerConfig(controller *ciskov1.NetworkController) (WorkerConfig, error) {
	if controller == nil {
		return WorkerConfig{}, fmt.Errorf("controlleradapter.NewWorkerConfig: nil NetworkController")
	}
	config := WorkerConfig{
		APIVersion: WorkerConfigAPIVersion,
		Kind:       WorkerConfigKind,
		ControllerRef: WorkerControllerReference{
			Namespace: controller.Namespace,
			Name:      controller.Name,
			UID:       string(controller.UID),
		},
		Type: string(controller.Spec.Type),
	}
	if err := config.Validate(); err != nil {
		return WorkerConfig{}, err
	}
	return config, nil
}

// Validate rejects malformed bootstrap identity before a worker constructs a
// Kubernetes client or adapter.
func (c WorkerConfig) Validate() error {
	if c.APIVersion != WorkerConfigAPIVersion {
		return fmt.Errorf("worker config apiVersion %q, want %q", c.APIVersion, WorkerConfigAPIVersion)
	}
	if c.Kind != WorkerConfigKind {
		return fmt.Errorf("worker config kind %q, want %q", c.Kind, WorkerConfigKind)
	}
	if problems := utilvalidation.IsDNS1123Label(c.ControllerRef.Namespace); len(problems) > 0 {
		return fmt.Errorf("worker config controller namespace %q is invalid: %s", c.ControllerRef.Namespace, strings.Join(problems, "; "))
	}
	if problems := utilvalidation.IsDNS1123Subdomain(c.ControllerRef.Name); len(problems) > 0 {
		return fmt.Errorf("worker config controller name %q is invalid: %s", c.ControllerRef.Name, strings.Join(problems, "; "))
	}
	if c.ControllerRef.UID == "" {
		return fmt.Errorf("worker config controller UID is required")
	}
	if problems := utilvalidation.IsDNS1123Label(c.Type); len(problems) > 0 {
		return fmt.Errorf("worker config controller type %q is invalid: %s", c.Type, strings.Join(problems, "; "))
	}
	return nil
}

// ValidateIdentity binds a projected bootstrap document to the immutable
// namespace/name/UID/type arguments in the worker PodSpec. A stale ConfigMap
// therefore cannot redirect a worker to another controller incarnation.
func (c WorkerConfig) ValidateIdentity(namespace, name, uid, typeName string) error {
	if c.ControllerRef.Namespace != namespace {
		return fmt.Errorf("worker config namespace %q does not match pod identity %q", c.ControllerRef.Namespace, namespace)
	}
	if c.ControllerRef.Name != name {
		return fmt.Errorf("worker config controller name %q does not match pod identity %q", c.ControllerRef.Name, name)
	}
	if c.ControllerRef.UID != uid {
		return fmt.Errorf("worker config controller UID %q does not match pod identity %q", c.ControllerRef.UID, uid)
	}
	if c.Type != typeName {
		return fmt.Errorf("worker config controller type %q does not match pod identity %q", c.Type, typeName)
	}
	return nil
}

func validateWorkerMountPath(field, value string, optional bool) error {
	if value == "" && optional {
		return nil
	}
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return fmt.Errorf("worker config %s must be a clean absolute path", field)
	}
	const mountRoot = "/var/run/secrets/cisco-vk/"
	if !strings.HasPrefix(value, mountRoot) {
		return fmt.Errorf("worker config %s must be below %s", field, mountRoot)
	}
	return nil
}
