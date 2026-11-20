# Git launches during workspace status

At 200 repositories, the final build completed status in **634.3 ms with one job** and **192.9 ms with four jobs**, down from 724.6 ms and 221.2 ms on the same Linux fixtures. Five paired samples at each job count measured the first observation in a fresh process. At four jobs, the first successful callback moved from 11.00 to 10.29 ms. Both builds returned the same 167 readable worktrees and 33 missing-worktree diagnostics.

## Final-build comparisons

Two series compare the original baseline (`9fa2c94`) with the merged inspection change (`ac81695`, its immediate child) and with the final integrated build (`9a1f331`). Each candidate has its own five paired baseline samples for every fixture and job count. Both candidates include the cancellation fix. The integrated build also includes later changes to catalog loading.

Each cell gives median milliseconds with minimum–maximum in parentheses. The first clock ends on the first callback with a Git observation. The full clock ends when `StatusWithProgress` returns all ordered items. Setup, client initialization, result validation and tracing are outside the timed interval.

| Candidate | Repos | Jobs | First callback before | First callback after | Full status before | Full status after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Merged inspection | 12 | 1 | 6.33 (5.75–6.85) | 6.84 (5.29–7.14) | 47.26 (45.50–56.70) | 41.63 (40.99–44.92) |
| Merged inspection | 12 | 4 | 6.91 (6.27–8.05) | 6.19 (5.51–13.23) | 16.19 (14.78–17.55) | 14.38 (13.57–20.63) |
| Merged inspection | 60 | 1 | 6.78 (6.52–7.27) | 6.41 (6.11–7.17) | 217.3 (216.5–241.9) | 189.1 (186.6–195.6) |
| Merged inspection | 60 | 4 | 7.42 (6.80–25.52) | 6.65 (6.42–6.98) | 67.75 (65.34–85.06) | 60.54 (56.24–61.53) |
| Merged inspection | 200 | 1 | 9.13 (8.94–10.72) | 8.70 (8.02–9.41) | 728.9 (722.5–736.8) | 631.4 (619.5–641.0) |
| Merged inspection | 200 | 4 | 9.88 (9.35–16.69) | 9.76 (9.13–16.77) | 217.2 (216.4–223.8) | 186.8 (184.2–193.1) |
| Final integrated | 12 | 1 | 5.92 (5.47–6.65) | 5.57 (5.27–5.94) | 45.55 (44.47–46.73) | 39.16 (38.62–39.89) |
| Final integrated | 12 | 4 | 6.57 (5.75–6.91) | 5.80 (5.66–6.13) | 15.48 (14.13–16.31) | 13.47 (12.45–13.73) |
| Final integrated | 60 | 1 | 6.49 (6.20–7.31) | 6.41 (5.89–7.97) | 220.9 (217.1–227.4) | 187.3 (185.8–190.1) |
| Final integrated | 60 | 4 | 7.73 (7.27–8.35) | 7.35 (6.90–8.40) | 68.68 (67.28–75.14) | 59.21 (57.97–65.32) |
| Final integrated | 200 | 1 | 9.34 (8.85–9.87) | 8.71 (8.20–13.25) | 724.6 (719.7–786.3) | 634.3 (618.2–657.3) |
| Final integrated | 200 | 4 | 11.00 (10.60–11.25) | 10.29 (9.71–10.79) | 221.2 (216.5–243.4) | 192.9 (191.3–208.2) |

The first-callback ranges overlap at 200 repositories. Full-status times were more consistent. On the final build the slowest 200-repository run at each job count was faster than the fastest corresponding baseline. At four jobs, median Go allocations fell from 76.3 to 63.5 MB per full status call. Separate Git Trace2 runs on all three revisions confirmed the process counts in the table below. [All 120 samples, allocations and result digests](git-inspection-samples.csv)

On November 20, 2026, the three linux/amd64 test binaries ran on Ubuntu 24.04 under WSL2 (kernel 6.6.114.1), native ext4, Intel Core i5-14600K, 20 logical CPUs, Go 1.27.4 and Git 2.43.0. Each archive used the same benchmark source (Git blob `a503c31`). Both series reused the same three mixed-state fixtures. The OS file cache was warm from preparation and previous processes. No timed process ran a warm-up scan. Each sample used a fresh service and catalog store, with the worker limit set on disk before the process started.

