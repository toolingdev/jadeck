import { defineConfig } from "@playwright/test";

// Runs against a jadeck that's already up with the simulator connected:
//   ptzsim --cameras 4 --setup http://127.0.0.1:8080
// JADECK_URL picks another address.
export default defineConfig({
  testDir: ".",
  testMatch: /.*\.spec\.ts/,
  outputDir: "results",
  timeout: 10 * 60_000,
  expect: { timeout: 8_000 },
  workers: 1,
  reporter: [["list"]],
  use: { baseURL: process.env.JADECK_URL ?? "http://127.0.0.1:8080" },
});
