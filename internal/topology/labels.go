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

// Package topology owns normalization of the scheduling identity and topology
// labels published on Cisco virtual Nodes.
package topology

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	v1 "k8s.io/api/core/v1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	// Identity label keys are written by CVK and cannot be overridden by a
	// caller-provided source label map.
	LabelPlatform = "platform"
	LabelProvider = "provider"
	LabelType     = "type"

	ProviderCiscoAppHosting = "cisco-apphosting"
	TypeVirtualKubelet      = "virtual-kubelet"

	CiscoTopologyLabelPrefix = "topology.cisco.vk/"
)

var (
	// ErrLabelConflict identifies two authorities supplying different values
	// for the same CVK-owned label.
	ErrLabelConflict = errors.New("node label ownership conflict")
	// ErrInvalidLabel identifies an invalid label key or value in a projection.
	ErrInvalidLabel = errors.New("invalid node label")
)

// ProjectionMode controls compatibility behavior when region or zone is not
// declared. Managed mode is the safe zero value: unknown topology is omitted.
type ProjectionMode uint8

const (
	ProjectionModeManaged ProjectionMode = iota
	// ProjectionModeStandaloneCompatibility preserves the historical behavior
	// of using the platform family as an otherwise-unknown region and zone.
	ProjectionModeStandaloneCompatibility
)

// NodeLabelsOptions describes the inputs to a Node label projection.
//
// SourceLabels can contain ordinary custom labels, topology.cisco.vk/* labels,
// and the standard region and zone keys. In managed mode Region and Zone are
// typed authorities and may only agree with SourceLabels. Standalone mode
// retains the legacy final-map-write precedence of SourceLabels.
type NodeLabelsOptions struct {
	Mode ProjectionMode

	NodeName string
	Platform string

	Region string
	Zone   string

	// LegacyPlatformTopology is used only in standalone compatibility mode and
	// only for a standard topology label with no declared source value.
	LegacyPlatformTopology string

	SourceLabels map[string]string
}

// IsReservedLabel reports whether key is part of CVK's fixed Node identity or
// standard topology contract. Custom topology.cisco.vk/* keys are valid source
// labels and are owned through the caller's approved projection policy.
func IsReservedLabel(key string) bool {
	switch key {
	case v1.LabelHostname,
		LabelPlatform,
		LabelProvider,
		LabelType,
		v1.LabelTopologyRegion,
		v1.LabelTopologyZone:
		return true
	default:
		return false
	}
}

