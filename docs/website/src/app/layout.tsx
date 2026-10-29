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

import type { Metadata } from "next";
import { Geist, Geist_Mono } from "next/font/google";
import "./globals.css";

const geistSans = Geist({
  variable: "--font-geist-sans",
  subsets: ["latin"],
});

const geistMono = Geist_Mono({
  variable: "--font-geist-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "Cisco Virtual Kubelet — Bring Kubernetes to Cisco Network Devices",
  description:
    "A Virtual Kubelet provider for app-hosted workloads, declarative configuration and opt-in topology-aware IOS-XE application deployment. Use native Kubernetes placement, scheduling, observability and security constructs.",
  keywords: [
    "Cisco",
    "Virtual Kubelet",
    "Kubernetes",
    "Edge Computing",
    "IOS-XE",
    "NX-OS",
    "Nexus",
    "Catalyst",
    "RESTCONF",
    "NX-API",
    "Containers",
    "gNOI",
    "Topology-Aware Upgrades",
  ],
  openGraph: {
    title: "Cisco Virtual Kubelet",
    description:
      "A Virtual Kubelet provider for app-hosted workloads, declarative configuration and opt-in topology-aware IOS-XE application deployment. Use native Kubernetes placement, scheduling, observability and security constructs.",
    type: "website",
  },
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className="dark">
      <body
        className={`${geistSans.variable} ${geistMono.variable} antialiased bg-background text-foreground`}
      >
        {children}
      </body>
    </html>
  );
}
