#!/bin/bash

checkExists () {
  # see https://github.com/temporalio/temporal/issues/4160
  if temporal  operator search-attribute list | grep -q "$1"; then
    return 0
  else
    return 1
fi
}

echo "now trying to register Dex system search attributes..."

for run in {1..60}; do
  sleep 1
  temporal  operator search-attribute  create --name FlowType --type Keyword
  sleep 0.1
  temporal  operator search-attribute  create --name ActiveStepTypes --type KeywordList
  sleep 0.1
  temporal  operator search-attribute  create --name CustomKeywordArrayField --type KeywordList
  sleep 0.1
  if checkExists "FlowType" && checkExists "ActiveStepTypes" && checkExists "CustomKeywordArrayField" ; then
      echo "All search attributes are registered"
      break
    fi
done

tctl namespace register default

tail -f /dev/null