The benchmark checked ordering, callback count, partial outcome and the expected state for every row. Outside the timer it hashed the full result and aggregate error, including all per-row diagnostics. Every sample for a given absolute fixture root produced the same SHA-256 digest across all three revisions and both job counts. [Benchmark source](../../internal/app/status_benchmark_test.go)

## Reproduce the paired first-use runs

Run from the checkout containing the benchmark harness on Linux. The preparation filter builds the fixtures without running a status scan. Each odd pair runs the baseline first and each even pair reverses the order.

```sh
set -eu
benchdir=$(mktemp -d)
export BEARFORGE_BENCH_FIXTURE_PARENT="$benchdir/fixtures"
mkdir "$BEARFORGE_BENCH_FIXTURE_PARENT"
for rev in 9fa2c94 ac81695 9a1f331; do
    mkdir "$benchdir/$rev"
    git archive "$rev" | tar -x -C "$benchdir/$rev"
    cp internal/app/status_benchmark_test.go "$benchdir/$rev/internal/app/"
    (cd "$benchdir/$rev" && CGO_ENABLED=0 go test -c -o "$benchdir/$rev.test" ./internal/app)
done
"$benchdir/9a1f331.test" '-test.run=^$' \
    '-test.bench=^BenchmarkStatusWorkspace$/^repos=(12|60|200)$/^prepare_only$' \
    -test.benchtime=1x -test.v

for candidate in 9a1f331 ac81695; do
    for n in 12 60 200; do
        for root in "$BEARFORGE_BENCH_FIXTURE_PARENT"/bearforge-"$n"-*/code; do
            export BEARFORGE_BENCH_ROOT="$root"
            for jobs in 1 4; do
                catalog="$root/.bearforge/catalog.yaml"
                sed -i -E "s/^  jobs: [0-9]+$/  jobs: $jobs/" "$catalog"
                grep -qx "  jobs: $jobs" "$catalog"
                for pair in 1 2 3 4 5; do
                    case $pair in 1|3|5) order="9fa2c94 $candidate" ;; *) order="$candidate 9fa2c94" ;; esac
                    for rev in $order; do
                        echo "candidate=$candidate n=$n jobs=$jobs pair=$pair build=$rev"
                        "$benchdir/$rev.test" '-test.run=^$' '-test.bench=^BenchmarkStatusFirstUse$' \
                            -test.benchtime=1x -test.benchmem -test.v
                    done
                done
            done
        done
    done
done
```

To count processes separately, set `GIT_TRACE2_EVENT` to a new absolute trace file and run a single invocation of `BenchmarkStatusFirstUse` against each prepared fixture. Count Trace2 `start` events for `rev-parse`, `config` and `status`. The client's one `--version` probe precedes the measurement.

## Historical Windows timings

The numbers below are median milliseconds from five alternating before/after pairs. First is the first successful Git observation delivered to the status callback. Total ends when the application returns all ordered results, including failures. Terminal rendering is measured separately.

| Members | Jobs | First before | First after | Total before | Total after | Total range before | Total range after |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 12 | 4 | 141 | 115 | 419 | 355 | 402–441 | 334–375 |
| 60 | 4 | 167 | 139 | 2,105 | 1,881 | 1,941–2,582 | 1,783–2,221 |
| 200 | 4 | 181 | 152 | 8,882 | 7,359 | 8,708–9,536 | 7,161–7,800 |
| 12 | 1 | 144 | 122 | 1,696 | 1,397 | 1,677–1,820 | 1,379–1,419 |
| 60 | 1 | 154 | 132 | 9,114 | 7,618 | 8,994–9,638 | 7,449–7,808 |

A single additional pair at 200 members and one job took 32.649 s before and 28.246 s after, with first results at 161 and 138 ms. Each build completed 167 successful Git inspections and reported 33 missing worktrees.

## Process counts

Separate, untimed runs used Git Trace2. Each entry counts launches before and after the change. Setup commands and the Git client version check occurred before tracing.

| Members | Successful worktrees | `rev-parse` | `config` | `status` | All launches |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 12 | 10 | 34 → 24 | 12 → 12 | 10 → 10 | 56 → 46 |
| 60 | 50 | 170 → 120 | 60 → 60 | 50 → 50 | 280 → 230 |
| 200 | 167 | 567 → 400 | 200 → 200 | 167 → 167 | 934 → 767 |

