---
name: proficiency-release
description: Prepare, publish, and verify Proficiency CLI, Go module, and GitHub Marketplace releases. Use for version bumps, release drafts, Marketplace publication, or major Action tag updates.
license: Apache-2.0
compatibility: Requires bash, git, gh, go, curl, jq, and tar.
---

# Proficiency release

Use the repository scripts as the source of truth. Do not duplicate archive
logic in ad-hoc shell commands.

## Before implementation

1. Read `docs/releasing.md`, `CHANGELOG.md`, `action.yml`, and
   `.github/workflows/release.yml`.
2. Inspect existing tags and releases:

   ```bash
   git tag --sort=-version:refname
   gh release list
   ```

3. If the intended release already exists and is immutable, never rewrite or
   replace it. Prepare a patch release.
4. Confirm the repository license is recognized:

   ```bash
   gh api repos/tuxerrante/proficiency --jq '.license.spdx_id'
   ```

## Prepare the release

1. Keep feature work separate from the release-only changelog PR.
2. Use semantic versions in `vMAJOR.MINOR.PATCH` form.
3. Update `action.yml`'s default binary version whenever the movable major
   Action tag will advance.
4. Build and verify assets locally with the same scripts used by CI:

   ```bash
   temp_dir="$(mktemp -d)"
   scripts/release/build-assets.sh v0.2.2 "$temp_dir"
   scripts/release/verify-assets.sh v0.2.2 "$temp_dir"
   ```

5. Run `make coverage`, `make e2e`, `make container-test`, and
   `make external-test`.
6. Request Copilot review and triage both inline and suppressed findings.

## Draft and publish

1. Merge the release preparation PR.
2. Run **Prepare release draft** with the chosen version.
3. Verify that the draft contains four archives and `checksums.txt`.
4. Stop for human approval. Marketplace publication requires the UI,
   developer agreement, categories, and 2FA.
5. In the draft select:
   - **Publish this Action to the GitHub Marketplace**
   - Primary category: **Continuous integration**
   - Secondary category: **Monitoring**
6. Publish the draft. Draft preparation does not create the Git tag; publishing
   creates the semantic-version tag and makes the release immutable.
7. Point the movable major tag at the immutable release commit:

   ```bash
   git tag -f v0 v0.2.2
   git push origin refs/tags/v0 --force
   ```

8. Verify the public release:

   ```bash
   scripts/release/verify-published.sh v0.2.2
   ```

## Invariants learned from v0.2

- A published immutable release cannot be retrofitted for Marketplace; use a
  new patch release and prepare a draft first.
- `go install` does not receive release-workflow ldflags. Tagged installs must
  derive their version from Go build information.
- The Action's movable `@v0` ref and its downloaded binary version are separate
  contracts. `action.yml` must default to a full immutable release version.
- Never assume draft tag behavior. Verify the remote tag before and after
  publication.
- Release archives, checksums, host binary version, tagged `go install`,
  Marketplace listing, and the movable major tag are all release gates.
