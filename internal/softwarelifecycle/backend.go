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

// Package softwarelifecycle defines the platform-neutral, optional capability
// used to inspect native software inventory and, where supported, register a
// device-resident image before the standard gNOI OS activation flow uses it.
package softwarelifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ValidateTargetVersion enforces the shared version syntax accepted by the
// software-upgrade API and native inventory adapters.
func ValidateTargetVersion(target string) error {
	if target == "" || len(target) > 128 || strings.TrimSpace(target) != target {
		return fmt.Errorf("target version has invalid length or surrounding whitespace")
	}
	segments := strings.Split(target, ".")
	if len(segments) < 2 {
		return fmt.Errorf("target version must contain at least two numeric segments")
	}
	for segmentIndex, segment := range segments {
		if segment == "" {
			return fmt.Errorf("target version contains an empty segment")
		}
		for charIndex, c := range segment {
			if c >= '0' && c <= '9' {
				continue
			}
			isFinalSuffix := segmentIndex == len(segments)-1 &&
				charIndex == len(segment)-1 && charIndex > 0 && c >= 'a' && c <= 'z'
			if !isFinalSuffix {
				return fmt.Errorf("target version contains unsupported characters")
			}
		}
	}
	return nil
}

var (
	// ErrUnsupported means the selected platform or transport cannot register a
	// device-local image. Callers must fail closed rather than invoking gNOI
	// OS.Activate with an unverified version string.
	ErrUnsupported = errors.New("software lifecycle capability unsupported")

	// ErrTargetNotFound means no inventory identity matched the requested target.
	ErrTargetNotFound = errors.New("software lifecycle target not found")

	// ErrAmbiguousTarget means a shortened target matched more than one exact
	// device inventory identity.
	ErrAmbiguousTarget = errors.New("software lifecycle target is ambiguous")

	// ErrOperationNotFound means neither the active nor historical operation
	// inventory contains the supplied operation identifier.
	ErrOperationNotFound = errors.New("software lifecycle operation not found")

	// ErrAmbiguousOperation means a supposedly unique operation identifier was
	// reused and cannot be observed safely.
	ErrAmbiguousOperation = errors.New("software lifecycle operation is ambiguous")

	// ErrInvalidDevicePath and ErrInvalidOperationID report inputs rejected
	// before any device mutation is attempted.
	ErrInvalidDevicePath  = errors.New("invalid device image path")
	ErrInvalidOperationID = errors.New("invalid software lifecycle operation ID")
)

// InventoryState is the normalized platform-neutral state of one exact image
// identity. Drivers map their native inventory states into this closed set.
type InventoryState string

const (
	InventoryStateAbsent                 InventoryState = "Absent"
	InventoryStatePresent                InventoryState = "Present"
	InventoryStateInstalled              InventoryState = "Installed"
	InventoryStateProvisionedUncommitted InventoryState = "ProvisionedUncommitted"
	InventoryStateProvisionedCommitted   InventoryState = "ProvisionedCommitted"
	InventoryStateInProgress             InventoryState = "InProgress"
	InventoryStateInvalid                InventoryState = "Invalid"
	InventoryStateUnknown                InventoryState = "Unknown"
)

// Activatable reports whether the platform explicitly identifies an image as
// installed and available for activation. Merely present, in-progress, and all
// provisioned/current states intentionally return false.
func (s InventoryState) Activatable() bool {
	return s == InventoryStateInstalled
}

// InventoryImage is one exact activation identity selected from the device's
// inventory. Version is the exact value to pass to gNOI OS.Activate, including
// any platform-provided version extension.
type InventoryImage struct {
	Version    string
	State      InventoryState
	SourcePath string
}

// DeviceFileRequest identifies a path-based registration mutation. OperationID
// must be a canonical UUID and must be persisted by the caller before it sends
// the request so reconciliation can observe rather than replay the mutation.
type DeviceFileRequest struct {
	Path        string
	OperationID string
}

// DeviceFileRegistration confirms that the device accepted a registration
// request. Acceptance is not completion; callers must poll ObserveDeviceFile.
type DeviceFileRegistration struct {
	OperationID string
}

// OperationState is the normalized state of a path-based registration RPC.
type OperationState string

const (
	OperationStateUnknown    OperationState = "Unknown"
	OperationStatePending    OperationState = "Pending"
	OperationStateInProgress OperationState = "InProgress"
	OperationStateSucceeded  OperationState = "Succeeded"
	OperationStateFailed     OperationState = "Failed"
)

// DeviceFileObservation combines the correlated operation state with the
// exact target inventory identity, when one is available. Succeeded alone does
// not authorize activation: Image must be non-nil and Activatable must be true.
type DeviceFileObservation struct {
	OperationID string
	State       OperationState
	Image       *InventoryImage
}

// Backend is an optional platform capability. Generic gNOI byte transfer and
// activation do not depend on it; preinstalled image selection, device-file
// registration, install observation, and rollback inventory checks do.
type Backend interface {
	Inspect(ctx context.Context, targetVersion string) (InventoryImage, error)
	ValidateDeviceFilePath(path string) error
	RegisterDeviceFile(ctx context.Context, request DeviceFileRequest) (DeviceFileRegistration, error)
	ObserveDeviceFile(ctx context.Context, operationID, targetVersion string) (DeviceFileObservation, error)
}