// NodeLabels returns one normalized identity/topology label projection.
// Conflicting or invalid source data is never allowed to replace an owned
// value. The returned map is safe for diagnostics, but callers must treat a
// non-nil error as a failed projection rather than publish a partial result.
func NodeLabels(opts NodeLabelsOptions) (map[string]string, error) {
	if opts.Mode == ProjectionModeStandaloneCompatibility {
		// Preserve the pre-topology-awareness merge contract exactly. Managed
		// mode below is the opt-in ownership boundary; changing precedence or
		// validation for standalone Nodes would make merely upgrading CVK a
		// scheduling migration.
		labels := map[string]string{
			v1.LabelHostname:       opts.NodeName,
			LabelPlatform:          opts.Platform,
			LabelProvider:          ProviderCiscoAppHosting,
			LabelType:              TypeVirtualKubelet,
			v1.LabelTopologyRegion: opts.LegacyPlatformTopology,
			v1.LabelTopologyZone:   opts.LegacyPlatformTopology,
		}
		if opts.Region != "" {
			labels[v1.LabelTopologyRegion] = opts.Region
		}
		if opts.Zone != "" {
			labels[v1.LabelTopologyZone] = opts.Zone
		}
		for key, value := range opts.SourceLabels {
			labels[key] = value
		}
		return labels, nil
	}
	labels := make(map[string]string, len(opts.SourceLabels)+6)
	var projectionErrs []error

	if opts.Mode != ProjectionModeManaged {
		projectionErrs = append(projectionErrs, fmt.Errorf("%w: unsupported projection mode %d", ErrInvalidLabel, opts.Mode))
	}

	identity := map[string]string{
		v1.LabelHostname: opts.NodeName,
		LabelPlatform:    opts.Platform,
		LabelProvider:    ProviderCiscoAppHosting,
		LabelType:        TypeVirtualKubelet,
	}
	for _, key := range []string{v1.LabelHostname, LabelPlatform, LabelProvider, LabelType} {
		value := identity[key]
		if err := validateLabel(key, value, true); err != nil {
			projectionErrs = append(projectionErrs, err)
			continue
		}
		labels[key] = value
	}

	var sourceRegion, sourceZone string
	var sourceRegionSet, sourceZoneSet bool
	keys := make([]string, 0, len(opts.SourceLabels))
	for key := range opts.SourceLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := opts.SourceLabels[key]
		nonEmpty := key == v1.LabelTopologyRegion || key == v1.LabelTopologyZone || strings.HasPrefix(key, CiscoTopologyLabelPrefix)
		if err := validateLabel(key, value, nonEmpty); err != nil {
			projectionErrs = append(projectionErrs, err)
			continue
		}

		switch key {
		case v1.LabelHostname, LabelPlatform, LabelProvider, LabelType:
			if value != identity[key] {
				projectionErrs = append(projectionErrs, labelConflict(key, identity[key], value))
			}
		case v1.LabelTopologyRegion:
			sourceRegion = value
			sourceRegionSet = true
		case v1.LabelTopologyZone:
			sourceZone = value
			sourceZoneSet = true
		default:
			labels[key] = value
		}
	}

	region, err := resolveTopologyValue(
		v1.LabelTopologyRegion,
		opts.Region,
		sourceRegion,
		sourceRegionSet,
		opts.LegacyPlatformTopology,
		opts.Mode,
	)
	if err != nil {
		projectionErrs = append(projectionErrs, err)
	} else if region != "" {
		labels[v1.LabelTopologyRegion] = region
	}

	zone, err := resolveTopologyValue(
		v1.LabelTopologyZone,
		opts.Zone,
		sourceZone,
		sourceZoneSet,
		opts.LegacyPlatformTopology,
		opts.Mode,
	)
	if err != nil {
		projectionErrs = append(projectionErrs, err)
	} else if zone != "" {
		labels[v1.LabelTopologyZone] = zone
	}

	return labels, errors.Join(projectionErrs...)
}

func resolveTopologyValue(key, declared, sourced string, sourcedSet bool, legacyFallback string, mode ProjectionMode) (string, error) {
	if declared != "" {
		if err := validateLabel(key, declared, true); err != nil {
			return "", err
		}
		if sourcedSet && sourced != declared {
			return "", labelConflict(key, declared, sourced)
		}
		return declared, nil
	}
	if sourcedSet {
		return sourced, nil
	}
	if mode != ProjectionModeStandaloneCompatibility || legacyFallback == "" {
		return "", nil
	}
	if err := validateLabel(key, legacyFallback, true); err != nil {
		return "", err
	}
	return legacyFallback, nil
}

func validateLabel(key, value string, requireValue bool) error {
	if problems := utilvalidation.IsQualifiedName(key); len(problems) > 0 {
		return fmt.Errorf("%w: key %q: %s", ErrInvalidLabel, key, strings.Join(problems, "; "))
	}
	if requireValue && value == "" {
		return fmt.Errorf("%w: label %q must have a non-empty value", ErrInvalidLabel, key)
	}
	if problems := utilvalidation.IsValidLabelValue(value); len(problems) > 0 {
		return fmt.Errorf("%w: value %q for %q: %s", ErrInvalidLabel, value, key, strings.Join(problems, "; "))
	}
	return nil
}

func labelConflict(key, authoritative, supplied string) error {
	return fmt.Errorf("%w: %q is %q but source supplied %q", ErrLabelConflict, key, authoritative, supplied)
}
