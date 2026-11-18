#!/usr/bin/env bash
#
# Read-only probe of the Schoolbox API. Prints response shapes (keys, types,
# counts) and HTTP status; never prints the token. Raw bodies go to probe-out/.

set -euo pipefail

readonly ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly OUT="${ROOT}/probe-out"
readonly PAUSE_SECONDS=2
readonly ENV_FILE="${ROOT}/.envrc.private"

err() {
  echo "error: $*" >&2
}

load_token() {
  local name value
  set -a
  # shellcheck disable=SC1091
  source "${ENV_FILE}"
  set +a
  # Variable name in the env file is unknown to the script author; pick the first JWT-shaped value.
  while IFS= read -r name; do
    value="${!name-}"
    if [[ "${value}" =~ ^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$ ]]; then
      TOKEN="${value}"
      echo "using token from \$${name}"
      return 0
    fi
  done < <(grep -oE '^(export +)?[A-Za-z_][A-Za-z0-9_]*=' "${ENV_FILE}" \
             | sed -E 's/^export +//; s/=$//')
  err "no JWT-shaped value found in ${ENV_FILE}"
  return 1
}

b64url_decode() {
  local s="${1//-/+}"
  s="${s//_//}"
  while (( ${#s} % 4 )); do s+="="; done
  printf '%s' "${s}" | base64 -d
}

print_claims() {
  local payload
  payload="$(cut -d. -f2 <<<"${TOKEN}")"
  b64url_decode "${payload}" | jq '{
    sub, iss, aud, jti,
    iat_utc: (.iat | if . then todate else null end),
    exp_utc: (.exp | if . then todate else null end),
    ttl_days: (if .exp and .iat then ((.exp - .iat) / 86400) else null end),
    scope
  }'
}

shape() {
  jq 'def s: if type == "object" then with_entries(.value |= s)
             elif type == "array" then (if length == 0 then [] else [(.[0] | s), "…\(length) items"] end)
             else type end;
      s'
}

# Usage: get <label> <path>
get() {
  local label="$1" path="$2" file status
  file="${OUT}/${label}.json"
  status="$(curl -sS -o "${file}" -w '%{http_code} %{content_type}' \
    -H "Authorization: Bearer ${TOKEN}" \
    -H 'Accept: application/json' \
    "${BASE_URL}${path}")"
  echo
  echo "=== ${label}  GET ${path}  -> ${status}"
  if jq -e . "${file}" >/dev/null 2>&1; then
    shape <"${file}" | head -c 3000
    echo
  else
    echo "(non-JSON body, $(wc -c <"${file}" | tr -d ' ') bytes)"
  fi
  sleep "${PAUSE_SECONDS}"
}

main() {
  local sub child start end
  mkdir -p "${OUT}"
  load_token
  if [[ -z "${SCHOOLBOX_URL:-}" ]]; then
    err "SCHOOLBOX_URL is not set (e.g. https://schoolbox.example.edu.au)"
    return 1
  fi
  readonly BASE_URL="${SCHOOLBOX_URL%/}"
  print_claims
  # The JWT sub is the SIS externalId; API paths need the internal user id.
  curl -sS -o "${OUT}/home.html" -H "Authorization: Bearer ${TOKEN}" "${BASE_URL}/"
  sub="$(perl -ne 'print $1 if /window\.schoolboxUser\s*=\s*\{"id":(\d+)/' "${OUT}/home.html")"
  if [[ -z "${sub}" ]]; then
    err "could not find window.schoolboxUser id on the homepage"
    return 1
  fi
  echo "internal user id: ${sub}"

  get me "/api/user/${sub}"
  get my_evidence "/learning/evidenceFeed/user/${sub}"
  get news_feed "/news/lists/feed"
  get news_topics "/news/topics"

  for child in $(jq -r '(.students // .data.students // [])[].id' "${OUT}/me.json" 2>/dev/null); do
    get "child_${child}_evidence" "/learning/evidenceFeed/user/${child}"
    get "child_${child}_pastoral" "/pastoral/student/${child}?limit=5"
    get "child_${child}_user" "/api/user/${child}"
    start="$(date +%s)"
    end="$(( start + 14 * 86400 ))"
    get "child_${child}_calendar" "/calendar/ajax/full?userId=${child}&start=${start}&end=${end}"
  done
}

main "$@"
