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

// Package engine exposes the neutral reconciliation state machine. The first
// extraction step aliases the established IOS-XE implementation.
package engine

import (
	"github.com/prometheus/client_golang/prometheus"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	iosxeengine "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
)

type (
	Engine          = iosxeengine.Engine
	ReconcilePolicy = iosxeengine.ReconcilePolicy
	Result          = iosxeengine.Result
	FamilyStatus    = iosxeengine.FamilyStatus
	DriftEntry      = iosxeengine.DriftEntry
	FamilyLeaser    = iosxeengine.FamilyLeaser
	LeaseResult     = iosxeengine.LeaseResult
)

const (
	PhasePending      = iosxeengine.PhasePending
	PhaseValidating   = iosxeengine.PhaseValidating
	PhasePlanning     = iosxeengine.PhasePlanning
	PhaseApplying     = iosxeengine.PhaseApplying
	PhaseVerifying    = iosxeengine.PhaseVerifying
	PhaseInSync       = iosxeengine.PhaseInSync
	PhaseDrifted      = iosxeengine.PhaseDrifted
	PhaseFailed       = iosxeengine.PhaseFailed
	PhasePaused       = iosxeengine.PhasePaused
	PhaseLeaseBlocked = iosxeengine.PhaseLeaseBlocked
	MaxDriftEntries   = iosxeengine.MaxDriftEntries
)

var ErrTransactionalCLIUnsupported = iosxeengine.ErrTransactionalCLIUnsupported

func ConflictCheck(deviceName string, allForDevice []*configv1alpha1.IOSXEConfig) map[string][]string {
	return iosxeengine.ConflictCheck(deviceName, allForDevice)
}

func LeaseName(device, family string) string {
	return iosxeengine.LeaseName(device, family)
}

func CapDrift(in []DriftEntry) (out []DriftEntry, dropped int) {
	return iosxeengine.CapDrift(in)
}

func RecordDriftTruncated(device string, dropped int) {
	iosxeengine.RecordDriftTruncated(device, dropped)
}

func RecordDriftTruncatedForRelease(device, release string, dropped int) {
	iosxeengine.RecordDriftTruncatedForRelease(device, release, dropped)
}

func RegisterMetrics(reg prometheus.Registerer) {
	iosxeengine.RegisterMetrics(reg)
}
