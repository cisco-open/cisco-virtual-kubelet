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
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/pin/tftp/v3"
	"github.com/pkg/sftp"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestDefaultImageResolverTFTPURL(t *testing.T) {
	payload := []byte("cat9k test image payload")
	wantSHA := sha256Hex(payload)

	srv := tftp.NewServer(func(filename string, rf io.ReaderFrom) error {
		if filename != "images/cat9k.bin" {
			return fmt.Errorf("unexpected filename %q", filename)
		}
		_, err := rf.ReadFrom(bytes.NewReader(payload))
		return err
	}, nil)
	srv.SetBlockSize(1468)

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(pc) }()
	t.Cleanup(func() {
		srv.Shutdown()
		_ = pc.Close()
		select {
		case err := <-errCh:
			if err == nil || errors.Is(err, net.ErrClosed) {
				return
			}
			t.Fatalf("tftp server: %v", err)
		case <-time.After(2 * time.Second):
			t.Fatal("tftp server did not stop")
		}
	})

	src := opsv1alpha1.UpgradeImageSource{
		URL:    "tftp://" + pc.LocalAddr().String() + "/images/cat9k.bin",
		SHA256: wantSHA,
	}
	res, err := NewDefaultImageResolver(nil, nil).Resolve(context.Background(), "default", src)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	t.Cleanup(func() { _ = res.Cleanup() })
	got, err := io.ReadAll(res.Reader)
	if err != nil {
		t.Fatalf("read resolved image: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q want %q", got, payload)
	}
	if res.Size != int64(len(payload)) {
		t.Fatalf("size mismatch: got %d want %d", res.Size, len(payload))
	}
}

func TestDefaultImageResolverTFTPURLRequestsDefaultBlockSize(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer pc.Close()

	rrqCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1024)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		rrqCh <- append([]byte(nil), buf[:n]...)
		_, _ = pc.WriteTo([]byte{0, 5, 0, 1, 's', 't', 'o', 'p', 0}, addr)
	}()

	src := opsv1alpha1.UpgradeImageSource{
		URL:    "tftp://" + pc.LocalAddr().String() + "/images/cat9k.bin",
		SHA256: strings.Repeat("0", 64),
	}
	_, err = NewDefaultImageResolver(nil, nil).Resolve(context.Background(), "default", src)
	if err == nil {
		t.Fatal("Resolve succeeded, want test server error")
	}

	select {
	case rrq := <-rrqCh:
		if !bytes.Contains(rrq, []byte("blksize\x008192")) {
			t.Fatalf("RRQ %q does not request default block size 8192", string(rrq))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RRQ")
	}
}

func TestDefaultImageResolverSCPRequiresHostKeyPolicy(t *testing.T) {
	resolver := resolverWithURLSecret(t, imageSourceSecret("image-creds", map[string]string{
		URLSecretAllowedSchemeKey: "scp",
		URLSecretAllowedHostKey:   "127.0.0.1",
		URLSecretAllowedPortKey:   "22",
		"username":                "user",
		"password":                "not-logged",
	}))
	src := opsv1alpha1.UpgradeImageSource{
		URL:          "scp://127.0.0.1/tmp/cat9k.bin",
		SHA256:       strings.Repeat("a", 64),
		URLSecretRef: &corev1.LocalObjectReference{Name: "image-creds"},
	}
	_, err := resolver.Resolve(context.Background(), "default", src)
	if err == nil {
		t.Fatal("Resolve admitted SCP without host-key policy")
	}
	if !strings.Contains(err.Error(), "knownHosts") {
		t.Fatalf("expected host-key policy error, got %v", err)
	}
	if strings.Contains(err.Error(), "not-logged") {
		t.Fatalf("error leaked URL password: %v", err)
	}
}

func TestDefaultImageResolverSCPInsecureRequiresOperatorEnv(t *testing.T) {
	t.Setenv(envAllowInsecureSSH, "")
	resolver := resolverWithURLSecret(t, imageSourceSecret("image-creds", map[string]string{
		URLSecretAllowedSchemeKey: "scp",
		URLSecretAllowedHostKey:   "127.0.0.1",
		URLSecretAllowedPortKey:   "22",
		"username":                "user",
		"password":                "not-logged",
	}))
	src := opsv1alpha1.UpgradeImageSource{
		URL:          "scp://127.0.0.1/tmp/cat9k.bin?insecureSkipHostKey=true",
		SHA256:       strings.Repeat("a", 64),
		URLSecretRef: &corev1.LocalObjectReference{Name: "image-creds"},
	}
	_, err := resolver.Resolve(context.Background(), "default", src)
	if err == nil {
		t.Fatal("Resolve admitted insecure SCP host-key bypass without operator env gate")
	}
	if !strings.Contains(err.Error(), envAllowInsecureSSH) {
		t.Fatalf("expected env-gate error, got %v", err)
	}
	if strings.Contains(err.Error(), "not-logged") {
		t.Fatalf("error leaked URL password: %v", err)
	}
}

