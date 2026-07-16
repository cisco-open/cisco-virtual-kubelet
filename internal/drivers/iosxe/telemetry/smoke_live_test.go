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

// Live-device smoke test — gated behind RUN_LIVE_GNMI_SMOKE=1 so it
// never runs in normal `make test`. Reads CAT9K_PASSWORD env var, dials
// 10.1.1.1:50052 plaintext as user "cisco", subscribes to OpenConfig
// interface counters, and asserts MessagesReceived ticks within 30s.
//
// Invoke:
//
//	RUN_LIVE_GNMI_SMOKE=1 CAT9K_PASSWORD=... \
//	  go test ./internal/drivers/iosxe/telemetry/ -run TestLiveSubscribeSmoke -v -count=1
package telemetry

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

func TestLiveSubscribeSmoke(t *testing.T) {
	if os.Getenv("RUN_LIVE_GNMI_SMOKE") != "1" {
		t.Skip("set RUN_LIVE_GNMI_SMOKE=1 to run the live-device smoke test")
	}
	pw := os.Getenv("CAT9K_PASSWORD")
	if pw == "" {
		t.Fatal("CAT9K_PASSWORD env required")
	}

	cfg := transport.GNMIConfig{
		Address:  "10.1.1.1",
		Port:     50052,
		Username: "cisco",
		Password: pw,
	}
	factory := NewDefaultSubscribeClientFactory(cfg)
	logger := funcr.New(func(prefix, args string) { t.Logf("%s %s", prefix, args) }, funcr.Options{Verbosity: 1})

	sub := NewSubscriber("cat9k-smoke", factory,
		WithLogger(logger),
		WithChannelCapacity(4096),
		WithReconnectConfig(&configv1alpha1.ReconnectConfig{
			InitialBackoff: metav1.Duration{Duration: 1 * time.Second},
			MaxBackoff:     metav1.Duration{Duration: 30 * time.Second},
		}),
	)
	if err := sub.AddSubscription(configv1alpha1.TelemetrySubscription{
		Name:           "interfaces",
		Origin:         "openconfig",
		Paths:          []string{"/interfaces/interface/state/counters"},
		Mode:           "STREAM",
		StreamMode:     "SAMPLE",
		SampleInterval: metav1.Duration{Duration: 5 * time.Second},
		Encoding:       "PROTO",
	}); err != nil {
		t.Fatalf("AddSubscription: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := sub.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sub.Stop()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		_, states := sub.StatusFor([]string{"interfaces"})
		if len(states) == 0 {
			continue
		}
		st := states[0]
		t.Logf("phase status: msgs=%d reconnects=%d backoff=%s err=%q",
			st.MessagesReceived, st.Reconnects, st.CurrentBackoff.Duration, st.LastError)
		if st.MessagesReceived > 0 {
			t.Logf("PASS: received %d notifications", st.MessagesReceived)
			return
		}
	}

	_, states := sub.StatusFor([]string{"interfaces"})
	if len(states) > 0 {
		t.Fatalf("no notifications received within 30s; final state: msgs=%d reconnects=%d err=%q",
			states[0].MessagesReceived, states[0].Reconnects, states[0].LastError)
	}
	t.Fatal("no notifications received within 30s")
}
