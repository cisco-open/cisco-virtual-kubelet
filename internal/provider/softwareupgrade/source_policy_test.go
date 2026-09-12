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

package softwareupgrade

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestManagedImageSourceRejectsLegacySchemesAndConfigMap(t *testing.T) {
	ctx := WithManagedImageSourcePolicy(context.Background())
	digest := strings.Repeat("a", 64)
	for _, rawURL := range []string{
		"http://images.example.net/cat9k.bin",
		"ftp://images.example.net/cat9k.bin",
		"scp://images.example.net/cat9k.bin",
		"tftp://images.example.net/cat9k.bin",
		"file:///var/lib/images/cat9k.bin",
	} {
		t.Run(strings.SplitN(rawURL, ":", 2)[0], func(t *testing.T) {
			resolver := NewDefaultImageResolver(nil, nil)
			resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
			_, err := resolver.Resolve(ctx, "default", opsv1alpha1.UpgradeImageSource{URL: rawURL, SHA256: digest})
			if err == nil || !strings.Contains(err.Error(), "use HTTPS or SFTP") {
				t.Fatalf("Resolve(%q) error = %v, want managed scheme denial", rawURL, err)
			}
		})
	}

	resolver := NewDefaultImageResolver(nil, nil)
	_, err := resolver.Resolve(ctx, "default", opsv1alpha1.UpgradeImageSource{
		ConfigMapRef: &corev1.LocalObjectReference{Name: "image"},
	})
	if err == nil || !strings.Contains(err.Error(), "must use an HTTPS or SFTP URL") {
		t.Fatalf("managed ConfigMap Resolve error = %v, want byte-source denial", err)
	}
}

func TestManagedImageSourceRejectsUnsafeURLFeatures(t *testing.T) {
	const secretValue = "must-not-appear"
	digest := strings.Repeat("a", 64)
	tests := map[string]struct {
		src  opsv1alpha1.UpgradeImageSource
		want string
	}{
		"userinfo": {
			src:  opsv1alpha1.UpgradeImageSource{URL: "https://user:" + secretValue + "@images.example.net/cat9k.bin", SHA256: digest},
			want: "must not contain user information",
		},
		"query": {
			src:  opsv1alpha1.UpgradeImageSource{URL: "https://images.example.net/cat9k.bin?token=" + secretValue, SHA256: digest},
			want: "must not contain a query string",
		},
		"fragment": {
			src:  opsv1alpha1.UpgradeImageSource{URL: "https://images.example.net/cat9k.bin#" + secretValue, SHA256: digest},
			want: "must not contain a fragment",
		},
		"HTTPS credentials": {
			src: opsv1alpha1.UpgradeImageSource{
				URL:          "https://images.example.net/cat9k.bin",
				SHA256:       digest,
				URLSecretRef: &corev1.LocalObjectReference{Name: "image-creds"},
			},
			want: "anonymous",
		},
		"SFTP without endpoint authorization": {
			src:  opsv1alpha1.UpgradeImageSource{URL: "sftp://images.example.net/cat9k.bin", SHA256: digest},
			want: "endpoint-bound urlSecretRef",
		},
		"SFTP insecure override": {
			src: opsv1alpha1.UpgradeImageSource{
				URL:          "sftp://images.example.net/cat9k.bin?insecureSkipHostKey=true",
				SHA256:       digest,
				URLSecretRef: &corev1.LocalObjectReference{Name: "image-creds"},
			},
			want: "must not contain a query string",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			resolver := NewDefaultImageResolver(nil, nil)
			resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
			_, err := resolver.Resolve(WithManagedImageSourcePolicy(context.Background()), "default", tc.src)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Resolve error = %v, want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), secretValue) {
				t.Fatalf("managed source error leaked URL secret material: %v", err)
			}
		})
	}
}

func TestManagedHTTPSRejectsPrivateLocalAndSpecialIPLiteral(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, host := range []string{
		"10.1.2.3",
		"[fd00::1]",
		"127.0.0.1",
		"[::1]",
		"169.254.169.254",
		"100.100.100.200",
		"168.63.129.16",
		"224.0.0.1",
		"0.0.0.0",
		"198.51.100.10",
	} {
		t.Run(host, func(t *testing.T) {
			resolver := NewDefaultImageResolver(nil, nil)
			resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
			_, err := resolver.Resolve(WithManagedImageSourcePolicy(context.Background()), "default", opsv1alpha1.UpgradeImageSource{
				URL: "https://" + host + "/cat9k.bin", SHA256: digest,
			})
			if err == nil || !strings.Contains(err.Error(), "not permitted") {
				t.Fatalf("Resolve(%s) error = %v, want destination denial", host, err)
			}
		})
	}
}

