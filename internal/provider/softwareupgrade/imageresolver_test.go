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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pin/tftp/v3"

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
	src := opsv1alpha1.UpgradeImageSource{
		URL:    "scp://user:pass@127.0.0.1/tmp/cat9k.bin",
		SHA256: strings.Repeat("a", 64),
	}
	_, err := NewDefaultImageResolver(nil, nil).Resolve(context.Background(), "default", src)
	if err == nil {
		t.Fatal("Resolve admitted SCP without host-key policy")
	}
	if !strings.Contains(err.Error(), "knownHosts") {
		t.Fatalf("expected host-key policy error, got %v", err)
	}
	if strings.Contains(err.Error(), "pass") {
		t.Fatalf("error leaked URL password: %v", err)
	}
}

func TestDefaultImageResolverSCPInsecureRequiresOperatorEnv(t *testing.T) {
	t.Setenv(envAllowInsecureSSH, "")
	src := opsv1alpha1.UpgradeImageSource{
		URL:    "scp://user:pass@127.0.0.1/tmp/cat9k.bin?insecureSkipHostKey=true",
		SHA256: strings.Repeat("a", 64),
	}
	_, err := NewDefaultImageResolver(nil, nil).Resolve(context.Background(), "default", src)
	if err == nil {
		t.Fatal("Resolve admitted insecure SCP host-key bypass without operator env gate")
	}
	if !strings.Contains(err.Error(), envAllowInsecureSSH) {
		t.Fatalf("expected env-gate error, got %v", err)
	}
	if strings.Contains(err.Error(), "pass") {
		t.Fatalf("error leaked URL password: %v", err)
	}
}

func TestRedactURLPreservesUsernameAndDropsPassword(t *testing.T) {
	raw := "sftp://user:secret@example.com/images/cat9k.bin"
	got := redactRawURL(raw)
	if !strings.Contains(got, "user@example.com") {
		t.Fatalf("redacted URL %q did not preserve username and host", got)
	}
	if strings.Contains(got, "secret") {
		t.Fatalf("redacted URL leaked password: %q", got)
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
