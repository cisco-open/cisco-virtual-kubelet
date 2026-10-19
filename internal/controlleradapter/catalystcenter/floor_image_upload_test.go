// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFloorImageUploadTransport(t *testing.T) {
	for _, mode := range []string{"accepted", "unauthorized", "missing-task", "server-error"} {
		t.Run(mode, func(t *testing.T) {
			payload := []byte("%PDF-1.7\nfloor fixture\n")
			writes, auth := 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					auth++
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				writes++
				if r.Method != http.MethodPost || r.URL.Path != "/dna/intent/api/v2/floors/floor/uploadImage" || r.Header.Get("X-Auth-Token") != "test" || !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data; boundary=") {
					t.Error("incorrect authenticated multipart request")
				}
				reader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					return
				}
				part, err := reader.NextPart()
				if err != nil {
					t.Error(err)
					return
				}
				got, err := io.ReadAll(part)
				if err != nil || part.FormName() != "image" || part.FileName() != "floor.pdf" || !bytes.Equal(got, payload) {
					t.Error("binary content or filename changed")
				}
				if _, err := reader.NextPart(); err != io.EOF {
					t.Error("unexpected form fields")
				}
				switch mode {
				case "unauthorized":
					w.WriteHeader(401)
					fmt.Fprint(w, "private-appliance-response")
				case "server-error":
					w.WriteHeader(500)
					fmt.Fprint(w, "private-appliance-response")
				case "missing-task":
					fmt.Fprint(w, `{"response":{}}`)
				default:
					fmt.Fprint(w, `{"response":{"taskId":"image-task","url":"https://untrusted.example/task"}}`)
				}
			})
			task, err := c.uploadFloorImage(context.Background(), "floor", "maps/floor.pdf", payload)
			if mode == "accepted" {
				if err != nil || task.ID != "image-task" {
					t.Fatal("upload failed", err)
				}
			} else if err == nil || strings.Contains(err.Error(), "private-appliance-response") {
				t.Fatal("failure accepted or response leaked")
			}
			if writes != 1 || auth != 1 {
				t.Fatal("mutation replayed or bypassed shared session")
			}
		})
	}
}
func TestFloorImageInputValidation(t *testing.T) {
	pdf := []byte("%PDF-1.7\nfixture")
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"", pdf}, {"x.txt", pdf}, {"x.png", pdf}, {"a\n.pdf", pdf}, {"a\\b.pdf", pdf}, {"floor.pdf", nil}, {"floor.pdf", bytes.Repeat([]byte("x"), maxFloorImageBytes+1)},
	} {
		if body, _, err := floorImageMultipart(tc.name, tc.data); err == nil || body != nil {
			t.Fatal("invalid upload accepted")
		}
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"floor.PNG", []byte{137, 80, 78, 71, 13, 10, 26, 10}},
		{"floor.jpeg", []byte{255, 216, 255, 224, 0, 16}},
		{"floor.pdf", pdf},
	} {
		if _, _, err := floorImageMultipart(tc.name, tc.data); err != nil {
			t.Fatal("supported signature rejected", err)
		}
	}
}