func TestSSHHandshakeHonorsContextDeadline(t *testing.T) {
	t.Setenv(envAllowInsecureSSH, "true")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	accepted := make(chan struct{})
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		close(accepted)
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn) // accept TCP but never send an SSH banner
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-serverDone:
		case <-time.After(2 * time.Second):
			t.Error("stalled SSH test server did not stop")
		}
	})

	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}
	data := validEndpointBinding("sftp", host, port)
	data["username"] = "image-reader"
	data["password"] = "credential-value"
	resolver := resolverWithURLSecret(t, imageSourceSecret("image-creds", data))
	u, err := url.Parse("sftp://" + listener.Addr().String() + "/cat9k.bin?insecureSkipHostKey=true")
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = resolver.sshClient(ctx, "default", u, &corev1.LocalObjectReference{Name: "image-creds"})
	if err == nil {
		t.Fatal("SSH handshake succeeded against a server that sent no banner")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("handshake error=%v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("SSH handshake ignored context deadline: elapsed=%v error=%v", elapsed, err)
	}
	select {
	case <-accepted:
	default:
		t.Fatal("test server never accepted the SSH TCP connection")
	}
}

func TestDefaultImageResolverRejectsURLUserInfo(t *testing.T) {
	const password = "must-not-appear"
	_, err := NewDefaultImageResolver(nil, nil).Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL:    "sftp://user:" + password + "@images.example.net/cat9k.bin",
		SHA256: strings.Repeat("a", 64),
	})
	if err == nil || !strings.Contains(err.Error(), "must not contain user information") {
		t.Fatalf("Resolve error = %v, want URL user information rejection", err)
	}
	if strings.Contains(err.Error(), password) {
		t.Fatalf("URL user information rejection leaked password: %v", err)
	}
}

func TestURLSecretRequiresPurposeAndCompleteEndpointBinding(t *testing.T) {
	tests := map[string]struct {
		labels map[string]string
		data   map[string]string
		want   string
	}{
		"missing purpose": {
			data: validEndpointBinding("sftp", "images.example.net", "22"),
			want: URLSecretPurposeLabel,
		},
		"wrong purpose": {
			labels: map[string]string{URLSecretPurposeLabel: "device-credentials"},
			data:   validEndpointBinding("sftp", "images.example.net", "22"),
			want:   URLSecretPurposeValue,
		},
		"missing scheme": {
			labels: map[string]string{URLSecretPurposeLabel: URLSecretPurposeValue},
			data:   validEndpointBinding("", "images.example.net", "22"),
			want:   URLSecretAllowedSchemeKey,
		},
		"missing host": {
			labels: map[string]string{URLSecretPurposeLabel: URLSecretPurposeValue},
			data:   validEndpointBinding("sftp", "", "22"),
			want:   URLSecretAllowedHostKey,
		},
		"missing port": {
			labels: map[string]string{URLSecretPurposeLabel: URLSecretPurposeValue},
			data:   validEndpointBinding("sftp", "images.example.net", ""),
			want:   URLSecretAllowedPortKey,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			secret := imageSourceSecret("image-creds", tc.data)
			secret.Labels = tc.labels
			resolver := resolverWithURLSecret(t, secret)
			u, err := url.Parse("sftp://images.example.net/cat9k.bin")
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolver.authorizedURLSecret(context.Background(), "default", u, &corev1.LocalObjectReference{Name: secret.Name})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("authorizedURLSecret error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestURLSecretEndpointBindingMatchesCanonicalURL(t *testing.T) {
	secret := imageSourceSecret("image-creds", map[string]string{
		URLSecretAllowedSchemeKey: "SFTP",
		URLSecretAllowedHostKey:   "IMAGES.EXAMPLE.NET.",
		URLSecretAllowedPortKey:   "022",
		"username":                "image-reader",
		"password":                "credential-value",
	})
	resolver := resolverWithURLSecret(t, secret)
	u, err := url.Parse("sftp://images.example.net./cat9k.bin")
	if err != nil {
		t.Fatal(err)
	}
	creds, err := resolver.urlCredentials(context.Background(), "default", u, &corev1.LocalObjectReference{Name: secret.Name})
	if err != nil {
		t.Fatalf("urlCredentials: %v", err)
	}
	if creds.Username != "image-reader" || creds.Password != "credential-value" {
		t.Fatalf("credentials not loaded after endpoint authorization: %+v", creds)
	}
}

func TestURLSecretEndpointBindingRejectsMismatchWithoutLeakingCredentials(t *testing.T) {
	tests := map[string]map[string]string{
		"scheme": validEndpointBinding("ftp", "images.example.net", "22"),
		"host":   validEndpointBinding("sftp", "attacker.example.net", "22"),
		"port":   validEndpointBinding("sftp", "images.example.net", "2222"),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			data["password"] = "credential-must-not-leak"
			secret := imageSourceSecret("image-creds", data)
			resolver := resolverWithURLSecret(t, secret)
			u, err := url.Parse("sftp://images.example.net/cat9k.bin")
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolver.urlCredentials(context.Background(), "default", u, &corev1.LocalObjectReference{Name: secret.Name})
			if err == nil || !strings.Contains(err.Error(), "does not authorize") {
				t.Fatalf("urlCredentials error = %v, want endpoint mismatch", err)
			}
			if strings.Contains(err.Error(), "credential-must-not-leak") {
				t.Fatalf("endpoint mismatch leaked credentials: %v", err)
			}
		})
	}
}

func TestURLSecretRejectsUnsupportedCredentialScheme(t *testing.T) {
	secret := imageSourceSecret("image-creds", validEndpointBinding("sftp", "images.example.net", "22"))
	resolver := resolverWithURLSecret(t, secret)
	_, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL:          "https://images.example.net/cat9k.bin",
		SHA256:       strings.Repeat("a", 64),
		URLSecretRef: &corev1.LocalObjectReference{Name: secret.Name},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported for scheme") {
		t.Fatalf("Resolve error = %v, want unsupported credential scheme", err)
	}
}

