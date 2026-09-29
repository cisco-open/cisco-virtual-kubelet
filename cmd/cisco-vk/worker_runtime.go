// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

type workerMode string

const (
	workerModeCombined          workerMode = managedprotocol.WorkerModeCombined
	workerModeAppHosting        workerMode = managedprotocol.WorkerModeAppHosting
	workerModeNetworkManagement workerMode = managedprotocol.WorkerModeNetworkManagement
)

type workerAccess string

const (
	workerAccessReadOnly  workerAccess = managedprotocol.WorkerAccessReadOnly
	workerAccessReadWrite workerAccess = managedprotocol.WorkerAccessReadWrite
)

type workerRuntimeProfile struct {
	Mode   workerMode
	Access workerAccess
}

func resolveWorkerRuntimeProfile(flagMode, flagAccess string, managedTopology bool) (workerRuntimeProfile, error) {
	modeValue := strings.TrimSpace(flagMode)
	if modeValue == "" {
		modeValue = strings.TrimSpace(os.Getenv(managedprotocol.EnvWorkerMode))
	}
	if modeValue == "" {
		modeValue = string(workerModeCombined)
	}

	accessValue := strings.TrimSpace(flagAccess)
	if accessValue == "" {
		accessValue = strings.TrimSpace(os.Getenv(managedprotocol.EnvWorkerAccess))
	}
	if accessValue == "" {
		accessValue = string(workerAccessReadWrite)
	}

	profile := workerRuntimeProfile{Mode: workerMode(modeValue), Access: workerAccess(accessValue)}
	if err := profile.validate(managedTopology); err != nil {
		return workerRuntimeProfile{}, err
	}
	return profile, nil
}

func (p workerRuntimeProfile) validate(managedTopology bool) error {
	switch p.Mode {
	case workerModeCombined, workerModeAppHosting, workerModeNetworkManagement:
	default:
		return fmt.Errorf("invalid worker mode %q: valid values are %q, %q, and %q",
			p.Mode, workerModeCombined, workerModeAppHosting, workerModeNetworkManagement)
	}
	switch p.Access {
	case workerAccessReadOnly, workerAccessReadWrite:
	default:
		return fmt.Errorf("invalid worker access %q: valid values are %q and %q",
			p.Access, workerAccessReadOnly, workerAccessReadWrite)
	}

	if managedTopology && p.Mode == workerModeCombined {
		return fmt.Errorf("worker mode %q is a standalone compatibility mode and is not permitted with managed topology; run separate %q and %q workers",
			workerModeCombined, workerModeAppHosting, workerModeNetworkManagement)
	}
	if p.runsAppHosting() && p.Access == workerAccessReadOnly {
		return fmt.Errorf("worker mode %q cannot run with access %q: a Virtual Kubelet provider can receive Pod create, update, and delete work; use %q or do not start an app-hosting worker",
			p.Mode, workerAccessReadOnly, workerAccessReadWrite)
	}
	return nil
}

func (p workerRuntimeProfile) runsAppHosting() bool {
	return p.Mode == workerModeCombined || p.Mode == workerModeAppHosting
}

func (p workerRuntimeProfile) runsNetworkManagement() bool {
	return p.Mode == workerModeCombined || p.Mode == workerModeNetworkManagement
}

func (p workerRuntimeProfile) constrainNetworkOptions(opts configReconcilerOptions) configReconcilerOptions {
	opts.ReadOnly = p.Access == workerAccessReadOnly
	if opts.ReadOnly {
		// Runtime enforcement is intentional defense in depth: a broad or stale
		// RoleBinding must not turn a read-only worker into a device writer.
		opts.EnableWriteClassGNOI = false
		opts.EnableIOSXESoftwareUpgrade = false
	}
	return opts
}
