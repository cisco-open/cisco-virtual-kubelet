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

//go:build envtest
// +build envtest

package provider

// Real-apiserver smoke tests for the ops.cisco.vk CRDs introduced by
// the gNOI pillar:
//
//   - DeviceOperation (extended with read-only gNOI kinds)
//   - IOSXESoftwareUpgrade (new)
//   - IOSXEOperationalAction (new, write-class destructive ops)
//
// Why these matter: the fake.Client used by unit tests skips OpenAPI
// validation. The lab-run that drove the gNOI pilot end-to-end on
// C9K-4 surfaced one regression that ONLY an apiserver-level test
// could catch: the targetVersion regex in IOSXESoftwareUpgrade was
// too tight to accept Cisco's full version string
// "26.01.01.0.340.1775586976". Commit 119d457 relaxed the regex; the
// scenarios below pin both the relaxed pattern AND the prefix-match
// contract so the next regex tightening fails CI rather than
// production.
//
// Same build tag and invocation as the IOSXEConfig envtest scenarios
// in envtest_apiserver_smoke_test.go: `make test-envtest`.

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

// --- DeviceOperation ---

// TestEnvtest_DeviceOperationGNOIKindsAccepted proves every new gNOI
// OperationKind value introduced in commit b9aa95a admits cleanly at
// the apiserver. Prior to b9aa95a the enum capped at
// ShowCommand;ConfigDiff;PacketCapture — a typo in the kubebuilder
// marker would have shipped silently because the fake.Client doesn't
// enforce enums.
func TestEnvtest_DeviceOperationGNOIKindsAccepted(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-devop-gnoi-kinds")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	kinds := []opsv1alpha1.OperationKind{
		opsv1alpha1.OperationKindGNOIPing,
		opsv1alpha1.OperationKindGNOITraceroute,
		opsv1alpha1.OperationKindGNOITime,
		opsv1alpha1.OperationKindGNOIFileGet,
		opsv1alpha1.OperationKindGNOIFileStat,
		opsv1alpha1.OperationKindGNOICertGet,
		opsv1alpha1.OperationKindGNOICanGenerateCSR,
		opsv1alpha1.OperationKindGNOIRebootStatus,
		opsv1alpha1.OperationKindGNOIOSVerify,
	}

	for i, kind := range kinds {
		name := strings.ToLower(string(kind))
		op := newDeviceOpForKind(name, "envtest-devop-gnoi-kinds", kind)
		if err := c.Create(ctx, op); err != nil {
			t.Errorf("[%d] kind %q rejected by apiserver: %v", i, kind, err)
		}
	}
}

// TestEnvtest_DeviceOperationLegacyKindStillAccepted is the negative
// control for the kind enum: existing read-only kinds must not
// regress when we extend the enum.
func TestEnvtest_DeviceOperationLegacyKindStillAccepted(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-devop-legacy")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	op := newDeviceOpForKind("show", "envtest-devop-legacy", opsv1alpha1.OperationKindShowCommand)
	op.Spec.Operation.Commands = []string{"show version"}
	if err := c.Create(ctx, op); err != nil {
		t.Fatalf("ShowCommand rejected: %v", err)
	}
}

// TestEnvtest_DeviceOperationBogusKindRejected confirms the enum is
// exhaustive. A misspelled / future kind must NOT slip through.
func TestEnvtest_DeviceOperationBogusKindRejected(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-devop-bogus")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	op := newDeviceOpForKind("bogus", "envtest-devop-bogus", opsv1alpha1.OperationKind("GNOITypo"))
	err := c.Create(ctx, op)
	if err == nil {
		t.Fatal("apiserver admitted bogus kind GNOITypo; enum is not enforced")
	}
	if !strings.Contains(err.Error(), "unsupported value") && !strings.Contains(err.Error(), "Unsupported value") {
		t.Fatalf("expected enum-rejection error, got %v", err)
	}
}

