# GitHub Action

Run DeployIQ on every pull request. It posts one comment (updated on each
push) with the possible saving and the proposed Dockerfile, writes the same
report to the job summary, and can fail the check above a threshold.

## Minimal

```yaml
# .github/workflows/dockerfile.yml
name: dockerfile
on: pull_request
permissions:
  contents: read
  pull-requests: write   # to post the comment
jobs:
  deployiq:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: dhyaneshsiddhartha15/DeployIQ@v1
```

## Gate pull requests, with real sizes and a vulnerability scan

```yaml
      - uses: dhyaneshsiddhartha15/DeployIQ@v1
        with:
          fail-over: 30          # fail if the image could be 30%+ smaller
          measure: "true"        # docker build both images for real sizes
          scan-vulns: "true"     # Trivy scan of both images
          build-args: |
            GIT_TOKEN=${{ secrets.PRIVATE_MODULES_TOKEN }}
```

## Inputs

| Input | Default | |
| --- | --- | --- |
| `path` | `.` | directory to analyse (a service in a monorepo, for example) |
| `dockerfile` | | existing Dockerfile relative to `path`; default `./Dockerfile`, then `build/`, `docker/`, `deploy/` |
| `comment` | `true` | post/update the PR comment |
| `fail-over` | `0` | fail when the saving is at least this percent; `0` never fails |
| `measure` | `false` | build both images with Docker and report measured sizes |
| `scan-vulns` | `false` | Trivy-scan both images (implies `measure`) |
| `build-args` | | newline-separated `KEY=VALUE` passed to the builds |
| `fail-on-unsupported` | `false` | fail when the stack is not supported or DeployIQ declines to guess |
| `github-token` | `github.token` | token for the comment |

## Outputs

`saving-percent`, `before-mb`, `after-mb`, `exit-code`, `report` (path to the
JSON report). Measured values are used when `measure` is on.

## Monorepos

One step per service:

```yaml
    strategy:
      matrix:
        service: [services/api, services/web, services/worker]
    steps:
      - uses: actions/checkout@v4
      - uses: dhyaneshsiddhartha15/DeployIQ@v1
        with:
          path: ${{ matrix.service }}
          comment: "false"   # one comment per PR is enough; use the job summary
```

## Company settings

A `.doiq.yml` in the repository (or the organisation's default in the user
config dir) declares internal base images and mirrors — see
[`CONFIG.md`](CONFIG.md). The action picks it up automatically.
