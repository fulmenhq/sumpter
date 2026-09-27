#!/usr/bin/env bash

set -euo pipefail

# Verify release checksum-manifest signatures. Fails closed.
#
# Usage: verify-release-signatures.sh <dir> <binary-name>
#
# Expected set (the sign step always signs both manifests per family):
#   - SHA256SUMS must exist.
#   - A family is present when any of its signature files exist:
#       minisign: SHA256SUMS.minisig, SHA512SUMS.minisig
#       PGP:      SHA256SUMS.asc,     SHA512SUMS.asc
#   - For each present family, every existing manifest needs its signature,
#     and every signature needs its manifest.
#   - At least one family must be present.
#
# Keys (exported artifacts in <dir>, never an ambient keyring):
#   minisign: <binary-name>-minisign.pub
#   PGP:      <binary-name>-release-signing-key.asc, imported into a fresh
#             temporary keyring that must hold exactly one primary key
#
# Trust boundary: this checks that the release artifacts are consistent with
# the exported keys shipped beside them. It does not independently
# authenticate those keys; replacing the whole directory, keys included,
# defeats it. Key authenticity is established by the release ceremony
# (keys exported on the signing machine, release-verify-keys, and publish-time
# provenance). Release tag signing (release-tag.sh) uses the operator's
# isolated homedir instead; the two boundaries are deliberately different.
#
# Success lines print only after every check has passed.

DIR=${1:?'usage: verify-release-signatures.sh <dir> <binary-name>'}
NAME=${2:?'usage: verify-release-signatures.sh <dir> <binary-name>'}

MANIFESTS=(SHA256SUMS SHA512SUMS)

die() {
	echo "error: $*" >&2
	exit 1
}

family_present() {
	local ext=$1 manifest
	for manifest in "${MANIFESTS[@]}"; do
		if [ -e "${DIR}/${manifest}.${ext}" ]; then
			return 0
		fi
	done
	return 1
}

# Every existing manifest has its signature and every signature its manifest.
check_family_set() {
	local ext=$1 label=$2 manifest
	for manifest in "${MANIFESTS[@]}"; do
		if [ -e "${DIR}/${manifest}" ] && [ ! -e "${DIR}/${manifest}.${ext}" ]; then
			die "${label}: ${manifest}.${ext} is missing but ${manifest} is present"
		fi
		if [ -e "${DIR}/${manifest}.${ext}" ] && [ ! -e "${DIR}/${manifest}" ]; then
			die "${label}: ${manifest}.${ext} is present but ${manifest} is missing"
		fi
	done
}

verify_minisign() {
	local pub="${DIR}/${NAME}-minisign.pub" manifest
	if [ ! -f "${pub}" ]; then
		die "minisign: public key ${NAME}-minisign.pub not found; run 'make release-export-keys' first"
	fi
	if ! command -v minisign >/dev/null 2>&1; then
		die "minisign: minisign not found in PATH"
	fi
	for manifest in "${MANIFESTS[@]}"; do
		[ -e "${DIR}/${manifest}" ] || continue
		echo "verifying ${manifest}.minisig"
		if ! minisign -V -p "${pub}" -m "${DIR}/${manifest}" -x "${DIR}/${manifest}.minisig"; then
			die "minisign: signature verification failed for ${manifest}"
		fi
	done
}

GPG_HOME=""

cleanup() {
	if [ -n "${GPG_HOME}" ] && [ -d "${GPG_HOME}" ]; then
		if command -v gpgconf >/dev/null 2>&1; then
			gpgconf --homedir "${GPG_HOME}" --kill all >/dev/null 2>&1 || true
		fi
		rm -rf "${GPG_HOME}"
	fi
}

verify_pgp() {
	local key="${DIR}/${NAME}-release-signing-key.asc" listing primaries primary manifest
	if [ ! -f "${key}" ]; then
		die "PGP: public key ${NAME}-release-signing-key.asc not found; run 'make release-export-keys' first"
	fi
	if ! command -v gpg >/dev/null 2>&1; then
		die "PGP: gpg not found in PATH"
	fi

	umask 077
	GPG_HOME=$(mktemp -d)
	trap cleanup EXIT

	if ! gpg --homedir "${GPG_HOME}" --batch --quiet --import "${key}"; then
		die "PGP: cannot import ${NAME}-release-signing-key.asc"
	fi
	if ! listing=$(gpg --homedir "${GPG_HOME}" --batch --with-colons --fixed-list-mode --list-keys); then
		die "PGP: cannot list the imported key"
	fi
	primaries=$(printf '%s\n' "${listing}" | awk -F: '
		$1 == "pub" { want = 1; next }
		$1 == "fpr" && want { want = 0; print $10 }
	')
	if [ "$(printf '%s' "${primaries}" | grep -c . || true)" -ne 1 ]; then
		die "PGP: ${NAME}-release-signing-key.asc must contain exactly one primary key"
	fi
	primary=${primaries}

	local status rc goodsig validsig bad signed_primary
	for manifest in "${MANIFESTS[@]}"; do
		[ -e "${DIR}/${manifest}" ] || continue
		echo "verifying ${manifest}.asc"
		rc=0
		status=$(gpg --homedir "${GPG_HOME}" --batch --status-fd 1 \
			--verify "${DIR}/${manifest}.asc" "${DIR}/${manifest}") || rc=$?
		if [ "${rc}" -ne 0 ]; then
			die "PGP: signature verification failed for ${manifest} (exit ${rc})"
		fi
		goodsig=$(printf '%s\n' "${status}" | grep -c '^\[GNUPG:\] GOODSIG ' || true)
		validsig=$(printf '%s\n' "${status}" | grep -c '^\[GNUPG:\] VALIDSIG ' || true)
		bad=$(printf '%s\n' "${status}" | awk '$1 == "[GNUPG:]" && $2 ~ /^(BADSIG|ERRSIG|EXPSIG|EXPKEYSIG|REVKEYSIG)$/ { printf "%s ", $2 }')
		bad=${bad% }
		if [ "${goodsig}" -ne 1 ] || [ "${validsig}" -ne 1 ] || [ -n "${bad}" ]; then
			die "PGP: ${manifest} does not carry a single good signature (GOODSIG=${goodsig} VALIDSIG=${validsig} rejected=${bad:-none})"
		fi
		signed_primary=$(printf '%s\n' "${status}" | awk '$2 == "VALIDSIG" { print $NF }')
		if [ "${signed_primary}" != "${primary}" ]; then
			die "PGP: ${manifest} was signed under ${signed_primary}, not the exported key ${primary}"
		fi
	done
}

if [ ! -d "${DIR}" ]; then
	die "directory ${DIR} not found"
fi
if [ ! -e "${DIR}/SHA256SUMS" ]; then
	die "SHA256SUMS not found in ${DIR}"
fi

has_minisign=false
has_pgp=false
if family_present minisig; then
	has_minisign=true
	check_family_set minisig minisign
fi
if family_present asc; then
	has_pgp=true
	check_family_set asc PGP
fi
if [ "${has_minisign}" = false ] && [ "${has_pgp}" = false ]; then
	die "no signatures found to verify in ${DIR}"
fi

if [ "${has_minisign}" = true ]; then
	verify_minisign
fi
if [ "${has_pgp}" = true ]; then
	verify_pgp
fi

if [ "${has_minisign}" = true ]; then
	echo "✅ Minisign signatures verified"
fi
if [ "${has_pgp}" = true ]; then
	echo "✅ PGP signatures verified"
fi
