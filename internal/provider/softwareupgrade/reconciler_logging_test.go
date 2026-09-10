// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwareupgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	vklogrus "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestControllerContextEmitsCorrelatedDispatchAndPhaseLogs(t *testing.T) {
	var output bytes.Buffer
	sink := logrus.New()
	sink.SetOutput(&output)
	sink.SetFormatter(&logrus.JSONFormatter{})
	ctx := log.WithLogger(context.Background(), vklogrus.FromLogrus(logrus.NewEntry(sink)))
	ctx = crlog.IntoContext(ctx, logr.Discard())
	rig := newRig(t)
	rig.os.verifyVersion = "17.14.01a"
	up := newUpgrade("upgrade-log-evidence", func(up *opsv1alpha1.IOSXESoftwareUpgrade) {
		up.Finalizers = []string{Finalizer}
		up.Spec.ImageSource = opsv1alpha1.UpgradeImageSource{URL: "https://example.invalid/image.bin", SHA256: strings.Repeat("a", 64)}
		up.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
	})
	r := newReconciler(t, rig, up)
	r.ImageResolver = &countingImageResolver{}
	req := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(up)}
	for range 2 {
		if _, err := r.Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	// A replacement reconciler receives the same durable CR and only observes.
	replacement := *r
	if _, err := replacement.Reconcile(ctx, req); err != nil {
		t.Fatal(err)
	}
	if rig.os.installCalls != 1 || rig.os.activateCalls != 1 {
		t.Fatalf("RPC calls: Install=%d Activate=%d", rig.os.installCalls, rig.os.activateCalls)
	}
	counts := map[string]int{}
	for _, line := range bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n")) {
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatal(err)
		}
		message, _ := entry["msg"].(string)
		counts[message]++
		if entry["softwareUpgrade"] != req.NamespacedName.String() {
			t.Fatalf("missing operation correlation: %s", line)
		}
		if message == "IOSXESoftwareUpgrade phase advanced" && (entry["from"] == "" || entry["to"] == "" || entry["reason"] == "") {
			t.Fatalf("missing durable phase evidence: %s", line)
		}
	}
	for _, message := range []string{"dispatching gNOI OS.Install", "dispatching gNOI OS.Activate"} {
		if counts[message] != 1 {
			t.Fatalf("log count %q=%d; logs:\n%s", message, counts[message], output.String())
		}
	}
	if counts["IOSXESoftwareUpgrade phase advanced"] < 2 {
		t.Fatalf("missing phase logs: %s", output.String())
	}
}
