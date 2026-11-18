# Dashboard refresh performance

The dashboard receives an initial list, one completed repository at a time, and a final summary. It updates the selected workspace by repository ID. A status change leaves the name filter and cursor in place, and only repositories on the current page are formatted.

## Measurements

On the real 60-repository workspace, the first visible status arrived in **152 ms** and the full dashboard refreshed in **2.064 s**, down from 172 ms and 2.355 s. This comparison includes both Git and dashboard changes. In the isolated renderer benchmark with 1,000 rows, a full refresh fell from 12.784 s to 0.763 s (16.8× faster). Average time between successive generated row frames in that workload fell from 12.77 ms to 0.76 ms.

Measured on Windows 11 Pro with an Intel Core i5-14600K, Go 1.27.4 and Git 2.47.0.windows.1. Values are medians of five paired runs. Both builds used the same benchmark source, with baseline `9fa2c94` and the working tree implementing plan tasks 1 and 2. The builds alternated order each round. Each sample started in a fresh process with warm filesystem caches. Git process-count tracing and the race suite ran separately.

| Repositories | First visible result, ms (before → after) | Full refresh, ms (before → after) | Allocated MB per refresh (before → after) |
| --- | ---: | ---: | ---: |
| 12 | 2.05 → 1.55 | 17.58 → 17.07 | 7.3 → 7.1 |
| 60 | 2.05 → 1.54 | 52.72 → 40.96 | 41.1 → 31.2 |
| 200 | 3.00 → 1.03 | 430.6 → 140.7 | 308.1 → 102 |
| 1,000 | 14.53 → 2.01 | 12,784 → 762.7 | 17,977.6 → 505.5 |
| 60, real Git | 172.20 → 151.90 | 2,355 → 2,064 | 76.2 → 60.8 |

Full refresh samples in milliseconds, in round order:

| Repositories | Before | After |
| --- | --- | --- |
| 12 | 19.18, 17.58, 16.95, 17.47, 17.85 | 18.05, 17.65, 16.95, 17.07, 16.44 |
| 60 | 54.78, 52.72, 47.76, 49.4, 63.31 | 41.77, 41.99, 40.27, 40.96, 39.99 |
| 200 | 460.2, 437.3, 395.3, 350.3, 430.6 | 135.9, 150.7, 140.7, 141.1, 134.7 |
| 1,000 | 13031, 12369, 12784, 12455, 12860 | 762.7, 800.3, 760.1, 829, 757.2 |
| 60, real Git | 2425, 2355, 2518, 2111, 2035 | 2053, 2169, 2064, 2089, 1816 |

## What the benchmarks measure

`BenchmarkDashboardRefresh` drives the real application service, stream, Bubble Tea update loop, and a 100-column by 32-line view. Its in-memory Git and store adapters allow every row to complete immediately. One worker delivers results in display order, and the harness generates a frame for every completion. The workload exposes the cost of rendering frequent updates. A live terminal may coalesce frames.

`BenchmarkDashboardGitRefresh` drives the same view against a prepared workspace of real local Git repositories using the catalog's four workers. A warm-up scan establishes the expected outcome of every row. In both benchmarks, the timer starts before model initialization. The first-result metric ends when a generated frame first contains a visible clean or changed repository. The refresh metric ends at the final frame. These timings exclude terminal output, OS scheduling of the terminal emulator, and physical display latency. Allocations count bytes allocated by Go across one full refresh, including memory subsequently reclaimed.

## Repeat

Run the renderer workload from the repository root:

```sh
go test ./internal/tui -run '^$' -bench '^BenchmarkDashboardRefresh$' -benchtime=1x -count=5 -benchmem
```

For the combined workload, first generate a disposable 60-member fixture. In PowerShell:

```powershell
$env:BEARFORGE_BENCH_FIXTURE_PARENT = $env:TEMP
go test ./internal/app -run '^$' -bench '^BenchmarkStatusWorkspace$/^repos=60$/^jobs=4$' -benchtime=1x -count=1 -v
$env:BEARFORGE_BENCH_ROOT = '<code root printed by the fixture>'
go test ./internal/tui -run '^$' -bench '^BenchmarkDashboardGitRefresh$' -benchtime=1x -count=5 -benchmem
```

The fixture includes clean, staged, unstaged, untracked, detached and missing worktrees with 256 tiny tracked files per repository. The [Git inspection measurements](git-inspection-performance.md) describe that workload and its subprocess counts.

## Verification

On Windows, `go test -race ./... -count=1 -timeout=25m` with `CGO_ENABLED=1` and `BEARFORGE_SCALE_TEST=1` passed, including the 60-repository lifecycle test. The run recorded 1,203 test and subtest passes, five skips, and zero failures. `go vet ./...` and the Windows application build also passed.
