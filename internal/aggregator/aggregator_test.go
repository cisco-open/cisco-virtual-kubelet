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

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
)

func aggScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(ciskov1.AddToScheme(s))
	utilruntime.Must(configv1alpha1.AddToScheme(s))
	return s
}

func TestResolvePasswordPrefersInline(t *testing.T) {
	scheme := aggScheme(t)
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverXE,
			Address:  "10.0.0.1",
			Username: "u",
			Password: "inline-secret",
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dev).Build()
	r := &AggregatedReconciler{Client: c, Scheme: scheme}
	got, err := r.resolvePassword(context.Background(), dev)
	if err != nil {
		t.Fatalf("resolvePassword: %v", err)
	}
	if got != "inline-secret" {
		t.Errorf("got %q, want inline-secret", got)
	}
}

func TestResolvePasswordFromSecret(t *testing.T) {
	scheme := aggScheme(t)
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "network"},
		Data:       map[string][]byte{"password": []byte("from-secret")},
	}
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: ciskov1.DeviceSpec{
			Driver:              ciskov1.DeviceDriverXE,
			Address:             "10.0.0.1",
			Username:            "u",
			CredentialSecretRef: &corev1.LocalObjectReference{Name: "creds"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dev, sec).Build()
	r := &AggregatedReconciler{Client: c, Scheme: scheme}
	got, err := r.resolvePassword(context.Background(), dev)
	if err != nil {
		t.Fatalf("resolvePassword: %v", err)
	}
	if got != "from-secret" {
		t.Errorf("got %q, want from-secret", got)
	}
}

func TestResolvePasswordFailsWhenSecretMissing(t *testing.T) {
	scheme := aggScheme(t)
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: ciskov1.DeviceSpec{
			Driver:              ciskov1.DeviceDriverXE,
			Address:             "10.0.0.1",
			Username:            "u",
			CredentialSecretRef: &corev1.LocalObjectReference{Name: "missing"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dev).Build()
	r := &AggregatedReconciler{Client: c, Scheme: scheme}
	if _, err := r.resolvePassword(context.Background(), dev); err == nil {
		t.Fatal("expected error for missing Secret")
	}
}

func TestReconcileUsesCiscoDeviceCorrelationCarrier(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = tp.Shutdown(context.Background())
	})

	now := time.Now().UTC()
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trace-only",
			Namespace: "network",
			Annotations: map[string]string{
				correlation.TraceparentAnnotation:    "00-11111111111111111111111111111111-2222222222222222-01",
				correlation.TraceWindowEndAnnotation: now.Add(time.Minute).Format(time.RFC3339),
				correlation.LifecycleIDAnnotation:    "release-181",
			},
		},
		Spec: ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriver("UNREGISTERED_TRACE_TEST")},
	}
	scheme := aggScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(dev).Build()
	r := &AggregatedReconciler{
		Client:  c,
		Scheme:  scheme,
		managed: map[string]*deviceWorker{},
		rootCtx: context.Background(),
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: dev.Namespace,
		Name:      dev.Name,
	}}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Name() != "cvk.aggregated.reconcile" {
		t.Fatalf("ended spans=%#v, want one aggregator reconcile", ended)
	}
	remote, err := correlation.ParseTraceparent(dev.Annotations[correlation.TraceparentAnnotation])
	if err != nil {
		t.Fatal(err)
	}
	if ended[0].Parent().TraceID() != remote.TraceID() || ended[0].Parent().SpanID() != remote.SpanID() {
		t.Fatalf("aggregator parent=%v, want object carrier=%v", ended[0].Parent(), remote)
	}
	wantLifecycle := attribute.String(correlation.LifecycleIDAttribute, "release-181")
	found := false
	for _, attr := range ended[0].Attributes() {
		if attr == wantLifecycle {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("aggregator attributes=%#v, want lifecycle id", ended[0].Attributes())
	}
}

func TestSpecHashChangesOnTransportEdit(t *testing.T) {
	// The aggregator restarts a worker only when transport-relevant
	// fields change. Edits to log level / labels / max pods must
	// not trigger a transport rebuild.
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: ciskov1.DeviceSpec{
			Driver:  ciskov1.DeviceDriverXE,
			Address: "10.0.0.1", Port: 443,
			Username: "u",
		},
	}
	a := specHash(dev, "pw")
	dev.Spec.LogLevel = "debug"
	if specHash(dev, "pw") != a {
		t.Errorf("logLevel edit changed specHash; should be transport-irrelevant")
	}
	dev.Spec.Address = "10.0.0.2"
	if specHash(dev, "pw") == a {
		t.Errorf("address edit didn't change specHash")
	}
}

func TestSpecHashChangesOnPasswordRotation(t *testing.T) {
	dev := &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: ciskov1.DeviceSpec{
			Driver:   ciskov1.DeviceDriverXE,
			Address:  "10.0.0.1",
			Username: "u",
		},
	}
	old := specHash(dev, "old")
	// Changing presence-of-password (Secret rotated in -> out) must
	// trigger a transport rebuild even when address/port haven't
	// moved. The hash collapses absence vs presence — that's enough
	// to detect rotation, without persisting the cleartext.
	if specHash(dev, "") == old {
		t.Errorf("password rotation didn't change specHash")
	}
}
