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

package main

import (
	"context"
	"path/filepath"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestSetupGNOIPropagatesTLSConfigError(t *testing.T) {
	t.Setenv(gNOIDisabledEnv, "")
	t.Setenv(gNOIInsecureEnv, "")
	t.Setenv(gNOIPortEnv, "")

	provider, cleanup, err := setupGNOI(context.Background(), configReconcilerOptions{
		Spec: &ciskov1.DeviceSpec{
			Address: "192.0.2.1",
			TLS: &ciskov1.TLSConfig{
				Enabled: true,
				CAFile:  filepath.Join(t.TempDir(), "missing-ca.pem"),
			},
		},
	})
	if err == nil {
		t.Fatal("setupGNOI returned nil error for an unreadable CA file")
	}
	if provider != nil {
		t.Fatalf("setupGNOI provider = %#v, want nil after TLS error", provider)
	}
	if cleanup != nil {
		t.Fatal("setupGNOI returned cleanup after TLS error, want nil")
	}
}
