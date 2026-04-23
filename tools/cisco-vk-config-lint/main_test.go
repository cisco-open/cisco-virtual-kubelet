// Copyright © 2026 Cisco Systems, Inc.
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
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validIOSXEConfig = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: edge-01, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan, system]
  source:
    configMapRef: {name: edge-01-data, key: data.nac.yaml}
`

const unknownFamilyConfig = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: edge-01, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan, not_a_real_family]
  source:
    inline: {system: {hostname: x}}
`

const missingSource = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: edge-01, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan]
`

const badDriftPolicy = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: edge-01, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan]
  driftPolicy: revoke
  source: {inline: {}}
`

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "cr.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestLintAcceptsValidConfig(t *testing.T) {
	p := writeTemp(t, validIOSXEConfig)
	var out, errBuf bytes.Buffer
	code := run([]string{p}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d stderr=%q", code, errBuf.String())
	}
}

func TestLintRejectsUnknownFamily(t *testing.T) {
	p := writeTemp(t, unknownFamilyConfig)
	var out, errBuf bytes.Buffer
	code := run([]string{p}, &out, &errBuf)
	if code != exitViolation {
		t.Fatalf("exit=%d, want exitViolation", code)
	}
	if !strings.Contains(errBuf.String(), "not_a_real_family") {
		t.Errorf("stderr missing family name:\n%s", errBuf.String())
	}
}

func TestLintRejectsMissingSource(t *testing.T) {
	p := writeTemp(t, missingSource)
	var out, errBuf bytes.Buffer
	code := run([]string{p}, &out, &errBuf)
	if code != exitViolation {
		t.Fatalf("exit=%d, want exitViolation", code)
	}
	if !strings.Contains(errBuf.String(), "neither inline nor configMapRef") {
		t.Errorf("stderr missing source error:\n%s", errBuf.String())
	}
}

func TestLintRejectsInvalidDriftPolicy(t *testing.T) {
	p := writeTemp(t, badDriftPolicy)
	var out, errBuf bytes.Buffer
	code := run([]string{p}, &out, &errBuf)
	if code != exitViolation {
		t.Fatalf("exit=%d, want exitViolation", code)
	}
	if !strings.Contains(errBuf.String(), `driftPolicy="revoke"`) {
		t.Errorf("stderr missing driftPolicy reason:\n%s", errBuf.String())
	}
}

func TestLintSkipsForeignKindsByDefault(t *testing.T) {
	body := `
apiVersion: apps/v1
kind: Deployment
metadata: {name: ignored}
`
	p := writeTemp(t, body)
	var out, errBuf bytes.Buffer
	code := run([]string{p}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d stderr=%q", code, errBuf.String())
	}
}

func TestLintStrictRejectsForeignKinds(t *testing.T) {
	body := `
apiVersion: apps/v1
kind: Deployment
metadata: {name: ignored}
`
	p := writeTemp(t, body)
	var out, errBuf bytes.Buffer
	code := run([]string{"--strict", p}, &out, &errBuf)
	if code != exitViolation {
		t.Fatalf("exit=%d, want exitViolation", code)
	}
}

func TestLintAcceptsMultiDoc(t *testing.T) {
	body := validIOSXEConfig + "\n---\n" + validIOSXEConfig
	p := writeTemp(t, body)
	var out, errBuf bytes.Buffer
	code := run([]string{p}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d stderr=%q", code, errBuf.String())
	}
}

func TestLintVerboseReportsOKFiles(t *testing.T) {
	p := writeTemp(t, validIOSXEConfig)
	var out, errBuf bytes.Buffer
	code := run([]string{"--verbose", p}, &out, &errBuf)
	if code != exitOK {
		t.Fatalf("exit=%d stderr=%q", code, errBuf.String())
	}
	if !strings.HasPrefix(out.String(), "ok ") {
		t.Errorf("verbose output missing 'ok' line:\n%s", out.String())
	}
}

func TestLintRejectsUnreadablePath(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"/no/such/path"}, &out, &errBuf)
	if code != exitBadInput {
		t.Fatalf("exit=%d, want exitBadInput", code)
	}
}

const unknownInlineFamily = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: edge-01, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan]
  source:
    inline:
      vlan: {vlans: [{id: 10, name: users}]}
      no_such_family: {foo: bar}
`

const missingKeyField = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: edge-01, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan]
  source:
    inline:
      vlan: {vlans: [{name: users_only, description: "no id"}]}
`

const wrongInnerShape = `
apiVersion: config.cisco.vk/v1alpha1
kind: IOSXEConfig
metadata: {name: edge-01, namespace: network}
spec:
  deviceRef: {name: edge-01}
  managedFamilies: [vlan]
  source:
    inline:
      vlan: {vlans: "not a list"}
`

func TestLintInlineUnknownFamily(t *testing.T) {
	p := writeTemp(t, unknownInlineFamily)
	var out, errBuf bytes.Buffer
	code := run([]string{p}, &out, &errBuf)
	if code != exitViolation {
		t.Fatalf("exit=%d, want exitViolation", code)
	}
	if !strings.Contains(errBuf.String(), "no_such_family") {
		t.Errorf("stderr missing family name:\n%s", errBuf.String())
	}
}

func TestLintInlineMissingKeyField(t *testing.T) {
	p := writeTemp(t, missingKeyField)
	var out, errBuf bytes.Buffer
	code := run([]string{p}, &out, &errBuf)
	if code != exitViolation {
		t.Fatalf("exit=%d, want exitViolation", code)
	}
	if !strings.Contains(errBuf.String(), `missing key field "id"`) {
		t.Errorf("stderr missing key-field diagnostic:\n%s", errBuf.String())
	}
}

func TestLintInlineWrongInnerShape(t *testing.T) {
	p := writeTemp(t, wrongInnerShape)
	var out, errBuf bytes.Buffer
	code := run([]string{p}, &out, &errBuf)
	if code != exitViolation {
		t.Fatalf("exit=%d, want exitViolation", code)
	}
	if !strings.Contains(errBuf.String(), "expected list") {
		t.Errorf("stderr missing shape diagnostic:\n%s", errBuf.String())
	}
}
