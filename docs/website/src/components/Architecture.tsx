// Copyright 2026 Cisco Systems Inc.
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

import { ArrowDown, ArrowRight, Boxes, Cloud, Container, Network, Server } from "lucide-react";

const devicePaths = [
  { name: "Catalyst 8000V", platform: "IOS-XE", transport: "RESTCONF", worker: "01", beta: false },
  { name: "Catalyst 9000", platform: "IOS-XE", transport: "RESTCONF", worker: "02", beta: false },
  { name: "Nexus", platform: "NX-OS", transport: "NX-API CLI", worker: "03", beta: true },
];

const guideLinkClass =
  "inline-flex items-center gap-2 rounded text-sm font-medium text-primary hover:text-primary-light focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-primary";

export default function Architecture() {
  return (
    <section id="architecture" className="relative scroll-mt-20 py-12 md:py-20">
      <div className="absolute inset-0 bg-gradient-to-b from-background via-surface/40 to-background" aria-hidden="true" />
      <div className="relative max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="max-w-2xl mx-auto text-center mb-7 md:mb-10">
          <p className="text-sm font-medium tracking-wide text-primary mb-3">From Kubernetes to a supported device</p>
          <h2 className="text-3xl sm:text-4xl md:text-5xl font-bold tracking-tight mb-4">
            How it <span className="text-primary">works</span>
          </h2>
          <p className="text-base sm:text-lg text-text-muted leading-relaxed">
            Declare your devices and applications in Kubernetes. Cisco Virtual
            Kubelet connects each virtual node to the device that runs its apps.
          </p>
        </div>

        <figure className="relative rounded-3xl border border-border bg-gradient-to-br from-surface-light/60 via-surface/80 to-background p-4 sm:p-6 lg:p-8">
          <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-widest text-text-muted mb-5 md:mb-7">
            <Container className="w-4 h-4 text-primary" aria-hidden="true" />
            App hosting
          </div>

          <div aria-hidden="true">
            <div className="max-w-lg mx-auto flex items-center justify-center gap-3 md:gap-4 rounded-2xl border border-primary/40 bg-primary/5 px-4 py-3 md:px-5 md:py-5 shadow-[0_0_40px_rgba(0,188,235,0.05)]">
              <div className="shrink-0 rounded-xl border border-primary/25 bg-primary/10 p-2 md:p-3">
                <Cloud className="w-5 h-5 md:w-7 md:h-7 text-primary" />
              </div>
              <div>
                <p className="font-semibold text-base sm:text-lg text-foreground">Kubernetes API</p>
                <p className="text-xs sm:text-sm text-text-muted mt-1">Devices and desired workloads</p>
              </div>
            </div>

            <div className="flex flex-col items-center py-1 md:py-2">
              <div className="w-px h-3 md:h-5 bg-gradient-to-b from-primary/60 to-accent-light/60" />
              <ArrowDown className="w-4 h-4 text-accent-light -mt-1" />
            </div>

            <div className="max-w-lg mx-auto flex items-center justify-center gap-3 md:gap-4 rounded-2xl border border-accent-light/40 bg-accent/5 px-4 py-3 md:px-5 md:py-5">
              <div className="shrink-0 rounded-xl border border-accent-light/25 bg-accent/10 p-2 md:p-3">
                <Boxes className="w-5 h-5 md:w-7 md:h-7 text-accent-light" />
              </div>
              <div>
                <p className="font-semibold text-base sm:text-lg text-foreground">CVK manager</p>
                <p className="text-xs sm:text-sm text-text-muted mt-1">Creates a worker for each device</p>
              </div>
            </div>

            <div className="hidden md:block h-10">
              <svg viewBox="0 0 900 40" preserveAspectRatio="none" className="w-full h-full text-accent-light/50" fill="none">
                <path d="M450 0V16M145 40V16H755V40M450 16V40" stroke="currentColor" strokeWidth="1.5" vectorEffect="non-scaling-stroke" />
              </svg>
            </div>
            <div className="md:hidden max-w-lg mx-auto">
              <div className="flex flex-col items-center py-1">
                <div className="w-px h-3 bg-accent-light/40" />
                <ArrowDown className="w-4 h-4 text-accent-light -mt-1" />
              </div>
              <div className="flex items-center justify-center gap-3 rounded-2xl border border-accent-light/30 bg-accent/5 px-4 py-3">
                <Server className="w-5 h-5 shrink-0 text-accent-light" />
                <div>
                  <p className="text-sm font-semibold text-foreground">Per-device CVK worker</p>
                  <p className="text-xs text-text-muted mt-1">Watches Pods on its virtual node</p>
                </div>
              </div>
              <div className="flex flex-col items-center py-1">
                <div className="w-px h-3 bg-success/40" />
                <ArrowDown className="w-4 h-4 text-success -mt-1" />
              </div>
              <div className="rounded-2xl border border-success/30 bg-success/5 px-4 py-4 text-center">
                <div className="flex items-center justify-center gap-2 mb-2">
                  <Network className="w-5 h-5 text-success" />
                  <p className="text-sm font-semibold text-foreground">Cisco device</p>
                </div>
                <div className="flex flex-wrap justify-center items-center gap-x-3 gap-y-1 text-xs text-text-muted">
                  <span>IOS-XE · RESTCONF</span>
                  <span>NX-OS · NX-API CLI <span className="text-amber-300">(Beta)</span></span>
                </div>
                <div className="inline-flex items-center gap-2 rounded-lg border border-success/20 bg-success/10 px-3 py-1.5 mt-3 text-xs text-success">
                  <Container className="w-3.5 h-3.5" />
                  Hosted application
                </div>
              </div>
            </div>

            <div className="hidden md:grid md:grid-cols-3 gap-5">
              {devicePaths.map((device) => (
                <div key={device.name} className="min-w-0 rounded-2xl border border-border bg-background/35 p-4 lg:p-5">
                  <div className="flex items-center gap-3 rounded-xl border border-accent-light/30 bg-accent/5 px-4 py-4">
                    <Server className="w-5 h-5 shrink-0 text-accent-light" />
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-semibold text-foreground">CVK worker</p>
                      <p className="text-xs text-text-muted mt-1">One virtual node</p>
                    </div>
                    <span className="text-xs font-mono text-accent-light">{device.worker}</span>
                  </div>

                  <div className="flex flex-col items-center py-1">
                    <div className="w-px h-4 bg-accent-light/40" />
                    <span className="rounded-full border border-border bg-surface px-3 py-1 text-[11px] font-medium tracking-wide text-text-muted">
                      {device.transport}
                    </span>
                    <div className="w-px h-4 bg-success/40" />
                    <ArrowDown className="w-4 h-4 text-success -mt-1" />
                  </div>

                  <div className="rounded-xl border border-success/30 bg-success/5 px-4 py-5 text-center">
                    <Network className="w-6 h-6 text-success mx-auto mb-3" />
                    <p className="text-sm font-semibold text-foreground">{device.name}</p>
                    <div className="flex flex-wrap justify-center items-center gap-2 mt-1.5 text-xs text-text-muted">
                      <span>{device.platform}</span>
                      {device.beta && (
                        <span className="rounded border border-amber-400/25 bg-amber-400/10 px-1.5 py-0.5 text-[10px] font-semibold text-amber-300">Beta</span>
                      )}
                    </div>
                    <div className="inline-flex items-center gap-2 rounded-lg border border-success/20 bg-success/10 px-3 py-1.5 mt-4 text-xs text-success">
                      <Container className="w-3.5 h-3.5" />
                      Hosted application
                    </div>
                  </div>
                </div>
              ))}
            </div>
          </div>

          <figcaption className="max-w-3xl mx-auto mt-5 md:mt-7 text-center text-sm text-text-muted leading-relaxed">
            Kubernetes holds the desired state. The manager creates one worker
            per device; each worker watches assigned Pods and manages app hosting.
            <span className="sr-only">
              {" "}IOS-XE devices use RESTCONF. Nexus NX-OS uses NX-API CLI, and
              its app-hosting support is Beta.
            </span>
          </figcaption>
        </figure>

        <div className="flex flex-col sm:flex-row items-center justify-center gap-3 sm:gap-8 mt-5 md:mt-7">
          <a href="/cisco-virtual-kubelet/docs/concepts/" className={guideLinkClass}>
            Understand the components
            <ArrowRight className="w-4 h-4" aria-hidden="true" />
          </a>
          <a href="/cisco-virtual-kubelet/docs/topology-scheduling/" className={guideLinkClass}>
            Workload placement
            <ArrowRight className="w-4 h-4" aria-hidden="true" />
          </a>
        </div>
      </div>
    </section>
  );
}
