# CI checks, stages and timing

The `Checks` workflow runs on pull requests and pushes to `main`. It retains the
Go race tests, three-platform bridge/distribution checks, fresh offline
installation checks and two-build reproducibility comparison. No hardware is
opened by CI.

## Job layout

The workflow has nine jobs after matrix expansion, compared with seven before
Web unit tests were separated:

- Three offline Go jobs: Ubuntu with Go 1.22 and 1.27, and macOS with Go 1.27.
  Each verifies dependencies, runs race tests with cross-package coverage,
  runs vet and builds the CLI.
- Two Web UI unit-test jobs: Ubuntu and macOS with Node.js 22. They run
  independently of the Go jobs and do not install Go. The former duplicate
  Linux Web run for a second Go version is removed; Linux and macOS coverage
  remain.
- Three bridge/distribution jobs: Linux, macOS and Windows. They build with
  `CGO_ENABLED=0`, run bridge/CLI/Web integration tests, build the native archive
  twice and compare its bytes, then verify a fresh offline installation.
- One Chromium job on Linux for browser/server restart, persistence,
  capabilities and reader/editor workflows.

Web unit tests no longer wait for Go tests to finish. The overall result still
waits for every required job. The Go, bridge and browser platform/version
coverage is unchanged. Large-input tests, recovery regressions and sanitizer
checks are unchanged.

Repository rules that require specific check names should include the two new
Web UI checks as well as the existing checks. This change does not modify branch
protection or repository rules automatically.

## Local commands

The default local command still runs the complete sequence:

```sh
npm ci --ignore-scripts
go mod download
./scripts/check.sh
```

The script also accepts one stage at a time:

```sh
./scripts/check.sh deps
./scripts/check.sh go
./scripts/check.sh vet
./scripts/check.sh webui
./scripts/check.sh build
```

`all` is equivalent to no argument. Stages run in the order shown above for the
complete sequence. `deps`, `go`, `vet` and `build` require Go; `webui` requires
Node.js, npm and the installed test dependencies, but does not require Go or a
populated Go module cache. Install the appropriate dependencies before invoking
a standalone stage. Local `all` also checks Node/jsdom availability after
dependency verification and before running the Go tests, preserving early
failure when Web test tools are missing.

The Go test stage retains `-race -count=1 -p 1 -coverpkg=./...` and writes the
coverage profile. Dependency verification, tests, vet and builds retain
`-mod=readonly`. The script disables Go module downloads and hardware-test
opt-ins. C recovery fixtures still require the documented compiler/development
libraries; CI requires those tests rather than skipping them.

## Tests for CI configuration changes

After `npm ci --ignore-scripts`, run the fast configuration tests locally:

```sh
python3 -m unittest discover -s scripts -p test_check.py -v
npm run test:ci
```

The Python tests use isolated command stubs to check stage dispatch, tool
requirements, exit codes and interruption reports. The Node tests parse the
workflow and composite action as YAML and check cancellation boundaries,
independent Web jobs, platform coverage, cache isolation and distribution gates.
They do not replace the real Go, Web, bridge or installation tests.

## Stage reports

Each stage prints its start and completion, records whole-second elapsed wall
time and appends a row to `reports/check-timings.tsv`:

```text
stage\tduration_seconds\tstatus
```

The actual file uses tab separators. Status is `success`, `failure` or
`interrupted`. A failed command keeps its exit code and stops the complete
sequence; a handled interruption is not recorded as success. Timing is a coarse
wall-clock measurement, not a CPU benchmark.

When `GITHUB_STEP_SUMMARY` is set, the script appends the same stage result to
the Actions summary. CI invokes the stages in separately named steps and
uploads timing/log reports even when a preceding check fails. Hard termination
or cancellation can prevent a report upload; it does not make a canceled check
successful.

The existing `reports/go-mod-verify.txt`, `unit-tests.txt`,
`coverage.out`, `coverage-functions.txt`, `go-vet.txt` and `webui-tests.txt`
remain available for their corresponding stages. The TSV appends across stage
invocations in the same job. Local repeated runs also append; archive the timing
file or use a fresh checkout when comparing separate runs.

## Go build caches

The local Go setup action caches only the paths reported by `go env GOCACHE`
and `go env GOMODCACHE`. Keys separate the operating system, architecture,
resolved Go toolchain version and job scope. Offline race/coverage, bridge
no-cgo and browser no-cgo jobs use different scopes. Dependency hashes and the
commit revision form the remainder of the key; restore prefixes can reuse an
earlier compatible cache and let Go rebuild changed inputs.

The built-in cache in `actions/setup-go` is disabled inside the local action to
avoid a second cache covering the same paths. The explicit cache action remains
enabled. A miss must still produce a valid build and full test run.

`-count=1` is unchanged, so successful Go test results are not reused in place
of execution. Reports, distribution archives, installed test environments and
runtime-generated C fixtures are not cached. In particular, this cache does not
replace sanitizer fixture compilation or the fresh installation used by the
distribution smoke test.

## Pull-request cancellation

In-progress pull-request runs share a cancellation group only with runs of the
same pull request and workflow. A replacement run cancels the earlier run. The
key contains the workflow, event and pull-request number; other pull requests
use different groups. Pushes to `main` use their individual run IDs and do not
cancel one another or pull-request checks.

## Comparing performance

Compare several successful runs with the same test coverage, recording the
commit, runner/Go versions, cache hit/miss and individual stage durations. A
first run with the new cache scope may be cold. Faster later runs alone do not
prove a code change caused the improvement.

For context, the previous workflow took 11 minutes 38 seconds for
[PR #24](https://github.com/soulteary/chameleon-desk/actions/runs/43810201118)
and 9 minutes 16 seconds for its
[main build](https://github.com/soulteary/chameleon-desk/actions/runs/43811902281).
Those are pre-change observations, not a promised result for this layout.
Splitting Web unit tests removes their execution from the serial Go path;
actual end-to-end improvement depends on the remaining stages and runner load.
