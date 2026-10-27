import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './e2e',
  testMatch: '*.spec.ts',
  fullyParallel: false,
  workers: 1,
  timeout: 90000,
  use: {
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    baseURL: 'http://127.0.0.1:18790',
    viewport: { width: 1440, height: 1000 },
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure'
  },
  webServer: {
    command: 'node e2e/server.mjs',
    url: 'http://127.0.0.1:18790',
    timeout: 30000,
    reuseExistingServer: false
  },
  reporter: 'list'
})
