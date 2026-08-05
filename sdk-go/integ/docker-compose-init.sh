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
registered=false
for run in {1..120}; do
  sleep 1
  temporal  operator search-attribute  create --name FlowType --type Keyword
  sleep 0.1
  temporal  operator search-attribute  create --name ActiveStepTypes --type KeywordList
  sleep 0.1
  temporal  operator search-attribute  create --name CustomKeywordField --type Keyword
  sleep 0.1
  temporal  operator search-attribute  create --name CustomIntField --type Int
  sleep 0.1
  temporal  operator search-attribute  create --name CustomBoolField --type Bool
  sleep 0.1
  temporal  operator search-attribute  create --name CustomDoubleField --type Double
  sleep 0.1
  temporal  operator search-attribute  create --name CustomDatetimeField --type Datetime
  sleep 0.1
  temporal  operator search-attribute  create --name CustomStringField --type Text
  sleep 0.1
  temporal  operator search-attribute  create --name CustomKeywordArrayField --type KeywordList
  sleep 0.1

  if checkExists "FlowType" && checkExists "ActiveStepTypes" && checkExists "CustomKeywordField" && checkExists "CustomIntField" && checkExists "CustomBoolField" && checkExists "CustomDoubleField" && checkExists "CustomDatetimeField" && checkExists "CustomStringField" && checkExists "CustomKeywordArrayField"; then
    echo "All search attributes are registered"
    registered=true
    break
  fi

done

if ! $registered; then
  echo "search attribute registration timed out" >&2
  exit 1
fi

touch /tmp/search-attributes-ready
tail -f /dev/null
