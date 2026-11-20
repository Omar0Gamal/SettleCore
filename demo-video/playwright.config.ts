import { defineConfig } from "@playwright/test";

/**
 * SettleCore 4K Retina Playwright Capture Configuration
 *
 * Renders the live Next.js 16.4 Pro Exchange UI at natural 1920x1080 desktop
 * proportions with deviceScaleFactor: 2, yielding crisp 3840x2160 (4K Retina)
 * PNG screenshots (8.29M pixels) for zero-blur Remotion 1080p compositing.
 */
export default defineConfig({
  testDir: "./capture",
  testMatch: "**/*.ts",
  timeout: 120_000,
  fullyParallel: false,
  workers: 1,
  retries: 0,
  use: {
    baseURL: process.env.NEXT_PUBLIC_WEB_URL ?? "http://localhost:3000",
    viewport: { width: 1920, height: 1080 },
    deviceScaleFactor: 2,
    colorScheme: "dark",
    video: "off",
    launchOptions: {
      args: [
        "--enable-gpu-rasterization",
        "--ignore-gpu-blocklist",
        "--force-color-profile=srgb",
      ],
    },
  },
  outputDir: "./public/captures",
});
