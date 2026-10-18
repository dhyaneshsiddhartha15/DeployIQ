# Company settings: `.doiq.yml`

Optional. With no file, `doiq` behaves exactly as documented in the README.
Companies use it to tell `doiq` what it cannot infer: their own base images,
their registry mirrors and their internal package registries.

## Where it is read from

1. `-config <file>` when given
2. `<repo>/.doiq.yml`
3. the organisation default, `<user config dir>/doiq/org.yml`
   (`~/.config/doiq/org.yml` on Linux, `%AppData%\doiq\org.yml` on Windows,
   `~/Library/Application Support/doiq/org.yml` on macOS)

The repo file and the organisation default are merged; the repo file wins. Put
company-wide settings in the org file (or bake it into your CI runner image)
and let teams override single entries. Unknown keys are errors, so a typo is
caught instead of silently ignored.

## Example

```yaml
# Internal base images. Without this, doiq refuses images it cannot
# classify (exit 4) rather than guess what they contain.
baseImages:
  - match: "registry.acme.com/base/node:*"   # glob on the full reference
    family: node        # node | python | golang | java | debian | ubuntu | alpine | rhel | distroless
    variant: slim       # full | slim | alpine — decides apt vs apk
    version: "20"       # language version, when the tag does not say it
    sizeMB: 260         # for the size estimate
  - match: "registry.acme.com/base/ubi9:*"
    family: rhel
    sizeMB: 210

# Every public image doiq generates is pulled through these mirrors.
mirrors:
  docker.io: registry.acme.com/dockerhub   # node:22-alpine -> registry.acme.com/dockerhub/library/node:22-alpine
  gcr.io: registry.acme.com/gcr            # distroless
mirrorKeepLibrary: true   # keep Docker Hub's library/ segment (Artifactory, Harbor: yes)

# Internal package registries, configured in the build stage only.
packages:
  npm: https://npm.acme.com/repository/npm/      # NPM_CONFIG_REGISTRY (+ Yarn Berry)
  pypi: https://pypi.acme.com/simple/            # PIP_INDEX_URL
  goproxy: https://goproxy.acme.com,direct       # GOPROXY
  maven: https://maven.acme.com/repository/central/  # Maven Central mirror
```

## What changes with a declared base image

- The image is **kept** in both stages of the generated Dockerfile. Companies
  mandate their base images for patching and compliance; the saving comes from
  the multi-stage build, dropped build tools and pruned dependencies, not from
  swapping the base.
- Package installs use the declared variant's package manager (apt, apk, or
  dnf/microdnf for `rhel`).
- The estimate uses `sizeMB`.

Registry and package mirrors never reach the runtime stage, and nothing in
this file is a secret: credentials for mirrors belong in the build
environment (`--build-arg`, `.npmrc` mounted as a BuildKit secret), not here.