Every repository had one catalog remote. The savings were one launch per readable worktree: Git reported both the common directory and the worktree root in one call. Missing worktrees produced the same number of launches. The wall-clock samples do not estimate the additional savings available from batching URL verification for multi-remote repositories.

## Workload and measurement

Measured on 2026-11-18 with Windows 11 Pro 10.0.26300, an Intel Core i5-14600K (20 logical processors), Go 1.27.4 and Git 2.47.0.windows.1. The baseline was `9fa2c94`, which already inspects repositories concurrently. The candidate ran the new Git inspection code with the same worker count.

The existing [status benchmark fixture](status-performance.md) creates 256 tracked files per repository in real local bare repositories and linked worktrees. The six-worktree pattern includes clean, unstaged, staged, untracked, detached and missing worktrees. The 200 case ends with a clean and an unstaged worktree. All timed calls checked the callback count, number of successful observations, expected partial outcome and ordering of repository IDs.

Both builds observed the same prepared files. A benchmark-only driver reused the fixture between processes, ran one complete `StatusWithProgress` call and recorded the two elapsed times around that call. Builds alternated execution order between pairs. Each call began in a fresh test process with the operating system's filesystem cache warm from previous scans. Setup and Go process startup were outside the interval. These measurements cover local Git and filesystem work, without network traffic or a cold-disk run.

The timed four-job build used Git blob IDs `f55cd29` (app service), `751c79a` (Git client) and `4291290` (worktree checks). The one-job comparisons include the subsequent guard that returns cancellation or timeout immediately from the combined query. Single four-job checks of that final build took 1.995 s for 60 members (first 136 ms) and 7.478 s for 200 (first 148 ms).

### Raw samples

Milliseconds rounded to whole numbers, in round order within each build.

| Members / jobs | First before | First after | Total before | Total after |
| --- | --- | --- | --- | --- |
| 12 / 4 | 132, 137, 141, 142, 142 | 115, 119, 123, 113, 112 | 402, 441, 412, 419, 426 | 355, 375, 361, 334, 341 |
| 60 / 4 | 146, 209, 167, 183, 167 | 144, 117, 141, 136, 139 | 1941, 2105, 2102, 2364, 2582 | 1804, 1783, 1948, 2221, 1881 |
| 200 / 4 | 197, 171, 184, 175, 181 | 152, 318, 191, 148, 147 | 9536, 8882, 8726, 8708, 9164 | 7315, 7501, 7359, 7800, 7161 |
| 12 / 1 | 144, 143, 177, 152, 138 | 122, 121, 124, 125, 117 | 1696, 1683, 1820, 1677, 1735 | 1397, 1379, 1419, 1396, 1403 |
| 60 / 1 | 160, 152, 162, 149, 154 | 134, 125, 132, 123, 139 | 9638, 9101, 9114, 8994, 9171 | 7808, 7776, 7562, 7449, 7618 |

## Reproduce

Run the same benchmark source on the candidate and on an archive of `9fa2c94` (copy `internal/app/status_benchmark_test.go` into the archive). To measure new workspace scans and observe process counts separately:

```sh
go test ./internal/app -run '^$' -bench '^BenchmarkStatusWorkspace$' -benchtime=1x -count=5 -timeout=40m
go test ./internal/app -run '^$' -bench '^BenchmarkStatusGitLaunches$' -benchtime=1x -count=1 -timeout=20m
```

These commands create equivalent disposable fixtures for each build; the timing benchmark warms each fixture before taking samples. To include 1,000 real repositories, set `BEARFORGE_BENCH_1000=1` and select `/repos=1000$` with the benchmark filter. This opt-in size is not included in the table.

To reuse a real Git fixture for the dashboard benchmark, set `BEARFORGE_BENCH_FIXTURE_PARENT` to an existing directory, then run the following. The first command logs the unique retained workspace root. Set `BEARFORGE_BENCH_ROOT` to that logged `code` path before the second command. The retained fixture remains available for repeated runs.

```sh
go test ./internal/app -run '^$' -bench '^BenchmarkStatusWorkspace$/^repos=60$/^jobs=4$' -benchtime=1x -count=1 -v
go test ./internal/tui -run '^$' -bench '^BenchmarkDashboardGitRefresh$' -benchtime=1x -count=5
```
