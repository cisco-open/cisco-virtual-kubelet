// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
)

func testClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	for _, name := range []string{"username", "password"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := newClient(clientConfig{Endpoint: srv.URL, CredentialPath: dir, InsecureSkipVerify: true, RequestTimeout: time.Second})
	t.Cleanup(c.Invalidate)
	return c
}

func TestAuthenticationClassificationAndRedaction(t *testing.T) {
	for _, code := range []int{401, 403, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				fmt.Fprint(w, "raw-secret-from-appliance")
			})
			err := c.Probe(context.Background())
			var restErr *transport.RESTError
			if !errors.As(err, &restErr) || restErr.StatusCode != code {
				t.Fatalf("classification lost: %v", err)
			}
			if strings.Contains(err.Error(), "raw-secret") || restErr.Body != "" {
				t.Fatal("remote body leaked")
			}
			result := classify(err)
			if result.ready() || result.authenticated {
				t.Fatalf("unexpected health: %+v", result)
			}
			if code != 500 && result.reason != "AuthenticationFailed" {
				t.Fatalf("wrong reason: %s", result.reason)
			}
		})
	}
}

func TestReadRefreshesOnlyUnauthorized(t *testing.T) {
	for _, code := range []int{401, 403} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var logins, reads atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					logins.Add(1)
					fmt.Fprint(w, `{"Token":"token"}`)
					return
				}
				if reads.Add(1) == 1 {
					w.WriteHeader(code)
					return
				}
				fmt.Fprint(w, `{"response":[]}`)
			})
			err := c.Probe(context.Background())
			if code == 401 {
				if err != nil || logins.Load() != 2 || reads.Load() != 2 {
					t.Fatalf("err=%v logins=%d reads=%d", err, logins.Load(), reads.Load())
				}
			} else {
				if err == nil || logins.Load() != 1 || reads.Load() != 1 {
					t.Fatal("forbidden request was retried")
				}
				result := classify(err)
				if result.reason != "Forbidden" || !result.authenticated || result.ready() {
					t.Fatalf("wrong authorization status: %+v", result)
				}
			}
		})
	}
}

func TestRedirectsNeverForwardCredentialsOrReplay(t *testing.T) {
	for _, path := range []string{authPath, devicesPath, distributePath} {
		t.Run(path, func(t *testing.T) {
			var forwarded atomic.Int32
			destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1) }))
			defer destination.Close()
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath && path != authPath {
					fmt.Fprint(w, `{"Token":"secret-token"}`)
					return
				}
				http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
			})
			var err error
			if path == distributePath {
				_, err = c.Distribute(context.Background(), "device-1", "image-1")
			} else {
				err = c.Probe(context.Background())
			}
			if err == nil || forwarded.Load() != 0 {
				t.Fatalf("redirect followed: err=%v forwarded=%d", err, forwarded.Load())
			}
		})
	}
}

func TestSWIMMutationsAreNeverReplayed(t *testing.T) {
	for _, code := range []int{401, 403, 429, 500} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var logins, writes atomic.Int32
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					logins.Add(1)
					fmt.Fprint(w, `{"Token":"token"}`)
					return
				}
				writes.Add(1)
				w.WriteHeader(code)
			})
			if _, err := c.Activate(context.Background(), "device-1", "image-1"); err == nil {
				t.Fatal("expected error")
			}
			if writes.Load() != 1 || logins.Load() != 1 {
				t.Fatalf("mutation replayed: writes=%d logins=%d", writes.Load(), logins.Load())
			}
		})
	}
}

