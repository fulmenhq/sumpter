# Release Checklist

Standard checklist for sumpter releases to ensure consistency and quality.

## Pre-Release Phase

### Version Planning

- [ ] Feature briefs in productbook sumpter stream (`fulmenhq-productbook-internal/content/projmgmt/sumpter/`) marked done
- [ ] All planned features implemented and tested
- [ ] Breaking changes documented
- [ ] Migration guide written (if applicable)
- [ ] Version number decided (semantic versioning: MAJOR.MINOR.PATCH)

### Code Quality

- [ ] All tests passing: `make test`
- [ ] Code formatted: `make fmt`
- [ ] Lint checks clean: `make lint`
- [ ] Application builds: `make build`
- [ ] Manual smoke tests completed (against `dist/sumpter` after `make build`):
  - [ ] `./dist/sumpter version` — version + build metadata
  - [ ] `./dist/sumpter --help` — CLI surface intact
  - [ ] `./dist/sumpter doctor` — environment + dependency checks
  - [ ] `./dist/sumpter envinfo` — runtime introspection
  - [ ] `./dist/sumpter inspect --file <small-sample.xml>` — streaming inspect path
  - [ ] `./dist/sumpter validate --recipe <sample-recipe.yaml>` — recipe load path
  - [ ] `./dist/sumpter docs list` — embedded docs intact post-build

### Documentation

- [ ] `README.md` reviewed and updated
- [ ] Feature documentation added to `docs/` (if applicable)
- [ ] CLI help text accurate

### Dependencies

- [ ] `go.mod` dependencies reviewed
- [ ] Local replace directives removed (switch to GitHub releases)
- [ ] Dependency versions finalized
- [ ] `go mod tidy` executed
- [ ] No security vulnerabilities in dependencies

## Release Preparation

### Version Updates

- [ ] Update `VERSION` file (sumpter injects version via LDFLAGS — no `.fulmen/app.yaml` or `internal/buildinfo/VERSION` mirror to keep in sync)
- [ ] Version sanity check: `make release-guard-tag-version RELEASE_TAG=v<version>`
- [ ] Search for hardcoded version references (`grep -rE "0\\.1\\.3" --include=\"*.go\" --include=\"*.md\" --include=\"*.yaml\"`)

### Git Hygiene

- [ ] All changes committed
- [ ] Commit messages follow attribution standard
- [ ] No uncommitted changes: `git status` clean
- [ ] All commits have proper trailers
- [ ] Pre-push checks run: `make prepush`

### Final Validation

- [ ] Fresh clone test: Clone repo fresh, run `make build && make test`
- [ ] Integration tests pass
- [ ] Performance benchmarks acceptable (if applicable)

## Release Execution

### Release Artifacts & Signing

Follow the Fulmen "manifest-only" provenance pattern:

- Generate SHA256 + SHA512 manifests
- Sign manifests with minisign (primary) and optionally PGP
- Ship trust anchors (public keys) with the release

- [ ] Download CI-built artifacts and generate manifests:

  ```bash
  make release-clean
  make release-download RELEASE_TAG=v<version>
  make release-checksums
  make release-verify-checksums
  ```

- [ ] Sign manifests (minisign required; PGP optional):

  ```bash
  export RELEASE_TAG=v<version>
  export SUMPTER_MINISIGN_KEY=/path/to/sumpter.key
  export SUMPTER_MINISIGN_PUB=/path/to/sumpter.pub
  export SUMPTER_PGP_KEY_ID="security@fulmenhq.dev"   # optional
  export SUMPTER_GPG_HOMEDIR=/path/to/gnupg-fulmenhq # required if PGP_KEY_ID set

  make release-sign RELEASE_TAG=$RELEASE_TAG
  ```

- [ ] Export public keys: `make release-export-keys`
- [ ] Verify exported keys are public-only: `make release-verify-keys`
- [ ] Verify signatures: `make release-verify-signatures`
- [ ] Copy release notes: `make release-notes RELEASE_TAG=v<version>`
- [ ] Upload provenance assets: `make release-upload`

### Tagging

- [ ] Create annotated git tag: `git tag -a v<version> -m "Release v<version>"`
- [ ] Tag message includes brief release summary

### Publishing

- [ ] Push commits: `git push origin main`
- [ ] Push tag: `git push origin v<version>`
- [ ] Verify GitHub release appears
- [ ] Create GitHub Release notes

### Distribution

- [ ] Verify `go install github.com/fulmenhq/sumpter/cmd/sumpter@v<version>` works
- [ ] Test CLI commands work correctly

## Post-Release

### Communication

- [ ] Announce release in Mattermost `#repo-sumpter-ops` (on `org-fulmenhq`)
- [ ] Notify downstream consumer teams via the relevant Mattermost channel if integration patterns changed (DSL semantics, recipe schema additions, CLI surface changes)

### Housekeeping

- [ ] Update productbook sumpter stream (`content/projmgmt/sumpter/index.md`) — mark shipped SUM-NNN briefs as ✅ done
- [ ] Plan next version features in productbook

### Monitoring

- [ ] Monitor GitHub issues for release-related bugs

## Version-Specific Checklists

### For Major Releases (x.0.0)

- [ ] Breaking changes documented with upgrade guide
- [ ] Deprecation warnings added to old APIs
- [ ] Migration scripts provided (if complex changes)

### For Minor Releases (0.x.0)

- [ ] New features documented with examples
- [ ] Integration tests cover new functionality

### For Patch Releases (0.0.x)

- [ ] Bug fixes documented with issue references
- [ ] Regression tests added for fixed bugs
- [ ] Security patches highlighted (if applicable)
- [ ] No new features or breaking changes

## Emergency Hotfix Process

### Hotfix Identification

- [ ] Critical bug or security issue identified
- [ ] Severity assessed (production-impacting?)
- [ ] Hotfix branch created: `hotfix/v<version>`

### Rapid Development

- [ ] Minimal fix implemented
- [ ] Tests added for regression prevention
- [ ] Code review expedited (but not skipped)
- [ ] Quality gates still enforced (no shortcuts)

### Hotfix Release

- [ ] Version bumped (patch level)
- [ ] Tag pushed immediately after merge
- [ ] Users notified of critical update

### Post-Hotfix

- [ ] Root cause analysis documented
- [ ] Process improvements identified

## Notes

- This checklist may evolve with project maturity
- Some items may not apply to all releases (use judgment)
- Prioritize quality over speed - never skip tests or code review
- When in doubt, consult @3leapsdave before proceeding
