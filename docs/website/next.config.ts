import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: 'export',
  basePath: '/cisco-virtual-kubelet',
  images: {
    unoptimized: true,
  },
  reactCompiler: true,
};

export default nextConfig;
