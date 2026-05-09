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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateIOSXETelemetry returns a Kubernetes-style invalid-object error for
// constraints that also exist in the generated CRD schema. Reconcilers call
// this as a defensive backstop for tests and clusters where admission has been
// bypassed.
func ValidateIOSXETelemetry(t *IOSXETelemetry) error {
	if t == nil {
		return apierrors.NewInvalid(
			GroupVersion.WithKind("IOSXETelemetry").GroupKind(),
			"",
			field.ErrorList{field.Required(field.NewPath("iosxetelemetry"), "object is required")},
		)
	}
	errs := ValidateIOSXETelemetrySpec(&t.Spec)
	if len(errs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(GroupVersion.WithKind("IOSXETelemetry").GroupKind(), t.Name, errs)
}

// ValidateIOSXETelemetrySpec enforces the Phase 1 v1alpha1 contract.
func ValidateIOSXETelemetrySpec(spec *IOSXETelemetrySpec) field.ErrorList {
	var errs field.ErrorList
	root := field.NewPath("spec")
	if spec == nil {
		return field.ErrorList{field.Required(root, "spec is required")}
	}
	if len(spec.Subscriptions) == 0 {
		errs = append(errs, field.Required(root.Child("subscriptions"), "at least one subscription is required"))
	}
	for i, sub := range spec.Subscriptions {
		p := root.Child("subscriptions").Index(i)
		if sub.Mode != TelemetryModeStream {
			errs = append(errs, field.NotSupported(p.Child("mode"), sub.Mode, []string{TelemetryModeStream}))
		}
		if sub.SuppressRedundant != nil && *sub.SuppressRedundant && sub.StreamMode != TelemetryStreamModeOnChange {
			errs = append(errs, field.Invalid(
				p.Child("suppressRedundant"),
				*sub.SuppressRedundant,
				"suppressRedundant requires streamMode=ON_CHANGE",
			))
		}
		if sub.HeartbeatInterval != nil && sub.StreamMode != TelemetryStreamModeOnChange {
			errs = append(errs, field.Invalid(
				p.Child("heartbeatInterval"),
				sub.HeartbeatInterval.Duration.String(),
				"heartbeatInterval requires streamMode=ON_CHANGE",
			))
		}
		switch sub.Encoding {
		case "", TelemetryEncodingProto, TelemetryEncodingJSONIETF:
		default:
			errs = append(errs, field.NotSupported(p.Child("encoding"), sub.Encoding,
				[]string{TelemetryEncodingProto, TelemetryEncodingJSONIETF}))
		}
	}
	if spec.CardinalityLimits != nil {
		if spec.CardinalityLimits.MaxSeriesPerSubscription < 1 {
			errs = append(errs, field.Invalid(
				root.Child("cardinalityLimits").Child("maxSeriesPerSubscription"),
				spec.CardinalityLimits.MaxSeriesPerSubscription,
				"must be at least 1",
			))
		}
		switch spec.CardinalityLimits.OnExceeded {
		case "", TelemetryOnExceededDropNewSeries:
		default:
			errs = append(errs, field.NotSupported(
				root.Child("cardinalityLimits").Child("onExceeded"),
				spec.CardinalityLimits.OnExceeded,
				[]string{TelemetryOnExceededDropNewSeries},
			))
		}
	}
	for i, signal := range spec.Output.Signal {
		switch signal {
		case TelemetrySignalMetrics, TelemetrySignalLogs, TelemetrySignalTraces:
		default:
			errs = append(errs, field.NotSupported(
				root.Child("output").Child("signal").Index(i),
				signal,
				[]string{TelemetrySignalMetrics, TelemetrySignalLogs, TelemetrySignalTraces},
			))
		}
	}
	if spec.Mapping != nil {
		for i, transition := range spec.Mapping.Transitions {
			p := root.Child("mapping").Child("transitions").Index(i)
			if transition.Path == "" {
				errs = append(errs, field.Required(p.Child("path"), "path is required"))
			}
			if len(transition.HealthyValues) == 0 {
				errs = append(errs, field.Required(p.Child("healthyValues"), "at least one healthy value is required"))
			}
			if len(transition.UnhealthyValues) == 0 {
				errs = append(errs, field.Required(p.Child("unhealthyValues"), "at least one unhealthy value is required"))
			}
		}
	}
	return errs
}
