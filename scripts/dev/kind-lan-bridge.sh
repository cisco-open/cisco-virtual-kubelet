#!/usr/bin/env bash
# Copyright © 2026 Cisco Systems Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# kind-lan-bridge.sh — bridge a LAN device IP into a kind cluster
# running on Docker Desktop for macOS (or Windows).
#
# Why this exists:
#
#   On Linux, kind containers join the host's network namespace
#   indirectly and outbound traffic is SNATed through the host's
#   primary interface, so pods can talk to LAN devices out of the
#   box. On macOS, Docker Desktop runs the daemon inside a Linux VM
#   that has no route to the Mac's physical LAN — pods see RST/timeout
#   on every LAN target. This is a Docker Desktop architecture
#   constraint, not a bug.
#
# What this script does:
#
#   1. Starts a Python TCP forwarder on the Mac, bound to
#      127.0.0.1:${HOST_PORT}, that forwards to ${DEVICE_IP}:${DEVICE_PORT}.
#      Python is preferred over socat because every macOS install has
#      a working python3; socat usually isn't installed.
#
#   2. Adds an iptables DNAT rule inside the kind node container that
#      rewrites traffic destined for ${DEVICE_IP}:${DEVICE_PORT}
#      to host.docker.internal:${HOST_PORT}. The rewrite happens in
#      the OUTPUT chain so it catches traffic from pods exiting the
#      node container, before the kernel decides "no route".
#
#   3. host.docker.internal is special-cased by Docker Desktop to
#      reach back to the Mac's localhost, where the forwarder is
#      waiting. So pods think they're reaching ${DEVICE_IP} on the
#      LAN, and the kernel transparently retargets the connection
#      back through the Mac.
#
# Usage:
#
#   scripts/dev/kind-lan-bridge.sh up   192.168.129.1 443
#   scripts/dev/kind-lan-bridge.sh down 192.168.129.1 443
#   scripts/dev/kind-lan-bridge.sh status
#
# Limitations:
#
#   - Single device IP at a time. Use ${HOST_PORT} ranges if you need
#     more than one — bump it for each new mapping.
#   - The kind node container must have iptables (it does — kind
#     ships with the nf_tables binary).
#   - Connections initiated FROM the device back into the cluster
#     are NOT bridged. This is fine for RESTCONF/NETCONF (cisco-vk
#     pulls), but won't work for unsolicited Subscribe streams
#     unless you add a second mapping in the reverse direction.

set -euo pipefail

HOST_PORT="${KIND_LAN_BRIDGE_HOST_PORT:-18443}"
KIND_NODE="${KIND_LAN_BRIDGE_NODE:-kind-control-plane}"
PIDFILE="${TMPDIR:-/tmp}/kind-lan-bridge.${HOST_PORT}.pid"

usage() {
  sed -n '/^# Usage:/,/^$/p' "$0" | sed 's/^# \{0,1\}//'
  exit 64
}

require() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "kind-lan-bridge: missing required tool: $1" >&2
    exit 1
  }
}

require docker
require python3

cmd="${1:-}"
case "$cmd" in
  up)
    device_ip="${2:?missing device IP, e.g. 192.168.129.1}"
    device_port="${3:?missing device port, e.g. 443}"

    if [[ -e "$PIDFILE" ]] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
      echo "kind-lan-bridge: forwarder already running (pid $(cat "$PIDFILE"))" >&2
      exit 1
    fi

    # 1. start host-side forwarder
    python3 - "$HOST_PORT" "$device_ip" "$device_port" >/tmp/kind-lan-bridge.log 2>&1 <<'PY' &
import socket, socketserver, sys, threading

host_port = int(sys.argv[1])
device_ip = sys.argv[2]
device_port = int(sys.argv[3])

class Forwarder(socketserver.BaseRequestHandler):
    def handle(self):
        upstream = socket.create_connection((device_ip, device_port), timeout=10)
        peer_pump = threading.Thread(
            target=lambda: pump(self.request, upstream, "down"), daemon=True
        )
        peer_pump.start()
        pump(upstream, self.request, "up")

