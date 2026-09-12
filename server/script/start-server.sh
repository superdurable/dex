#!/bin/bash

CONFIG_TEMPLATE_PATH="${CONFIG_TEMPLATE_PATH:-/dex/config/config_template.yaml}"
SRC_ROOT="${SRC_ROOT:-/dex}"
BACKEND_ADDRESS=''
TEMPORAL_SERVICE_NAME="${TEMPORAL_SERVICE_NAME:-temporal}"
CADENCE_SERVICE_NAME="${CADENCE_SERVICE_NAME:-cadence}"

REQUESTED_SERVICES=''
SERVICES_FLAG_PRESENT='false'
EXPECT_SERVICES_VALUE='false'
for ARGUMENT in "$@"; do
  if [[ "${EXPECT_SERVICES_VALUE}" = "true" ]]; then
    REQUESTED_SERVICES="${ARGUMENT}"
    SERVICES_FLAG_PRESENT='true'
    EXPECT_SERVICES_VALUE='false'
    continue
  fi
  case "${ARGUMENT}" in
    --services)
      SERVICES_FLAG_PRESENT='true'
      EXPECT_SERVICES_VALUE='true'
      ;;
    --services=*)
      REQUESTED_SERVICES="${ARGUMENT#--services=}"
      SERVICES_FLAG_PRESENT='true'
      ;;
  esac
done

NORMALIZED_SERVICES=$(echo "${REQUESTED_SERVICES}" | tr -d '[:space:]')
WAIT_FOR_BACKEND='true'
if [[ "${SERVICES_FLAG_PRESENT}" = "true" ]] \
  && [[ ",${NORMALIZED_SERVICES}," != *",api,"* ]] \
  && [[ ",${NORMALIZED_SERVICES}," != *",interpreter,"* ]]; then
  WAIT_FOR_BACKEND='false'
fi

if [[ "${WAIT_FOR_BACKEND}" = "true" ]]; then
  if [[ -n "${BACKEND_DEPENDENCY}" && "${BACKEND_DEPENDENCY}" = "cadence" ]]; then
    BACKEND_ADDRESS="${CADENCE_HOST_PORT:-"${CADENCE_SERVICE_NAME}:7833"}"
  else
    BACKEND_ADDRESS="${TEMPORAL_HOST_PORT:-"${TEMPORAL_SERVICE_NAME}:7233"}"
  fi
  BACKEND_HOST="${BACKEND_ADDRESS%:*}"
  BACKEND_PORT="${BACKEND_ADDRESS##*:}"

  RESULT=1
  while [[ "${RESULT}" = "1" ]]
  do
    nc -z "${BACKEND_HOST}" "${BACKEND_PORT}"
    RESULT=$?
    if [[ "${RESULT}" = "1" ]]; then
      sleep 3
      echo "Waiting for ${BACKEND_ADDRESS} to be ready..."
    fi
  done

  for ATTEMPT in {1..60}; do
    sleep 1
    if nc -zv "${BACKEND_HOST}" "${BACKEND_PORT}"; then
      break
    fi
    if [[ "${ATTEMPT}" = "60" ]]; then
      echo "Workflow backend ${BACKEND_ADDRESS} stopped before startup"
      exit 1
    fi
  done

  echo "waiting 10s for the workflow backend to become ready"
  sleep 10
fi

exec "${SRC_ROOT}/dex-server" --config "${CONFIG_TEMPLATE_PATH}" start "$@"
