#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONTRACT_FILE="${SUMPTER_TOOLCHAIN_CONTRACT:-${ROOT_DIR}/config/toolchain.env}"

if [[ ! -f "${CONTRACT_FILE}" ]]; then
	echo "error: toolchain contract not found: ${CONTRACT_FILE}" >&2
	exit 2
fi

# shellcheck disable=SC1090
source "${CONTRACT_FILE}"

: "${SUMPTER_GO_VERSION:?missing SUMPTER_GO_VERSION in ${CONTRACT_FILE}}"
: "${SUMPTER_GOLANGCI_LINT_VERSION:?missing SUMPTER_GOLANGCI_LINT_VERSION in ${CONTRACT_FILE}}"
: "${SUMPTER_EXPECTED_GOFLAGS:=}"

EXPECTED_GOLANGCI_LINT_VERSION="${SUMPTER_GOLANGCI_LINT_VERSION#v}"

die() {
	echo "error: $*" >&2
	exit 1
}

info() {
	echo "toolchain-contract: $*"
}

go_bin_dir() {
	local gobin
	gobin="$(GOTOOLCHAIN=local go env GOBIN 2>/dev/null || true)"
	if [[ -n "${gobin}" ]]; then
		printf "%s\n" "${gobin}"
	else
		printf "%s/bin\n" "$(GOTOOLCHAIN=local go env GOPATH)"
	fi
}

golangci_lint_bin() {
	local candidate
	if command -v go >/dev/null 2>&1; then
		candidate="$(go_bin_dir)/golangci-lint"
		if [[ -x "${candidate}" ]]; then
			printf "%s\n" "${candidate}"
			return 0
		fi
	fi

	if command -v golangci-lint >/dev/null 2>&1; then
		command -v golangci-lint
		return 0
	fi

	return 1
}

golangci_lint_version() {
	local bin="$1"
	"${bin}" --version | sed -E 's/.* version v?([0-9]+\.[0-9]+\.[0-9]+).*/\1/'
}

check_go() {
	if ! command -v go >/dev/null 2>&1; then
		die "go is not installed or not in PATH; install Go ${SUMPTER_GO_VERSION}"
	fi

	local actual_go
	actual_go="$(go version | awk '{print $3}' | sed 's/^go//')"
	if [[ "${actual_go}" != "${SUMPTER_GO_VERSION}" ]]; then
		die "Go toolchain mismatch: expected go${SUMPTER_GO_VERSION}, found go${actual_go} ($(command -v go)). Set up Go ${SUMPTER_GO_VERSION}; with toolchain-managed Go, check 'go env GOTOOLCHAIN'."
	fi

	local go_mod_toolchain
	go_mod_toolchain="$(awk '$1 == "toolchain" { print $2 }' "${ROOT_DIR}/go.mod")"
	if [[ "${go_mod_toolchain}" != "go${SUMPTER_GO_VERSION}" ]]; then
		die "go.mod toolchain mismatch: expected go${SUMPTER_GO_VERSION} from ${CONTRACT_FILE}, found ${go_mod_toolchain:-<missing>}"
	fi

	local actual_goflags
	actual_goflags="$(GOTOOLCHAIN=local go env GOFLAGS)"
	if [[ "${actual_goflags}" != "${SUMPTER_EXPECTED_GOFLAGS}" ]]; then
		die "GOFLAGS mismatch: expected '${SUMPTER_EXPECTED_GOFLAGS}', found '${actual_goflags}'. Clear GOFLAGS or update ${CONTRACT_FILE} if CI intentionally changes."
	fi

	info "go ok: go${actual_go}; GOFLAGS='${actual_goflags}'"
}

install_golangci_lint() {
	check_go
	local target_dir
	target_dir="$(go_bin_dir)"
	mkdir -p "${target_dir}"
	info "installing golangci-lint ${SUMPTER_GOLANGCI_LINT_VERSION} to ${target_dir}"
	GOBIN="${target_dir}" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${SUMPTER_GOLANGCI_LINT_VERSION}"
	check_golangci_lint
}

check_golangci_lint() {
	local bin
	if ! bin="$(golangci_lint_bin)"; then
		die "golangci-lint is not installed. Install the pinned CI version with: make install-golangci-lint"
	fi

	local actual_linter
	actual_linter="$(golangci_lint_version "${bin}")"
	if [[ "${actual_linter}" != "${EXPECTED_GOLANGCI_LINT_VERSION}" ]]; then
		die "golangci-lint mismatch: expected ${SUMPTER_GOLANGCI_LINT_VERSION}, found v${actual_linter} at ${bin}. Install the pinned CI version with: make install-golangci-lint"
	fi

	info "golangci-lint ok: v${actual_linter} (${bin})"
}

yaml_tool_field() {
	local tool="$1"
	local field="$2"
	awk -v tool="${tool}" -v field="${field}" '
		$0 == "tools:" { in_tools_root = 1; next }
		!in_tools_root { next }
		$0 == "  " tool ":" { in_tool = 1; next }
		in_tool && /^  [A-Za-z0-9_-]+:/ { exit }
		in_tool && $1 == field ":" {
			print $2
			exit
		}
	' "${ROOT_DIR}/.goneat/tools.yaml"
}

