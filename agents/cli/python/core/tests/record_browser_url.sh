#!/bin/sh
# A fake BROWSER for the tests: append the URL to $DEX_AGENTS_TEST_OPENED_URLS.
printf "%s\n" "$1" >> "$DEX_AGENTS_TEST_OPENED_URLS"
