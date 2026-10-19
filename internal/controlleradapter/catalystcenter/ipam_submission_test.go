// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

func TestIPAMSubmissionConsumedOnEveryOutcome(t *testing.T) {
	for _, kind := range []string{"pool-create", "pool-update", "reservation-update"} {
		for _, outcome := range []string{"accepted", "lost-ack", "rejected", "preflight-failure"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				intent, err := parseNetworkReportIntent([]byte(poolModel))
				if err != nil {
					t.Fatal(err)
				}
				pools, reservations := fixturePools(), fixtureReservations()
				if kind == "pool-create" {
					pools = nil
					reservations = nil
					intent.reservations = nil
				}
				if kind == "pool-update" {
					intent.reservations = nil
					reservations = nil
					gateway := "192.0.2.2"
					intent.pools[0].Space.Gateway = &gateway
				}
				if kind == "reservation-update" {
					gateway := "192.0.2.130"
					intent.reservations[0].V4.Gateway = &gateway
				}
				var failReads atomic.Bool
				var mutations atomic.Int32
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if r.Method != "GET" {
						mutations.Add(1)
						switch outcome {
						case "accepted":
							fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
						case "lost-ack":
							fmt.Fprint(w, `{}`)
						default:
							w.WriteHeader(400)
						}
						return
					}
					if failReads.Load() {
						w.WriteHeader(503)
						return
					}
					var rows any
					switch r.URL.Path {
					case globalPoolsPath:
						rows = pools
						if pools == nil {
							rows = []ipPool{}
						}
					case sitePoolsPath:
						rows = reservations
						if reservations == nil {
							rows = []ipReservation{}
						}
					case sitesPath:
						rows = sampleSites()
					default:
						t.Error("unexpected read", r.URL.Path)
						w.WriteHeader(404)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				})
				e := &ipamConfigurationExecutor{client: c, intent: intent}
				write, err := e.next(context.Background())
				if err != nil || write == nil {
					t.Fatal("plan", err)
				}
				failReads.Store(outcome == "preflight-failure")
				var successes atomic.Int32
				var wg sync.WaitGroup
				for range 8 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if _, err := write.submit(context.Background()); err == nil {
							successes.Add(1)
						}
					}()
				}
				wg.Wait()
				wantWrites, wantSuccess := int32(1), int32(0)
				if outcome == "preflight-failure" {
					wantWrites = 0
				}
				if outcome == "accepted" {
					wantSuccess = 1
				}
				if mutations.Load() != wantWrites || successes.Load() != wantSuccess {
					t.Fatal("duplicate or unexpected mutation", mutations.Load(), successes.Load())
				}
				failReads.Store(false)
				if _, err := write.submit(context.Background()); err == nil || mutations.Load() != wantWrites {
					t.Fatal("consumed plan replayed")
				}
			})
		}
	}
}
