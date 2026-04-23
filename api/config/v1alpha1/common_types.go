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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DriftPolicy controls how the config driver responds when the device's
// observed configuration no longer matches the resolved intent.
//
// +kubebuilder:validation:Enum=revert;report;pause
type DriftPolicy string

const (
	// DriftPolicyRevert rewrites the managed families back to the declared
	// intent. This is the default and matches continuous-reconciliation
	// GitOps semantics.
	DriftPolicyRevert DriftPolicy = "revert"

	// DriftPolicyReport records drift in status without writing. Used during
	// parallel-run cutovers from an existing pipeline (see RFC §11).
	DriftPolicyReport DriftPolicy = "report"

	// DriftPolicyPause disables reconciliation until the annotation is cleared.
	// Intended as a break-glass for live CLI troubleshooting.
	DriftPolicyPause DriftPolicy = "pause"
)

// DeviceRef names a CiscoDevice in the same namespace as the referring CR.
type DeviceRef struct {
	// Name of the CiscoDevice the CR targets.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// ConfigurationSource provides the netascode-shaped configuration body.
// Exactly one of the fields must be set; the driver rejects a CR that sets
// neither or both.
type ConfigurationSource struct {
	// Inline is an inline netascode configuration. The payload is the content
	// of an iosxe.devices[].configuration block; the field is schemaless so
	// family shape evolves with the YANG release, not the CRD.
	//
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Inline *runtime.RawExtension `json:"inline,omitempty"`

	// ConfigMapRef references a ConfigMap in the same namespace whose keyed
	// entry holds a netascode YAML document. Used for large configurations
	// that would otherwise bloat the CR past etcd's per-object limit.
	// +optional
	ConfigMapRef *ConfigMapKeyRef `json:"configMapRef,omitempty"`
}

// ConfigMapKeyRef selects one key from a namespaced ConfigMap.
type ConfigMapKeyRef struct {
	// Name of the ConfigMap (same namespace as the CR).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key within the ConfigMap whose value is the netascode YAML body.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// TemplateRef refers to an IOSXETemplate and supplies parameter values used
// during expansion.
type TemplateRef struct {
	// Name of the IOSXETemplate (same namespace as the referring CR).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Values overrides the template's parameter defaults. Shape is validated
	// against the template's declared parameters at expansion time.
	//
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Values *runtime.RawExtension `json:"values,omitempty"`
}

// FamilyStatus reports per-family reconciliation state.
type FamilyStatus struct {
	// Name of the netascode family (e.g. "vlan", "interface_ethernet").
	Name string `json:"name"`

	// State is one of Pending, InSync, Drifted, ApplyError, Skipped,
	// Unsupported. The taxonomy is closed; unknown values are rejected.
	// +kubebuilder:validation:Enum=Pending;InSync;Drifted;ApplyError;Skipped;Unsupported
	State string `json:"state"`

	// Entries counts the number of keyed items the driver observed for this
	// family on the device (e.g. number of VLANs). Zero for singleton families.
	// +optional
	Entries int32 `json:"entries,omitempty"`

	// Message is a short human-readable description. Empty when state is InSync.
	// +optional
	Message string `json:"message,omitempty"`
}

// DriftEntry records a single observed-vs-desired divergence. Paths are YANG
// xpaths so the entry is unambiguous regardless of the family's shape.
type DriftEntry struct {
	// Family the drift belongs to.
	Family string `json:"family"`

	// Path is the YANG xpath of the leaf or list key that differs.
	Path string `json:"path"`

	// Desired is the declared value (optional; omitted for deletes).
	// +optional
	Desired string `json:"desired,omitempty"`

	// Observed is the value currently on the device (optional; omitted for
	// leaves absent from the device).
	// +optional
	Observed string `json:"observed,omitempty"`

	// Detected is the time the driver first observed this specific entry.
	// +optional
	Detected metav1.Time `json:"detected,omitempty"`
}
