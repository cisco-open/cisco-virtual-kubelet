// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestWirelessFeatureNativeFingerprint(t *testing.T) {
	base, err := wirelessFeatureNativeFingerprint([]byte(`{"id":"a","fields":{"known":true,"unmanaged":9007199254740992},"unlocked":["a","b"]}`))
	if err != nil {
		t.Fatal(err)
	}
	equivalent, err := wirelessFeatureNativeFingerprint([]byte(`{ "unlocked":["a","b"],"fields":{"unmanaged":9007199254740992,"known":true},"id":"a" }`))
	if err != nil || base != equivalent {
		t.Fatal("object order or whitespace changed fingerprint", err)
	}
	for _, raw := range []string{
		`{"id":"a","fields":{"known":true,"unmanaged":9007199254740993},"unlocked":["a","b"]}`,
		`{"id":"a","fields":{"known":true},"unlocked":["a","b"]}`,
		`{"id":"a","fields":{"known":true,"unmanaged":9007199254740992},"unlocked":["b","a"]}`,
	} {
		got, err := wirelessFeatureNativeFingerprint([]byte(raw))
		if err != nil || got == base {
			t.Fatal("unmanaged/large-number/array change lost", err)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"a":1,"a":2}`, `{"a":{"b":1,"b":2}}`, `{"a":[{"b":1,"b":2}]}`, `{} {}`, `{"a":`, `{"a":NaN}`} {
		if _, err := wirelessFeatureNativeFingerprint([]byte(raw)); err == nil {
			t.Fatal("ambiguous state accepted", raw)
		}
	}
}
func TestFeatureReceiptOptionalNativeFingerprint(t *testing.T) {
	key := "fra:" + featureIdentityDigest("FRA")
	for _, suffix := range []string{"", ":" + strings.Repeat("a", 64)} {
		id, hash, err := featureReceiptIdentity(featureCreatedPrefix+key+":"+featureTestFRAUUID+suffix, featureCreatedPrefix, key)
		if err != nil || id != featureTestFRAUUID || (hash == "") != (suffix == "") {
			t.Fatal("legacy or extended identity failed", id, hash, err)
		}
	}
	for _, suffix := range []string{":", ":bad", ":" + strings.Repeat("A", 64), ":" + strings.Repeat("a", 64) + ":other"} {
		if _, _, err := featureReceiptIdentity(featureCreatedPrefix+key+":"+featureTestFRAUUID+suffix, featureCreatedPrefix, key); err == nil {
			t.Fatal("malformed state digest accepted", suffix)
		}
	}
}
func TestWirelessFeatureObservedResultUsesSameDetailResponse(t *testing.T) {
	for _, mode := range []string{"known", "source-drift", "duplicate-unmanaged"} {
		t.Run(mode, func(t *testing.T) {
			now := time.Now()
			at := now.Add(-time.Second)
			want := wirelessFRA{Name: "FRA", Attributes: wirelessFRAAttributes{Band: "2_4GHZ_5GHZ", Interval: 3, Sensitivity: "Medium"}}
			interval := 3
			if mode == "source-drift" {
				interval = 6
			}
			detail := fmt.Sprintf(`{"id":%q,"designName":"FRA","unlockedAttributes":["future"],"featureAttributes":{"radioBand":"2_4GHZ_5GHZ","fraFreeze":false,"fraStatus":false,"fraInterval":%d,"fraSensitivity":"Medium","futureNativeValue":9007199254740993}}`, featureTestFRAUUID, interval)
			if mode == "duplicate-unmanaged" {
				detail = strings.Replace(detail, `"futureNativeValue":9007199254740993`, `"futureNativeValue":9007199254740993,"futureNativeValue":2`, 1)
			}
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case taskPath + "task":
					json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": "task", "startTime": at.UnixMilli(), "endTime": now.UnixMilli(), "isError": false, "serviceType": "NCNC", "progress": "Successfully updated model config design with name FRA", "data": featureTestFRAUUID}})
				case wirelessFeatureSummaryPath:
					fmt.Fprintf(w, `{"response":[{"type":"RRM_FRA_CONFIGURATION","count":1,"instances":[{"id":%q,"designName":"FRA","systemTemplate":false}]}]}`, featureTestFRAUUID)
				case wirelessFRAPath + "/" + featureTestFRAUUID:
					reads++
					if reads > 1 {
						t.Error("fingerprint used another detail response")
					}
					fmt.Fprint(w, `{"response":`+detail+`}`)
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			ref := wirelessFeatureTaskReference{ID: "task", Name: "FRA", Action: "Update", Target: featureTestFRAUUID, Submitted: at}
			id, hash, done, err := verifyWirelessFeatureObservedResult(context.Background(), c, wirelessFRAKind, ref, want, c.wirelessFRAReadState, wirelessFRAMatches, now)
			if mode == "known" {
				expected, _ := wirelessFeatureNativeFingerprint([]byte(detail))
				if err != nil || !done || id != featureTestFRAUUID || hash != expected || reads != 1 {
					t.Fatal("same-response native proof missing", id, hash, done, err, reads)
				}
			} else if done || id != "" || hash != "" || reads != 1 {
				t.Fatal("invalid readback acquired state proof", id, hash, done, err, reads)
			}
		})
	}
}
