import {test as setup} from '@mattermost/playwright-lib';

import {inboxPluginId} from '@/support/constant';

setup('ensure plugin is enabled', async ({pw}) => {
    await pw.ensurePluginsLoaded([inboxPluginId]);
});
