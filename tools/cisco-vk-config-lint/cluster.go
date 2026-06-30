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
	"context"
	"encoding/json"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// iosxeConfigGVR is the dynamic-resource handle for IOSXEConfig.
// Resource (lowercase plural) matches what controller-gen names the
// CRD; if the project ever renames the CR, this is the one place to
// update.
var iosxeConfigGVR = schema.GroupVersionResource{
	Group:    "config.cisco.vk",
	Version:  "v1alpha1",
	Resource: "iosxeconfigs",
}

// crListerFactory builds a NamespaceableResourceInterface. Indirected
// so unit tests can inject a fake dynamic client.
type crListerFactory func(*rest.Config) (dynamic.NamespaceableResourceInterface, error)

func defaultCRListerFactory(cfg *rest.Config) (dynamic.NamespaceableResourceInterface, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	return dyn.Resource(iosxeConfigGVR), nil
}

// loadCRsFromCluster reads IOSXEConfig CRs from a running cluster
// rather than local YAML files. It mirrors the file loader's
// contract: only CRs whose spec.deviceRef.name matches deviceName
// come back; everything else is silently ignored. CRs decoded
// through the same docShape path the file loader uses, so the two
// loaders agree on edge cases (missing fields, non-matching
// apiVersion).
func loadCRsFromCluster(
	ctx context.Context,
	cfg *rest.Config,
	namespace string,
	allNamespaces bool,
	deviceName string,
	listerFactory crListerFactory,
) ([]loadedCR, error) {
	if listerFactory == nil {
		listerFactory = defaultCRListerFactory
	}
	lister, err := listerFactory(cfg)
	if err != nil {
		return nil, err
	}
	var rd dynamic.ResourceInterface = lister
	if !allNamespaces {
		rd = lister.Namespace(namespace)
	}
	list, err := rd.List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list IOSXEConfigs: %w", err)
	}

	var out []loadedCR
	for i := range list.Items {
		item := &list.Items[i]
		// Round-trip via JSON so the same docShape decoder the file
		// loader uses handles both paths. Avoids duplicating the
		// shape-detection logic, and handles future spec fields
		// without a parallel codepath.
		raw, err := json.Marshal(item.Object)
		if err != nil {
			return nil, fmt.Errorf("marshal %s/%s: %w",
				item.GetNamespace(), item.GetName(), err)
		}
		cr, isCR, err := tryDecodeIOSXEConfig(raw)
		if err != nil {
			return nil, fmt.Errorf("decode %s/%s: %w",
				item.GetNamespace(), item.GetName(), err)
		}
		if !isCR {
			continue
		}
		if cr.DeviceName != deviceName {
			continue
		}
		out = append(out, cr)
	}
	return out, nil
}

// resolveKubeconfig follows the same precedence kubectl uses:
// explicit --kubeconfig flag > $KUBECONFIG > in-cluster > $HOME/.kube/config.
// The "in-cluster preferred over the user kubeconfig" rule matches
// the convention for tools that may run as a Pod (pre-commit-as-Job,
// CI runner inside the cluster). When no kubeconfig is reachable,
// callers get a clear error rather than a silent fallback to a
// stale file.
func resolveKubeconfig(explicit string) (*rest.Config, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); os.IsNotExist(err) {
			return nil, fmt.Errorf("kubeconfig %q does not exist", explicit)
		}
		return clientcmd.BuildConfigFromFlags("", explicit)
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		return clientcmd.BuildConfigFromFlags("", env)
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRules, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf(
			"no kubeconfig: pass --kubeconfig, set $KUBECONFIG, or run inside a cluster (%w)", err)
	}
	return cfg, nil
}
