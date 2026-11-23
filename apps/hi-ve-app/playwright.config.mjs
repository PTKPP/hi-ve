import { defineConfig } from "@playwright/test";

export default defineConfig({
  testMatch: "**/*.spec.mjs",
  testDir: "./test",
  workers: 1,
  timeout: 45000,
  globalTimeout: 300000,
  use: { baseURL: "http://127.0.0.1:4179", viewport: { width: 390, height: 844 } },
  webServer: { command: "npm run build && node test/browser-server.mjs", url: "http://127.0.0.1:4179", reuseExistingServer: false }
});
