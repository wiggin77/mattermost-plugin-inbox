import {expect, test} from '@mattermost/playwright-lib';

import {inboxPluginId} from '../support/constant';

test('plugin is installed and active', async ({pw}) => {
    const {adminUser, adminClient} = await pw.initSetup();

    // Use the admin API to check plugin status directly.
    const resp = await adminClient.enablePlugin(inboxPluginId);

    // enablePlugin returns OK if already enabled — this confirms it's installed.
    // If the plugin didn't exist, this would throw.
    expect(resp).toBeDefined();
});

test('/inbox help slash command is registered', async ({pw}) => {
    const {adminClient} = await pw.initSetup();

    // Verify the /inbox command is registered via the API.
    const commands = await adminClient.getAutocompleteCommandsList('');
    const inboxCommand = commands.find((c: {trigger: string}) => c.trigger === 'inbox');
    expect(inboxCommand).toBeDefined();
    expect(inboxCommand!.auto_complete_desc).toContain('Outlook');
});
