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

import { ArrowDown, ArrowRight, Github } from "lucide-react";
import NetworkAnimation from "./NetworkAnimation";

export default function Hero() {
  return (
    <section className="relative flex items-center justify-center overflow-hidden">
      <div className="absolute inset-0 opacity-65" aria-hidden="true"><NetworkAnimation /></div>
      <div className="absolute inset-0 bg-gradient-to-b from-background/25 via-background/65 to-background" aria-hidden="true" />
      <div className="relative z-10 w-full min-w-0 max-w-6xl mx-auto px-5 sm:px-8 pt-36 pb-16 sm:pt-44 sm:pb-20 text-center">
        <p className="inline-flex items-center gap-2.5 px-4 py-2 rounded-full border border-primary/20 bg-primary/5 text-xs sm:text-sm text-primary mb-7"><span className="w-1.5 h-1.5 rounded-full bg-primary" aria-hidden="true" />Open source · Built on Virtual Kubelet</p>
        <h1 className="text-4xl sm:text-6xl lg:text-7xl font-bold tracking-tight mb-6 leading-[1.12]">Bring Kubernetes<br /><span className="gradient-text">to Cisco Network Devices</span></h1>
        <p className="text-base sm:text-xl text-text-muted max-w-2xl mx-auto mb-8 leading-relaxed">A <strong className="font-semibold text-foreground">Virtual Kubelet provider</strong> for app-hosted workloads, declarative configuration and opt-in topology-aware IOS-XE application deployment. Use native Kubernetes placement, scheduling, observability and security constructs.</p>
        <div className="flex flex-col sm:flex-row items-center justify-center gap-3">
          <a href="/cisco-virtual-kubelet/docs/getting-started/" className="inline-flex items-center justify-center gap-2 px-6 py-3.5 bg-primary text-background font-semibold rounded-xl hover:bg-primary-light transition-colors">Get started<ArrowRight className="w-4 h-4" aria-hidden="true" /></a>
          <a href="#architecture" className="inline-flex items-center justify-center gap-2 px-6 py-3.5 bg-surface/80 border border-border text-foreground font-medium rounded-xl hover:bg-surface-light hover:border-primary/50 transition-colors">See how it works<ArrowDown className="w-4 h-4" aria-hidden="true" /></a>
        </div>
        <div className="mt-9 flex flex-wrap items-center justify-center gap-x-6 gap-y-3 text-xs text-text-muted">
          <span>IOS-XE app hosting</span><span className="hidden sm:block text-border" aria-hidden="true">/</span><span>NX-OS · Beta</span><span className="hidden sm:block text-border" aria-hidden="true">/</span>
          <a href="https://github.com/cisco-open/cisco-virtual-kubelet" className="inline-flex items-center gap-1.5 hover:text-primary"><Github className="w-3.5 h-3.5" aria-hidden="true" />Explore on GitHub</a>
        </div>
      </div>
    </section>
  );
}
