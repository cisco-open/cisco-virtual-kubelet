// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestConfigurationTaskFailureSanitizesAndCorrelates(t *testing.T) {
	for _, code := range []string{"NCSP11000", "NCSP11008", "NCND00010", "", "SECRET-MARKER", "NCSP11000\nSECRET-MARKER", "NCSP11000: SECRET-MARKER", strings.Repeat("X", 10000)} {
		t.Run(fmt.Sprintf("length%d", len(code)), func(t *testing.T) {
			id := "task"
			now := time.Now()
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "startTime": now.UnixMilli(), "endTime": now.UnixMilli(), "isError": true, "errorCode": code, "failureReason": "SECRET-MARKER", "progress": "SECRET-MARKER", "data": "SECRET-MARKER"}})
			})
			done, err := c.configurationTaskComplete(context.Background(), "task", now, now)
			var failure *configurationTaskFailure
			if done || !errors.As(err, &failure) || strings.Contains(err.Error(), "SECRET") || len(err.Error()) > 180 {
				t.Fatal("unsafe or missing task failure", err)
			}
			valid := code == "NCSP11000" || code == "NCSP11008" || code == "NCND00010"
			if valid && !strings.Contains(err.Error(), code) {
				t.Fatal("valid machine code lost")
			}
			if !valid && failure.code != "" {
				t.Fatal("invalid machine code retained")
			}
			if (code == "NCSP11008") != strings.Contains(err.Error(), "required controller application unavailable") {
				t.Fatal("application prerequisite guidance missing or attributed to another failure")
			}
			id = "foreign-task"
			_, err = c.configurationTaskComplete(context.Background(), "task", now, now)
			if errors.As(err, &failure) {
				t.Fatal("foreign task classified as our operation's failure")
			}
		})
	}
}
