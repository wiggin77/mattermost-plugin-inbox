import {test, expect, Page} from '@playwright/test';

const baseURL = process.env.MM_SERVICESETTINGS_SITEURL || 'http://localhost:8065';
const adminUsername = process.env.MM_ADMIN_USERNAME || 'sysadmin';
const adminPassword = process.env.MM_ADMIN_PASSWORD || 'Sys@dmin-sample1';

async function loginViaAPI(page: Page) {
    // Log in via API and set the auth token as a cookie.
    const response = await page.request.post(`${baseURL}/api/v4/users/login`, {
        data: {login_id: adminUsername, password: adminPassword},
    });
    expect(response.ok()).toBeTruthy();
    const token = response.headers()['token'];
    await page.context().addCookies([{
        name: 'MMAUTHTOKEN',
        value: token,
        domain: new URL(baseURL).hostname,
        path: '/',
    }]);
}

test.describe('Plugin load', () => {
    test.beforeEach(async ({page}) => {
        await loginViaAPI(page);
    });

    test('plugin is active in System Console', async ({page}) => {
        await page.goto('/admin_console/plugins/plugin_com.mattermost.plugin-inbox');
        await expect(page.locator('text=Mattermost Inbox (Outlook Sync)')).toBeVisible({timeout: 15000});
    });

    test('/inbox help returns usage information', async ({page}) => {
        // Navigate to a channel.
        await page.goto('/');
        await page.waitForURL('**/channels/**', {timeout: 15000});

        // Type the slash command in the message box.
        const messageInput = page.getByRole('textbox', {name: /write to/i});
        await messageInput.fill('/inbox help');
        await messageInput.press('Enter');

        // The help response should appear as an ephemeral post.
        await expect(page.locator('text=Mattermost Inbox - Outlook Email Sync')).toBeVisible({timeout: 10000});
        await expect(page.locator('text=/inbox connect')).toBeVisible();
    });
});
