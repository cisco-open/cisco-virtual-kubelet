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

package nexusdashboard

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestVersionReadsProductVersionAndIgnoresBanner(t *testing.T) {
	f := newFakeND(t, false)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	v, err := c.Version(context.Background())
	if err != nil || v != "4.3.1" {
		t.Fatalf("version = %q err=%v, want 4.3.1", v, err)
	}
	if strings.Contains(v, "UNAUTHORIZED") {
		t.Fatal("the pre-login banner must never reach the result")
	}
}

func TestVersionFallbackAndErrors(t *testing.T) {
	f := newFakeND(t, false)
	c := testClient(f, writeCreds(t, "s3cret", ""), "", false)
	serve := func(body string) {
		f.about.Store(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	}
	serve(`{"buildVersion":"4.3.1.175"}`)
	if v, err := c.Version(context.Background()); err != nil || v != "4.3.1.175" {
		t.Fatalf("fallback to buildVersion: %q %v", v, err)
	}
	for _, bad := range []string{`{}`, `{"productVersion":"  "}`, `not json`} {
		serve(bad)
		if _, err := c.Version(context.Background()); err == nil {
			t.Fatalf("%q must be an error", bad)
		}
	}
}

func TestHealthPassPublishesControllerVersion(t *testing.T) {
	var failVersion bool
	a, _, get := newTestAdapter(t, func(context.Context) error { return nil })
	a.version = func(context.Context) (string, error) {
		if failVersion {
			return "", errors.New("about unavailable")
		}
		return "4.3.1", nil
	}
	ctx := context.Background()
	a.check(ctx)
	if got := get().Status.ControllerVersion; got != "4.3.1" {
		t.Fatalf("controllerVersion = %q", got)
	}

	failVersion = true // informational only: health stays Ready, last version is kept
	a.version = func(context.Context) (string, error) { return "", errors.New("about unavailable") }
	a.check(ctx)
	nc := get()
	if nc.Status.ControllerVersion != "4.3.1" || nc.Status.Phase != "Ready" {
		t.Fatalf("a failed version read must not change health or clear the version: %+v", nc.Status)
	}
}

func TestVersionNotReadWhenProbeFails(t *testing.T) {
	a, _, get := newTestAdapter(t, func(context.Context) error { return errAuthentication })
	called := false
	a.version = func(context.Context) (string, error) { called = true; return "4.3.1", nil }
	a.check(context.Background())
	if called || get().Status.ControllerVersion != "" {
		t.Fatal("the version must be read only after a successful probe")
	}
}
