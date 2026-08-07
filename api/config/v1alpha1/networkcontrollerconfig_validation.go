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

package v1alpha1

import (
	"regexp"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var (
	networkControllerSectionPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	networkControllerSecretAliasPattern = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)
	networkControllerScopePattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
	netAsCodeModelFormatPattern         = regexp.MustCompile(`^netascode-[a-z0-9]+(-[a-z0-9]+)*$`)
	sha256DigestPattern                 = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

const maxNetworkControllerConfigDuration = 720 * time.Hour

// ValidateNetworkControllerConfig returns a Kubernetes-style invalid-object
// error for controller-neutral API constraints. Adapter-specific model and
// section support is validated after resolving spec.controllerRef.
func ValidateNetworkControllerConfig(config *NetworkControllerConfig) error {
	if config == nil {
		return apierrors.NewInvalid(
			GroupVersion.WithKind("NetworkControllerConfig").GroupKind(),
			"",
			field.ErrorList{field.Required(field.NewPath("networkControllerConfig"), "object is required")},
		)
	}
	errs := ValidateNetworkControllerConfigSpec(&config.Spec)
	if len(errs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(GroupVersion.WithKind("NetworkControllerConfig").GroupKind(), config.Name, errs)
}

// ValidateNetworkControllerConfigurationSource enforces the controller source
// union outside API-server admission without changing the established device
// ConfigurationSource contract.
func ValidateNetworkControllerConfigurationSource(source NetworkControllerConfigurationSource, path *field.Path) field.ErrorList {
	if path == nil {
		path = field.NewPath("source")
	}
	if (source.Inline == nil) == (source.ConfigMapRef == nil) {
		return field.ErrorList{field.Invalid(path, "", "exactly one of inline or configMapRef must be set")}
	}
	if source.ConfigMapRef == nil {
		return nil
	}

	var errs field.ErrorList
	refPath := path.Child("configMapRef")
	if source.ConfigMapRef.Name == "" {
		errs = append(errs, field.Required(refPath.Child("name"), "ConfigMap name is required"))
	} else if problems := utilvalidation.IsDNS1123Subdomain(source.ConfigMapRef.Name); len(problems) > 0 {
		errs = append(errs, field.Invalid(refPath.Child("name"), source.ConfigMapRef.Name, strings.Join(problems, "; ")))
	}
	if problems := utilvalidation.IsConfigMapKey(source.ConfigMapRef.Key); len(problems) > 0 {
		errs = append(errs, field.Invalid(refPath.Child("key"), source.ConfigMapRef.Key, strings.Join(problems, "; ")))
	}
	return errs
}

// ValidateNetworkControllerConfigSpec validates the reusable controller config
// shape. Unknown but well-formed formats and sections remain admissible so a
// future registered adapter can own them without a CRD schema change.
func ValidateNetworkControllerConfigSpec(spec *NetworkControllerConfigSpec) field.ErrorList {
	root := field.NewPath("spec")
	if spec == nil {
		return field.ErrorList{field.Required(root, "spec is required")}
	}

	var errs field.ErrorList
	controllerNamePath := root.Child("controllerRef").Child("name")
	if spec.ControllerRef.Name == "" {
		errs = append(errs, field.Required(controllerNamePath, "NetworkController name is required"))
	} else if problems := utilvalidation.IsDNS1123Subdomain(spec.ControllerRef.Name); len(problems) > 0 {
		errs = append(errs, field.Invalid(controllerNamePath, spec.ControllerRef.Name, strings.Join(problems, "; ")))
	}

	if spec.Scope != "" && (!networkControllerScopePattern.MatchString(spec.Scope) || len(spec.Scope) > 253) {
		errs = append(errs, field.Invalid(root.Child("scope"), spec.Scope, "must be a stable controller ownership scope"))
	}

	sectionSet := make(map[string]struct{}, len(spec.ManagedSections))
	sectionsPath := root.Child("managedSections")
	if len(spec.ManagedSections) == 0 {
		errs = append(errs, field.Required(sectionsPath, "at least one managed section is required"))
	}
	if len(spec.ManagedSections) > 64 {
		errs = append(errs, field.TooMany(sectionsPath, len(spec.ManagedSections), 64))
	}
	for i, section := range spec.ManagedSections {
		sectionPath := sectionsPath.Index(i)
		if !networkControllerSectionPattern.MatchString(section) || len(section) > 63 {
			errs = append(errs, field.Invalid(sectionPath, section, "must match ^[a-z][a-z0-9_]*$ and contain at most 63 characters"))
		}
		if _, duplicate := sectionSet[section]; duplicate {
			errs = append(errs, field.Duplicate(sectionPath, section))
		}
		sectionSet[section] = struct{}{}
	}

	errs = append(errs, ValidateNetworkControllerConfigurationSource(spec.Source, root.Child("source"))...)

	modelPath := root.Child("modelSource")
	if !netAsCodeModelFormatPattern.MatchString(string(spec.ModelSource.Format)) {
		errs = append(errs, field.Invalid(modelPath.Child("format"), spec.ModelSource.Format, "must be a qualified netascode-* format"))
	}
	if strings.TrimSpace(spec.ModelSource.ModelVersion) == "" {
		errs = append(errs, field.Required(modelPath.Child("modelVersion"), "a model version is required"))
	}
	if !spec.ModelSource.Resolved {
		errs = append(errs, field.Invalid(modelPath.Child("resolved"), false, "controller intent must be resolved before reconciliation"))
	}
	if digest := spec.ModelSource.SchemaDigest; digest != "" && !sha256DigestPattern.MatchString(digest) {
		errs = append(errs, field.Invalid(modelPath.Child("schemaDigest"), digest, "must be sha256:<64 lowercase hex characters>"))
	}

	secretKeys := make(map[string]struct{}, len(spec.SecretRefs))
	secretRefsPath := root.Child("secretRefs")
	if len(spec.SecretRefs) > 128 {
		errs = append(errs, field.TooMany(secretRefsPath, len(spec.SecretRefs), 128))
	}
	for i, ref := range spec.SecretRefs {
		refPath := secretRefsPath.Index(i)
		if _, managed := sectionSet[ref.Section]; !managed {
			errs = append(errs, field.Invalid(refPath.Child("section"), ref.Section, "must name a managed section"))
		}
		if !validJSONPointer(ref.Path) {
			errs = append(errs, field.Invalid(refPath.Child("path"), ref.Path, "must be a valid non-empty RFC 6901 JSON pointer"))
		}
		identity := ref.Section + "\x00" + ref.Path
		if _, duplicate := secretKeys[identity]; duplicate {
			errs = append(errs, field.Duplicate(refPath, ref.Section+":"+ref.Path))
		}
		secretKeys[identity] = struct{}{}
		if !networkControllerSecretAliasPattern.MatchString(ref.Source) || len(ref.Source) > 63 {
			errs = append(errs, field.Invalid(refPath.Child("source"), ref.Source, "must be a lowercase DNS-label-style alias"))
		}
	}

	if driftDetectInterval := spec.DriftDetectInterval; driftDetectInterval != nil {
		interval := driftDetectInterval.Duration
		if interval < 30*time.Second || interval > maxNetworkControllerConfigDuration {
			errs = append(errs, field.Invalid(root.Child("driftDetectInterval"), interval.String(), "must be at least 30s and at most 720h"))
		}
	}
	if taskTimeout := spec.TaskTimeout; taskTimeout != nil {
		timeout := taskTimeout.Duration
		if timeout <= 0 || timeout > maxNetworkControllerConfigDuration {
			errs = append(errs, field.Invalid(root.Child("taskTimeout"), timeout.String(), "must be greater than 0s and at most 720h"))
		}
	}
	switch spec.Mode {
	case "", NetworkControllerApplyModeReport, NetworkControllerApplyModeApply:
	default:
		errs = append(errs, field.NotSupported(root.Child("mode"), spec.Mode, []string{string(NetworkControllerApplyModeReport), string(NetworkControllerApplyModeApply)}))
	}
	for path, policy := range map[string]NetworkControllerRetentionPolicy{
		"prunePolicy":    spec.PrunePolicy,
		"deletionPolicy": spec.DeletionPolicy,
	} {
		switch policy {
		case "", NetworkControllerRetentionPolicyRetain, NetworkControllerRetentionPolicyDelete:
		default:
			errs = append(errs, field.NotSupported(root.Child(path), policy, []string{string(NetworkControllerRetentionPolicyRetain), string(NetworkControllerRetentionPolicyDelete)}))
		}
	}

	return errs
}

func validJSONPointer(pointer string) bool {
	if pointer == "" || pointer[0] != '/' || len(pointer) > 1024 {
		return false
	}
	for i := 0; i < len(pointer); i++ {
		if pointer[i] != '~' {
			continue
		}
		if i+1 >= len(pointer) || (pointer[i+1] != '0' && pointer[i+1] != '1') {
			return false
		}
		i++
	}
	return true
}
