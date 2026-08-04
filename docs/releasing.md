# Releasing and Marketplace publication

Proficiency uses immutable semantic-version releases and a movable `v0` Action
tag.

## Prepare a draft

1. Merge the release preparation PR into `main`.
2. Run **Prepare release draft** from the Actions tab with a version such as
   `v0.2.1`.
3. Wait for the workflow to test the source, build Linux/macOS archives, attach
   checksums, and create an editable draft release.

The release remains editable because Marketplace publication requires a human
to select the listing options before immutable release publication.

## Publish to GitHub Marketplace

Open the draft release and:

1. Select **Publish this Action to the GitHub Marketplace**.
2. Accept the Marketplace developer agreement if prompted.
3. Choose **Continuous integration** as the primary category.
4. Choose **Monitoring** as the secondary category.
5. Confirm the root `action.yml` metadata preview.
6. Publish the release.

Publishing creates the immutable semantic-version tag and release.

## Update the major Action tag

After the release is published, point `v0` at the new immutable release commit:

```bash
git fetch origin --tags
git tag -f v0 v0.2.1
git push origin refs/tags/v0 --force
```

The full `v0.2.1` tag remains immutable. The intentionally movable `v0` tag
lets users receive compatible fixes with:

```yaml
uses: tuxerrante/proficiency@v0
```

Security-sensitive users can pin the full release commit SHA instead.