// TestEnvtest_DeviceOperationGNOIFilePathPatternEnforced pins the
// IOS-XE filesystem-prefix pattern on operation.gnoi.file.path.
// Operators must not be able to slip a bare relative path through —
// the reconciler also validates, but apiserver-level rejection gives
// a clean error before the workload is queued.
func TestEnvtest_DeviceOperationGNOIFilePathPatternEnforced(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-devop-filepath")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name     string
		path     string
		wantPass bool
	}{
		{"valid-flash", "flash:cat9k.bin", true},
		{"valid-bootflash", "bootflash:cat9k.bin", true},
		{"valid-harddisk", "harddisk:cat9k.bin", true},
		{"valid-usbflash0", "usbflash0:cat9k.bin", true},
		{"valid-nvram", "nvram:config.bin", true},
		{"valid-webui", "webui:status.bin", true},
		{"invalid-relative", "cat9k.bin", false},
		{"invalid-unixpath", "/tmp/cat9k.bin", false},
		{"invalid-emptyprefix", ":cat9k.bin", false},
		{"invalid-unknownfs", "scratch:cat9k.bin", false},
	}
	for _, tc := range cases {
		op := newDeviceOpForKind(tc.name, "envtest-devop-filepath", opsv1alpha1.OperationKindGNOIFileStat)
		op.Spec.Operation.GNOI = &opsv1alpha1.GNOIArgs{
			File: &opsv1alpha1.GNOIFileArgs{Path: tc.path},
		}
		err := c.Create(ctx, op)
		switch {
		case tc.wantPass && err != nil:
			t.Errorf("%s: path %q rejected unexpectedly: %v", tc.name, tc.path, err)
		case !tc.wantPass && err == nil:
			t.Errorf("%s: path %q admitted but should be rejected", tc.name, tc.path)
		}
	}
}

// --- IOSXESoftwareUpgrade ---

// TestEnvtest_IOSXESoftwareUpgradeMultiSegmentVersionAccepted pins the
// regex relaxation from commit 119d457. The original pattern
// '^[0-9]+\.[0-9]+(\.[0-9]+([a-z])?)?$' rejected the full Cisco YANG
// form "26.01.01.0.340.1775586976" that the gNOI server actually
// expects. The relaxed pattern '^[0-9]+(\.[0-9]+)+([a-z])?$' must
// admit it.
func TestEnvtest_IOSXESoftwareUpgradeMultiSegmentVersionAccepted(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-upgrade-version")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name    string
		version string
	}{
		{"three-segment-release-letter", "17.15.01a"},
		{"three-segment-build", "26.01.01"},
		{"five-segment-install-summary", "26.01.01.0.340"},
		{"six-segment-yang-full", "26.01.01.0.340.1775586976"},
		{"six-segment-oper-data", "17.18.02.0.4112.1766116039"},
	}
	for _, tc := range cases {
		up := newUpgrade(tc.name, "envtest-upgrade-version", tc.version)
		if err := c.Create(ctx, up); err != nil {
			t.Errorf("%s: targetVersion %q rejected: %v", tc.name, tc.version, err)
		}
	}
}

// TestEnvtest_IOSXESoftwareUpgradeInvalidVersionRejected pins the
// negative side of the relaxed regex: filenames, paths, and
// non-numeric-leading strings must still bounce at the apiserver.
func TestEnvtest_IOSXESoftwareUpgradeInvalidVersionRejected(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-upgrade-bad-version")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []string{
		"cat9k_iosxe.26.01.01.SPA.bin",
		"26.01.01.0.340.bin",
		"flash:cat9k.bin",
		"26", // single segment — no dot
		"v17.15.01",
		"",
	}
	for _, ver := range cases {
		name := "bad-" + strings.ReplaceAll(strings.ReplaceAll(ver, ":", ""), "/", "")
		if name == "bad-" {
			name = "bad-empty"
		}
		// CRD names cap at 63 chars and don't allow dots; sanitise.
		name = sanitiseEnvtestName(name)
		up := newUpgrade(name, "envtest-upgrade-bad-version", ver)
		err := c.Create(ctx, up)
		if err == nil {
			t.Errorf("apiserver admitted invalid version %q", ver)
		}
	}
}

