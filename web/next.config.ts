import type { NextConfig } from "next";

// The console is deployed as a standalone Next.js app (separate from the public
// site). API calls go to the Go backend; the base URL is configured via
// NEXT_PUBLIC_API_BASE (defaults to same-origin for dev proxying).
const nextConfig: NextConfig = {
  reactStrictMode: true,
  // Don't let lint warnings (e.g. style nits) fail production/Docker builds;
  // TypeScript type errors still block the build.
  eslint: { ignoreDuringBuilds: true },
  // Proxy /api and /sse to the backend during local dev so the browser sees a
  // same-origin app (no CORS). In production these are set via env / ingress.
  async rewrites() {
    const backend = process.env.BACKEND_ORIGIN || "http://localhost:9090";
    return [
      { source: "/api/:path*", destination: `${backend}/api/:path*` },
      { source: "/sse", destination: `${backend}/sse` },
      { source: "/files/:path*", destination: `${backend}/files/:path*` },
    ];
  },
};

export default nextConfig;
