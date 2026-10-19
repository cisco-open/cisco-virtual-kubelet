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
	"os"
	"path/filepath"
	"strings"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ValidateConfigContract is the common fail-closed gate an adapter runs before
// planning or calling a controller API. It binds a resolved NaC document to
// the referenced NetworkController and to the exact model format, qualified
// versions, and top-level sections advertised by the registered descriptor.
//
// intentSecretRoot is the read-only directory supplied in Factory Options.
// Product-specific semantic validation remains adapter-owned, but it must run
// only after this function confirms every authorized Secret input is mounted.
func ValidateConfigContract(controller *ciskov1.NetworkController, config *configv1alpha1.NetworkControllerConfig, intentSecretRoot string) error {
	projections, err := configMetadataContract(controller, config)
	if err != nil {
		return err
	}
	for _, projection := range projections {
		if intentSecretRoot == "" || !filepath.IsAbs(intentSecretRoot) || filepath.Clean(intentSecretRoot) != intentSecretRoot {
			return fmt.Errorf("intent Secret root must be a clean absolute path")
		}
		info, err := os.Stat(filepath.Join(intentSecretRoot, projection.path))
		if err != nil {
			return fmt.Errorf("intent Secret source alias %q is not projected for NetworkControllerConfig %s/%s: %w", projection.source, config.Namespace, config.Name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("intent Secret source alias %q projection for NetworkControllerConfig %s/%s is not a regular file", projection.source, config.Namespace, config.Name)
		}
	}
	return nil
}

// ValidateConfigMetadataContract checks the complete descriptor, config and
// authorized Secret bindings without reading worker-local projected files.
// This is for manager metadata decisions and observation of already acknowledged
// work. It never authorizes new controller mutation: dispatch must still use the full
// ValidateConfigContract gate and adapter-specific projected-value validation.
func ValidateConfigMetadataContract(controller *ciskov1.NetworkController, config *configv1alpha1.NetworkControllerConfig) error {
	_, err := configMetadataContract(controller, config)
	return err
}

type configContractProjection struct{ source, path string }

// Keep both public gates on the same metadata checks and safe path derivation.
func configMetadataContract(controller *ciskov1.NetworkController, config *configv1alpha1.NetworkControllerConfig) ([]configContractProjection, error) {
	if err := ciskov1.ValidateNetworkController(controller); err != nil {
		return nil, fmt.Errorf("controller contract: %w", err)
	}
	// Scaling the worker Deployment to zero is asynchronous. Keep the common
	// pre-request gate authoritative during that termination window so a queued
	// reconcile cannot issue one last controller API call after pause was
	// requested.
	if controller.Spec.Paused {
		return nil, fmt.Errorf("controller contract: NetworkController %s/%s is paused", controller.Namespace, controller.Name)
	}
	if err := configv1alpha1.ValidateNetworkControllerConfig(config); err != nil {
		return nil, fmt.Errorf("controller config contract: %w", err)
	}
	if controller.Namespace != config.Namespace || controller.Name != config.Spec.ControllerRef.Name {
		return nil, fmt.Errorf(
			"controller config %s/%s references %q but worker owns %s/%s",
			config.Namespace,
			config.Name,
			config.Spec.ControllerRef.Name,
			controller.Namespace,
			controller.Name,
		)
	}

	descriptor, registered := DescriptorFor(string(controller.Spec.Type))
	if !registered {
		return nil, unknownTypeError(string(controller.Spec.Type))
	}
	if got, want := string(config.Spec.ModelSource.Format), descriptor.NetAsCode.Format; got != want {
		return nil, fmt.Errorf("Network as Code format %q is incompatible with controller type %q (want %q)", got, descriptor.Type, want)
	}
	if !containsString(descriptor.NetAsCode.ModelVersions, config.Spec.ModelSource.ModelVersion) {
		return nil, fmt.Errorf(
			"Network as Code model version %q is not qualified for controller type %q (qualified: %s)",
			config.Spec.ModelSource.ModelVersion,
			descriptor.Type,
			strings.Join(descriptor.NetAsCode.ModelVersions, ", "),
		)
	}
	for _, section := range config.Spec.ManagedSections {
		if !containsString(descriptor.NetAsCode.Sections, section) {
			return nil, fmt.Errorf("Network as Code section %q is not supported by controller type %q", section, descriptor.Type)
		}
	}
	projections := make([]configContractProjection, 0, len(config.Spec.SecretRefs))
	authorizedSources := make(map[string]ciskov1.NetworkControllerIntentSecretSource, len(controller.Spec.IntentSecretSources))
	for _, source := range controller.Spec.IntentSecretSources {
		authorizedSources[source.Alias] = source
	}
	for _, ref := range config.Spec.SecretRefs {
		if ref.Use != "" && ref.Use != "Value" && (ref.Use != "PrivateComparisonKey" || !containsString(descriptor.Capabilities, "private-input-comparison")) {
			return nil, fmt.Errorf("intent Secret use is not supported by the registered adapter")
		}
		authorizedSource, authorized := authorizedSources[ref.Source]
		if !authorized {
			return nil, fmt.Errorf("intent Secret source alias %q is not authorized by NetworkController %s/%s", ref.Source, controller.Namespace, controller.Name)
		}
		relativePath, err := IntentSecretRelativePath(IntentSecretPathInput{
			ConfigName:  config.Name,
			Section:     ref.Section,
			JSONPointer: ref.Path,
			SourceAlias: ref.Source,
			SecretName:  authorizedSource.Name,
			SecretKey:   authorizedSource.Key,
		})
		if err != nil {
			return nil, fmt.Errorf("intent Secret source alias %q has no safe projected path: %w", ref.Source, err)
		}
		projections = append(projections, configContractProjection{source: ref.Source, path: relativePath})
	}
	return projections, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