func TestRedactURLDropsUserinfoQueryAndFragment(t *testing.T) {
	raw := "sftp://user:secret@example.com/images/cat9k.bin?token=secret-query#secret-fragment"
	got := redactRawURL(raw)
	if got != "sftp://example.com/images/cat9k.bin" {
		t.Fatalf("redacted URL = %q", got)
	}
	if strings.Contains(got, "user") || strings.Contains(got, "secret") || strings.Contains(got, "token") {
		t.Fatalf("redacted URL leaked sensitive URL material: %q", got)
	}
}

func TestDefaultImageResolverRedactsInvalidURLParseError(t *testing.T) {
	const token = "parse-token-must-not-leak"
	_, err := NewDefaultImageResolver(nil, nil).Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL:    "https://images.example.net/cat9k.bin?token=" + token + "\n",
		SHA256: strings.Repeat("a", 64),
	})
	if err == nil {
		t.Fatal("Resolve accepted invalid URL")
	}
	if strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "<invalid-url-redacted>") {
		t.Fatalf("invalid URL error was not fully redacted: %v", err)
	}
}

func TestDefaultImageResolverRedactsHTTPTransportErrorURL(t *testing.T) {
	const token = "signed-token-must-not-leak"
	transportErr := &net.DNSError{Err: "transport failed with " + token, Name: "images.example.net", IsTimeout: true}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("request %s: %w", req.URL.String(), transportErr)
	})}
	resolver := NewDefaultImageResolver(nil, client)
	resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
	_, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL:    "https://images.example.net/cat9k.bin?token=" + token + "#fragment-secret",
		SHA256: strings.Repeat("a", 64),
	})
	if err == nil {
		t.Fatal("Resolve succeeded, want HTTP transport error")
	}
	if !errors.Is(err, transportErr) {
		t.Fatalf("Resolve error = %v, want wrapped transport cause", err)
	}
	if !IsRetryableResolveError(err) {
		t.Fatalf("Resolve error = %v, want retryable classification", err)
	}
	var networkErr net.Error
	if !errors.As(err, &networkErr) || !networkErr.Timeout() {
		t.Fatalf("Resolve error = %v, want preserved net.Error classification", err)
	}
	if got := err.Error(); got != "image source HTTP get https://images.example.net/cat9k.bin: failed" {
		t.Fatalf("Resolve error = %q, want only the redacted endpoint", got)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "fragment-secret") {
		t.Fatalf("HTTP transport error leaked URL credentials: %v", err)
	}
}

