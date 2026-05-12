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

package state

import (
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/mapper"
)

func TestExtractAppEventsFromMappedState(t *testing.T) {
	ts := time.Now()
	events := []mapper.MappedEvent{{
		Attributes: []mapper.KeyValue{{Key: "name", Value: "cvk0000_abc"}},
		Resource:   []mapper.KeyValue{{Key: "device", Value: "edge-01"}},
		Timestamp:  ts,
		Body:       "RUNNING",
		CanonicalPath: "/Cisco-IOS-XE-app-hosting-oper:app-hosting-oper-data/" +
			"app[name=cvk0000_abc]/details/state",
	}}
	got := ExtractAppEvents(events)
	if len(got) != 1 {
		t.Fatalf("events=%d, want 1", len(got))
	}
	if got[0].Device != "edge-01" || got[0].AppID != "cvk0000_abc" || got[0].State != "RUNNING" {
		t.Fatalf("unexpected event: %#v", got[0])
	}
	if !got[0].LastSeen.Equal(ts) {
		t.Fatalf("LastSeen=%s, want %s", got[0].LastSeen, ts)
	}
}

func TestCacheApplyMappedEventsStoresAppState(t *testing.T) {
	c := NewCache()
	c.ApplyMappedEvents([]mapper.MappedEvent{{
		Resource:      []mapper.KeyValue{{Key: "device", Value: "edge-01"}},
		Body:          "RUNNING",
		CanonicalPath: "/app-hosting-oper-data/app[name=cvk0000_abc]/details/state",
	}})
	rec, ok := c.Get("edge-01", KindApp, "cvk0000_abc")
	if !ok {
		t.Fatal("expected cached app record")
	}
	if rec.Values["state"] != "RUNNING" {
		t.Fatalf("state=%q, want RUNNING", rec.Values["state"])
	}
}
