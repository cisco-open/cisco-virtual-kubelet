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

package controller

import (
	"testing"
)

// OTEL_EXPORTER_OTLP_HEADERS must NOT appear in the literal-value propagation
// list — its content can carry collector auth tokens and copying them into
// every per-device pod spec leaks them to anyone with `get pod` on the
// device's namespace. The SecretKeyRef path
// (propagatedTelemetryHeadersEnvVar) is the supported way to mirror them.
func TestTelemetryEnvPropagationOmitsOTLPHeaders(t *testing.T) {
	for _, name := range telemetryEnvPropagationNames {
		if name == envOTELExporterOTLPHeaders {
			t.Fatalf("%s must not appear in telemetryEnvPropagationNames; use the SecretKeyRef path via headersSecret instead", envOTELExporterOTLPHeaders)
		}
	}
}

// When neither CVK_OTLP_HEADERS_SECRET_NAME nor the inline-headers Secret
// reference is configured, the helper returns nil so per-device pods get
// no OTLP_HEADERS env var at all (rather than an unsafe empty literal).
func TestPropagatedTelemetryHeadersEnvVarUnsetReturnsNil(t *testing.T) {
	t.Setenv(envCVKOTLPHeadersSecretName, "")
	t.Setenv(envCVKOTLPHeadersSecretKey, "")
	if hdr := propagatedTelemetryHeadersEnvVar(); hdr != nil {
		t.Fatalf("expected nil when no Secret ref configured, got %+v", hdr)
	}
}

// When the controller is configured with the chart's headersSecret, the
// helper returns an EnvVar whose ValueFrom.SecretKeyRef matches.
func TestPropagatedTelemetryHeadersEnvVarFromSecretRef(t *testing.T) {
	t.Setenv(envCVKOTLPHeadersSecretName, "otlp-auth")
	t.Setenv(envCVKOTLPHeadersSecretKey, "headers")

	hdr := propagatedTelemetryHeadersEnvVar()
	if hdr == nil {
		t.Fatalf("expected non-nil envvar")
	}
	if hdr.Name != envOTELExporterOTLPHeaders {
		t.Fatalf("name=%q want %q", hdr.Name, envOTELExporterOTLPHeaders)
	}
	if hdr.Value != "" {
		t.Fatalf("value should be empty when ValueFrom is set; got %q", hdr.Value)
	}
	if hdr.ValueFrom == nil || hdr.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected ValueFrom.SecretKeyRef, got %+v", hdr.ValueFrom)
	}
	if hdr.ValueFrom.SecretKeyRef.Name != "otlp-auth" {
		t.Fatalf("secret name=%q want otlp-auth", hdr.ValueFrom.SecretKeyRef.Name)
	}
	if hdr.ValueFrom.SecretKeyRef.Key != "headers" {
		t.Fatalf("secret key=%q want headers", hdr.ValueFrom.SecretKeyRef.Key)
	}
}

// Default-key fallback: when only the name is set, the helper assumes the
// Secret stores the headers under a key matching the env var name.
func TestPropagatedTelemetryHeadersEnvVarDefaultsKey(t *testing.T) {
	t.Setenv(envCVKOTLPHeadersSecretName, "otlp-auth")
	t.Setenv(envCVKOTLPHeadersSecretKey, "")

	hdr := propagatedTelemetryHeadersEnvVar()
	if hdr == nil || hdr.ValueFrom == nil || hdr.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("expected SecretKeyRef envvar, got %+v", hdr)
	}
	if hdr.ValueFrom.SecretKeyRef.Key != envOTELExporterOTLPHeaders {
		t.Fatalf("default key=%q want %q", hdr.ValueFrom.SecretKeyRef.Key, envOTELExporterOTLPHeaders)
	}
}
