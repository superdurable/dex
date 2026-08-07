#!/usr/bin/env bash
set -euo pipefail

release_version="${1:?usage: ./validate-publication.sh VERSION [all-natives]}"
if [[ "${2:-}" == "all-natives" ]]; then
  ./gradlew validatePublication \
    "-PreleaseVersion=${release_version}" \
    -PnativeResourcesPrepared=true \
    -PexpectAllNativePlatforms=true \
    --no-daemon
else
  ./gradlew validatePublication \
    "-PreleaseVersion=${release_version}" \
    --no-daemon
fi

publication_repository="file://${PWD}/build/publication-repository"

./gradlew -p publication-smoke-test clean run \
  "-PdexSdkVersion=${release_version}" \
  "-PpublicationRepository=${publication_repository}" \
  --no-daemon

mvn --batch-mode --no-transfer-progress \
  -f publication-smoke-test/pom.xml \
  "-DdexSdkVersion=${release_version}" \
  "-DpublicationRepository=${publication_repository}" \
  clean compile exec:java
