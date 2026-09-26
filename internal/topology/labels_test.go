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

package topology

import (
	"errors"
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
)

func TestNodeLabelsManagedOmitsUnknownStandardTopology(t *testing.T) {
	got, err := NodeLabels(NodeLabelsOptions{
		Mode:                   ProjectionModeManaged,
		NodeName:               "edge-01",
		Platform:               "cisco-ios-xe",
		LegacyPlatformTopology: "cisco-iosxe",
	})
	if err != nil {
		t.Fatalf("NodeLabels() error = %v", err)
	}
	if _, ok := got[v1.LabelTopologyRegion]; ok {
		t.Fatalf("managed projection invented region: %v", got)
	}
	if _, ok := got[v1.LabelTopologyZone]; ok {
		t.Fatalf("managed projection invented zone: %v", got)
	}
	if got[v1.LabelHostname] != "edge-01" || got[LabelPlatform] != "cisco-ios-xe" {
		t.Fatalf("identity labels = %v", got)
	}
}

func TestNodeLabelsStandalonePreservesLegacyTopologyFallback(t *testing.T) {
	got, err := NodeLabels(NodeLabelsOptions{
		Mode:                   ProjectionModeStandaloneCompatibility,
		NodeName:               "edge-01",
		Platform:               "cisco-ios-xe",
		LegacyPlatformTopology: "cisco-iosxe",
	})
	if err != nil {
		t.Fatalf("NodeLabels() error = %v", err)
	}
	if got[v1.LabelTopologyRegion] != "cisco-iosxe" || got[v1.LabelTopologyZone] != "cisco-iosxe" {
		t.Fatalf("legacy topology fallback = %v", got)
	}
}

func TestNodeLabelsAcceptsDeclaredAndCustomTopology(t *testing.T) {
	got, err := NodeLabels(NodeLabelsOptions{
		Mode:     ProjectionModeManaged,
		NodeName: "edge-01",
		Platform: "cisco-ios-xe",
		SourceLabels: map[string]string{
			v1.LabelTopologyRegion:                   "eu-central",
			v1.LabelTopologyZone:                     "berlin-1",
			CiscoTopologyLabelPrefix + "site":        "berlin-campus",
			CiscoTopologyLabelPrefix + "rack-domain": "eu-central.berlin-1.rack-7",
			"workload":                               "edge",
		},
	})
	if err != nil {
		t.Fatalf("NodeLabels() error = %v", err)
	}
	want := map[string]string{
		v1.LabelHostname:                         "edge-01",
		LabelPlatform:                            "cisco-ios-xe",
		LabelProvider:                            ProviderCiscoAppHosting,
		LabelType:                                TypeVirtualKubelet,
		v1.LabelTopologyRegion:                   "eu-central",
		v1.LabelTopologyZone:                     "berlin-1",
		CiscoTopologyLabelPrefix + "site":        "berlin-campus",
		CiscoTopologyLabelPrefix + "rack-domain": "eu-central.berlin-1.rack-7",
		"workload":                               "edge",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NodeLabels() = %v, want %v", got, want)
	}
}

func TestNodeLabelsRejectsIdentityOverride(t *testing.T) {
	got, err := NodeLabels(NodeLabelsOptions{
		Mode:     ProjectionModeManaged,
		NodeName: "edge-01",
		Platform: "cisco-ios-xe",
		SourceLabels: map[string]string{
			v1.LabelHostname: "attacker-controlled",
			LabelPlatform:    "cisco-nxos",
			LabelProvider:    "foreign-provider",
			LabelType:        "physical-kubelet",
		},
	})
	if !errors.Is(err, ErrLabelConflict) {
		t.Fatalf("NodeLabels() error = %v, want ErrLabelConflict", err)
	}
	if got[v1.LabelHostname] != "edge-01" ||
		got[LabelPlatform] != "cisco-ios-xe" ||
		got[LabelProvider] != ProviderCiscoAppHosting ||
		got[LabelType] != TypeVirtualKubelet {
		t.Fatalf("source overrode owned identity: %v", got)
	}
}

