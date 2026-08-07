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
	"fmt"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

var requiredManagerCRDs = []schema.GroupVersionResource{
	ciskov1.GroupVersion.WithResource("networkcontrollers"),
	configv1alpha1.GroupVersion.WithResource("networkcontrollerconfigs"),
	configv1alpha1.GroupVersion.WithResource("iosxetelemetries"),
	opsv1alpha1.GroupVersion.WithResource("deviceoperations"),
	configv1alpha1.GroupVersion.WithResource("iosxeconfigrevisions"),
}

var controllerFoundationCRDNames = map[string]struct{}{
	crdNameForGVR(ciskov1.GroupVersion.WithResource("networkcontrollers")):              {},
	crdNameForGVR(configv1alpha1.GroupVersion.WithResource("networkcontrollerconfigs")): {},
}

func missingRequiredCRDs(cfg *rest.Config) ([]string, error) {
	cli, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("create discovery client: %w", err)
	}
	byGV := map[schema.GroupVersion][]schema.GroupVersionResource{}
	for _, gvr := range requiredManagerCRDs {
		byGV[gvr.GroupVersion()] = append(byGV[gvr.GroupVersion()], gvr)
	}
	gvs := make([]schema.GroupVersion, 0, len(byGV))
	for gv := range byGV {
		gvs = append(gvs, gv)
	}
	sort.Slice(gvs, func(i, j int) bool { return gvs[i].String() < gvs[j].String() })

	var missing []string
	for _, gv := range gvs {
		required := byGV[gv]
		resources, err := cli.ServerResourcesForGroupVersion(gv.String())
		if err != nil {
			if apierrors.IsNotFound(err) {
				for _, gvr := range required {
					missing = append(missing, crdNameForGVR(gvr))
				}
				continue
			}
			return nil, fmt.Errorf("discover %s: %w", gv.String(), err)
		}
		found := map[string]struct{}{}
		for _, resource := range resources.APIResources {
			found[resource.Name] = struct{}{}
		}
		for _, gvr := range required {
			if _, ok := found[gvr.Resource]; !ok {
				missing = append(missing, crdNameForGVR(gvr))
			}
		}
	}
	sort.Strings(missing)
	return missing, nil
}

func crdNameForGVR(gvr schema.GroupVersionResource) string {
	return strings.Join([]string{gvr.Resource, gvr.Group}, ".")
}

// partitionMissingRequiredCRDs keeps missing pre-existing CRDs fatal while
// allowing an upgrade from a pre-controller-scaffold release to preserve its
// existing CiscoDevice reconcilers. Network-controller reconciliation remains
// disabled for that manager process until both additive CRDs are installed and
// the Deployment is restarted.
func partitionMissingRequiredCRDs(missing []string) (blocking, controllerFoundation []string) {
	for _, name := range missing {
		if _, optionalDuringUpgrade := controllerFoundationCRDNames[name]; optionalDuringUpgrade {
			controllerFoundation = append(controllerFoundation, name)
			continue
		}
		blocking = append(blocking, name)
	}
	return blocking, controllerFoundation
}
