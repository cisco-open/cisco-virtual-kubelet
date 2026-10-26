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
	"net/url"
	"regexp"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

var networkControllerTypePattern = regexp.MustCompile(`^[a-z]([a-z0-9-]*[a-z0-9])?$`)

const maxNetworkControllerConnectionDuration = 24 * time.Hour

// ValidateNetworkController returns a Kubernetes-style invalid-object error.
// It mirrors CRD admission rules and adds URL and duration checks that are
// clearer and safer to implement in Go.
func ValidateNetworkController(controller *NetworkController) error {
	if controller == nil {
		return apierrors.NewInvalid(
			GroupVersion.WithKind("NetworkController").GroupKind(),
			"",
			field.ErrorList{field.Required(field.NewPath("networkController"), "object is required")},
		)
	}
	errs := ValidateNetworkControllerSpec(&controller.Spec)
	if len(errs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(GroupVersion.WithKind("NetworkController").GroupKind(), controller.Name, errs)
}

// ValidateNetworkControllerSpec performs controller-neutral structural
// validation. Whether Type is registered is deliberately a runtime registry
// concern: an unknown but well-formed type remains API-compatible and is
// reported through AdapterAvailable=False.
func ValidateNetworkControllerSpec(spec *NetworkControllerSpec) field.ErrorList {
	root := field.NewPath("spec")
	if spec == nil {
		return field.ErrorList{field.Required(root, "spec is required")}
	}

	var errs field.ErrorList
	controllerType := string(spec.Type)
	if !networkControllerTypePattern.MatchString(controllerType) || len(controllerType) > 63 {
		errs = append(errs, field.Invalid(root.Child("type"), spec.Type, "must be a lowercase DNS-label-style adapter key"))
	}

	endpoint, err := url.Parse(spec.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.Hostname() == "" {
		errs = append(errs, field.Invalid(root.Child("endpoint"), spec.Endpoint, "must be an absolute HTTPS URL"))
	} else {
		if endpoint.User != nil {
			errs = append(errs, field.Forbidden(root.Child("endpoint"), "URL userinfo is not allowed; use credentialSecretRef"))
		}
		if endpoint.RawQuery != "" || endpoint.Fragment != "" {
			errs = append(errs, field.Invalid(root.Child("endpoint"), spec.Endpoint, "query and fragment components are not allowed"))
		}
	}

	credentialPath := root.Child("credentialSecretRef").Child("name")
	if spec.CredentialSecretRef.Name == "" {
		errs = append(errs, field.Required(credentialPath, "credential Secret name is required"))
	} else if problems := utilvalidation.IsDNS1123Subdomain(spec.CredentialSecretRef.Name); len(problems) > 0 {
		errs = append(errs, field.Invalid(credentialPath, spec.CredentialSecretRef.Name, strings.Join(problems, "; ")))
	}

	intentSourcesPath := root.Child("intentSecretSources")
	if len(spec.IntentSecretSources) > 128 {
		errs = append(errs, field.TooMany(intentSourcesPath, len(spec.IntentSecretSources), 128))
	}
	aliases := make(map[string]struct{}, len(spec.IntentSecretSources))
	for i, source := range spec.IntentSecretSources {
		sourcePath := intentSourcesPath.Index(i)
		if !networkControllerTypePattern.MatchString(source.Alias) || len(source.Alias) > 63 {
			errs = append(errs, field.Invalid(sourcePath.Child("alias"), source.Alias, "must be a lowercase DNS-label-style alias"))
		}
		if _, duplicate := aliases[source.Alias]; duplicate {
			errs = append(errs, field.Duplicate(sourcePath.Child("alias"), source.Alias))
		}
		aliases[source.Alias] = struct{}{}
		if source.Name == "" {
			errs = append(errs, field.Required(sourcePath.Child("name"), "Secret name is required"))
		} else if problems := utilvalidation.IsDNS1123Subdomain(source.Name); len(problems) > 0 {
			errs = append(errs, field.Invalid(sourcePath.Child("name"), source.Name, strings.Join(problems, "; ")))
		}
		if problems := utilvalidation.IsConfigMapKey(source.Key); len(problems) > 0 {
			errs = append(errs, field.Invalid(sourcePath.Child("key"), source.Key, strings.Join(problems, "; ")))
		}
	}

	if spec.TLS != nil && spec.TLS.CAConfigMapRef != nil {
		caPath := root.Child("tls").Child("caConfigMapRef")
		if spec.TLS.InsecureSkipVerify {
			errs = append(errs, field.Invalid(root.Child("tls").Child("insecureSkipVerify"), true, "cannot be combined with caConfigMapRef"))
		}
		if spec.TLS.CAConfigMapRef.Name == "" {
			errs = append(errs, field.Required(caPath.Child("name"), "CA ConfigMap name is required"))
		} else if problems := utilvalidation.IsDNS1123Subdomain(spec.TLS.CAConfigMapRef.Name); len(problems) > 0 {
			errs = append(errs, field.Invalid(caPath.Child("name"), spec.TLS.CAConfigMapRef.Name, strings.Join(problems, "; ")))
		}
		if problems := utilvalidation.IsConfigMapKey(spec.TLS.CAConfigMapRef.Key); len(problems) > 0 {
			errs = append(errs, field.Invalid(caPath.Child("key"), spec.TLS.CAConfigMapRef.Key, strings.Join(problems, "; ")))
		}
	}

	errs = append(errs, validateDeviceAdoption(root.Child("deviceAdoption"), spec.DeviceAdoption)...)

	connectionPath := root.Child("connection")
	if requestTimeout := spec.Connection.RequestTimeout; requestTimeout != nil {
		duration := requestTimeout.Duration
		if duration <= 0 || duration > maxNetworkControllerConnectionDuration {
			errs = append(errs, field.Invalid(connectionPath.Child("requestTimeout"), duration.String(), "must be greater than 0s and at most 24h"))
		}
	}
	if healthCheckInterval := spec.Connection.HealthCheckInterval; healthCheckInterval != nil {
		duration := healthCheckInterval.Duration
		if duration < 30*time.Second || duration > maxNetworkControllerConnectionDuration {
			errs = append(errs, field.Invalid(connectionPath.Child("healthCheckInterval"), duration.String(), "must be at least 30s and at most 24h"))
		}
	}
	if concurrency := spec.Connection.MaxConcurrentRequests; concurrency < 0 || concurrency > 64 {
		errs = append(errs, field.Invalid(connectionPath.Child("maxConcurrentRequests"), concurrency, "must be between 1 and 64 when set"))
	}
	if spec.Connection.RateLimit != nil {
		ratePath := connectionPath.Child("rateLimit")
		if spec.Connection.RateLimit.RequestsPerSecond < 1 || spec.Connection.RateLimit.RequestsPerSecond > 10000 {
			errs = append(errs, field.Invalid(ratePath.Child("requestsPerSecond"), spec.Connection.RateLimit.RequestsPerSecond, "must be between 1 and 10000"))
		}
		if spec.Connection.RateLimit.Burst < 1 || spec.Connection.RateLimit.Burst > 10000 {
			errs = append(errs, field.Invalid(ratePath.Child("burst"), spec.Connection.RateLimit.Burst, "must be between 1 and 10000"))
		}
	}
	if spec.PreferredAPIVersion != "" && strings.TrimSpace(spec.PreferredAPIVersion) == "" {
		errs = append(errs, field.Invalid(root.Child("preferredAPIVersion"), spec.PreferredAPIVersion, "must not be whitespace"))
	}

	return errs
}

var deviceScopePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func validateSecretName(path *field.Path, name string) field.ErrorList {
	if name == "" {
		return field.ErrorList{field.Required(path, "Secret name is required")}
	}
	if problems := utilvalidation.IsDNS1123Subdomain(name); len(problems) > 0 {
		return field.ErrorList{field.Invalid(path, name, strings.Join(problems, "; "))}
	}
	return nil
}

// validateDeviceAdoption mirrors the CRD rules and, because overrides replace
// fields one by one, checks that every scope still resolves to a complete
// access set.
func validateDeviceAdoption(path *field.Path, a *NetworkControllerDeviceAdoption) field.ErrorList {
	if a == nil {
		return nil
	}
	var errs field.ErrorList
	if a.Enabled && a.Defaults == nil {
		errs = append(errs, field.Required(path.Child("defaults"), "required when enabled is true"))
	}
	if a.Defaults != nil {
		d := path.Child("defaults")
		if a.Defaults.Username == "" {
			errs = append(errs, field.Required(d.Child("username"), "device username is required"))
		}
		errs = append(errs, validateSecretName(d.Child("credentialSecretRef").Child("name"), a.Defaults.CredentialSecretRef.Name)...)
		errs = append(errs, validateDeviceLabels(d.Child("labels"), a.Defaults.Labels)...)
	}
	if len(a.ScopeOverrides) > 64 {
		errs = append(errs, field.TooMany(path.Child("scopeOverrides"), len(a.ScopeOverrides), 64))
	}
	seen := make(map[string]struct{}, len(a.ScopeOverrides))
	for i, o := range a.ScopeOverrides {
		p := path.Child("scopeOverrides").Index(i)
		if len(o.Scope) == 0 || len(o.Scope) > 64 || !deviceScopePattern.MatchString(o.Scope) {
			errs = append(errs, field.Invalid(p.Child("scope"), o.Scope, "must be 1-64 characters of [A-Za-z0-9_.-] starting with an alphanumeric"))
		}
		if _, dup := seen[o.Scope]; dup {
			errs = append(errs, field.Duplicate(p.Child("scope"), o.Scope))
		}
		seen[o.Scope] = struct{}{}
		errs = append(errs, validateDeviceLabels(p.Child("labels"), o.Labels)...)
		if o.CredentialSecretRef != nil {
			errs = append(errs, validateSecretName(p.Child("credentialSecretRef").Child("name"), o.CredentialSecretRef.Name)...)
		}
	}
	if r := a.Removal; r != nil {
		p := path.Child("removal")
		if g := r.EffectiveGracePeriod(); g < MinDeviceRemovalGracePeriod || g > MaxDeviceRemovalGracePeriod {
			errs = append(errs, field.Invalid(p.Child("gracePeriod"), g.String(), "must be between 10m and 720h"))
		}
		if m := r.EffectiveMaxPrunePercent(); m < 1 || m > 100 {
			errs = append(errs, field.Invalid(p.Child("maxPrunePercent"), m, "must be between 1 and 100"))
		}
		if pol := r.EffectivePolicy(); pol != DeviceRemovalRetain && pol != DeviceRemovalPrune {
			errs = append(errs, field.NotSupported(p.Child("policy"), pol, []string{string(DeviceRemovalRetain), string(DeviceRemovalPrune)}))
		}
	}
	return errs
}

// reservedDeviceLabelPrefixes cannot be set through deviceAdoption labels: the
// adapter owns its own keys, and topology keys need the administrator
// allowlist (projectedTopologyKeys).
var reservedDeviceLabelPrefixes = []string{"nd.cisco.vk/", "cisco.vk/", "topology.cisco.vk/"}

// reservedLabelDomain reports whether the key's namespace is kubernetes.io or
// k8s.io, or a subdomain of either; those are reserved for Kubernetes itself.
func reservedLabelDomain(key string) bool {
	domain, _, found := strings.Cut(key, "/")
	if !found {
		return false
	}
	for _, d := range []string{"kubernetes.io", "k8s.io"} {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	return false
}

func validateDeviceLabels(path *field.Path, labels map[string]string) field.ErrorList {
	var errs field.ErrorList
	if len(labels) > 16 {
		errs = append(errs, field.TooMany(path, len(labels), 16))
	}
	for k, v := range labels {
		p := path.Key(k)
		if problems := utilvalidation.IsQualifiedName(k); len(problems) > 0 {
			errs = append(errs, field.Invalid(p, k, strings.Join(problems, "; ")))
			continue
		}
		if reservedLabelDomain(k) {
			errs = append(errs, field.Forbidden(p, "the kubernetes.io and k8s.io label namespaces are reserved"))
		}
		for _, prefix := range reservedDeviceLabelPrefixes {
			if strings.HasPrefix(k, prefix) {
				errs = append(errs, field.Forbidden(p, "label namespace "+prefix+" is reserved"))
			}
		}
		if problems := utilvalidation.IsValidLabelValue(v); len(problems) > 0 {
			errs = append(errs, field.Invalid(p, v, strings.Join(problems, "; ")))
		}
	}
	return errs
}
