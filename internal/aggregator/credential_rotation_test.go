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

package aggregator

// Wave 6B regression tests for external-review-followup Finding #5
// against the aggregator path. specHash must change when the
// resolved password changes; otherwise the aggregator never
// restarts the worker holding the stale credential. The pre-fix
// hash recorded only "password is non-empty" (a bool), so a
// rotation went undetected.

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestSpecHash_PasswordChangeRotatesHash(t *testing.T) {
	t.Parallel()
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "d1", Namespace: "n"},
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverFAKE,
			Address:  "10.0.0.1",
			Username: "u",
		},
	}
	a := specHash(dev, "passwordA")
	b := specHash(dev, "passwordB")
	if a == b {
		t.Errorf("specHash must change when password changes: a=%q b=%q", a, b)
	}
}

func TestSpecHash_SamePasswordSameHash(t *testing.T) {
	t.Parallel()
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "d1", Namespace: "n"},
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverFAKE,
			Address:  "10.0.0.1",
			Username: "u",
		},
	}
	a := specHash(dev, "p")
	b := specHash(dev, "p")
	if a != b {
		t.Errorf("specHash must be stable across calls: a=%q b=%q", a, b)
	}
}

func TestSpecHash_EmptyPasswordHandled(t *testing.T) {
	t.Parallel()
	dev := &ciskov1.CiscoDevice{
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverFAKE,
			Address:  "10.0.0.1",
			Username: "u",
		},
	}
	h := specHash(dev, "")
	if !strings.HasSuffix(h, "|empty") {
		t.Errorf("expected empty-password sentinel in hash, got %q", h)
	}
}

func TestPasswordDigest_NotCleartext(t *testing.T) {
	t.Parallel()
	// passwordDigest must NOT embed the cleartext — that would put
	// the credential into the in-memory deviceWorker struct's
	// observable state.
	pwd := "topSecretPassword123"
	d := passwordDigest(pwd)
	if d == pwd {
		t.Errorf("passwordDigest returned cleartext: %q", d)
	}
	if strings.Contains(d, pwd) {
		t.Errorf("passwordDigest output contains cleartext substring: %q", d)
	}
}
