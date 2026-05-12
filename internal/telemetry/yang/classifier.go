// Copyright 2026 Cisco Systems Inc.
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

package yang

import "github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"

type Classifier struct {
	registry *Registry
	fallback classifier.Classifier
}

func NewClassifier(registry *Registry, fallback classifier.Classifier) classifier.Classifier {
	return Classifier{registry: registry, fallback: fallback}
}

func (c Classifier) Classify(canonicalPath string) classifier.MetricKind {
	if c.registry != nil {
		if kind, ok := c.registry.Lookup(canonicalPath); ok {
			return kind
		}
	}
	if c.fallback != nil {
		return c.fallback.Classify(canonicalPath)
	}
	return classifier.MetricKindGauge
}