check_goneat_tools_contract() {
	local expected_linter_package="github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${SUMPTER_GOLANGCI_LINT_VERSION}"
	local actual_linter_package actual_linter_min actual_linter_recommended actual_go_min actual_go_recommended

	actual_linter_package="$(yaml_tool_field golangci-lint install_package)"
	actual_linter_min="$(yaml_tool_field golangci-lint minimum_version)"
	actual_linter_recommended="$(yaml_tool_field golangci-lint recommended_version)"
	actual_go_min="$(yaml_tool_field go minimum_version)"
	actual_go_recommended="$(yaml_tool_field go recommended_version)"

	if [[ "${actual_linter_package}" != "${expected_linter_package}" ]]; then
		die ".goneat/tools.yaml golangci-lint install_package drift: expected ${expected_linter_package}, found ${actual_linter_package:-<missing>}"
	fi

	if [[ "${actual_linter_min}" != "${EXPECTED_GOLANGCI_LINT_VERSION}" || "${actual_linter_recommended}" != "${EXPECTED_GOLANGCI_LINT_VERSION}" ]]; then
		die ".goneat/tools.yaml golangci-lint version drift: expected minimum/recommended ${EXPECTED_GOLANGCI_LINT_VERSION}, found minimum=${actual_linter_min:-<missing>} recommended=${actual_linter_recommended:-<missing>}"
	fi

	if [[ "${actual_go_min}" != "${SUMPTER_GO_VERSION}" || "${actual_go_recommended}" != "${SUMPTER_GO_VERSION}" ]]; then
		die ".goneat/tools.yaml Go version drift: expected minimum/recommended ${SUMPTER_GO_VERSION}, found minimum=${actual_go_min:-<missing>} recommended=${actual_go_recommended:-<missing>}"
	fi

	info ".goneat/tools.yaml ok: Go ${SUMPTER_GO_VERSION}; golangci-lint ${SUMPTER_GOLANGCI_LINT_VERSION}"
}

check_all() {
	check_go
	check_goneat_tools_contract
	check_golangci_lint
}

lint() {
	check_go
	check_goneat_tools_contract
	local bin
	if ! bin="$(golangci_lint_bin)"; then
		install_golangci_lint
		bin="$(golangci_lint_bin)"
	fi
	check_golangci_lint
	exec "${bin}" run "$@"
}

staticcheck_probe() {
	check_all
	local bin
	bin="$(golangci_lint_bin)"
	local tmp
	tmp="$(mktemp -d)"
	trap "rm -rf '${tmp}'" EXIT
	mkdir -p "${tmp}/go-cache" "${tmp}/golangci-cache"

	cat >"${tmp}/go.mod" <<EOF
module example.com/sumpter-staticcheck-probe

go 1.26.0

toolchain go${SUMPTER_GO_VERSION}
EOF

	cat >"${tmp}/.golangci.yml" <<'EOF'
version: "2"
run:
  tests: true
linters:
  default: none
  enable:
    - staticcheck
EOF

	cat >"${tmp}/probe.go" <<'EOF'
package probe

func StaticcheckSA5011Probe(value *int) int {
	if value == nil {
		println("nil pointer")
	}
	return *value
}
EOF

	local output status
	set +e
	output="$(cd "${tmp}" && GOCACHE="${tmp}/go-cache" GOLANGCI_LINT_CACHE="${tmp}/golangci-cache" "${bin}" run ./... 2>&1)"
	status=$?
	set -e

	if [[ "${status}" -eq 0 || "${output}" != *"SA5011"* ]]; then
		printf "%s\n" "${output}" >&2
		die "staticcheck probe did not fail with SA5011; local lint no longer matches the SUM-035 false-green regression class"
	fi

	info "staticcheck probe ok: pinned lint reports SA5011"
}

usage() {
	cat <<EOF
usage: scripts/toolchain-contract.sh <command> [args...]

Commands:
  check-go                Verify Go version, go.mod toolchain, and GOFLAGS.
  check-goneat-tools      Verify goneat tool metadata mirrors the contract.
  check-golangci-lint     Verify golangci-lint version.
  check                   Verify the full CI/local toolchain contract.
  install-golangci-lint   Install the pinned golangci-lint version.
  lint [args...]          Verify the contract, then run golangci-lint.
  staticcheck-probe       Verify the pinned lint catches a synthetic SA5011 case.
EOF
}

cmd="${1:-}"
case "${cmd}" in
check-go)
	check_go
	;;
check-golangci-lint)
	check_golangci_lint
	;;
check-goneat-tools)
	check_goneat_tools_contract
	;;
check)
	check_all
	;;
install-golangci-lint)
	install_golangci_lint
	;;
lint)
	shift
	lint "$@"
	;;
staticcheck-probe)
	staticcheck_probe
	;;
"" | -h | --help | help)
	usage
	;;
*)
	usage >&2
	exit 2
	;;
esac
