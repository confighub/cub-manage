#!/usr/bin/env bash
# Bump every github.com/confighub/sdk/* requirement to one SDK release. The SDK
# modules are tagged in lockstep, so one version applies to all of them.
#
# Usage: scripts/update-sdk.sh [version]     # default: latest release of sdk/core
#
# Env:
#   SUMMARY_FILE  write a markdown summary here (for the PR body)
#
# Exits non-zero, leaving the bump in the working tree, if it does not build or
# its tests fail: that needs a code change, not a dependency bump.

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

version="${1:-}"
if [[ -z "${version}" ]]; then
  version="$(curl -fsSL https://proxy.golang.org/github.com/confighub/sdk/core/@latest |
    sed -n 's/.*"Version":"\([^"]*\)".*/\1/p')"
fi
if [[ ! "${version}" =~ ^v[0-9] ]]; then
  echo "Could not determine an SDK version (got: '${version}')" >&2
  exit 1
fi
echo "==> Target SDK version: ${version}"

# Only the SDK modules this module requires directly; go get brings the rest.
direct=()
while IFS= read -r mod; do
  direct+=("${mod}@${version}")
done < <(go list -m -f '{{if and (not .Main) (not .Indirect)}}{{.Path}}{{end}}' all | grep '^github.com/confighub/sdk/' | sort -u)

go get "${direct[@]}"
go mod tidy

status="builds and passes its tests"
if ! go build ./... || ! go vet ./... || ! go test ./...; then
  status="does not build or pass its tests; it needs a code change"
fi

summary="Bumped \`github.com/confighub/sdk/*\` to \`${version}\`. cub-manage ${status}."
printf '\n%s\n' "${summary}"
if [[ -n "${SUMMARY_FILE:-}" ]]; then
  printf '%s\n' "${summary}" > "${SUMMARY_FILE}"
fi
[[ "${status}" == "builds and passes its tests" ]]
