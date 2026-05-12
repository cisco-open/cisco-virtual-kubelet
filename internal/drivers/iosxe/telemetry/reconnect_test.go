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

package telemetry

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func TestReconnectStateBackoffSequence(t *testing.T) {
	b := NewReconnectState(&configv1alpha1.ReconnectConfig{
		InitialBackoff: metav1.Duration{Duration: 100 * time.Millisecond},
		MaxBackoff:     metav1.Duration{Duration: 250 * time.Millisecond},
		MaxRetries:     3,
	})
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 250 * time.Millisecond}
	for i, d := range want {
		got, ok := b.Next()
		if !ok {
			t.Fatalf("Next(%d) ok=false, want true", i)
		}
		if got != d {
			t.Fatalf("Next(%d)=%s, want %s", i, got, d)
		}
	}
	if got, ok := b.Next(); ok || got != 0 {
		t.Fatalf("Next after max retries=(%s,%v), want (0,false)", got, ok)
	}
}

func TestReconnectStateReset(t *testing.T) {
	b := NewReconnectState(&configv1alpha1.ReconnectConfig{
		InitialBackoff: metav1.Duration{Duration: time.Second},
		MaxBackoff:     metav1.Duration{Duration: 5 * time.Second},
	})
	if got, ok := b.Next(); !ok || got != time.Second {
		t.Fatalf("first Next=(%s,%v), want (1s,true)", got, ok)
	}
	if got, ok := b.Next(); !ok || got != 2*time.Second {
		t.Fatalf("second Next=(%s,%v), want (2s,true)", got, ok)
	}
	b.Reset()
	if got, ok := b.Next(); !ok || got != time.Second {
		t.Fatalf("after Reset Next=(%s,%v), want (1s,true)", got, ok)
	}
}

func TestReconnectStateDefaultAndMaxClamp(t *testing.T) {
	b := NewReconnectState(&configv1alpha1.ReconnectConfig{
		InitialBackoff: metav1.Duration{Duration: 10 * time.Second},
		MaxBackoff:     metav1.Duration{Duration: time.Second},
	})
	got, ok := b.Next()
	if !ok || got != 10*time.Second {
		t.Fatalf("first Next=(%s,%v), want (10s,true)", got, ok)
	}
	got, ok = b.Next()
	if !ok || got != 10*time.Second {
		t.Fatalf("clamped Next=(%s,%v), want (10s,true)", got, ok)
	}
}
