// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const plannedAPObservedFixture = `{"id":"ap-id","name":"AP1","type":"C9130AXI","macAddress":"AA-BB-CC-DD-EE-FF","position":{"x":4.5,"y":6,"z":2},"radios":[{"id":"radio-id","bands":[5],"channel":36,"txPower":12,"antenna":{"name":"Internal","azimuth":90,"elevation":-10}}]}`

func TestPlannedAPObservation(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "missing", "null", "partial", "duplicate-name", "duplicate-id", "missing-radio-id", "missing-position", "missing-channel", "bad-band", "duplicate-band", "bad-antenna", "bad-mac"} {
		t.Run(mode, func(t *testing.T) {
			body := plannedAPObservedFixture
			response := "[" + body + "]"
			switch mode {
			case "empty":
				response = "[]"
			case "null":
				response = "null"
			case "duplicate-name":
				response = "[" + body + "," + strings.Replace(body, `"id":"ap-id"`, `"id":"other"`, 1) + "]"
			case "duplicate-id":
				response = "[" + body + "," + strings.Replace(body, `"name":"AP1"`, `"name":"AP2"`, 1) + "]"
			case "missing-radio-id":
				response = strings.Replace(response, `"id":"radio-id",`, "", 1)
			case "missing-position":
				response = strings.Replace(response, `"x":4.5,`, "", 1)
			case "missing-channel":
				response = strings.Replace(response, `"channel":36,`, "", 1)
			case "bad-band":
				response = strings.Replace(response, `"bands":[5]`, `"bands":[7]`, 1)
			case "duplicate-band":
				response = strings.Replace(response, `"bands":[5]`, `"bands":[5,5]`, 1)
			case "bad-antenna":
				response = strings.Replace(response, `"azimuth":90`, `"azimuth":361`, 1)
			case "bad-mac":
				response = strings.Replace(response, `AA-BB-CC-DD-EE-FF`, `invalid`, 1)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != plannedAPPositionsPath("floor") || r.URL.Query().Get("offset") != "1" || r.URL.Query().Get("limit") != "500" {
					t.Error("wrong planned AP read")
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				if mode == "missing" {
					fmt.Fprint(w, `{}`)
				} else {
					fmt.Fprint(w, `{"response":`+response+`}`)
				}
			})
			got, err := c.plannedAPPositions(context.Background(), "floor")
			valid := mode == "valid" || mode == "empty"
			if valid != (err == nil) {
				t.Fatalf("mode=%s error=%v", mode, err)
			}
			if !valid && got != nil {
				t.Fatal("partial inventory escaped")
			}
			if mode == "valid" && (len(got) != 1 || *got["AP1"].MAC != "aa:bb:cc:dd:ee:ff") {
				t.Fatal("identity or normalization lost")
			}
		})
	}
}

func TestPlannedAPObservationPagination(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(strconv.FormatBool(repeat), func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
				if err != nil || (offset != 1 && offset != 501) {
					t.Error("bad pagination offset")
				}
				page := []plannedAPObservation{}
				count := 500
				if offset == 501 {
					count = 1
				}
				for i := 0; i < count; i++ {
					var ap plannedAPObservation
					if json.Unmarshal([]byte(plannedAPObservedFixture), &ap) != nil {
						t.Error("fixture")
					}
					n := offset + i
					if repeat && offset == 501 {
						n = 1
					}
					ap.ID = fmt.Sprintf("id-%d", n)
					ap.Name = fmt.Sprintf("AP-%d", n)
					page = append(page, ap)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": page})
			})
			got, err := c.plannedAPPositions(context.Background(), "floor")
			if reads != 2 {
				t.Fatal("second page missing")
			}
			if repeat {
				if err == nil || got != nil {
					t.Fatal("repeated page accepted")
				}
			} else if err != nil || len(got) != 501 {
				t.Fatal("incomplete pagination", err)
			}
		})
	}
}