func TestManagedSFTPEndpointAuthorizationAllowsOnlyPrivateNotLocal(t *testing.T) {
	for _, raw := range []string{"10.1.2.3", "172.16.4.5", "192.168.7.8", "fd00::1"} {
		if err := validateManagedResolvedAddress(netip.MustParseAddr(raw), true); err != nil {
			t.Fatalf("endpoint-authorized private SFTP address %s rejected: %v", raw, err)
		}
	}
	for _, raw := range []string{"127.0.0.1", "169.254.169.254", "100.100.100.200", "fd00:ec2::254"} {
		if err := validateManagedResolvedAddress(netip.MustParseAddr(raw), true); err == nil {
			t.Fatalf("endpoint-authorized SFTP address %s bypassed local/metadata denial", raw)
		}
	}
}

func TestManagedHTTPSPinsValidatedResolutionAndDisablesProxy(t *testing.T) {
	payload := []byte("verified managed image")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Host == "" || req.URL.Path != "/cat9k.bin" {
			t.Errorf("unexpected request host/path: %q %q", req.Host, req.URL.Path)
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(serverURL.Host)
	if err != nil {
		t.Fatal(err)
	}
	baseTransport := server.Client().Transport.(*http.Transport).Clone()
	var proxyCalls atomic.Int32
	baseTransport.Proxy = func(*http.Request) (*url.URL, error) {
		proxyCalls.Add(1)
		return url.Parse("http://127.0.0.1:1")
	}
	baseClient := &http.Client{Transport: baseTransport}

	var lookupCalls, dialCalls atomic.Int32
	ctx := WithManagedImageSourcePolicy(context.Background())
	ctx = withManagedImageSourceNetworkHooks(ctx,
		func(_ context.Context, host string) ([]netip.Addr, error) {
			lookupCalls.Add(1)
			if host != "example.com" {
				t.Fatalf("lookup host = %q, want example.com", host)
			}
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		func(ctx context.Context, network, address string) (net.Conn, error) {
			dialCalls.Add(1)
			if address != net.JoinHostPort("93.184.216.34", port) {
				t.Fatalf("validated dial address = %q, want pinned public IP", address)
			}
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	)

	resolver := NewDefaultImageResolver(nil, baseClient)
	resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
	resolved, err := resolver.Resolve(ctx, "default", opsv1alpha1.UpgradeImageSource{
		URL: "https://example.com:" + port + "/cat9k.bin", SHA256: sha256Hex(payload),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	t.Cleanup(func() { _ = resolved.Cleanup() })
	got, err := io.ReadAll(resolved.Reader)
	if err != nil {
		t.Fatalf("read resolved image: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("resolved payload = %q, want %q", got, payload)
	}
	if got := lookupCalls.Load(); got != 1 {
		t.Fatalf("DNS lookup calls = %d, want exactly 1", got)
	}
	if got := dialCalls.Load(); got != 1 {
		t.Fatalf("dial calls = %d, want exactly 1", got)
	}
	if got := proxyCalls.Load(); got != 0 {
		t.Fatalf("proxy selection calls = %d, want 0", got)
	}
}

func TestManagedHTTPSRejectsRedirectWithoutContactingTarget(t *testing.T) {
	var targetRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/start":
			w.Header().Set("Location", "/target")
			w.WriteHeader(http.StatusFound)
		case "/target":
			targetRequests.Add(1)
			_, _ = w.Write([]byte("must not be fetched"))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	serverURL, _ := url.Parse(server.URL)
	_, port, _ := net.SplitHostPort(serverURL.Host)

	ctx := WithManagedImageSourcePolicy(context.Background())
	ctx = withManagedImageSourceNetworkHooks(ctx,
		func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
		},
	)
	resolver := NewDefaultImageResolver(nil, server.Client())
	resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
	_, err := resolver.Resolve(ctx, "default", opsv1alpha1.UpgradeImageSource{
		URL: "https://example.com:" + port + "/start", SHA256: strings.Repeat("a", 64),
	})
	if err == nil || !errors.Is(err, errManagedImageSourceRedirect) {
		t.Fatalf("Resolve redirect error = %v, want errManagedImageSourceRedirect", err)
	}
	if got := targetRequests.Load(); got != 0 {
		t.Fatalf("redirect target requests = %d, want 0", got)
	}
}

func TestManagedHTTPSRejectsMixedSafeAndPrivateDNSAnswerBeforeDial(t *testing.T) {
	var lookupCalls, dialCalls atomic.Int32
	ctx := WithManagedImageSourcePolicy(context.Background())
	ctx = withManagedImageSourceNetworkHooks(ctx,
		func(context.Context, string) ([]netip.Addr, error) {
			lookupCalls.Add(1)
			return []netip.Addr{
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("10.0.0.8"),
			}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("dial must not be reached")
		},
	)
	resolver := NewDefaultImageResolver(nil, nil)
	resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
	_, err := resolver.Resolve(ctx, "default", opsv1alpha1.UpgradeImageSource{
		URL: "https://images.example.net/cat9k.bin", SHA256: strings.Repeat("a", 64),
	})
	if err == nil || !errors.Is(err, errManagedImageSourceDestinationDenied) {
		t.Fatalf("Resolve mixed DNS answer error = %v, want private destination denial", err)
	}
	if got := lookupCalls.Load(); got != 1 {
		t.Fatalf("DNS lookup calls = %d, want exactly 1", got)
	}
	if got := dialCalls.Load(); got != 0 {
		t.Fatalf("dial calls = %d, want 0", got)
	}
}

func TestManagedHTTPSRequiresVerifiableStandardTransport(t *testing.T) {
	ctx := WithManagedImageSourcePolicy(context.Background())
	u, _ := url.Parse("https://images.example.net/cat9k.bin")
	if _, err := managedHTTPClient(ctx, &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, nil
	})}, u); err == nil || !strings.Contains(err.Error(), "enforceable") {
		t.Fatalf("custom managed HTTP transport error = %v, want enforceability denial", err)
	}
	if _, err := managedHTTPClient(ctx, &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // verifies rejection
	}}, u); err == nil || !strings.Contains(err.Error(), "certificate verification") {
		t.Fatalf("insecure managed TLS error = %v, want verification denial", err)
	}

	ctx = withManagedImageSourceNetworkHooks(ctx,
		func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("not reached")
		},
	)
	clientCertificateCalls := atomic.Int32{}
	verifyConnectionCalls := atomic.Int32{}
	var keyLog bytes.Buffer
	managed, err := managedHTTPClient(ctx, &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		Certificates: []tls.Certificate{{}},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			clientCertificateCalls.Add(1)
			return &tls.Certificate{}, nil
		},
		VerifyConnection: func(tls.ConnectionState) error {
			verifyConnectionCalls.Add(1)
			return nil
		},
		KeyLogWriter: &keyLog,
	}}}, u)
	if err != nil {
		t.Fatalf("managedHTTPClient with base client certificate: %v", err)
	}
	managedTLS := managed.Transport.(*http.Transport).TLSClientConfig
	if len(managedTLS.Certificates) != 0 || managedTLS.GetClientCertificate != nil ||
		managedTLS.VerifyConnection != nil || managedTLS.VerifyPeerCertificate != nil || managedTLS.KeyLogWriter != nil ||
		clientCertificateCalls.Load() != 0 || verifyConnectionCalls.Load() != 0 || keyLog.Len() != 0 {
		t.Fatal("managed anonymous HTTPS retained a credential or callback from the base TLS config")
	}
}

