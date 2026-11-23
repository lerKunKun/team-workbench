import { defineConfig } from "@playwright/test"

export default defineConfig({
  testDir: "./tests",
  use: {
    baseURL: "http://127.0.0.1:3100",
    headless: true,
    ...(process.env.WB_TEST_CHROME
      ? { launchOptions: { executablePath: process.env.WB_TEST_CHROME } }
      : {}),
  },
  webServer: {
    command: "pnpm dev --hostname 127.0.0.1 --port 3100",
    url: "http://127.0.0.1:3100",
    timeout: 60000,
    reuseExistingServer: false,
    stderr: "pipe",
  },
})
