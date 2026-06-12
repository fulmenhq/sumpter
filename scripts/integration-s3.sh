#!/bin/bash

# Sumpter S3 live-integration test runner
#
# Runs the `s3integration`-tagged tests (cloud read boundary, source-in) against
# a live S3-compatible endpoint. Two ways to get an endpoint, tried in order:
#
#   1. BYO (bring-your-own): export the standard contract — a URI + an S3 credential
#      reference — and the suite runs against it (moto, MinIO, Wasabi, R2, real S3).
#      https endpoints keep TLS enforced; http:// opts into insecure.
#
#        SUMPTER_TEST_S3_ENDPOINT   e.g. https://s3.us-east-1.amazonaws.com
#        SUMPTER_TEST_S3_BUCKET     a pre-created bucket
#        SUMPTER_TEST_S3_PROFILE    AWS profile (preferred; creds from ~/.aws, no
#                                   secret in env) — OR the literal pair below
#        SUMPTER_TEST_S3_KEY_ID     access key id   (used only when no profile)
#        SUMPTER_TEST_S3_SECRET     secret access key (used only when no profile)
#        SUMPTER_TEST_S3_REGION     optional, default us-east-1
#
#      Tip: keep these in a gitignored .envrc (direnv). Never commit endpoint,
#      bucket, profile, or credential values. See docs/sop/cicd-and-local-gates.md.
#
#   2. Self-provision: with no BYO endpoint, this script stands up an ephemeral
#      moto server in a cached Python venv, creates a bucket, runs, and tears it
#      down. Needs python3 on PATH (first run also needs network to pip-install
#      moto; the venv is cached under .cache/integration afterward).
#
# Escape hatch: SUMPTER_SKIP_S3_INTEGRATION=1 skips the suite entirely (exit 0)
# with a loud notice — for genuinely offline machines with no BYO endpoint. This
# is the documented way to pass `make pr-final` without the harness; see
# docs/sop/cicd-and-local-gates.md.
#
# Last Updated: June 12, 2026

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

TAG="s3integration"
VENV_DIR=".cache/integration/motovenv"
SELF_BUCKET="sumpter-itest"
MOTO_PORT="${SUMPTER_MOTO_PORT:-5005}"

run_suite() {
	echo -e "${BLUE}Running S3 integration suite (-tags ${TAG})...${NC}"
	go test -tags "${TAG}" -count=1 -timeout=300s ./...
}

# Escape hatch first.
if [ "${SUMPTER_SKIP_S3_INTEGRATION:-}" = "1" ]; then
	echo -e "${YELLOW}⚠️  SUMPTER_SKIP_S3_INTEGRATION=1 — skipping S3 live-integration suite.${NC}"
	echo -e "${YELLOW}   (Documented escape for offline machines; see docs/sop/cicd-and-local-gates.md.)${NC}"
	exit 0
fi

# Path 1: BYO endpoint.
if [ -n "${SUMPTER_TEST_S3_ENDPOINT:-}" ] && [ -n "${SUMPTER_TEST_S3_BUCKET:-}" ]; then
	echo -e "${BLUE}Using BYO S3 endpoint: ${SUMPTER_TEST_S3_ENDPOINT} (bucket ${SUMPTER_TEST_S3_BUCKET})${NC}"
	run_suite
	exit $?
fi

# Path 2: self-provision moto.
fail_no_harness() {
	echo -e "${RED}❌ Cannot run the S3 integration suite: ${1}${NC}" >&2
	echo -e "${RED}   Provide a BYO endpoint (SUMPTER_TEST_S3_ENDPOINT + SUMPTER_TEST_S3_BUCKET${NC}" >&2
	echo -e "${RED}   + SUMPTER_TEST_S3_KEY_ID/SECRET), or set SUMPTER_SKIP_S3_INTEGRATION=1 to skip.${NC}" >&2
	echo -e "${RED}   See docs/sop/cicd-and-local-gates.md.${NC}" >&2
	exit 1
}

if ! command -v python3 >/dev/null 2>&1; then
	fail_no_harness "no BYO endpoint and python3 is not on PATH for self-provisioning moto"
fi

if [ ! -x "${VENV_DIR}/bin/moto_server" ]; then
	echo -e "${BLUE}Bootstrapping moto into ${VENV_DIR} (first run only)...${NC}"
	python3 -m venv "${VENV_DIR}" || fail_no_harness "could not create the moto venv"
	"${VENV_DIR}/bin/pip" install --quiet 'moto[server]' ||
		fail_no_harness "could not pip-install moto[server] (offline?)"
fi

echo -e "${BLUE}Starting ephemeral moto on 127.0.0.1:${MOTO_PORT}...${NC}"
"${VENV_DIR}/bin/moto_server" -p "${MOTO_PORT}" >/tmp/sumpter-moto.log 2>&1 &
MOTO_PID=$!
# shellcheck disable=SC2329  # invoked indirectly via the EXIT trap below.
cleanup() { kill "${MOTO_PID}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# Health wait.
up=0
for _ in $(seq 1 40); do
	if curl -sf "http://127.0.0.1:${MOTO_PORT}/moto-api/" >/dev/null 2>&1; then
		up=1
		break
	fi
	sleep 0.25
done
[ "${up}" = "1" ] || fail_no_harness "moto did not become healthy (see /tmp/sumpter-moto.log)"

# Create the bucket via the venv's boto3.
"${VENV_DIR}/bin/python3" - "${MOTO_PORT}" "${SELF_BUCKET}" <<'PY'
import sys, boto3
port, bucket = sys.argv[1], sys.argv[2]
s3 = boto3.client("s3", endpoint_url=f"http://127.0.0.1:{port}",
                  aws_access_key_id="test", aws_secret_access_key="test",
                  region_name="us-east-1")
try:
    s3.create_bucket(Bucket=bucket)
except Exception:
    pass  # already exists
PY

export SUMPTER_TEST_S3_ENDPOINT="http://127.0.0.1:${MOTO_PORT}"
export SUMPTER_TEST_S3_BUCKET="${SELF_BUCKET}"
export SUMPTER_TEST_S3_KEY_ID="test"
export SUMPTER_TEST_S3_SECRET="test"
export SUMPTER_TEST_S3_REGION="us-east-1"

run_suite
result=$?
echo -e "${GREEN}✅ S3 integration suite finished (ephemeral moto).${NC}"
exit $result
