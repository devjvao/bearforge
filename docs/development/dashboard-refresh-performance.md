# Dashboard refresh performance

The dashboard receives an initial list, one completed repository at a time, and a final summary. It updates the selected workspace by repository ID. A status change leaves the name filter and cursor in place, and only repositories on the current page are formatted.

At 1,000 repositories in a 100×32 view, the full refresh formatted 9,018 repository rows, down from 1,002,000. Each completion carried one repository record, down from 1,000. [Measured item copies and row formatting](dashboard-work-counts.md)

## Generated frames after a warm-up scan

On the real 60-repository workspace, the model generated its first frame containing a completed status in **152 ms** and the final frame in **2.064 s**, down from 172 ms and 2.355 s. This comparison includes both Git and dashboard changes. In the isolated renderer benchmark with 1,000 rows, a full refresh fell from 12.784 s to 0.763 s (16.8× faster). Average time between successive generated row frames in that workload fell from 12.77 ms to 0.76 ms.

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

## First use at a terminal writer

With 60 local repositories and four jobs, the first completed repository row reached the writer in a median **36.54 ms before and 36.45 ms on final main**. The final summary arrived in **170.76 ms and 153.63 ms**, respectively. At one job both medians were effectively unchanged. Bubble Tea schedules output at up to 60 frames per second and can combine messages. In 19 of 40 samples, the first write that showed inspecting repositories also showed a completed repository. [All 40 writer samples](dashboard-writer-samples.csv)

The support for streaming one row at a time landed in `ac81695`. We compared its immediate parent `9fa2c94` with that commit and, separately, with `9a1f331` after the remaining plan changes. Each comparison alternated baseline-first and candidate-first order across five fresh-process pairs. Values are milliseconds, with the median and minimum–maximum range for each build. The first column of measurements is the median first output showing any inspecting repository.

| Comparison | Jobs | Build | Inspecting | First clean or changed row | Final observed summary |
| --- | ---: | --- | ---: | ---: | ---: |
| PR #14 | 1 | Parent | 21.70 | 21.70 (20.55–59.34) | 420.50 (369.35–1,692.56) |
| PR #14 | 1 | `ac81695` | 21.65 | 21.65 (21.51–36.89) | 387.40 (336.90–437.67) |
| PR #14 | 4 | Parent | 22.02 | 36.99 (20.61–43.17) | 169.13 (152.30–193.25) |
| PR #14 | 4 | `ac81695` | 22.03 | 36.64 (35.83–37.15) | 136.78 (136.02–153.79) |
| Final main | 1 | Baseline | 22.00 | 22.12 (21.58–37.05) | 403.58 (386.64–471.48) |
| Final main | 1 | `9a1f331` | 21.92 | 22.13 (21.51–69.80) | 403.74 (353.24–436.51) |
| Final main | 4 | Baseline | 22.30 | 36.54 (21.62–38.17) | 170.76 (154.28–220.07) |
| Final main | 4 | `9a1f331` | 24.04 | 36.45 (21.53–57.61) | 153.63 (135.68–187.58) |

All three binaries used the same 100×32 Bubble Tea program with its production dashboard model, real Git client, disk store, and renderer. Timing began before creating the store and Git client, including Git's version check. The selected job count was applied in memory on the first catalog load. Each process opened the same prepared 60-member, mixed-state workspace with warm OS file caches. Each had a fresh service and store and performed its first scan during measurement. The environment was Ubuntu 24.04 under WSL2, native ext4, Intel Core i5-14600K, Go 1.27.4 linux/amd64, and Git 2.43.0. Process launch, fixture creation, and offline replay were outside the measured interval.

The test writer timestamped each ANSI write from the renderer. Replaying those writes on a 100×32 virtual terminal located the first visible `[clean]` or `[changes]` in a named repository row, and the final `Observed` header with the expected counts. The elapsed times end at the write that produced those cells. Replay also checked visible statuses against the fixture. Each test process verified the order and outcomes of all 60 results, including the ten missing worktrees. The physical display was outside the measurement. [Writer harness](../../internal/tui/dashboard_writer_test.go) · [Terminal replay](../../internal/tui/testdata/replay_dashboard_writer.py)

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

For the writer measurements, run the following from the repository root on Linux. Set `BEARFORGE_BENCH_ROOT` to the prepared 60-member root. Use a Python environment with `pyte==0.8.2` for offline replay.

```sh
benchdir=$(mktemp -d)
mkdir "$benchdir/captures"
for ref in 9fa2c94 ac81695 9a1f331; do
    mkdir "$benchdir/$ref"
    git archive "$ref" | tar -x -C "$benchdir/$ref"
    cp internal/tui/dashboard_writer_test.go "$benchdir/$ref/internal/tui/"
    (cd "$benchdir/$ref" && CGO_ENABLED=0 go test -c -o "$benchdir/$ref.test" ./internal/tui)
done
for pair in 1 2 3 4 5; do
    for jobs in 1 4; do
        for candidate in ac81695 9a1f331; do
            case "$pair" in 1|3|5) order="9fa2c94 $candidate" ;; *) order="$candidate 9fa2c94" ;; esac
            for build in $order; do
                capture="$benchdir/captures/${candidate}_${pair}_${jobs}_${build}.json"
                BEARFORGE_BENCH_JOBS=$jobs BEARFORGE_BENCH_DASHBOARD_CAPTURE="$capture" \
                    "$benchdir/$build.test" '-test.run=^TestDashboardFirstUseWriter$'
            done
        done
    done
done
python3 internal/tui/testdata/replay_dashboard_writer.py "$benchdir/captures"
```

## Verification

For the final-build measurements, `go test -race -json ./... -count=1 -timeout=25m` on Windows recorded 1,246 passing tests and subtests and six skips. This run included the opt-in writer test against a separate 60-repository Windows fixture. Its output replayed successfully. `go vet ./...` passed, and the separate Linux `TestPhase2Scale60` lifecycle run passed in 12.82 s.

For the original Git and dashboard change, `go test -race ./... -count=1 -timeout=25m` on Windows with `CGO_ENABLED=1` and `BEARFORGE_SCALE_TEST=1` passed, including the 60-repository lifecycle test. The run recorded 1,203 test and subtest passes, five skips, and zero failures. `go vet ./...` and the Windows application build also passed.
