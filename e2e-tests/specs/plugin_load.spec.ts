import {expect, test} from '@mattermost/playwright-lib';

import {inboxPluginId} from '../support/constant';

test('plugin is installed and running', async ({pw}) => {
    const {adminUser, adminClient} = await pw.initSetup();

    // Verify plugin is active via the admin API.
    const plugins = await adminClient.getPluginStatuses();
    const inboxStatus = plugins.find((p: {plugin_id: string}) => p.plugin_id === inboxPluginId);
    expect(inboxStatus).toBeDefined();
    expect(inboxStatus!.state).toBe(1); // 1 = running
});

test('/inbox help returns usage information', async ({pw}) => {
    const {user} = await pw.initSetup();
    const {channelsPage} = await pw.testBrowser.login(user);

    await channelsPage.goto();
    await channelsPage.toBeVisible();

    // Type the slash command.
    await channelsPage.postMessage('/inbox help');

    // The help response should appear as an ephemeral post.
    const post = await channelsPage.getLastPost();
    await post.toBeVisible();
    await post.toContainText('Mattermost Inbox - Outlook Email Sync');
});
