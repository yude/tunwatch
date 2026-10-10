import { defineConfig } from "@playwright/test";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const data = mkdtempSync(join(tmpdir(), "tunwatch-e2e-"));
const config = join(data, "config.json");
const hosts = join(data, "test.hosts");
writeFileSync(hosts, "192.0.2.15 configured-ap.example\n");
writeFileSync(
  config,
  JSON.stringify({
    listen: "127.0.0.1:18089",
    data_dir: join(data, "data"),
    demo: true,
    hosts_file: hosts,
    node_profiles: { "demo-sensor": { interfaces: ["demo0"] } },
    stale_after: "10s",
    offline_after: "25s",
  }),
);
export default defineConfig({
  testDir: "./tests",
  fullyParallel: false,
  workers: 1,
  timeout: 30000,
  use: {
    baseURL: "http://127.0.0.1:18089",
    browserName: "chromium",
    headless: true,
    launchOptions: process.env.TUNWATCH_CHROMIUM
      ? { executablePath: process.env.TUNWATCH_CHROMIUM }
      : {},
  },
  webServer: {
    command: `../bin/tunwatch -config ${config}`,
    url: "http://127.0.0.1:18089/healthz",
    reuseExistingServer: false,
    env: {
      TUNWATCH_NODE_NAME: "demo-sensor",
      TUNWATCH_API_TOKEN: "tunwatch-e2e-test-token-not-a-real-secret",
    },
  },
});