func TestInvalidAPIResponsesCannotReportHealthy(t *testing.T) {
	for _, body := range []string{`<html>login</html>`, `{}`, `{"response":null}`, `{"response":{}}`, `{"response":[{}]}`, `{"response":[{"id":42}]}`} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"token"}`)
					return
				}
				fmt.Fprint(w, body)
			})
			if result := classify(c.Probe(context.Background())); result.ready() {
				t.Fatal("malformed inventory reported ready")
			}
			if _, err := c.ListDevices(context.Background()); err == nil {
				t.Fatal("malformed inventory accepted")
			}
		})
	}
}

func TestImageInventoryPaginationAndWireSchema(t *testing.T) {
	var pages atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"token"}`)
			return
		}
		page := pages.Add(1)
		if r.URL.Path != imagesPath || r.URL.Query().Get("limit") != "500" || r.URL.Query().Get("offset") != fmt.Sprint(1+(page-1)*500) {
			t.Errorf("unexpected request: %s", r.URL)
		}
		count := 500
		if page == 2 {
			count = 1
		}
		items := make([]map[string]any, count)
		for i := range items {
			items[i] = map[string]any{"imageUuid": fmt.Sprintf("%d-%d", page, i), "name": "cat9k.bin", "imageName": "cat9k.bin", "isTaggedGolden": true, "imageIntegrityStatus": "VERIFIED"}
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"response": items}); err != nil {
			t.Error(err)
		}
	})
	images, err := c.ListImages(context.Background())
	if err != nil || len(images) != 501 || pages.Load() != 2 {
		t.Fatalf("images=%d pages=%d err=%v", len(images), pages.Load(), err)
	}
	if images[0].Name != "cat9k.bin" || images[0].ImageName != "cat9k.bin" || !images[0].Golden || images[0].IntegrityStatus != "VERIFIED" {
		t.Fatalf("wire schema mismatch: %+v", images[0])
	}
}

func TestRepeatedPageFailsWithoutPartialInventory(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"token"}`)
			return
		}
		items := make([]Device, 500)
		for i := range items {
			items[i].ID = fmt.Sprint(i)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"response": items}); err != nil {
			t.Error(err)
		}
	})
	items, err := c.ListDevices(context.Background())
	if err == nil || len(items) != 0 {
		t.Fatalf("repeated page accepted: len=%d err=%v", len(items), err)
	}
}

func TestInvalidSWIMIdentifiersDoNotDial(t *testing.T) {
	var requests atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { requests.Add(1) })
	for _, id := range []string{"", "..", "a/b", "a?b", "a#b", "a%b", "a b"} {
		if _, err := c.GetTask(context.Background(), id); err == nil {
			t.Fatalf("invalid task ID accepted: %q", id)
		}
		if _, err := c.Distribute(context.Background(), id, "image-1"); err == nil {
			t.Fatalf("invalid device ID accepted: %q", id)
		}
		if _, err := c.Activate(context.Background(), "device-1", id); err == nil {
			t.Fatalf("invalid image ID accepted: %q", id)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid request reached endpoint: %d", requests.Load())
	}
}

func TestStaleSessionInvalidationPreservesNewSession(t *testing.T) {
	old, current := &transport.RESTClient{}, &transport.RESTClient{}
	c := newClient(clientConfig{})
	c.rest, c.token = current, "new-token"
	c.invalidateSession(old)
	if c.rest != current || c.token != "new-token" {
		t.Fatal("stale request invalidated new session")
	}
	c.invalidateSession(current)
	if c.rest != nil || c.token != "" {
		t.Fatal("expired session was retained")
	}
}

func TestConcurrencyWaitHonorsCancellation(t *testing.T) {
	c := newClient(clientConfig{MaxConcurrent: 1})
	release, err := c.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestCredentialRotationReloadsMaterial(t *testing.T) {
	var logins atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			attempt := logins.Add(1)
			user, password, ok := r.BasicAuth()
			want := "test"
			if attempt == 2 {
				want = "rotated password with spaces"
			}
			if !ok || user != "test" || password != want {
				t.Error("login did not use current credential material")
			}
			fmt.Fprint(w, `{"Token":"token"}`)
			return
		}
		fmt.Fprint(w, `{"response":[]}`)
	})
	if err := c.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.cfg.CredentialPath, "password"), []byte("rotated password with spaces\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Invalidate()
	if err := c.Probe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logins.Load() != 2 {
		t.Fatalf("logins=%d", logins.Load())
	}
}

func TestTLSVerificationIsEnabledUnlessExplicitlyDisabled(t *testing.T) {
	var reached atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) { reached.Add(1) })
	c.cfg.InsecureSkipVerify = false
	if err := c.Probe(context.Background()); err == nil {
		t.Fatal("untrusted certificate accepted")
	}
	if reached.Load() != 0 {
		t.Fatal("credentials sent before certificate verification")
	}
}
