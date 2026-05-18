#!/usr/bin/env bash

# 这个脚本是 QTA 上报的 best-effort 辅助步骤。UT-Bench 的评测结果以
# generated test 和 evaluator 输出为准，上报失败不应阻断单测生成流程。

function _uid() {
  if command -v uuidgen >/dev/null 2>&1; then
    uuidgen | awk '{print tolower($0)}'
    return 0
  fi
  date +%s%N 2>/dev/null || echo "unknown"
}

function _username() {
  local user_email=""
  if command -v git >/dev/null 2>&1; then
    user_email="$(git config user.email 2>/dev/null || true)"
  fi
  local username="${user_email%@tencent.com}"
  if [[ "${username}" == "" ]]; then
    username="${USER:-anonymous}"
  fi
  echo "${username}"
}

function _repository() {
  local remote_url=""
  if command -v git >/dev/null 2>&1; then
    remote_url="$(git remote get-url origin 2>/dev/null || true)"
  fi
  local repo="${remote_url#git@}"
  repo="${repo#https://}"
  repo="${repo#http://}"
  repo="${repo%.git}"
  repo="${repo//://}"
  echo "${repo:-unknown}"
}

function _commit() {
  if command -v git >/dev/null 2>&1; then
    git rev-parse HEAD 2>/dev/null || echo "unknown"
    return 0
  fi
  echo "unknown"
}

function _cmd() {
  if [[ "${CLAUDECODE:-0}" -gt "0" ]]; then
    echo "claude"
  else
    echo "codebuddy"
  fi
}

if [[ "${1:-}" == "" ]]; then
  echo "no data to push"
  exit 0
fi

push_data='{
  "Version": 1,
  "UID": "'$(_uid)'",
  "Args": ["'$(_cmd)'", "/qta-gen-ut"],
  "Username": "'$(_username)'",
  "Repository": "'$(_repository)'",
  "Commit": "'$(_commit)'",
  "Results": {
    "Generate": {
      "Batches": '${1}'
    }
  }
}'

echo "Request:"
echo "${push_data}"

if ! command -v curl >/dev/null 2>&1; then
  echo "Response: curl not found; skip QTA trace push"
  exit 0
fi

echo "Response:"
curl --fail --show-error --connect-timeout 5 --max-time 15 \
  http://qta.woa.com/api/qta/ut-agent/v1/runrecords \
  -H 'Content-Type: application/json' \
  --data "${push_data}" || echo "QTA trace push failed; ignored"

exit 0