func TestManagedURLSecretFetchRequiresFrozenUIDAndAllowsRotation(t *testing.T) {
	secret := imageSourceSecret("image-creds", map[string]string{
		URLSecretAllowedSchemeKey: "sftp",
		URLSecretAllowedHostKey:   "images.example.net",
		URLSecretAllowedPortKey:   "22",
	})
	secret.UID = "secret-uid"
	secret.ResourceVersion = "23"
	u, err := url.Parse("sftp://images.example.net/cat9k.bin")
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		uid  string
		ok   bool
	}{
		{name: "exact", uid: "secret-uid", ok: true},
		{name: "same UID rotated resourceVersion", uid: "secret-uid", ok: true},
		{name: "missing binding"},
		{name: "recreated", uid: "other-uid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := secret.DeepCopy()
			if strings.Contains(tc.name, "rotated") {
				live.ResourceVersion = "24"
				live.Data["password"] = []byte("rotated-credential")
			}
			resolver := resolverWithURLSecret(t, live)
			ctx := withManagedImageSourceSecretIdentity(WithManagedImageSourcePolicy(context.Background()), tc.uid)
			_, err := resolver.authorizedURLSecret(ctx, "default", u, &corev1.LocalObjectReference{Name: secret.Name})
			if tc.ok && err != nil {
				t.Fatalf("exact frozen Secret denied: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("non-frozen Secret incarnation was accepted")
			}
		})
	}
}