// TestEnvtest_IOSXESoftwareUpgradeStrategyEnumEnforced pins the
// kubebuilder enum on .spec.strategy. Adding a new strategy value
// without updating the enum would silently default to "Reload" today;
// this catches the missing enum entry.
func TestEnvtest_IOSXESoftwareUpgradeStrategyEnumEnforced(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-upgrade-strategy")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Positive control — all three enum values admitted.
	for _, s := range []opsv1alpha1.UpgradeStrategy{
		opsv1alpha1.UpgradeStrategyReload,
		opsv1alpha1.UpgradeStrategyISSU,
		opsv1alpha1.UpgradeStrategyNoReboot,
	} {
		up := newUpgrade("ok-"+strings.ToLower(string(s)), "envtest-upgrade-strategy", "17.15.01")
		up.Spec.Strategy = s
		if err := c.Create(ctx, up); err != nil {
			t.Errorf("strategy %q rejected: %v", s, err)
		}
	}

	// Negative — unknown strategy must be rejected.
	up := newUpgrade("bogus-strategy", "envtest-upgrade-strategy", "17.15.01")
	up.Spec.Strategy = opsv1alpha1.UpgradeStrategy("BlueGreen")
	err := c.Create(ctx, up)
	if err == nil {
		t.Fatal("apiserver admitted bogus strategy 'BlueGreen'")
	}
	if !strings.Contains(err.Error(), "supported value") && !strings.Contains(err.Error(), "Unsupported value") {
		t.Fatalf("expected enum-rejection error, got %v", err)
	}
}

// TestEnvtest_IOSXESoftwareUpgradeImageSourcePathPattern pins the
// IOS-XE filesystem-prefix regex on .spec.imageSource.localPath. Same
// rationale as DeviceOperation's gnoi.file.path.
func TestEnvtest_IOSXESoftwareUpgradeImageSourcePathPattern(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-upgrade-localpath")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name     string
		path     string
		wantPass bool
	}{
		{"valid-flash", "flash:cat9k_iosxe.26.01.01.SPA.bin", true},
		{"valid-bootflash", "bootflash:cat9k.bin", true},
		{"valid-harddisk", "harddisk:cat9k.bin", true},
		{"invalid-relative", "cat9k.bin", false},
		{"invalid-unixpath", "/tmp/cat9k.bin", false},
	}
	for _, tc := range cases {
		up := newUpgrade(tc.name, "envtest-upgrade-localpath", "26.01.01")
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{LocalPath: tc.path}
		err := c.Create(ctx, up)
		switch {
		case tc.wantPass && err != nil:
			t.Errorf("%s: localPath %q rejected: %v", tc.name, tc.path, err)
		case !tc.wantPass && err == nil:
			t.Errorf("%s: localPath %q admitted but should be rejected", tc.name, tc.path)
		}
	}
}

// TestEnvtest_IOSXESoftwareUpgradeImageSourceURLSchemePattern pins the
// URL schemes accepted by the CRD. The reconciler still requires SHA256
// for every URL source and streams all fetched bytes through gNOI
// OS.Install; this test only protects admission-level scheme support.
func TestEnvtest_IOSXESoftwareUpgradeImageSourceURLSchemePattern(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-upgrade-url-scheme")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	validSHA := strings.Repeat("a", 64)
	cases := []struct {
		name     string
		url      string
		wantPass bool
	}{
		{"valid-http", "http://images.example.com/cat9k.bin", true},
		{"valid-https", "https://images.example.com/cat9k.bin", true},
		{"valid-tftp", "tftp://198.51.100.20/images/cat9k.bin", true},
		{"valid-ftp", "ftp://images.example.com/cat9k.bin", true},
		{"valid-scp", "scp://images.example.com/home/images/cat9k.bin", true},
		{"valid-sftp", "sftp://images.example.com/home/images/cat9k.bin", true},
		{"invalid-file", "file:///home/cisco/images/cat9k.bin", false},
		{"invalid-rsync", "rsync://images.example.com/cat9k.bin", false},
	}
	for _, tc := range cases {
		up := newUpgrade(tc.name, "envtest-upgrade-url-scheme", "26.01.01")
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{URL: tc.url, SHA256: validSHA}
		err := c.Create(ctx, up)
		switch {
		case tc.wantPass && err != nil:
			t.Errorf("%s: URL %q rejected: %v", tc.name, tc.url, err)
		case !tc.wantPass && err == nil:
			t.Errorf("%s: URL %q admitted but should be rejected", tc.name, tc.url)
		}
	}
}

