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

"use client";

import { useRef, useSyncExternalStore } from "react";
import { Activity, ArrowDown, ArrowRight, Box, Check, FileCode, GitCompareArrows, HeartPulse, Layers, ListChecks, Network, Server, SlidersHorizontal } from "lucide-react";

const foundations = [
  {
    id: "app-hosting", label: "App hosting", shortLabel: "App hosting", icon: Box, number: "01",
    title: "Put applications closer to the network.",
    description: "Deploy containers to supported Cisco devices using the Kubernetes workflow you already know.",
    details: ["Describe your workload as a Pod.", "Let Virtual Kubelet manage its lifecycle on the device."],
    href: "/cisco-virtual-kubelet/docs/getting-started/", link: "Explore app hosting",
    steps: [
      { icon: FileCode, label: "Describe", text: "Kubernetes Pod" },
      { icon: Layers, label: "Reconcile", text: "Virtual Kubelet" },
      { icon: Box, label: "Run", text: "Device app hosting" },
    ],
    caption: "A Cisco device appears as a virtual node. Your application runs on the device.",
    note: "For devices with supported app-hosting capabilities.",
  },
  {
    id: "network-as-code", label: "Network as Code", shortLabel: "Configuration", icon: SlidersHorizontal, number: "02",
    title: "Declare supported device configuration.",
    description: "Express supported device settings as Kubernetes resources, then follow their configuration and verification.",
    details: ["Keep configuration intent in version control.", "Detect drift and verify the resulting device state."],
    href: "/cisco-virtual-kubelet/docs/netascode-config/", link: "Explore Network as Code",
    steps: [
      { icon: FileCode, label: "Declare", text: "Configuration intent" },
      { icon: GitCompareArrows, label: "Reconcile", text: "Compare & apply" },
      { icon: Network, label: "Verify", text: "Device configuration" },
    ],
    caption: "A continuous reconciliation loop connects your declared intent to device configuration.",
    note: "Beta. Supported configuration families vary by platform.",
  },
  {
    id: "device-operations", label: "Operations & visibility", shortLabel: "Operations", icon: Activity, number: "03",
    title: "See device health. Act with context.",
    description: "Inspect device signals and run supported diagnostics, with explicit opt-in for software operations.",
    details: ["Inspect device health through metrics and diagnostics.", "Opt in to controlled IOS-XE software upgrades when needed."],
    href: "/cisco-virtual-kubelet/docs/operator-guide/", link: "Explore device operations",
    steps: [
      { icon: ListChecks, label: "Request", text: "Operational task" },
      { icon: Server, label: "Execute", text: "Device operation" },
      { icon: HeartPulse, label: "Observe", text: "Status & signals" },
    ],
    caption: "Operational resources and device signals give you a consistent place to follow progress.",
    note: "Capabilities vary. Software mutation is opt-in and disabled by default.",
  },
];

function subscribeToHash(onChange: () => void) {
  window.addEventListener("hashchange", onChange);
  window.addEventListener("popstate", onChange);
  return () => {
    window.removeEventListener("hashchange", onChange);
    window.removeEventListener("popstate", onChange);
  };
}

function selectedFromHash() {
  const index = foundations.findIndex((item) => `#${item.id}` === window.location.hash);
  return index < 0 ? 0 : index;
}

function selectFoundation(index: number, replace = false) {
  const hash = `#${foundations[index].id}`;
  if (window.location.hash === hash) return;
  // Keep Next's history state and the reader's scroll position when switching tabs.
  if (replace) window.history.replaceState(window.history.state, "", hash);
  else window.history.pushState(window.history.state, "", hash);
  window.dispatchEvent(new HashChangeEvent("hashchange"));
}

