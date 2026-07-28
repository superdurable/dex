#! /usr/bin/env bash

# This script ensures that the poetry.lock file does not contain references to the internal Indeed Nexus PyPI repository.
# It is used as a git hook to automatically remove the [package.source] section for Nexus from poetry.lock.
# This is necessary to ensure the lock file is portable and open-source friendly.
# The script uses gsed to remove the Nexus source block and any resulting empty lines, then checks if any changes were made.
# If Nexus references are found and removed, the script exits with 1 to prevent the commit, so we can stage the changes made by the script before committing again.

gsed -i'' \
    -e '/./{H;$!d}' \
    -e 'x' \
    -e 's|\[package.source\]\ntype\s*=\s*\"legacy\"\nurl\s*=\s*\"https://nexus.corp.indeed.com/repository/pypi/simple\"\nreference\s*=\s*\"nexus\"||' \
    poetry.lock

gsed -i'' \
    -e '1{/^\s*$/d}' \
    poetry.lock

gsed -i'' \
    -e '/^\s*$/N;/^\s*\n$/D' \
    poetry.lock

CHANGES=$(git diff --exit-code poetry.lock | grep -Pzo '\-\[package.source\]\n\-type = "legacy"\n\-url = "https://nexus.corp.indeed.com/repository/pypi/simple"\n\-reference = "nexus"\n' | wc -c)

if [[ $CHANGES -eq 0 ]]; then
  exit 0
fi

exit 1
