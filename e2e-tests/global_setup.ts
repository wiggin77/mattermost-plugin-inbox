import {baseGlobalSetup, testConfig} from '@mattermost/playwright-lib';

async function globalSetup() {
    try {
        await baseGlobalSetup();
    } catch (error: unknown) {
        // eslint-disable-next-line no-console
        console.error(error);
        throw new Error(
            `Global setup failed.\n\tEnsure the server at ${testConfig.baseURL} is running and accessible.\n\tPlease check the logs for more details.`,
        );
    }

    return function () {
        // placeholder for teardown
    };
}

export default globalSetup;
