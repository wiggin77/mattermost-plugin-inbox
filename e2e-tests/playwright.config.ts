import {defineConfig} from '@playwright/test';
import {duration, testConfig} from '@mattermost/playwright-lib';

export default defineConfig({
    globalSetup: './global_setup.ts',
    forbidOnly: testConfig.isCI,
    outputDir: 'test-results',
    retries: testConfig.isCI ? 2 : 0,
    testDir: 'specs',
    timeout: duration.one_min,
    workers: testConfig.workers,
    expect: {
        timeout: duration.ten_sec,
    },
    use: {
        baseURL: testConfig.baseURL,
        ignoreHTTPSErrors: true,
        headless: testConfig.headless,
        locale: 'en-US',
        launchOptions: {
            slowMo: testConfig.slowMo,
        },
        screenshot: 'only-on-failure',
        trace: 'off',
        video: 'retain-on-failure',
        actionTimeout: duration.half_min,
    },
    projects: [
        {name: 'setup', testMatch: /test\.setup\.ts/},
        {
            name: 'chrome',
            use: {
                browserName: 'chromium',
                permissions: ['notifications'],
                viewport: {width: 1280, height: 1024},
            },
            dependencies: ['setup'],
        },
    ],
    reporter: [
        ['html', {open: 'never', outputFolder: './results/reporter'}],
        ['json', {outputFile: './results/reporter/results.json'}],
        ['list'],
    ],
});
