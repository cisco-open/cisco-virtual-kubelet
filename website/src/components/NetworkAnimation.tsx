// Copyright 2026 Cisco Systems, Inc.
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

"use client";

import { useEffect, useRef } from "react";

interface RouterNode {
  x: number;
  y: number;
  vx: number;
  vy: number;
  size: number;
  connections: number[];
  pulsePhase: number;
  type: "router" | "switch" | "endpoint";
}

interface DataPacket {
  fromNode: number;
  toNode: number;
  progress: number;
  speed: number;
  color: string;
}

export default function NetworkAnimation() {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const animationRef = useRef<number>(0);
  const nodesRef = useRef<RouterNode[]>([]);
  const packetsRef = useRef<DataPacket[]>([]);
  const mouseRef = useRef({ x: -1000, y: -1000 });

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;

    const ctx = canvas.getContext("2d");
    if (!ctx) return;

    let w = 0;
    let h = 0;

    const resize = () => {
      const dpr = window.devicePixelRatio || 1;
      w = canvas.offsetWidth;
      h = canvas.offsetHeight;
      canvas.width = w * dpr;
      canvas.height = h * dpr;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    };

    const handleMouseMove = (e: MouseEvent) => {
      const rect = canvas.getBoundingClientRect();
      mouseRef.current = { x: e.clientX - rect.left, y: e.clientY - rect.top };
    };

    const handleMouseLeave = () => {
      mouseRef.current = { x: -1000, y: -1000 };
    };

    resize();
    window.addEventListener("resize", resize);
    canvas.addEventListener("mousemove", handleMouseMove);
    canvas.addEventListener("mouseleave", handleMouseLeave);

    // Initialize router nodes - kept moderate for full mesh performance
    const nodeCount = Math.min(
      30,
      Math.floor((w * h) / 28000)
    );
    const types: Array<"router" | "switch" | "endpoint"> = [
      "router",
      "router",
      "switch",
      "switch",
      "endpoint",
    ];

    nodesRef.current = Array.from({ length: nodeCount }, (_, i) => ({
      x: Math.random() * w,
      y: Math.random() * h,
      vx: (Math.random() - 0.5) * 0.25,
      vy: (Math.random() - 0.5) * 0.25,
      size: types[i % types.length] === "router" ? 8 : types[i % types.length] === "switch" ? 6 : 4,
      connections: [],
      pulsePhase: Math.random() * Math.PI * 2,
      type: types[i % types.length],
    }));

    // Draw router icon
    const drawRouter = (
      x: number,
      y: number,
      size: number,
      alpha: number,
      highlighted: boolean
    ) => {
      const s = size;
      // Router body - rounded rectangle
      ctx.beginPath();
      const r = s * 0.3;
      ctx.roundRect(x - s, y - s * 0.6, s * 2, s * 1.2, r);
      ctx.fillStyle = highlighted
        ? `rgba(0, 188, 235, ${0.15 * alpha})`
        : `rgba(0, 188, 235, ${0.08 * alpha})`;
      ctx.fill();
      ctx.strokeStyle = `rgba(0, 188, 235, ${0.6 * alpha})`;
      ctx.lineWidth = 1;
      ctx.stroke();

      // Antenna lines (top)
      ctx.beginPath();
      ctx.moveTo(x - s * 0.4, y - s * 0.6);
      ctx.lineTo(x - s * 0.4, y - s * 1.1);
      ctx.moveTo(x + s * 0.4, y - s * 0.6);
      ctx.lineTo(x + s * 0.4, y - s * 1.1);
      ctx.strokeStyle = `rgba(0, 188, 235, ${0.4 * alpha})`;
      ctx.lineWidth = 1;
      ctx.stroke();

      // Signal arcs
      ctx.beginPath();
      ctx.arc(x - s * 0.4, y - s * 1.1, s * 0.2, Math.PI, Math.PI * 2);
      ctx.strokeStyle = `rgba(0, 188, 235, ${0.25 * alpha})`;
      ctx.lineWidth = 0.8;
      ctx.stroke();

      ctx.beginPath();
      ctx.arc(x + s * 0.4, y - s * 1.1, s * 0.2, Math.PI, Math.PI * 2);
      ctx.stroke();

      // Status LED
      ctx.beginPath();
      ctx.arc(x, y, s * 0.15, 0, Math.PI * 2);
      ctx.fillStyle = `rgba(52, 211, 153, ${0.8 * alpha})`;
      ctx.fill();
    };

    // Draw switch icon
    const drawSwitch = (
      x: number,
      y: number,
      size: number,
      alpha: number,
      highlighted: boolean
    ) => {
      const s = size;
      // Switch body
      ctx.beginPath();
      ctx.roundRect(x - s, y - s * 0.5, s * 2, s, s * 0.2);
      ctx.fillStyle = highlighted
        ? `rgba(107, 92, 231, ${0.15 * alpha})`
        : `rgba(107, 92, 231, ${0.08 * alpha})`;
      ctx.fill();
      ctx.strokeStyle = `rgba(107, 92, 231, ${0.5 * alpha})`;
      ctx.lineWidth = 1;
      ctx.stroke();

      // Port indicators
      for (let i = -2; i <= 2; i++) {
        ctx.beginPath();
        ctx.rect(x + i * s * 0.35 - s * 0.08, y - s * 0.15, s * 0.16, s * 0.3);
        ctx.fillStyle = `rgba(107, 92, 231, ${0.3 * alpha})`;
        ctx.fill();
      }
    };

    // Draw endpoint icon
    const drawEndpoint = (
      x: number,
      y: number,
      size: number,
      alpha: number,
      highlighted: boolean
    ) => {
      const s = size;
      // Simple circle endpoint
      ctx.beginPath();
      ctx.arc(x, y, s, 0, Math.PI * 2);
      ctx.fillStyle = highlighted
        ? `rgba(52, 211, 153, ${0.15 * alpha})`
        : `rgba(52, 211, 153, ${0.06 * alpha})`;
      ctx.fill();
      ctx.strokeStyle = `rgba(52, 211, 153, ${0.5 * alpha})`;
      ctx.lineWidth = 1;
      ctx.stroke();

      // Inner dot
      ctx.beginPath();
      ctx.arc(x, y, s * 0.35, 0, Math.PI * 2);
      ctx.fillStyle = `rgba(52, 211, 153, ${0.6 * alpha})`;
      ctx.fill();
    };

    const animate = () => {
      ctx.clearRect(0, 0, w, h);

      const nodes = nodesRef.current;
      const packets = packetsRef.current;
      const mouse = mouseRef.current;
      const time = Date.now() * 0.001;
      // Max possible distance across the canvas diagonal
      const maxDist = Math.sqrt(w * w + h * h);

      // Update node positions
      for (const node of nodes) {
        // Gentle mouse interaction - attract slightly on hover
        const dx = mouse.x - node.x;
        const dy = mouse.y - node.y;
        const distMouse = Math.sqrt(dx * dx + dy * dy);
        if (distMouse < 200 && distMouse > 30) {
          const force = (200 - distMouse) / 200;
          node.vx += (dx / distMouse) * force * 0.008;
          node.vy += (dy / distMouse) * force * 0.008;
        } else if (distMouse <= 30) {
          // Repel if too close
          node.vx -= (dx / distMouse) * 0.05;
          node.vy -= (dy / distMouse) * 0.05;
        }

        node.x += node.vx;
        node.y += node.vy;
        node.vx *= 0.995;
        node.vy *= 0.995;

        // Keep in bounds with soft bounce
        const margin = 20;
        if (node.x < margin) { node.vx += 0.02; node.x = margin; }
        if (node.x > w - margin) { node.vx -= 0.02; node.x = w - margin; }
        if (node.y < margin) { node.vy += 0.02; node.y = margin; }
        if (node.y > h - margin) { node.vy -= 0.02; node.y = h - margin; }
      }

      // FULL MESH: Every node connects to every other node
      for (let i = 0; i < nodes.length; i++) {
        nodes[i].connections = [];
      }

      for (let i = 0; i < nodes.length; i++) {
        for (let j = i + 1; j < nodes.length; j++) {
          const dx = nodes[j].x - nodes[i].x;
          const dy = nodes[j].y - nodes[i].y;
          const dist = Math.sqrt(dx * dx + dy * dy);

          // All nodes are connected in a full mesh
          nodes[i].connections.push(j);
          nodes[j].connections.push(i);

          // Alpha based on distance - close links are brighter, far links are dimmer but still visible
          const nearThreshold = 180;
          let alpha: number;
          if (dist < nearThreshold) {
            // Close connections: strong
            alpha = 0.6 + 0.4 * (1 - dist / nearThreshold);
          } else {
            // Far connections: dimmer but always visible
            const farFactor = (dist - nearThreshold) / (maxDist - nearThreshold);
            alpha = Math.max(0.08, 0.6 * (1 - farFactor));
          }

          // Main connection line
          ctx.beginPath();
          ctx.moveTo(nodes[i].x, nodes[i].y);
          ctx.lineTo(nodes[j].x, nodes[j].y);

          // Color based on connection types
          let lineColor: string;
          if (nodes[i].type === "router" && nodes[j].type === "router") {
            lineColor = `rgba(0, 188, 235, ${0.5 * alpha})`;
          } else if (nodes[i].type === "router" || nodes[j].type === "router") {
            lineColor = `rgba(73, 215, 245, ${0.4 * alpha})`;
          } else {
            lineColor = `rgba(107, 92, 231, ${0.35 * alpha})`;
          }
          ctx.strokeStyle = lineColor;
          ctx.lineWidth = alpha > 0.5 ? 1.5 : alpha > 0.3 ? 1 : 0.5;
          ctx.stroke();

          // Draw animated dashes on closer links
          if (dist < nearThreshold * 1.5) {
            const dashLen = 4;
            const gapLen = 8;
            const offset = (time * 30) % (dashLen + gapLen);

            ctx.strokeStyle = `rgba(0, 188, 235, ${0.15 * alpha})`;
            ctx.lineWidth = 3;
            ctx.setLineDash([dashLen, gapLen]);
            ctx.lineDashOffset = -offset;
            ctx.beginPath();
            ctx.moveTo(nodes[i].x, nodes[i].y);
            ctx.lineTo(nodes[j].x, nodes[j].y);
            ctx.stroke();
            ctx.setLineDash([]);
          }
        }
      }

      // Spawn data packets
      if (Math.random() < 0.06 && packets.length < 20) {
        const fromIdx = Math.floor(Math.random() * nodes.length);
        const from = nodes[fromIdx];
        if (from.connections.length > 0) {
          const toIdx = from.connections[Math.floor(Math.random() * from.connections.length)];
          const colors = [
            "rgba(0, 188, 235, 0.9)",
            "rgba(52, 211, 153, 0.9)",
            "rgba(107, 92, 231, 0.9)",
          ];
          packets.push({
            fromNode: fromIdx,
            toNode: toIdx,
            progress: 0,
            speed: 0.008 + Math.random() * 0.012,
            color: colors[Math.floor(Math.random() * colors.length)],
          });
        }
      }

      // Update and draw data packets
      for (let i = packets.length - 1; i >= 0; i--) {
        const pkt = packets[i];
        pkt.progress += pkt.speed;

        if (pkt.progress >= 1) {
          packets.splice(i, 1);
          continue;
        }

        const from = nodes[pkt.fromNode];
        const to = nodes[pkt.toNode];
        const px = from.x + (to.x - from.x) * pkt.progress;
        const py = from.y + (to.y - from.y) * pkt.progress;

        // Packet glow
        ctx.beginPath();
        ctx.arc(px, py, 6, 0, Math.PI * 2);
        ctx.fillStyle = pkt.color.replace("0.9", "0.1");
        ctx.fill();

        // Packet core
        ctx.beginPath();
        ctx.arc(px, py, 2.5, 0, Math.PI * 2);
        ctx.fillStyle = pkt.color;
        ctx.fill();

        // Packet trail
        const trailLen = 0.06;
        const trailStart = Math.max(0, pkt.progress - trailLen);
        const tx = from.x + (to.x - from.x) * trailStart;
        const ty = from.y + (to.y - from.y) * trailStart;
        const gradient = ctx.createLinearGradient(tx, ty, px, py);
        gradient.addColorStop(0, "rgba(0, 188, 235, 0)");
        gradient.addColorStop(1, pkt.color.replace("0.9", "0.4"));
        ctx.beginPath();
        ctx.moveTo(tx, ty);
        ctx.lineTo(px, py);
        ctx.strokeStyle = gradient;
        ctx.lineWidth = 2;
        ctx.stroke();
      }

      // Draw nodes on top
      for (let i = 0; i < nodes.length; i++) {
        const node = nodes[i];
        const pulse = Math.sin(time * 1.5 + node.pulsePhase) * 0.15 + 0.85;

        // Check if mouse is near
        const dx = mouse.x - node.x;
        const dy = mouse.y - node.y;
        const distMouse = Math.sqrt(dx * dx + dy * dy);
        const highlighted = distMouse < 100;

        // Node halo when highlighted
        if (highlighted) {
          ctx.beginPath();
          ctx.arc(node.x, node.y, node.size * 2.5, 0, Math.PI * 2);
          ctx.fillStyle = node.type === "router"
            ? "rgba(0, 188, 235, 0.04)"
            : node.type === "switch"
            ? "rgba(107, 92, 231, 0.04)"
            : "rgba(52, 211, 153, 0.04)";
          ctx.fill();
        }

        // Draw appropriate icon
        if (node.type === "router") {
          drawRouter(node.x, node.y, node.size, pulse, highlighted);
        } else if (node.type === "switch") {
          drawSwitch(node.x, node.y, node.size, pulse, highlighted);
        } else {
          drawEndpoint(node.x, node.y, node.size, pulse, highlighted);
        }
      }

      // Draw mouse proximity effect - highlight connections near mouse
      if (mouse.x > 0 && mouse.y > 0) {
        for (const node of nodes) {
          const dx = mouse.x - node.x;
          const dy = mouse.y - node.y;
          const dist = Math.sqrt(dx * dx + dy * dy);
          if (dist < 120) {
            for (const connIdx of node.connections) {
              const other = nodes[connIdx];
              const odx = mouse.x - other.x;
              const ody = mouse.y - other.y;
              const oDist = Math.sqrt(odx * odx + ody * ody);
              if (oDist < 150) {
                ctx.beginPath();
                ctx.moveTo(node.x, node.y);
                ctx.lineTo(other.x, other.y);
                ctx.strokeStyle = "rgba(0, 188, 235, 0.35)";
                ctx.lineWidth = 2.5;
                ctx.stroke();
              }
            }
          }
        }
      }

      animationRef.current = requestAnimationFrame(animate);
    };

    animate();

    return () => {
      cancelAnimationFrame(animationRef.current);
      window.removeEventListener("resize", resize);
      canvas.removeEventListener("mousemove", handleMouseMove);
      canvas.removeEventListener("mouseleave", handleMouseLeave);
    };
  }, []);

  return (
    <canvas
      ref={canvasRef}
      className="absolute inset-0 w-full h-full"
      style={{ opacity: 0.85 }}
    />
  );
}
