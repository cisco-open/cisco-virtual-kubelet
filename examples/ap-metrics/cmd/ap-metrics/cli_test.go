package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

type cliExchange struct{ command, response string }

// The mock speaks SSH/PTY so collection is checked without AP hardware.
func mockAP(t *testing.T, prompt string, exchanges []cliExchange) (config, <-chan error) {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(private)
	if err != nil {
		t.Fatal(err)
	}
	serverConfig := &ssh.ServerConfig{PasswordCallback: func(_ ssh.ConnMetadata, _ []byte) (*ssh.Permissions, error) { return nil, nil }}
	serverConfig.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		done <- func() error {
			conn, err := listener.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			server, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
			if err != nil {
				return err
			}
			defer server.Close()
			go ssh.DiscardRequests(requests)
			newChannel, ok := <-channels
			if !ok {
				return fmt.Errorf("no session")
			}
			channel, sessionRequests, err := newChannel.Accept()
			if err != nil {
				return err
			}
			defer channel.Close()
			for request := range sessionRequests {
				if request.Type != "pty-req" && request.Type != "shell" {
					return fmt.Errorf("unexpected request %q", request.Type)
				}
				if err := request.Reply(true, nil); err != nil {
					return err
				}
				if request.Type == "shell" {
					break
				}
			}
			if _, err := io.WriteString(channel, prompt); err != nil {
				return err
			}
			reader := bufio.NewReader(channel)
			for _, exchange := range exchanges {
				command, err := reader.ReadString('\n')
				if err != nil {
					return err
				}
				if strings.TrimSuffix(command, "\n") != exchange.command {
					return fmt.Errorf("unexpected CLI command, expected %q", exchange.command)
				}
				if _, err := io.WriteString(channel, exchange.response); err != nil {
					return err
				}
			}
			// Wait for client close; any further command is unexpected.
			if extra, _ := reader.ReadString('\n'); extra != "" {
				return fmt.Errorf("unexpected command after script")
			}
			return nil
		}()
	}()
	return config{APAddress: listener.Addr().String(), APUsername: "user", APPassword: "secret", APEnableSecret: "enable", APName: "test-ap", APHostKeySHA256: ssh.FingerprintSHA256(signer.PublicKey())}, done
}

func awaitMockAP(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mock AP did not finish")
	}
}

func TestCollectSSHWithoutCommandEcho(t *testing.T) {
	for _, privileged := range []bool{false, true} {
		t.Run(fmt.Sprintf("privileged=%t", privileged), func(t *testing.T) {
			prompt := "AP#"
			var exchanges []cliExchange
			if !privileged {
				prompt = "AP>"
				exchanges = append(exchanges, cliExchange{"enable", "Password:"}, cliExchange{"enable", "\r\nAP#"})
			}
			exchanges = append(exchanges,
				cliExchange{"terminal length 0", "\r\nAP#"},
				cliExchange{"show processes cpu 0", "CPU: 84% idle\r\nAP#"},
				cliExchange{"show memory", "MemTotal: 100 kB\r\nMemAvailable: 20 kB\r\nAP#"},
				cliExchange{"show dot11 clients", "Total dot11 clients: 0\r\nAP#"},
				cliExchange{"show controllers dot11Radio 0 frequency", "RX packets: 123\r\nServing Frequency: 5180Mhz Channel: 36 80MHz\r\nAP#"},
				cliExchange{"show controllers dot11Radio 1 frequency", "% Invalid interface\r\nAP#"},
				cliExchange{"show controllers dot11Radio 2 frequency", "% Invalid interface\r\nAP#"},
			)
			cfg, done := mockAP(t, prompt, exchanges)
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			metrics, err := collect(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, metric := range metrics {
				if metric.name == "cisco_ap_radio_rx_packets_total" && metric.labels["slot"] == "0" && metric.labels["band"] == "5ghz" && metric.value == 123 {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing radio metric: %v", metrics)
			}
			awaitMockAP(t, done)
		})
	}
}

func TestCollectRejectsFailedEnable(t *testing.T) {
	cfg, done := mockAP(t, "AP>", []cliExchange{{"enable", "Password:"}, {"enable", "% Access denied\r\nAP>"}})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := collect(ctx, cfg); err == nil || !strings.Contains(err.Error(), "rejected enable mode") {
		t.Fatalf("collect error = %v", err)
	}
	awaitMockAP(t, done)
}

func TestCollectCancelsBlockedSSHHandshake(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	cfg := config{APAddress: listener.Addr().String(), APUsername: "user", APPassword: "secret", APEnableSecret: "enable", APName: "test", APHostKeySHA256: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}
	// No deadline: cancellation must interrupt SSH's blocking handshake too.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := collect(ctx, cfg); done <- err }()
	select {
	case conn := <-accepted:
		defer conn.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not connect")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled collection succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt SSH handshake")
	}
}