func TestManagedHTTPSDropsRegisteredAlternateProtocol(t *testing.T) {
	baseTransport := &http.Transport{}
	var alternateCalls, dialCalls atomic.Int32
	baseTransport.RegisterProtocol("https", roundTripFunc(func(*http.Request) (*http.Response, error) {
		alternateCalls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("policy bypass")),
		}, nil
	}))
	ctx := withManagedImageSourceNetworkHooks(
		WithManagedImageSourcePolicy(context.Background()),
		func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
		func(context.Context, string, string) (net.Conn, error) {
			dialCalls.Add(1)
			return nil, errors.New("sentinel pinned dial failure")
		},
	)
	u, _ := url.Parse("https://images.example.net/cat9k.bin")
	managed, err := managedHTTPClient(ctx, &http.Client{Transport: baseTransport}, u)
	if err != nil {
		t.Fatalf("managedHTTPClient: %v", err)
	}
	if _, err := managed.Get(u.String()); err == nil || !strings.Contains(err.Error(), "sentinel pinned dial failure") {
		t.Fatalf("managed GET error = %v, want pinned dial failure", err)
	}
	if got := alternateCalls.Load(); got != 0 {
		t.Fatalf("base alternate-protocol calls = %d, want 0", got)
	}
	if got := dialCalls.Load(); got != 1 {
		t.Fatalf("pinned dial calls = %d, want 1", got)
	}
}

func TestManagedSFTPDialerPinsAuthorizedResolutionAndRejectsEndpointSubstitution(t *testing.T) {
	var dialCalls atomic.Int32
	ctx := WithManagedImageSourcePolicy(context.Background())
	ctx = withManagedImageSourceNetworkHooks(ctx,
		func(_ context.Context, host string) ([]netip.Addr, error) {
			if host != "images.internal.example" {
				t.Fatalf("lookup host=%q", host)
			}
			return []netip.Addr{netip.MustParseAddr("10.20.30.40")}, nil
		},
		func(_ context.Context, network, address string) (net.Conn, error) {
			dialCalls.Add(1)
			if network != "tcp" || address != "10.20.30.40:2222" {
				t.Fatalf("dial %s %s, want pinned private SFTP endpoint", network, address)
			}
			return nil, errors.New("sentinel dial failure")
		},
	)
	u, _ := url.Parse("sftp://images.internal.example:2222/cat9k.bin")
	dial, err := managedEndpointDialer(ctx, u, "22", true)
	if err != nil {
		t.Fatalf("managedEndpointDialer: %v", err)
	}
	if _, err := dial(ctx, "tcp", "images.internal.example:2222"); err == nil || !strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("pinned dial error=%v", err)
	}
	if _, err := dial(ctx, "udp", "images.internal.example:2222"); err == nil || !strings.Contains(err.Error(), "unexpected network") {
		t.Fatalf("unexpected-network error=%v", err)
	}
	if _, err := dial(ctx, "tcp", "attacker.internal.example:2222"); err == nil || !strings.Contains(err.Error(), "unexpected endpoint") {
		t.Fatalf("endpoint-substitution error=%v", err)
	}
	if _, err := dial(ctx, "tcp", "images.internal.example:22"); err == nil || !strings.Contains(err.Error(), "unexpected endpoint") {
		t.Fatalf("port-substitution error=%v", err)
	}
	if got := dialCalls.Load(); got != 1 {
		t.Fatalf("underlying dial calls=%d, want only exact authorized endpoint", got)
	}
}

func TestStandaloneResolverPreservesRedirectAndCustomClientBehaviour(t *testing.T) {
	t.Run("redirect", func(t *testing.T) {
		payload := []byte("standalone redirect payload")
		var targetRequests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.URL.Path == "/start" {
				http.Redirect(w, req, "/target", http.StatusFound)
				return
			}
			targetRequests.Add(1)
			_, _ = w.Write(payload)
		}))
		t.Cleanup(server.Close)
		resolver := NewDefaultImageResolver(nil, server.Client())
		resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
		resolved, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
			URL: server.URL + "/start", SHA256: sha256Hex(payload),
		})
		if err != nil {
			t.Fatalf("standalone redirect Resolve: %v", err)
		}
		t.Cleanup(func() { _ = resolved.Cleanup() })
		if got := targetRequests.Load(); got != 1 {
			t.Fatalf("standalone redirect target requests = %d, want 1", got)
		}
	})

	t.Run("custom RoundTripper", func(t *testing.T) {
		payload := []byte("standalone custom transport payload")
		var calls atomic.Int32
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: int64(len(payload)),
				Header:        make(http.Header),
				Body:          io.NopCloser(bytes.NewReader(payload)),
			}, nil
		})}
		resolver := NewDefaultImageResolver(nil, client)
		resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
		resolved, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
			URL: "https://images.example.net/cat9k.bin", SHA256: sha256Hex(payload),
		})
		if err != nil {
			t.Fatalf("standalone custom-client Resolve: %v", err)
		}
		t.Cleanup(func() { _ = resolved.Cleanup() })
		if got := calls.Load(); got != 1 {
			t.Fatalf("custom RoundTripper calls = %d, want 1", got)
		}
	})
}
