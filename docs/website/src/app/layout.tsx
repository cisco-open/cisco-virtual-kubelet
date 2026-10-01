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
  title: "Cisco Virtual Kubelet — Network Workloads and Topology-Aware Upgrades",
  description:
    "Native Kubernetes app hosting and configuration on Cisco devices, with opt-in topology-aware IOS-XE rollouts, secure gNOI and topology-scoped image selection. NX-OS app hosting and configuration remain Beta.",
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
      "Place app-hosted workloads and coordinate opt-in topology-aware IOS-XE software upgrades using native Kubernetes workflows and secure gNOI.",
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
