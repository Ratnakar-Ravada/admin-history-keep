import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./e2e",
  workers: 1,
  timeout: 45000,
  expect: { timeout: 12000 },
  reporter: "list",
  use: {
    baseURL: process.env.APP_URL || "http://127.0.0.1:8080",
    viewport: { width: 1440, height: 1100 },
    launchOptions: {
      executablePath:
        process.env.BROWSER_EXECUTABLE_PATH ||
        (process.platform === "darwin"
          ? "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"
          : undefined),
      args: ["--use-angle=swiftshader", "--enable-unsafe-swiftshader"],
    },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
});
