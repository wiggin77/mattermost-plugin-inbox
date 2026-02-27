import {expect, test} from '@mattermost/playwright-lib';

import {inboxPluginId} from '../support/constant';

test('plugin is installed and running', async ({pw}) => {
    const {adminClient} = await pw.initSetup();

    const plugins = await adminClient.getPlugins();
    const activeIDs = plugins.active.map((p: {id: string}) => p.id);
    expect(activeIDs).toContain(inboxPluginId);
});

test('/inbox help returns usage information', async ({pw}) => {
    const {adminUser} = await pw.initSetup();
    const {page} = await pw.testBrowser.login(adminUser);

    // Navigate to Town Square.
    await page.goto('/');
    await page.waitForURL('**/channels/**', {timeout: 15000});

    // Type the slash command.
    const messageInput = page.locator('#post_textbox');
    await messageInput.fill('/inbox help');
    await messageInput.press('Enter');

    // The help response should appear as an ephemeral post.
    await page.getByText('Mattermost Inbox - Outlook Email Sync').waitFor({timeout: 10000});
    await page.getByText('/inbox connect').waitFor();
});