// --- IOSXEOperationalAction ---

// TestEnvtest_IOSXEOperationalActionConfirmRequired pins the
// safety-guard contract: spec.confirm must be provided AND have
// MinLength=1. Reconciler-side enforcement checks confirm ==
// deviceRef.name; the apiserver enforces presence. Together they
// implement the kubectl-drain-style typo guard.
func TestEnvtest_IOSXEOperationalActionConfirmRequired(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-opaction-confirm")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Missing confirm → rejected.
	act := newOpAction("noconfirm", "envtest-opaction-confirm", opsv1alpha1.ActionKindReboot)
	act.Spec.Confirm = ""
	if err := c.Create(ctx, act); err == nil {
		t.Fatal("apiserver admitted empty spec.confirm")
	}

	// Present confirm → admitted (reconciler does the value match
	// separately at runtime).
	ok := newOpAction("withconfirm", "envtest-opaction-confirm", opsv1alpha1.ActionKindReboot)
	ok.Spec.Confirm = "dev1"
	if err := c.Create(ctx, ok); err != nil {
		t.Fatalf("apiserver rejected valid CR with confirm set: %v", err)
	}
}

// TestEnvtest_IOSXEOperationalActionKindEnumEnforced pins
// .spec.action.kind to the six destructive kinds.
func TestEnvtest_IOSXEOperationalActionKindEnumEnforced(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-opaction-kind")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, k := range []opsv1alpha1.ActionKind{
		opsv1alpha1.ActionKindReboot,
		opsv1alpha1.ActionKindCancelReboot,
		opsv1alpha1.ActionKindKillProcess,
		opsv1alpha1.ActionKindFilePut,
		opsv1alpha1.ActionKindFileRemove,
		opsv1alpha1.ActionKindFactoryReset,
	} {
		name := "ok-" + strings.ToLower(string(k))
		act := newOpAction(name, "envtest-opaction-kind", k)
		if err := c.Create(ctx, act); err != nil {
			t.Errorf("kind %q rejected: %v", k, err)
		}
	}

	bogus := newOpAction("bogus", "envtest-opaction-kind", opsv1alpha1.ActionKind("Erase"))
	if err := c.Create(ctx, bogus); err == nil {
		t.Fatal("apiserver admitted bogus action kind 'Erase'")
	}
}

// TestEnvtest_IOSXEOperationalActionFilePathPattern pins the IOS-XE
// filesystem-prefix regex on FilePut.path and FileRemove.path. The
// pattern in the CRD is slightly different from
// IOSXESoftwareUpgrade.localPath (omits crashinfo/nvram/webui by
// design — destructive write-ops should not touch those filesystems).
func TestEnvtest_IOSXEOperationalActionFilePathPattern(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	envtestNamespace(t, c, "envtest-opaction-filepath")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cases := []struct {
		name     string
		path     string
		wantPass bool
	}{
		{"valid-flash", "flash:dropoff.bin", true},
		{"valid-bootflash", "bootflash:dropoff.bin", true},
		{"valid-harddisk", "harddisk:dropoff.bin", true},
		{"valid-usbflash0", "usbflash0:dropoff.bin", true},
		{"valid-usbflash1", "usbflash1:dropoff.bin", true},
		{"invalid-nvram", "nvram:config", false},
		{"invalid-crashinfo", "crashinfo:dump", false},
		{"invalid-relative", "dropoff.bin", false},
	}
	for _, tc := range cases {
		act := newOpAction(tc.name, "envtest-opaction-filepath", opsv1alpha1.ActionKindFileRemove)
		act.Spec.Action.FileRemove = &opsv1alpha1.FileRemoveArgs{Path: tc.path}
		err := c.Create(ctx, act)
		switch {
		case tc.wantPass && err != nil:
			t.Errorf("%s: path %q rejected: %v", tc.name, tc.path, err)
		case !tc.wantPass && err == nil:
			t.Errorf("%s: path %q admitted but should be rejected", tc.name, tc.path)
		}
	}
}