export default function Features() {
  const selected = useSyncExternalStore(subscribeToHash, selectedFromHash, () => 0);
  const tabs = useRef<Array<HTMLButtonElement | null>>([]);
  const foundation = foundations[selected];

  return (
    <section id="features" className="relative scroll-mt-20 py-12 sm:py-20">
      {foundations.map((item) => (
        <span key={item.id} id={item.id} aria-hidden="true" className="absolute top-0 scroll-mt-20" />
      ))}
      <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="max-w-2xl mx-auto text-center mb-9">
          <p className="text-xs font-semibold tracking-[0.18em] uppercase text-primary mb-3">The foundations</p>
          <h2 className="text-3xl sm:text-4xl font-bold tracking-tight mb-4">Start with app hosting. Explore from there.</h2>
          <p className="text-text-muted leading-relaxed">Deploy your first workload, then explore supported configuration and diagnostic workflows when you need them.</p>
        </div>

        <div role="tablist" aria-label="Explore the main capabilities" className="grid grid-cols-3 gap-1 sm:gap-2 p-1.5 sm:p-2 rounded-2xl bg-surface/70 border border-border mb-5">
          {foundations.map((item, index) => (
            <button
              key={item.id}
              ref={(element) => { tabs.current[index] = element; }}
              type="button"
              role="tab"
              id={`tab-${item.id}`}
              aria-selected={selected === index}
              aria-controls="foundation-panel"
              tabIndex={selected === index ? 0 : -1}
              onClick={() => selectFoundation(index)}
              onKeyDown={(event) => {
                let next = index;
                if (event.key === "ArrowRight") next = (index + 1) % foundations.length;
                else if (event.key === "ArrowLeft") next = (index + foundations.length - 1) % foundations.length;
                else if (event.key === "Home") next = 0;
                else if (event.key === "End") next = foundations.length - 1;
                else return;
                event.preventDefault();
                selectFoundation(next, true);
                tabs.current[next]?.focus();
              }}
              className={`flex flex-col sm:flex-row items-center justify-center gap-1.5 sm:gap-2.5 rounded-xl px-1.5 sm:px-3 py-3 text-xs sm:text-sm font-semibold transition-colors ${selected === index ? "bg-primary/10 text-primary border border-primary/30" : "text-text-muted border border-transparent hover:bg-surface-light hover:text-foreground"}`}
            >
              <item.icon className="w-4 h-4 shrink-0" aria-hidden="true" />
              <span className="lg:hidden">{item.shortLabel}</span>
              <span className="hidden lg:inline">{item.label}</span>
            </button>
          ))}
        </div>

        <div
          role="tabpanel"
          id="foundation-panel"
          aria-labelledby={`tab-${foundation.id}`}
          tabIndex={0}
          className="grid md:grid-cols-[0.9fr_1.1fr] rounded-3xl border border-border overflow-hidden bg-surface/40"
        >
          <div className="p-6 sm:p-8 lg:p-10 flex flex-col justify-center">
            <div className="flex items-center gap-3 mb-5">
              <span className="flex items-center justify-center w-10 h-10 rounded-xl bg-primary/10 text-primary">
                <foundation.icon className="w-5 h-5" aria-hidden="true" />
              </span>
              <span className="text-xs text-text-muted font-mono">{foundation.number} / {foundation.label}</span>
            </div>
            <h3 className="text-2xl sm:text-3xl font-semibold tracking-tight leading-tight mb-4">{foundation.title}</h3>
            <p className="text-text-muted leading-relaxed mb-5">{foundation.description}</p>
            <ul className="space-y-3 mb-7">
              {foundation.details.map((detail) => (
                <li key={detail} className="flex gap-2.5 text-sm leading-relaxed">
                  <Check className="text-primary w-4 h-4 mt-0.5 shrink-0" aria-hidden="true" />
                  {detail}
                </li>
              ))}
            </ul>
            <a href={foundation.href} className="inline-flex items-center gap-2 self-start text-sm font-semibold text-primary hover:text-primary-light">
              {foundation.link}
              <ArrowRight className="w-4 h-4" aria-hidden="true" />
            </a>
          </div>

          <figure className="foundation-visual border-t md:border-t-0 md:border-l border-border p-6 sm:p-8 lg:p-10 flex flex-col justify-center">
            <p className="text-[11px] uppercase tracking-[0.16em] text-text-muted text-center mb-5">The workflow at a glance</p>
            <ol className="flex flex-col items-center">
              {foundation.steps.map((step, index) => (
                <li key={step.label} className="w-full max-w-xs flex flex-col items-center">
                  {index > 0 && (
                    <div className="flex flex-col items-center py-1" aria-hidden="true">
                      <div className="h-3 w-px bg-primary/40" />
                      <ArrowDown className="w-4 h-4 text-primary/80" />
                    </div>
                  )}
                  <div className={`w-full flex items-center gap-4 px-5 py-3.5 rounded-xl border ${index === 1 ? "bg-accent/10 border-accent-light/40" : index === 2 ? "bg-success/5 border-success/30" : "bg-surface border-primary/30"}`}>
                    <step.icon className={`w-6 h-6 shrink-0 ${index === 1 ? "text-accent-light" : index === 2 ? "text-success" : "text-primary"}`} aria-hidden="true" />
                    <div>
                      <p className="text-[10px] uppercase tracking-widest text-text-muted mb-0.5">{step.label}</p>
                      <p className="font-medium text-sm">{step.text}</p>
                    </div>
                  </div>
                </li>
              ))}
            </ol>
            <figcaption className="text-center text-sm text-text-muted leading-relaxed max-w-sm mx-auto mt-5">{foundation.caption}</figcaption>
          </figure>
        </div>

        <div className="mt-5 flex flex-col sm:flex-row sm:items-center justify-between gap-3 text-xs leading-relaxed text-text-muted">
          <p>{foundation.note}</p>
          <a href="/cisco-virtual-kubelet/docs/status/" className="text-primary hover:underline underline-offset-4 shrink-0">
            Platform support & feature status <span aria-hidden="true">↗</span>
          </a>
        </div>
      </div>
    </section>
  );
}
