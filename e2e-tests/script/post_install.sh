#!/bin/bash

if [ -n "$PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD" ]; then
    echo "Skipped browsers download for Playwright"
    exit 0
fi

npx playwright install chromium
