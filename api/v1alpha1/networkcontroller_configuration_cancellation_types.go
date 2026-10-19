// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package v1alpha1

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NetworkControllerConfigurationGroupCancellation preserves the exact cancelled
// operation and reservation incarnations. It cannot represent submitted work.
// +kubebuilder:validation:XValidation:rule="self.operationID == self.operation.id && self.operation.phase == 'Cancelling' && has(self.operation.physicalGroup) && has(self.operation.cancellationRequestedAt) && self.settledAt >= self.operation.cancellationRequestedAt",message="group cancellation receipt requires its exact cancelled operation"
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="retained group cancellation evidence is immutable"
type NetworkControllerConfigurationGroupCancellation struct {
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{32}$`
	// +kubebuilder:validation:MaxLength=32
	OperationID string                                  `json:"operationID"`
	Operation   NetworkControllerConfigurationOperation `json:"operation"`
	// Members binds the Kubernetes UID of each retained member reservation.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1000
	// +listType=map
	// +listMapKey=name
	Members   []ops.ConfigurationReservationReference `json:"members"`
	SettledAt metav1.Time                             `json:"settledAt"`
}
