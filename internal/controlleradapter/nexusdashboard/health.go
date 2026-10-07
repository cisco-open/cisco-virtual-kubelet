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

package nexusdashboard

import (
	"errors"
	"reflect"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

const maxMessageLen = 512

// healthResult is the sanitized outcome of one probe.
type healthResult struct {
	phase         ciskov1.NetworkControllerPhase
	authenticated bool
	compatible    bool
	reason        string
	message       string
}

func (r healthResult) ready() bool { return r.authenticated && r.compatible }

func (r healthResult) authMessage() string {
	if r.authenticated {
		return "login to Nexus Dashboard succeeded"
	}
	return r.message
}

func (r healthResult) compatMessage() string {
	switch {
	case r.compatible:
		return "Manage API /api/v1/manage is available"
	case r.authenticated:
		return r.message
	default:
		return "not evaluated: authentication did not succeed"
	}
}

// classify maps a probe error to conditions. Messages are redacted and bounded.
func classify(err error) healthResult {
	if err == nil {
		return healthResult{
			phase: ciskov1.NetworkControllerPhaseReady, authenticated: true, compatible: true,
			reason: "Connected", message: "Nexus Dashboard is reachable and the Manage API responded",
		}
	}
	msg := bound(transport.RedactCredentials(err.Error()))
	var restErr *transport.RESTError
	switch {
	case errors.Is(err, errCredentials):
		return healthResult{phase: ciskov1.NetworkControllerPhaseError, reason: "InvalidCredentials", message: msg}
	case errors.Is(err, errAuthentication):
		return healthResult{phase: ciskov1.NetworkControllerPhaseError, reason: "AuthenticationFailed",
			message: "Nexus Dashboard rejected the supplied credentials"}
	case errors.As(err, &restErr) && restErr.StatusCode == 404:
		// Logged in, but the Manage API is missing: ND older than 4.x /api/v1/manage.
		return healthResult{phase: ciskov1.NetworkControllerPhaseDegraded, authenticated: true,
			reason: "ManageAPIUnavailable", message: msg}
	case errors.As(err, &restErr) && restErr.AuthFailure():
		// Authenticated but authorized to nothing useful (403 on the probe).
		return healthResult{phase: ciskov1.NetworkControllerPhaseDegraded, authenticated: true,
			reason: "Forbidden", message: msg}
	default:
		return healthResult{phase: ciskov1.NetworkControllerPhaseDegraded, reason: "Unreachable", message: msg}
	}
}

func bound(s string) string {
	if len(s) > maxMessageLen {
		return s[:maxMessageLen]
	}
	return s
}

func setCapability(st *ciskov1.NetworkControllerStatus, name string, supported bool, message string) {
	entry := ciskov1.NetworkControllerCapabilityStatus{Name: name, Supported: supported, Message: bound(message)}
	for i := range st.Capabilities {
		if st.Capabilities[i].Name == name {
			st.Capabilities[i] = entry
			return
		}
	}
	st.Capabilities = append(st.Capabilities, entry)
}

// equalStatus ignores LastAttemptTime so unchanged health does not churn the API.
func equalStatus(a, b *ciskov1.NetworkController) bool {
	x, y := a.Status.DeepCopy(), b.Status.DeepCopy()
	x.LastAttemptTime, y.LastAttemptTime = nil, nil
	return reflect.DeepEqual(x, y)
}
