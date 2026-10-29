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

import { ArrowRight, BookOpen, Box, Network, Package } from "lucide-react";

const steps = [
  { number: "01", icon: Package, title: "Install", text: "Add Cisco Virtual Kubelet to your Kubernetes cluster." },
  { number: "02", icon: Network, title: "Connect", text: "Register a supported device and its credentials." },
  { number: "03", icon: Box, title: "Deploy", text: "Run your first application and check its status." },
];

export default function GetStarted() {
  return (
    <section id="get-started" className="py-16 sm:py-24 scroll-mt-20">
      <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="rounded-3xl border border-primary/20 bg-gradient-to-br from-primary/5 via-surface/50 to-accent/5 p-7 sm:p-12">
          <div className="text-center max-w-xl mx-auto mb-9"><p className="text-xs uppercase tracking-[0.18em] font-semibold text-primary mb-3">Your first deployment</p><h2 className="text-3xl sm:text-4xl font-bold tracking-tight mb-4">Start with one device.</h2><p className="text-text-muted leading-relaxed">A guided path from your Kubernetes cluster to an application running on your device.</p></div>
          <ol className="grid md:grid-cols-3 gap-6 sm:gap-8 mb-10">{steps.map((step) => <li key={step.number} className="flex gap-4"><div className="text-primary/80 pt-1"><step.icon className="w-5 h-5" aria-hidden="true" /></div><div><h3 className="font-semibold mb-2"><span className="text-text-muted font-mono text-xs mr-2">{step.number}</span>{step.title}</h3><p className="text-sm text-text-muted leading-relaxed">{step.text}</p></div></li>)}</ol>
          <div className="flex flex-col sm:flex-row items-center justify-center gap-5">
            <a href="/cisco-virtual-kubelet/docs/getting-started/" className="inline-flex items-center justify-center gap-2 px-6 py-3.5 bg-primary text-background font-semibold rounded-xl hover:bg-primary-light transition-colors">Follow the setup guide<ArrowRight className="w-4 h-4" aria-hidden="true" /></a>
            <a href="/cisco-virtual-kubelet/docs/" className="inline-flex items-center gap-2 text-sm font-medium text-primary hover:underline underline-offset-4"><BookOpen className="w-4 h-4" aria-hidden="true" />Browse documentation</a>
          </div>
        </div>
      </div>
    </section>
  );
}