func TestNodeLabelsAllowsMatchingIdentityWithoutDuplicatingAuthority(t *testing.T) {
	got, err := NodeLabels(NodeLabelsOptions{
		Mode:     ProjectionModeManaged,
		NodeName: "edge-01",
		Platform: "cisco-ios-xe",
		SourceLabels: map[string]string{
			v1.LabelHostname: "edge-01",
			LabelPlatform:    "cisco-ios-xe",
			LabelProvider:    ProviderCiscoAppHosting,
			LabelType:        TypeVirtualKubelet,
		},
	})
	if err != nil {
		t.Fatalf("NodeLabels() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("identity projection = %v", got)
	}
}

func TestNodeLabelsStandalonePreservesLegacyLastWriterWins(t *testing.T) {
	got, err := NodeLabels(NodeLabelsOptions{
		Mode:     ProjectionModeStandaloneCompatibility,
		NodeName: "edge-01",
		Platform: "cisco-ios-xe",
		Region:   "eu-central",
		Zone:     "berlin-1",
		SourceLabels: map[string]string{
			v1.LabelTopologyRegion: "us-west",
			v1.LabelTopologyZone:   "berlin-2",
			LabelProvider:          "legacy-override",
		},
	})
	if err != nil {
		t.Fatalf("NodeLabels() error = %v", err)
	}
	if got[v1.LabelTopologyRegion] != "us-west" || got[v1.LabelTopologyZone] != "berlin-2" ||
		got[LabelProvider] != "legacy-override" {
		t.Fatalf("legacy source-label precedence changed: %v", got)
	}
}

func TestNodeLabelsRejectsInvalidSourceData(t *testing.T) {
	tests := []struct {
		name   string
		opts   NodeLabelsOptions
		badKey string
	}{
		{
			name: "invalid key",
			opts: NodeLabelsOptions{SourceLabels: map[string]string{
				"not a valid key": "value",
			}},
			badKey: "not a valid key",
		},
		{
			name: "empty custom topology domain",
			opts: NodeLabelsOptions{SourceLabels: map[string]string{
				CiscoTopologyLabelPrefix + "site": "",
			}},
			badKey: CiscoTopologyLabelPrefix + "site",
		},
		{
			name: "empty standard topology domain",
			opts: NodeLabelsOptions{SourceLabels: map[string]string{
				v1.LabelTopologyZone: "",
			}},
			badKey: v1.LabelTopologyZone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.opts.Mode = ProjectionModeManaged
			tt.opts.NodeName = "edge-01"
			tt.opts.Platform = "cisco-ios-xe"
			got, err := NodeLabels(tt.opts)
			if !errors.Is(err, ErrInvalidLabel) {
				t.Fatalf("NodeLabels() error = %v, want ErrInvalidLabel", err)
			}
			if _, ok := got[tt.badKey]; ok {
				t.Fatalf("invalid label was projected: %v", got)
			}
		})
	}
}

func TestNodeLabelsRejectsMissingIdentity(t *testing.T) {
	got, err := NodeLabels(NodeLabelsOptions{Mode: ProjectionModeManaged})
	if !errors.Is(err, ErrInvalidLabel) {
		t.Fatalf("NodeLabels() error = %v, want ErrInvalidLabel", err)
	}
	if _, ok := got[v1.LabelHostname]; ok {
		t.Fatalf("empty hostname identity was projected: %v", got)
	}
	if _, ok := got[LabelPlatform]; ok {
		t.Fatalf("empty platform identity was projected: %v", got)
	}
}

func TestIsReservedLabel(t *testing.T) {
	for _, key := range []string{
		v1.LabelHostname,
		LabelPlatform,
		LabelProvider,
		LabelType,
		v1.LabelTopologyRegion,
		v1.LabelTopologyZone,
	} {
		if !IsReservedLabel(key) {
			t.Errorf("IsReservedLabel(%q) = false", key)
		}
	}
	for _, key := range []string{CiscoTopologyLabelPrefix + "site", "workload"} {
		if IsReservedLabel(key) {
			t.Errorf("IsReservedLabel(%q) = true", key)
		}
	}
}
