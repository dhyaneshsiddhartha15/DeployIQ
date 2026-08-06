# Test fixtures

Real sample repositories the CLI is run against in CI. This directory backs
what Phase 10.2 calls **the single most important test**:

> Does the generated Dockerfile actually build, for every supported stack,
> every time?

Nothing ships if that fails, no exceptions. It is the check that protects the
product's core trust property (Phase 0.5.1: zero critical build-breaking bugs),
and it is why a static assertion on the generated text is not enough — the
suite runs a real `docker build` on the output.

## Naming

`<stack>-<scenario>/`, per Phase 4.4. For example:

```
testdata/fixtures/node-express-basic/
testdata/fixtures/go-module-nodockerfile/
testdata/fixtures/python-poetry-multistage/
```

## What belongs in a fixture

A minimal but genuinely buildable application. Enough to exercise the detector
and one rule path, and no more — every fixture is built in CI on every commit,
so size is wall-clock cost on every pull request.

A fixture without a Dockerfile is a valid and important case: Phase 1.4
requires the tool to infer one from stack conventions rather than demanding an
existing file to optimize.

## The rule that governs this directory

Phase 1.5, verbatim in effect: **a stack may not be advertised as supported
until it has fixture coverage here.** Documentation claims must match what is
actually tested. Adding a stack to `constants.SupportedStacks` without adding a
fixture is the change that breaks that rule.

Empty until Phase 1 of the build plan, which lands the detector these fixtures
exercise.
