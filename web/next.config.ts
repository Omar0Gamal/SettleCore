import type { NextConfig } from "next";

const isStaticExport = process.env.NEXT_PUBLIC_STATIC_EXPORT === "1";
const rawBasePath = process.env.NEXT_PUBLIC_BASE_PATH?.trim();
const basePath = rawBasePath && rawBasePath !== "/" ? rawBasePath.replace(/\/+$/, "") : undefined;

const nextConfig: NextConfig = {
  reactStrictMode: true,
  output: isStaticExport ? "export" : "standalone",
  ...(isStaticExport ? { images: { unoptimized: true } } : {}),
  ...(basePath ? { basePath } : {}),
  poweredByHeader: false,
  devIndicators: false,
};

export default nextConfig;
