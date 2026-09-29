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

package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestRolloutSourceSelectionPrefersMatchingScopedPriority(t *testing.T) {
	image := rolloutSourceImageFixture()
	candidates, err := compileRolloutSources(image, []string{
		"topology.cisco.vk/site",
		"topology.kubernetes.io/region",
	})
	if err != nil {
		t.Fatalf("compileRolloutSources() error = %v", err)
	}

	for _, test := range []struct {
		name   string
		labels labels.Set
		want   string
	}{
		{
			name: "lowest priority matching scoped source",
			labels: labels.Set{
				"topology.cisco.vk/site":        "berlin",
				"topology.kubernetes.io/region": "eu-central",
			},
			want: "eu-regional",
		},
		{
			name:   "scoped source precedes lower-numbered fallback",
			labels: labels.Set{"topology.cisco.vk/site": "berlin"},
			want:   "berlin-local",
		},
		{
			name:   "catch-all for unmatched target",
			labels: labels.Set{"topology.cisco.vk/site": "hamburg"},
			want:   "global",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selected, err := selectRolloutSourceIndex(candidates, test.labels)
			if err != nil {
				t.Fatalf("selectRolloutSourceIndex() error = %v", err)
			}
			if got := candidates[selected].spec.Name; got != test.want {
				t.Fatalf("selected source = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRolloutSourceSelectionRejectsEqualPriorityMatches(t *testing.T) {
	image := rolloutSourceImageFixture()
	image.Sources[1].Priority = image.Sources[2].Priority
	candidates, err := compileRolloutSources(image, []string{
		"topology.cisco.vk/site",
		"topology.kubernetes.io/region",
	})
	if err != nil {
		t.Fatalf("compileRolloutSources() error = %v", err)
	}
	_, err = selectRolloutSourceIndex(candidates, labels.Set{
		"topology.cisco.vk/site":        "berlin",
		"topology.kubernetes.io/region": "eu-central",
	})
	if err == nil || !strings.Contains(err.Error(), "equal priority") {
		t.Fatalf("selectRolloutSourceIndex() error = %v, want equal-priority ambiguity", err)
	}
}

func TestCompileRolloutSourcesFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*opsv1alpha1.IOSXESoftwareRolloutImageSpec)
		want   string
	}{
		{
			name: "invalid image digest",
			mutate: func(image *opsv1alpha1.IOSXESoftwareRolloutImageSpec) {
				image.SHA256 = ""
			},
			want: "image SHA256",
		},
		{
			name: "invalid image family grammar",
			mutate: func(image *opsv1alpha1.IOSXESoftwareRolloutImageSpec) {
				image.ImageFamily = "cat9k family"
			},
			want: "image family",
		},
		{
			name: "invalid image family length",
			mutate: func(image *opsv1alpha1.IOSXESoftwareRolloutImageSpec) {
				image.ImageFamily = strings.Repeat("a", 64)
			},
			want: "image family",
		},
		{
			name: "selector outside administrator topology",
			mutate: func(image *opsv1alpha1.IOSXESoftwareRolloutImageSpec) {
				image.Sources[1].DeviceSelector.MatchLabels = map[string]string{"untrusted.example/placement": "berlin"}
			},
			want: "not an administrator-required topology key",
		},
		{
			name: "duplicate source name",
			mutate: func(image *opsv1alpha1.IOSXESoftwareRolloutImageSpec) {
				image.Sources[1].Name = image.Sources[0].Name
			},
			want: "duplicated",
		},
		{
			name: "invalid source name",
			mutate: func(image *opsv1alpha1.IOSXESoftwareRolloutImageSpec) {
				image.Sources[1].Name = "Berlin_Local"
			},
			want: "invalid name",
		},
		{
			name: "no catch-all",
			mutate: func(image *opsv1alpha1.IOSXESoftwareRolloutImageSpec) {
				image.Sources[0].DeviceSelector = &opsv1alpha1.IOSXESoftwareRolloutLabelSelector{
					MatchLabels: map[string]string{"topology.cisco.vk/site": "global"},
				}
			},
			want: "exactly one unscoped catch-all; found 0",
		},
		{
			name: "multiple catch-alls",
			mutate: func(image *opsv1alpha1.IOSXESoftwareRolloutImageSpec) {
				image.Sources[1].DeviceSelector = &opsv1alpha1.IOSXESoftwareRolloutLabelSelector{}
			},
			want: "exactly one unscoped catch-all; found 2",
		},
		{
			name: "malformed selector",
			mutate: func(image *opsv1alpha1.IOSXESoftwareRolloutImageSpec) {
				image.Sources[1].DeviceSelector = &opsv1alpha1.IOSXESoftwareRolloutLabelSelector{
					MatchExpressions: []opsv1alpha1.IOSXESoftwareRolloutLabelSelectorRequirement{{
						Key: "topology.cisco.vk/site", Operator: metav1.LabelSelectorOperator("Unexpected"),
					}},
				}
			},
			want: "selector",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			image := rolloutSourceImageFixture()
			test.mutate(&image)
			_, err := compileRolloutSources(image, []string{
				"topology.cisco.vk/site",
				"topology.kubernetes.io/region",
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("compileRolloutSources() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseRolloutSourceURLRejectsUnsafeOrIncompleteURLs(t *testing.T) {
	for _, rawURL := range []string{
		"http://images.example.test/cat9k.bin",
		"https://user:password@images.example.test/cat9k.bin",
		"https://images.example.test/cat9k.bin?token=secret",
		"https://images.example.test/cat9k.bin?",
		"https://images.example.test/cat9k.bin#fragment",
		"https://images.example.test/",
		"https://images.example.test",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := parseRolloutSourceURL(rawURL); err == nil {
				t.Fatalf("parseRolloutSourceURL(%q) succeeded", rawURL)
			}
		})
	}
	for _, rawURL := range []string{
		"https://images.example.test/cat9k.bin",
		"sftp://images.example.test/cat9k.bin",
	} {
		t.Run("accept "+rawURL, func(t *testing.T) {
			if _, err := parseRolloutSourceURL(rawURL); err != nil {
				t.Fatalf("parseRolloutSourceURL(%q) error = %v", rawURL, err)
			}
		})
	}
}

func rolloutSourceImageFixture() opsv1alpha1.IOSXESoftwareRolloutImageSpec {
	return opsv1alpha1.IOSXESoftwareRolloutImageSpec{
		SHA256:      strings.Repeat("a", 64),
		ImageFamily: "cat9k",
		Sources: []opsv1alpha1.IOSXESoftwareRolloutSourceSpec{
			{
				Name: "global", Priority: 0,
				URL: "https://images.example.test/cat9k.bin",
			},
			{
				Name: "berlin-local", Priority: 100,
				DeviceSelector: &opsv1alpha1.IOSXESoftwareRolloutLabelSelector{
					MatchLabels: map[string]string{"topology.cisco.vk/site": "berlin"},
				},
				URL: "https://berlin.images.example.test/cat9k.bin",
			},
			{
				Name: "eu-regional", Priority: 10,
				DeviceSelector: &opsv1alpha1.IOSXESoftwareRolloutLabelSelector{
					MatchLabels: map[string]string{"topology.kubernetes.io/region": "eu-central"},
				},
				URL: "https://eu.images.example.test/cat9k.bin",
			},
		},
	}
}