func TestDefaultImageResolverKeepsPermanentHTTPTransportErrorTerminal(t *testing.T) {
	const token = "redirect-token-must-not-leak"
	policyErr := errors.New("redirect policy rejected " + token)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("request %s: %w", req.URL.String(), policyErr)
	})}
	resolver := NewDefaultImageResolver(nil, client)
	resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
	_, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL:    "https://images.example.net/cat9k.bin?token=" + token,
		SHA256: strings.Repeat("a", 64),
	})
	if err == nil || !errors.Is(err, policyErr) {
		t.Fatalf("Resolve error = %v, want preserved permanent transport cause", err)
	}
	if IsRetryableResolveError(err) {
		t.Fatalf("permanent HTTP transport error classified retryable: %v", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("permanent HTTP transport error leaked URL credentials: %v", err)
	}
}

func TestDefaultImageResolverClassifiesHTTPStatus(t *testing.T) {
	payload := []byte("verified image")
	tests := map[string]struct {
		status        int
		wantRetryable bool
		wantSuccess   bool
	}{
		"service unavailable": {status: http.StatusServiceUnavailable, wantRetryable: true},
		"request timeout":     {status: http.StatusRequestTimeout, wantRetryable: true},
		"too early":           {status: http.StatusTooEarly, wantRetryable: true},
		"too many requests":   {status: http.StatusTooManyRequests, wantRetryable: true},
		"not found":           {status: http.StatusNotFound},
		"success":             {status: http.StatusOK, wantSuccess: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				if tc.wantSuccess {
					_, _ = w.Write(payload)
				}
			}))
			t.Cleanup(server.Close)
			resolver := NewDefaultImageResolver(nil, server.Client())
			resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
			resolved, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
				URL: server.URL + "/cat9k.bin", SHA256: sha256Hex(payload),
			})
			if tc.wantSuccess {
				if err != nil {
					t.Fatalf("Resolve: %v", err)
				}
				t.Cleanup(func() { _ = resolved.Cleanup() })
				if IsRetryableResolveError(err) {
					t.Fatal("successful HTTP resolution was classified as retryable")
				}
				return
			}
			if err == nil {
				t.Fatalf("Resolve succeeded for HTTP status %d", tc.status)
			}
			if got := IsRetryableResolveError(err); got != tc.wantRetryable {
				t.Fatalf("retryable classification = %t, want %t for error %v", got, tc.wantRetryable, err)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("status %d", tc.status)) {
				t.Fatalf("Resolve error = %v, want HTTP status", err)
			}
		})
	}
}

func TestDefaultImageResolverClassifiesAndRedactsHTTPBodyInterruption(t *testing.T) {
	const token = "body-token-must-not-leak"
	bodyErr := errors.New("interrupted response for " + token)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: -1,
			Header:        make(http.Header),
			Body:          &interruptingReadCloser{data: []byte("partial"), err: bodyErr},
		}, nil
	})}
	cacheDir := filepath.Join(t.TempDir(), "cache")
	resolver := NewDefaultImageResolver(nil, client)
	resolver.CacheDir = cacheDir
	_, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL: "https://images.example.net/cat9k.bin?token=" + token, SHA256: strings.Repeat("a", 64),
	})
	if err == nil || !IsRetryableResolveError(err) || !errors.Is(err, bodyErr) {
		t.Fatalf("Resolve error = %v, want retryable wrapped body interruption", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatalf("HTTP body error leaked URL credentials: %v", err)
	}
	entries, readErr := os.ReadDir(cacheDir)
	if readErr != nil {
		t.Fatalf("read cache directory: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("interrupted HTTP body left cache entries: %v", entries)
	}
}

func TestDefaultImageResolverClassifiesTransientConfigMapGet(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	transientErr := apierrors.NewServiceUnavailable("temporary API outage")
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
				return transientErr
			},
		}).
		Build()
	resolver := NewDefaultImageResolver(k8sClient, nil)
	_, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		ConfigMapRef: &corev1.LocalObjectReference{Name: "image"},
	})
	if !IsRetryableResolveError(err) {
		t.Fatalf("Resolve error = %v, want retryable classification", err)
	}
	if !errors.Is(err, transientErr) {
		t.Fatalf("Resolve error = %v, want preserved Kubernetes API cause", err)
	}
	_, err = resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL:          "ftp://images.example.net/cat9k.bin",
		SHA256:       strings.Repeat("a", 64),
		URLSecretRef: &corev1.LocalObjectReference{Name: "image-credentials"},
	})
	if !IsRetryableResolveError(err) || !errors.Is(err, transientErr) {
		t.Fatalf("URL Secret Resolve error = %v, want retryable preserved Kubernetes API cause", err)
	}
}

