// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewTLSServer(h)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "username"), []byte("user"), 0600)
	os.WriteFile(filepath.Join(dir, "password"), []byte("password"), 0600)
	c := &Client{Endpoint: server.URL, CredentialPath: dir, InsecureSkipVerify: true, RequestsPerSecond: 10000, MaxSessionLifetime: time.Hour}
	t.Cleanup(c.Invalidate)
	return c
}
func auth(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/j_security_check" {
		r.ParseForm()
		if r.Form.Get("j_password") != "password" {
			http.Error(w, "bad", 401)
			return true
		}
		http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "session", Path: "/"})
		return true
	}
	if r.URL.Path == "/dataservice/client/token" {
		fmt.Fprint(w, "token")
		return true
	}
	return false
}
func TestClientReadRefreshAndNoMutationReplay(t *testing.T) {
	var logins, reads, writes atomic.Int32
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/j_security_check" {
			logins.Add(1)
		}
		if auth(w, r) {
			return
		}
		if r.Header.Get("X-XSRF-TOKEN") != "token" {
			t.Error("missing token")
		}
		if r.Method == "POST" {
			writes.Add(1)
			http.Error(w, "SECRET", 401)
			return
		}
		if reads.Add(1) == 1 {
			http.Error(w, "SECRET", 401)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	})
	var out map[string]any
	if err := c.Request(context.Background(), "GET", "/dataservice/test", nil, &out); err != nil {
		t.Fatal(err)
	}
	if logins.Load() != 2 || reads.Load() != 2 {
		t.Fatal("read refresh missing")
	}
	err := c.Request(context.Background(), "POST", "/dataservice/test", map[string]any{}, &out)
	if err == nil || strings.Contains(err.Error(), "SECRET") || writes.Load() != 1 || logins.Load() != 2 {
		t.Fatal("mutation replay or leak")
	}
}
func TestClientRejectsLoginHTMLAndRedirect(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		t.Run(fmt.Sprint(redirect), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if redirect {
					http.Redirect(w, r, "https://example.invalid", 302)
				} else {
					http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: "x"})
					fmt.Fprint(w, "<html>login</html>")
				}
			})
			if c.Request(context.Background(), "GET", "/dataservice/device", nil, nil) == nil {
				t.Fatal("accepted rejected login")
			}
		})
	}
}
func TestClientNoPartialInventory(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			return
		}
		if strings.HasSuffix(r.URL.Path, "server") {
			fmt.Fprint(w, `{"data":{"platformVersion":"20.18.2.2"}}`)
		} else {
			fmt.Fprint(w, `{"data":null}`)
		}
	})
	if _, err := c.Discover(context.Background()); err == nil {
		t.Fatal("accepted incomplete inventory")
	}
}
func TestNativeBannerLifecycle(t *testing.T) {
	var stored map[string]any
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			return
		}
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/feature"):
			if stored == nil {
				fmt.Fprint(w, `{"data":[]}`)
			} else {
				json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"templateId": "fixture-id", "templateName": stored["templateName"], "templateType": "cisco_banner"}}})
			}
		case r.Method == "GET":
			stored["attachedMastersCount"] = 0
			stored["devicesAttached"] = 0
			json.NewEncoder(w).Encode(stored)
		case r.Method == "POST" || r.Method == "PUT":
			writes++
			json.NewDecoder(r.Body).Decode(&stored)
			fmt.Fprint(w, `{"templateId":"fixture-id"}`)
		case r.Method == "DELETE":
			stored = nil
			fmt.Fprint(w, `{}`)
		}
	})
	resources, e := Compile([]byte("sdwan:\n  edge_feature_templates:\n    banner_templates:\n      - name: fixture\n        description: test\n"))
	if e != nil {
		t.Fatal(e)
	}
	r := resources[0]
	ctx := context.Background()
	o, e := c.Observe(ctx, r)
	if e != nil || o != nil {
		t.Fatal("initial observation", e)
	}
	id, e := c.Create(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	o, e = c.Observe(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	same, e := Equal(r, o.Body)
	if e != nil || !same {
		t.Fatal("readback", e)
	}
	if writes != 1 {
		t.Fatal("writes")
	}
	if e = c.DeleteFixture(ctx, id); e != nil {
		t.Fatal(e)
	}
	o, e = c.Observe(ctx, r)
	if e != nil || o != nil {
		t.Fatal("cleanup", e)
	}
}
