# Releasing aflock-tui

## How releases work

Releases are automated via [GoReleaser](https://goreleaser.com/) + GitHub Actions.

When you push a tag, the workflow:
1. Builds binaries for linux/darwin/windows (amd64 + arm64)
2. Signs all artifacts with [cosign](https://github.com/sigstore/cosign) (keyless, GitHub OIDC)
3. Generates SBOMs with [syft](https://github.com/anchore/syft) (SPDX format)
4. Creates a GitHub release with all assets

## Creating a release

```bash
# 1. Make sure everything is committed and pushed
git status  # should be clean
git push

# 2. Tag the release
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0

# 3. Wait for GitHub Actions to build (~2 minutes)
# Watch at: https://github.com/manzil-infinity180/aflock-tui/actions

# 4. Verify the release
gh release view v0.2.0
```

That's it. GoReleaser handles everything else.

## Version numbering

Follow [semver](https://semver.org/):
- `v0.x.y` — pre-1.0, breaking changes allowed
- Bump patch (`v0.1.1`) for bug fixes
- Bump minor (`v0.2.0`) for new features
- Bump major (`v1.0.0`) when stable

## Verifying a release

Anyone can verify the cosign signature:

```bash
# Download
gh release download v0.1.0 --repo manzil-infinity180/aflock-tui --pattern "*darwin_arm64*"

# Verify
cosign verify-blob \
  --bundle aflock-tui_0.1.0_darwin_arm64.tar.gz.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp "github.com/manzil-infinity180/aflock-tui" \
  aflock-tui_0.1.0_darwin_arm64.tar.gz
```

## Install script

Users can install with one command:

```bash
bash <(curl -s https://raw.githubusercontent.com/manzil-infinity180/aflock-tui/main/install.sh)
```

This downloads the latest release, verifies the checksum, and installs to `/usr/local/bin`.

Custom install directory:
```bash
bash <(curl -s https://raw.githubusercontent.com/manzil-infinity180/aflock-tui/main/install.sh) ~/.local/bin
```

## Files involved

| File | Purpose |
|------|---------|
| `.goreleaser.yaml` | GoReleaser config (builds, signing, SBOMs, changelog) |
| `.github/workflows/release.yml` | GitHub Action triggered on tag push |
| `install.sh` | One-line install script for users |
| `RELEASING.md` | This document |

## Re-doing a release

If a release has issues:

```bash
# Delete the release and tag
gh release delete v0.x.y --yes
git push origin --delete v0.x.y
git tag -d v0.x.y

# Fix the issue, commit, push
git add . && git commit -m "fix: ..." && git push

# Re-tag and push
git tag -a v0.x.y -m "v0.x.y"
git push origin v0.x.y
```

## Testing locally

Test GoReleaser without publishing:

```bash
goreleaser release --snapshot --clean
ls dist/
```

## What each release asset is

```
aflock-tui_0.1.0_darwin_arm64.tar.gz                  # binary archive
aflock-tui_0.1.0_darwin_arm64.tar.gz.sigstore.json     # cosign signature (verify with cosign)
aflock-tui_0.1.0_darwin_arm64.tar.gz.sbom.json          # SPDX SBOM (inspect with syft/grype)
aflock-tui_0.1.0_darwin_arm64.tar.gz.sbom.json.sigstore.json  # signed SBOM
aflock-tui_0.1.0_checksums.txt                          # SHA256 checksums for all archives
aflock-tui_0.1.0_checksums.txt.sigstore.json            # signed checksums
```
