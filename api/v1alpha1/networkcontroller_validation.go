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
