// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	vklogrus "github.com/virtual-kubelet/virtual-kubelet/log/logrus"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestWorkerManagerBaseContextPreservesLoggerAndManagerShutdown(t *testing.T) {
	var output bytes.Buffer
	sink := logrus.New()
	sink.SetOutput(&output)
	logger := vklogrus.FromLogrus(logrus.NewEntry(sink)).WithField("device", "cat9k-test")
	workerCtx, cancelWorker := context.WithCancel(log.WithLogger(context.Background(), logger))
	defer cancelWorker()
	base := workerManagerBaseContext(workerCtx)()
	cancelWorker()
	if base.Err() != nil || base.Done() != nil {
		t.Fatal("worker cancellation bypassed manager-controlled runnable shutdown")
	}
	if log.G(base) != logger {
		t.Fatal("manager BaseContext lost the worker logger")
	}
	// controller-runtime adds its own logger to each request. The VK logger
	// must survive this independent context key, including request cancellation.
	requestCtx, cancelRequest := context.WithCancel(crlog.IntoContext(base, logr.Discard()))
	log.G(requestCtx).Info("controller lifecycle evidence")
	cancelRequest()
	if requestCtx.Err() != context.Canceled {
		t.Fatal("request did not remain independently cancellable")
	}
	if got := output.String(); !strings.Contains(got, "controller lifecycle evidence") || !strings.Contains(got, "device=cat9k-test") {
		t.Fatalf("missing correlated worker log: %s", got)
	}
}