func TestProtocolRetryClassifiers(t *testing.T) {
	networkErr := &net.DNSError{Err: "temporary lookup failure", Name: "images.example.net", IsTimeout: true}
	if got := classifyConnectionFailure(networkErr); !IsRetryableResolveError(got) || !errors.Is(got, networkErr) {
		t.Fatalf("network classification = %v, want retryable preserved cause", got)
	}
	ftpTransient := &textproto.Error{Code: ftp.StatusNotAvailable, Msg: "try later"}
	if got := classifyFTPFailure(ftpTransient); !IsRetryableResolveError(got) || !errors.Is(got, ftpTransient) {
		t.Fatalf("FTP 4xx classification = %v, want retryable preserved cause", got)
	}
	for _, permanent := range []error{
		&textproto.Error{Code: ftp.StatusInvalidCredentials, Msg: "invalid credentials"},
		&textproto.Error{Code: ftp.StatusFileUnavailable, Msg: "missing file"},
	} {
		if got := classifyFTPFailure(permanent); IsRetryableResolveError(got) {
			t.Fatalf("FTP permanent error classified retryable: %v", got)
		}
	}
	if got := classifySFTPFailure(sftp.ErrSSHFxConnectionLost); !IsRetryableResolveError(got) {
		t.Fatalf("SFTP connection loss classification = %v, want retryable", got)
	}
	if got := classifySFTPFailure(sftp.ErrSSHFxPermissionDenied); IsRetryableResolveError(got) {
		t.Fatalf("SFTP permission error classified retryable: %v", got)
	}
}

func TestRetryableContextWriterStopsCanceledTransfer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var destination bytes.Buffer
	writer := &retryableContextWriter{ctx: ctx, writer: &destination}
	if _, err := writer.Write([]byte("first block")); err != nil {
		t.Fatalf("write before cancellation: %v", err)
	}
	cancel()
	if n, err := writer.Write([]byte("second block")); n != 0 || !errors.Is(err, context.Canceled) || !IsRetryableResolveError(err) {
		t.Fatalf("write after cancellation = (%d, %v), want (0, retryable context.Canceled)", n, err)
	}
	if got := destination.String(); got != "first block" {
		t.Fatalf("destination after cancellation = %q, want only first block", got)
	}
}

func TestSCPControlLinesAreBounded(t *testing.T) {
	tests := map[string]struct {
		prefix byte
		read   func(*bufio.Reader) error
	}{
		"file header": {
			prefix: 'C',
			read: func(reader *bufio.Reader) error {
				_, _, err := readSCPLine(reader)
				return err
			},
		},
		"remote response error": {
			prefix: 1,
			read: func(reader *bufio.Reader) error {
				_, _, err := readSCPLine(reader)
				return err
			},
		},
		"remote file error": {
			prefix: 1,
			read:   readSCPStatus,
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			input := append([]byte{tc.prefix}, bytes.Repeat([]byte{'x'}, maxSCPControlLineSize+1)...)
			input = append(input, '\n')
			err := tc.read(bufio.NewReader(bytes.NewReader(input)))
			if !errors.Is(err, errSCPControlLineTooLong) {
				t.Fatalf("control-line error = %v, want errSCPControlLineTooLong", err)
			}
			if IsRetryableResolveError(err) {
				t.Fatalf("oversized SCP control line classified retryable: %v", err)
			}
		})
	}
}

func TestDefaultImageResolverRejectsStreamingImageOverLimit(t *testing.T) {
	payload := []byte("image-larger-than-limit")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Flush the headers before writing so net/http cannot synthesize a
		// Content-Length; this exercises the streaming limit, not the precheck.
		w.(http.Flusher).Flush()
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)

	cacheDir := filepath.Join(t.TempDir(), "cache")
	if err := os.Mkdir(cacheDir, 0o755); err != nil {
		t.Fatalf("create permissive cache directory: %v", err)
	}
	if err := os.Chmod(cacheDir, 0o755); err != nil {
		t.Fatalf("set permissive cache directory mode: %v", err)
	}
	resolver := NewDefaultImageResolver(nil, server.Client())
	resolver.CacheDir = cacheDir
	resolver.MaxImageBytes = 8
	_, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL:    server.URL + "/cat9k.bin",
		SHA256: sha256Hex(payload),
	})
	if !errors.Is(err, errImageTooLarge) {
		t.Fatalf("Resolve error = %v, want errImageTooLarge", err)
	}
	entries, readErr := os.ReadDir(cacheDir)
	if readErr != nil {
		t.Fatalf("read cache directory: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("oversized image left cache entries: %v", entries)
	}
}

