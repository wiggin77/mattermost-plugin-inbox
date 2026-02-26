import {test, expect} from '@playwright/test';

const adminUsername = process.env.MM_ADMIN_USERNAME || 'sysadmin';
const adminPassword = process.env.MM_ADMIN_PASSWORD || 'Sys@dmin-sample1';

test.describe('Plugin load', () => {
    test.beforeEach(async ({page}) => {
        await page.goto('/login');
        await page.getByPlaceholder('Email or Username').fill(adminUsername);
        await page.getByPlaceholder('Password').fill(adminPassword);
        await page.getByRole('button', {name: 'Log in'}).click();

        // Wait for the main channel view to load.
        await page.waitForURL('**/channels/**');
    });

    test('plugin is active in System Console', async ({page}) => {
        await page.goto('/admin_console/plugins/plugin_com.mattermost.plugin-inbox');
        await expect(page.locator('text=Mattermost Inbox (Outlook Sync)')).toBeVisible();
    });

    test('/inbox help returns usage information', async ({page}) => {
        // Type the slash command in the message box.
        const messageInput = page.getByRole('textbox', {name: /write to/i});
        await messageInput.fill('/inbox help');
        await messageInput.press('Enter');

        // The help response should appear as an ephemeral post.
        await expect(page.locator('text=Mattermost Inbox - Outlook Email Sync')).toBeVisible({timeout: 10000});
        await expect(page.locator('text=/inbox connect')).toBeVisible();
    });
});
