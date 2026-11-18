# Git launches during workspace status

At the default four jobs, the 200-repository fixture completed in 7.359 s after combining the worktree identity queries, down from 8.882 s (17.1%). Git launched 767 times for the same 200 observations, down from 934. The 60-repository fixture took 1.881 s, down from 2.105 s (10.7%).

## Timings

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