func TestDefaultImageResolverRejectsOversizedHTTPContentLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	resolver := NewDefaultImageResolver(nil, server.Client())
	resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
	resolver.MaxImageBytes = 16
	_, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL:    server.URL + "/cat9k.bin",
		SHA256: strings.Repeat("a", 64),
	})
	if !errors.Is(err, errImageTooLarge) {
		t.Fatalf("Resolve error = %v, want errImageTooLarge", err)
	}
}

func TestDefaultImageResolverBoundsRemoteMaterializationTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-req.Context().Done()
	}))
	t.Cleanup(server.Close)

	resolver := NewDefaultImageResolver(nil, server.Client())
	resolver.CacheDir = filepath.Join(t.TempDir(), "cache")
	resolver.ResolveTimeout = 25 * time.Millisecond
	started := time.Now()
	_, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL: server.URL + "/cat9k.bin", SHA256: strings.Repeat("a", 64),
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Resolve error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Resolve exceeded bounded cancellation time: %s", elapsed)
	}
}

func TestDefaultImageResolverCreatesPrivateCache(t *testing.T) {
	payload := []byte("verified image")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)

	cacheDir := filepath.Join(t.TempDir(), "cache")
	resolver := NewDefaultImageResolver(nil, server.Client())
	resolver.CacheDir = cacheDir
	resolved, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL:    server.URL + "/cat9k.bin",
		SHA256: sha256Hex(payload),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	t.Cleanup(func() { _ = resolved.Cleanup() })

	dirInfo, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("stat cache directory: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("cache directory permissions = %#o, want 0700", got)
	}
	cachePath := filepath.Join(cacheDir, sha256Hex(payload)+".bin")
	cacheInfo, err := os.Stat(cachePath)
	if err != nil {
		t.Fatalf("stat cache image: %v", err)
	}
	if !cacheInfo.Mode().IsRegular() {
		t.Fatalf("cache image mode = %v, want regular file", cacheInfo.Mode())
	}
	if got := cacheInfo.Mode().Perm(); got != 0o600 {
		t.Fatalf("cache image permissions = %#o, want 0600", got)
	}
	file, ok := resolved.Reader.(*os.File)
	if !ok {
		t.Fatalf("resolved reader type = %T, want the verified cache temp descriptor", resolved.Reader)
	}
	descriptorInfo, err := file.Stat()
	if err != nil {
		t.Fatalf("stat resolved descriptor: %v", err)
	}
	if !os.SameFile(cacheInfo, descriptorInfo) {
		t.Fatal("cache miss returned a descriptor for a copied or reopened file")
	}
	if _, err := os.Lstat(file.Name()); !os.IsNotExist(err) {
		t.Fatalf("original cache temp path still exists after atomic publish: %v", err)
	}

	// Replacing the pathname after verification must not change the bytes read
	// from the already-open, verified descriptor returned by Resolve.
	if err := os.Rename(cachePath, cachePath+".verified"); err != nil {
		t.Fatalf("rename verified cache image: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("write replacement cache image: %v", err)
	}
	got, err := io.ReadAll(resolved.Reader)
	if err != nil {
		t.Fatalf("read resolved image: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("resolved reader followed replaced path: got %q want %q", got, payload)
	}
}

func TestDefaultImageResolverCacheHitRevalidationIsStable(t *testing.T) {
	payload := []byte("stable verified image")
	digest := sha256Hex(payload)
	requests := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests <- struct{}{}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)

	cacheDir := filepath.Join(t.TempDir(), "cache")
	resolver := NewDefaultImageResolver(nil, server.Client())
	resolver.CacheDir = cacheDir
	src := opsv1alpha1.UpgradeImageSource{URL: server.URL + "/cat9k.bin", SHA256: digest}
	resolveAndRead := func() {
		t.Helper()
		resolved, err := resolver.Resolve(context.Background(), "default", src)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		got, err := io.ReadAll(resolved.Reader)
		if err != nil {
			t.Fatalf("read resolved image: %v", err)
		}
		if err := resolved.Cleanup(); err != nil {
			t.Fatalf("close resolved image: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("resolved image = %q, want %q", got, payload)
		}
	}

	resolveAndRead()
	resolveAndRead()
	if got := len(requests); got != 1 {
		t.Fatalf("remote requests after stable cache hit = %d, want 1", got)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, digest+".bin"), []byte("corrupt"), 0o600); err != nil {
		t.Fatalf("corrupt cache image: %v", err)
	}
	resolveAndRead()
	resolveAndRead()
	if got := len(requests); got != 2 {
		t.Fatalf("remote requests after corruption recovery = %d, want 2", got)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != digest+".bin" {
		t.Fatalf("cache entries after repeated resolution = %v", entries)
	}
}

func TestMaterializeRemoteImageCleansTempOnCancellation(t *testing.T) {
	cacheDir := t.TempDir()
	payload := []byte("partial image")
	_, err := materializeRemoteImage("test image", sha256Hex(payload), cacheDir, 1024, func(w io.Writer) (int64, error) {
		n, writeErr := w.Write(payload[:4])
		if writeErr != nil {
			return int64(n), writeErr
		}
		return int64(n), context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("materializeRemoteImage error = %v, want context cancellation", err)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("read cache directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("canceled materialization left cache entries: %v", entries)
	}
}

func TestPublishMaterializedImageRejectsHijackedTempPath(t *testing.T) {
	cacheDir := t.TempDir()
	payload := []byte("verified image")
	digest := sha256Hex(payload)
	materialized, err := materializeRemoteImage("test image", digest, cacheDir, 1024, func(w io.Writer) (int64, error) {
		return io.Copy(w, bytes.NewReader(payload))
	})
	if err != nil {
		t.Fatalf("materializeRemoteImage: %v", err)
	}
	originalPath := materialized.path
	heldPath := originalPath + ".held"
	if err := os.Rename(originalPath, heldPath); err != nil {
		t.Fatalf("move verified temp inode: %v", err)
	}
	if err := os.Symlink(heldPath, originalPath); err != nil {
		t.Fatalf("hijack temp path: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Remove(originalPath)
		_ = os.Remove(heldPath)
	})

	cachePath := filepath.Join(cacheDir, digest+".bin")
	_, err = publishMaterializedImage(materialized, cachePath, digest, 1024)
	if !errors.Is(err, errUnsafeCacheFile) {
		t.Fatalf("publishMaterializedImage error = %v, want errUnsafeCacheFile", err)
	}
	if _, err := os.Lstat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("hijacked materialization was published: %v", err)
	}
	got, err := os.ReadFile(heldPath)
	if err != nil {
		t.Fatalf("read held verified inode: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("held verified inode changed: got %q want %q", got, payload)
	}
}

func TestDefaultImageResolverRetainsOnlyRequestedDigest(t *testing.T) {
	firstPayload := []byte("first verified image")
	secondPayload := []byte("second verified image")
	firstDigest := sha256Hex(firstPayload)
	secondDigest := sha256Hex(secondPayload)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	secondSawFirstCache := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/first.bin":
			_, _ = w.Write(firstPayload)
		case "/second.bin":
			_, statErr := os.Stat(filepath.Join(cacheDir, firstDigest+".bin"))
			secondSawFirstCache = statErr == nil
			_, _ = w.Write(secondPayload)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)

	resolver := NewDefaultImageResolver(nil, server.Client())
	resolver.CacheDir = cacheDir
	first, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL: server.URL + "/first.bin", SHA256: firstDigest,
	})
	if err != nil {
		t.Fatalf("resolve first image: %v", err)
	}
	if err := first.Cleanup(); err != nil {
		t.Fatalf("close first image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "operator-note"), []byte("not a managed cache entry"), 0o600); err != nil {
		t.Fatalf("write unmanaged sentinel: %v", err)
	}
	orphanTemp := filepath.Join(cacheDir, cacheTempPrefix+"orphan")
	if err := os.WriteFile(orphanTemp, []byte("incomplete image"), 0o600); err != nil {
		t.Fatalf("write orphan cache temp: %v", err)
	}

	second, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
		URL: server.URL + "/second.bin", SHA256: secondDigest,
	})
	if err != nil {
		t.Fatalf("resolve second image: %v", err)
	}
	t.Cleanup(func() { _ = second.Cleanup() })
	if secondSawFirstCache {
		t.Fatal("old digest remained on disk while the replacement was downloaded")
	}
	if _, err := os.Stat(filepath.Join(cacheDir, firstDigest+".bin")); !os.IsNotExist(err) {
		t.Fatalf("old digest was not evicted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, secondDigest+".bin")); err != nil {
		t.Fatalf("requested digest is not cached: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "operator-note")); err != nil {
		t.Fatalf("unmanaged cache-directory entry was removed: %v", err)
	}
	if _, err := os.Stat(orphanTemp); !os.IsNotExist(err) {
		t.Fatalf("orphan cache temp was not removed: %v", err)
	}
}

func TestDefaultImageResolverRejectsUnsafeCacheEntries(t *testing.T) {
	payload := []byte("verified image")
	digest := sha256Hex(payload)

	tests := map[string]func(*testing.T, string){
		"symlink": func(t *testing.T, cachePath string) {
			t.Helper()
			target := filepath.Join(t.TempDir(), "target.bin")
			if err := os.WriteFile(target, payload, 0o600); err != nil {
				t.Fatalf("write symlink target: %v", err)
			}
			if err := os.Symlink(target, cachePath); err != nil {
				t.Fatalf("create cache symlink: %v", err)
			}
		},
		"directory": func(t *testing.T, cachePath string) {
			t.Helper()
			if err := os.Mkdir(cachePath, 0o700); err != nil {
				t.Fatalf("create cache directory entry: %v", err)
			}
		},
		"permissive regular file": func(t *testing.T, cachePath string) {
			t.Helper()
			if err := os.WriteFile(cachePath, payload, 0o600); err != nil {
				t.Fatalf("create cache file: %v", err)
			}
			if err := os.Chmod(cachePath, 0o644); err != nil {
				t.Fatalf("make cache file permissive: %v", err)
			}
		},
	}

	for name, createEntry := range tests {
		t.Run(name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests++
				_, _ = w.Write(payload)
			}))
			t.Cleanup(server.Close)

			cacheDir := filepath.Join(t.TempDir(), "cache")
			if err := os.Mkdir(cacheDir, 0o700); err != nil {
				t.Fatalf("create cache: %v", err)
			}
			createEntry(t, filepath.Join(cacheDir, digest+".bin"))

			resolver := NewDefaultImageResolver(nil, server.Client())
			resolver.CacheDir = cacheDir
			_, err := resolver.Resolve(context.Background(), "default", opsv1alpha1.UpgradeImageSource{
				URL:    server.URL + "/cat9k.bin",
				SHA256: digest,
			})
			if !errors.Is(err, errUnsafeCacheFile) {
				t.Fatalf("Resolve error = %v, want errUnsafeCacheFile", err)
			}
			if requests != 0 {
				t.Fatalf("unsafe cache entry caused %d remote requests, want 0", requests)
			}
		})
	}
}

