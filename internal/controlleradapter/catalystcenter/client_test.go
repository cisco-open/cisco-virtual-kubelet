// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestClientAuthenticatesAndReadsInventory(t *testing.T) {
	var gotAuth, gotDevices bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case authPath:
			gotAuth = r.Method == http.MethodPost && r.Header.Get("Authorization") != ""
			_, _ = w.Write([]byte(`{"Token":"test-token"}`))
		case devicesPath:
			gotDevices = r.Header.Get("X-Auth-Token") == "test-token" && r.URL.Query().Get("limit") == "500" && r.URL.Query().Get("offset") == "1"
			_, _ = w.Write([]byte(`{"response":[{"id":"d1","hostname":"cat9k-1","managementIpAddress":"192.0.2.10","reachabilityStatus":"Reachable"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "username"), []byte("user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "password"), []byte("password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newClient(clientConfig{Endpoint: srv.URL, CredentialPath: dir, InsecureSkipVerify: true, RequestTimeout: time.Second, MaxSessionLifetime: time.Minute})
	devices, err := c.ListDevices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !gotAuth || !gotDevices || len(devices) != 1 || devices[0].Hostname != "cat9k-1" {
		t.Fatalf("auth=%v devices=%v result=%+v", gotAuth, gotDevices, devices)
	}
}

func TestSWIMClientContracts(t *testing.T) {
	var paths []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case authPath:
			_, _ = w.Write([]byte(`{"Token":"test-token"}`))
		case imagesPath:
			_, _ = w.Write([]byte(`{"response":[{"imageUuid":"img-1","version":"17.18.3"}]}`))
		case distributePath, activatePath:
			_, _ = w.Write([]byte(`{"response":{"taskId":"task-1","url":"/dna/intent/api/v1/task/task-1"}}`))
		case taskPath + "task-1":
			_, _ = w.Write([]byte(`{"response":{"isError":false,"data":"complete"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	for _, item := range []struct{ name, value string }{{"username", "user"}, {"password", "password"}} {
		if err := os.WriteFile(filepath.Join(dir, item.name), []byte(item.value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := newClient(clientConfig{Endpoint: srv.URL, CredentialPath: dir, InsecureSkipVerify: true, RequestTimeout: time.Second, MaxSessionLifetime: time.Minute})
	images, err := c.ListImages(context.Background())
	if err != nil || len(images) != 1 || images[0].ID != "img-1" {
		t.Fatalf("images=%+v err=%v", images, err)
	}
	for _, fn := range []func(context.Context) (Task, error){func(ctx context.Context) (Task, error) { return c.Distribute(ctx, "d1", "img-1") }, func(ctx context.Context) (Task, error) { return c.Activate(ctx, "d1", "img-1") }} {
		task, err := fn(context.Background())
		if err != nil || task.ID != "task-1" {
			t.Fatalf("task=%+v err=%v", task, err)
		}
	}
	if _, err := c.GetTask(context.Background(), "task-1"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 5 || paths[0] != authPath || paths[1] != imagesPath || paths[2] != distributePath || paths[3] != activatePath || paths[4] != taskPath+"task-1" {
		t.Fatalf("unexpected API requests: %v", paths)
	}
}

func TestListDevicesPaginates(t *testing.T) {
	var offsets []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == authPath {
			_, _ = w.Write([]byte(`{"Token":"test-token"}`))
			return
		}
		offsets = append(offsets, r.URL.Query().Get("offset"))
		if offsets[len(offsets)-1] == "1" {
			_, _ = w.Write([]byte(`{"response":[`))
			for i := 0; i < 500; i++ {
				if i > 0 {
					_, _ = w.Write([]byte(","))
				}
				_, _ = w.Write([]byte(`{"id":"` + strconv.Itoa(i) + `"}`))
			}
			_, _ = w.Write([]byte(`]}`))
			return
		}
		_, _ = w.Write([]byte(`{"response":[{"id":"last"}]}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	for _, name := range []string{"username", "password"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := newClient(clientConfig{Endpoint: srv.URL, CredentialPath: dir, InsecureSkipVerify: true, RequestTimeout: time.Second, MaxSessionLifetime: time.Minute})
	devices, err := c.ListDevices(context.Background())
	if err != nil || len(devices) != 501 || len(offsets) != 2 || offsets[0] != "1" || offsets[1] != "501" {
		t.Fatalf("devices=%d offsets=%v err=%v", len(devices), offsets, err)
	}
}
