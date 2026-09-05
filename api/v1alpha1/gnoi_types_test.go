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
	"strings"
	"testing"
)

func TestXEGNOIConfigValidateCertificateProvisioning(t *testing.T) {
	valid := func() (*XEGNOIConfig, *GNOIConfig) {
		return &XEGNOIConfig{
			CertificateProvisioning: &XEGNOICertificateProvisioning{
				CertificateID:         "cvk-gnoi_17.18",
				SecretRef:             XEGNOIProvisioningSecretReference{Name: "router-gnoi-certificate"},
				ReplaceTargetCABundle: true,
			},
		}, &GNOIConfig{TransportSecurity: GNOITransportSecurityTLS}
	}

	tests := []struct {
		name    string
		mutate  func(*XEGNOIConfig, *GNOIConfig)
		wantErr string
	}{
		{name: "valid"},
		{
			name: "requires explicit tls",
			mutate: func(_ *XEGNOIConfig, gnoi *GNOIConfig) {
				gnoi.TransportSecurity = GNOITransportSecurityAuto
			},
			wantErr: "requires spec.gnoi.transportSecurity to be tls",
		},
		{
			name: "certificate id required",
			mutate: func(cfg *XEGNOIConfig, _ *GNOIConfig) {
				cfg.CertificateProvisioning.CertificateID = ""
			},
			wantErr: "certificateID is required",
		},
		{
			name: "certificate id pattern",
			mutate: func(cfg *XEGNOIConfig, _ *GNOIConfig) {
				cfg.CertificateProvisioning.CertificateID = "bad/id"
			},
			wantErr: "must match",
		},
		{
			name: "certificate id length",
			mutate: func(cfg *XEGNOIConfig, _ *GNOIConfig) {
				cfg.CertificateProvisioning.CertificateID = strings.Repeat("a", 65)
			},
			wantErr: "at most 64",
		},
		{
			name: "secret name required",
			mutate: func(cfg *XEGNOIConfig, _ *GNOIConfig) {
				cfg.CertificateProvisioning.SecretRef.Name = ""
			},
			wantErr: "secretRef.name is required",
		},
		{
			name: "secret name must be dns subdomain",
			mutate: func(cfg *XEGNOIConfig, _ *GNOIConfig) {
				cfg.CertificateProvisioning.SecretRef.Name = "INVALID_SECRET"
			},
			wantErr: "secretRef.name is invalid",
		},
		{
			name: "CA bundle replacement acknowledgement required",
			mutate: func(cfg *XEGNOIConfig, _ *GNOIConfig) {
				cfg.CertificateProvisioning.ReplaceTargetCABundle = false
			},
			wantErr: "replaceTargetCABundle must be true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, gnoi := valid()
			if tt.mutate != nil {
				tt.mutate(cfg, gnoi)
			}
			err := cfg.Validate(gnoi)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestGNOIConfigValidateTransportSecurity(t *testing.T) {
	for _, mode := range []GNOITransportSecurity{"", GNOITransportSecurityAuto, GNOITransportSecurityTLS} {
		if err := (&GNOIConfig{TransportSecurity: mode}).Validate(); err != nil {
			t.Errorf("Validate() mode %q: %v", mode, err)
		}
	}
	if err := (&GNOIConfig{TransportSecurity: "plaintext"}).Validate(); err == nil {
		t.Fatal("Validate() accepted plaintext gNOI transport")
	}
}