// --- helpers ---

func newDeviceOpForKind(name, namespace string, kind opsv1alpha1.OperationKind) *opsv1alpha1.DeviceOperation {
	return &opsv1alpha1.DeviceOperation{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: sanitiseEnvtestName(name)},
		Spec: opsv1alpha1.DeviceOperationSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev1"},
			Operation: opsv1alpha1.DeviceOperationRequest{Kind: kind},
		},
	}
}

func newUpgrade(name, namespace, targetVersion string) *opsv1alpha1.IOSXESoftwareUpgrade {
	return &opsv1alpha1.IOSXESoftwareUpgrade{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: sanitiseEnvtestName(name)},
		Spec: opsv1alpha1.IOSXESoftwareUpgradeSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev1"},
			ImageSource: opsv1alpha1.UpgradeImageSource{
				LocalPath: "flash:cat9k.bin",
			},
			TargetVersion: targetVersion,
		},
	}
}

func newOpAction(name, namespace string, kind opsv1alpha1.ActionKind) *opsv1alpha1.IOSXEOperationalAction {
	a := &opsv1alpha1.IOSXEOperationalAction{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: sanitiseEnvtestName(name)},
		Spec: opsv1alpha1.IOSXEOperationalActionSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "dev1"},
			Confirm:   "dev1",
			Action:    opsv1alpha1.ActionRequest{Kind: kind},
		},
	}
	// Per-kind required sub-blocks. The CRD doesn't enforce
	// presence (the reconciler does), but populating sensible
	// defaults here keeps the test focused on the field under
	// test.
	switch kind {
	case opsv1alpha1.ActionKindReboot:
		a.Spec.Action.Reboot = &opsv1alpha1.RebootActionArgs{Method: "COLD"}
	case opsv1alpha1.ActionKindCancelReboot:
		a.Spec.Action.CancelReboot = &opsv1alpha1.CancelRebootArgs{}
	case opsv1alpha1.ActionKindKillProcess:
		a.Spec.Action.KillProcess = &opsv1alpha1.KillProcessArgs{Name: "iosd", Signal: "TERM"}
	case opsv1alpha1.ActionKindFilePut:
		a.Spec.Action.FilePut = &opsv1alpha1.FilePutArgs{Path: "flash:f.bin", ConfigMapName: "payload"}
	case opsv1alpha1.ActionKindFileRemove:
		a.Spec.Action.FileRemove = &opsv1alpha1.FileRemoveArgs{Path: "flash:f.bin"}
	case opsv1alpha1.ActionKindFactoryReset:
		a.Spec.Action.FactoryReset = &opsv1alpha1.FactoryResetArgs{}
	}
	return a
}

// sanitiseEnvtestName produces a DNS-1123-subdomain-safe name. CR
// names like "bad-cat9k_iosxe.26.01.01.SPA.bin" contain '_', '.' and
// uppercase letters that the apiserver rejects on Create. The
// sanitiser is conservative: drop disallowed chars, lowercase the
// rest, and cap at 63 characters.
func sanitiseEnvtestName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 63 {
		out = out[:63]
	}
	if out == "" {
		out = "x"
	}
	return out
}