def pump(src, dst, label):
    try:
        while True:
            buf = src.recv(65536)
            if not buf:
                break
            dst.sendall(buf)
    except OSError:
        pass
    finally:
        try: src.shutdown(socket.SHUT_RD)
        except OSError: pass
        try: dst.shutdown(socket.SHUT_WR)
        except OSError: pass

class ReusableTCPServer(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True

with ReusableTCPServer(("127.0.0.1", host_port), Forwarder) as s:
    print(f"kind-lan-bridge: 127.0.0.1:{host_port} -> {device_ip}:{device_port}", flush=True)
    s.serve_forever()
PY
    fwd_pid=$!
    echo "$fwd_pid" > "$PIDFILE"
    sleep 0.3
    if ! kill -0 "$fwd_pid" 2>/dev/null; then
      rm -f "$PIDFILE"
      echo "kind-lan-bridge: forwarder failed to start (see /tmp/kind-lan-bridge.log)" >&2
      exit 1
    fi
    echo "kind-lan-bridge: forwarder up on 127.0.0.1:${HOST_PORT} (pid ${fwd_pid})"

    # 2. iptables DNAT inside the kind node — rewrite outbound connections
    #    to ${device_ip}:${device_port} so they end up at host.docker.internal:${HOST_PORT}.
    docker exec "$KIND_NODE" sh -c "
      gw=\$(getent hosts host.docker.internal | awk '{print \$1}')
      if [ -z \"\$gw\" ]; then
        echo 'kind-lan-bridge: host.docker.internal does not resolve in the kind node' >&2
        exit 1
      fi
      iptables -t nat -C OUTPUT     -d ${device_ip} -p tcp --dport ${device_port} -j DNAT --to-destination \"\$gw:${HOST_PORT}\" 2>/dev/null \
        || iptables -t nat -A OUTPUT     -d ${device_ip} -p tcp --dport ${device_port} -j DNAT --to-destination \"\$gw:${HOST_PORT}\"
      iptables -t nat -C PREROUTING -d ${device_ip} -p tcp --dport ${device_port} -j DNAT --to-destination \"\$gw:${HOST_PORT}\" 2>/dev/null \
        || iptables -t nat -A PREROUTING -d ${device_ip} -p tcp --dport ${device_port} -j DNAT --to-destination \"\$gw:${HOST_PORT}\"
      echo \"kind-lan-bridge: DNAT installed (\$gw:${HOST_PORT})\"
    "
    ;;

  down)
    device_ip="${2:?missing device IP}"
    device_port="${3:?missing device port}"

    docker exec "$KIND_NODE" sh -c "
      gw=\$(getent hosts host.docker.internal | awk '{print \$1}')
      iptables -t nat -D OUTPUT     -d ${device_ip} -p tcp --dport ${device_port} -j DNAT --to-destination \"\$gw:${HOST_PORT}\" 2>/dev/null || true
      iptables -t nat -D PREROUTING -d ${device_ip} -p tcp --dport ${device_port} -j DNAT --to-destination \"\$gw:${HOST_PORT}\" 2>/dev/null || true
      echo 'kind-lan-bridge: DNAT removed'
    "
    if [[ -e "$PIDFILE" ]]; then
      pid=$(cat "$PIDFILE")
      kill "$pid" 2>/dev/null || true
      rm -f "$PIDFILE"
      echo "kind-lan-bridge: forwarder stopped (pid ${pid})"
    fi
    ;;

  status)
    if [[ -e "$PIDFILE" ]] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
      echo "host forwarder: running (pid $(cat "$PIDFILE"))"
    else
      echo "host forwarder: not running"
    fi
    docker exec "$KIND_NODE" iptables -t nat -L OUTPUT     -n 2>/dev/null | grep DNAT || echo "OUTPUT     DNAT: none"
    docker exec "$KIND_NODE" iptables -t nat -L PREROUTING -n 2>/dev/null | grep DNAT || echo "PREROUTING DNAT: none"
    ;;

  *)
    usage
    ;;
esac