func TestDefaultImageResolverMaxImageBytesEnvOverride(t *testing.T) {
	t.Setenv(envMaxImageBytes, "23")
	resolver := NewDefaultImageResolver(nil, nil)
	resolver.MaxImageBytes = 99
	got, err := resolver.imageSizeLimit()
	if err != nil {
		t.Fatalf("imageSizeLimit: %v", err)
	}
	if got != 23 {
		t.Fatalf("imageSizeLimit = %d, want environment override 23", got)
	}

	t.Setenv(envMaxImageBytes, "unbounded")
	if _, err := resolver.imageSizeLimit(); err == nil {
		t.Fatal("imageSizeLimit accepted invalid environment override")
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func imageSourceSecret(name string, stringData map[string]string) *corev1.Secret {
	data := make(map[string][]byte, len(stringData))
	for key, value := range stringData {
		if value != "" {
			data[key] = []byte(value)
		}
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      name,
			Labels:    map[string]string{URLSecretPurposeLabel: URLSecretPurposeValue},
		},
		Data: data,
	}
}

func validEndpointBinding(scheme, host, port string) map[string]string {
	return map[string]string{
		URLSecretAllowedSchemeKey: scheme,
		URLSecretAllowedHostKey:   host,
		URLSecretAllowedPortKey:   port,
	}
}

func resolverWithURLSecret(t *testing.T, secret *corev1.Secret) *DefaultImageResolver {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	return NewDefaultImageResolver(fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build(), nil)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type interruptingReadCloser struct {
	data []byte
	err  error
}

func (reader *interruptingReadCloser) Read(p []byte) (int, error) {
	if len(reader.data) > 0 {
		n := copy(p, reader.data)
		reader.data = reader.data[n:]
		return n, nil
	}
	return 0, reader.err
}

func (*interruptingReadCloser) Close() error { return nil }
