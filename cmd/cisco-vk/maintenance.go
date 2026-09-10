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
	"os"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/maintenance"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func maintenanceEnabled(opts configReconcilerOptions) bool {
	return supportsIOSXEMutationControllers(opts.Spec) &&
		!envEnabled("DISABLE_IN_POD_CONFIG_RECONCILER")
}

func newMaintenanceCoordinator(cfg *rest.Config, nodeName string, opts configReconcilerOptions) (*maintenance.Coordinator, error) {
	if !maintenanceEnabled(opts) {
		return nil, nil
	}
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}
	leaseNamespace := os.Getenv("CONFIG_LEASE_NAMESPACE")
	if leaseNamespace == "" {
		leaseNamespace = operationNamespace()
	}
	return &maintenance.Coordinator{
		Client: c, Namespace: operationNamespace(), DeviceName: nodeName,
		NodeName: nodeName, LeaseNamespace: leaseNamespace,
		MutationsEnabled: (opts.EnableIOSXESoftwareUpgrade || opts.EnableWriteClassGNOI) && !envEnabled(gNOIDisabledEnv),
	}, nil
}
