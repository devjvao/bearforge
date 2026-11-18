# Dashboard refreshes with large operation histories

With 1,000 terminal journals, the median time to the first inspecting row on a repeat refresh fell from **138.3 ms to 57.68 ms** (58% less time). Both builds read all 1,003 files. On the repeat refresh, the new store decoded the two unfinished operations and reported the corrupt file. A fresh store still checked every journal and took 143.6 ms, compared with 140.4 ms before the change.

## Time to initial rows

These are medians from five paired runs on Windows 11 Pro 10.0.26300, an Intel Core i5-14600K and Go 1.27.4 (amd64). Cold means first observation using a new journal store. Repeat means another observation using the same store after one untimed scan. The operating system's file cache was warm in both cases.

| Terminal journals | Cold before (ms) | Cold after (ms) | Repeat before (ms) | Repeat after (ms) | Repeat allocated MB before / after |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | 1.03 | 1.03 | 0.51 | 0.51 | 0.081 / 0.088 |
| 100 | 15.15 | 14.41 | 14.50 | 6.03 | 7.11 / 0.77 |
| 1,000 | 140.4 | 143.6 | 138.3 | 57.68 | 69.68 / 5.29 |

The first-row clock runs from entry to `DashboardWithUpdates` until it publishes the initial list. The benchmark checks the inspecting row, both sorted pending IDs and the corrupt-file problem, then cancels before Git starts. Allocation totals cover the full benchmarked call, including cancellation and the assertions. These numbers exclude TUI frame generation.

## Work done per scan

| Terminal journals | Files opened | Bytes read | Strict decode attempts, cold (both) | Strict decode attempts, repeat before / after | Directory enumeration alone, before / after (ms) |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | 3 | 4,010 | 3 | 3 / 3 | 0.076 / 0.072 |
| 100 | 103 | 906,254 | 103 | 103 / 3 | 0.092 / 0.091 |
| 1,000 | 1,003 | 9,027,416 | 1,003 | 1,003 / 3 | 0.266 / 0.266 |

Separate untimed probes counted successful journal opens, bytes returned by the file reader, and entries into the strict JSON decoder. The corrupt file accounts for one decode attempt on every scan. All 1,000 unchanged terminal files hit the new cache on repeat. Every file's contents are read so a replacement or an in-place edit with preserved timestamps can be detected. The directory enumeration benchmark measures `os.ReadDir` on the operations directory alone and excludes opening, hashing and decoding its files.

Each dashboard journal store retains up to 2,048 validated terminal digests in memory. If a history exceeds that bound, refreshes reuse a stable subset and fully validate the rest. Restarting the dashboard starts with an empty cache.

In each group of five terminal journals, one is a 60-repository import carrying GitHub identity metadata on every step, one is an abandoned 12-step workspace operation, and three are completed 12-step workspace operations. Every fixture also has two unfinished operations in different workspaces and one one-byte damaged journal. The real journal store writes the fixtures before measurement.

## Repeat the benchmark

Use a new, empty directory on the same filesystem to share the exact fixture files across invocations. The benchmark creates a child directory per history size the first time, then opens that history read-only. Without the environment variable it uses disposable fixtures.

```powershell
$env:BEARFORGE_BENCH_JOURNAL_PARENT = (New-Item -ItemType Directory -Path (Join-Path $env:TEMP ("bearforge-history-" + [guid]::NewGuid()))).FullName
go test ./internal/app -run '^$' -bench '^BenchmarkDashboardJournalHistory$' -benchtime=1x -count=5 -benchmem
```

For the comparison, the baseline was an archive of `ac81695` with the same [benchmark harness](../../internal/app/dashboard_journal_benchmark_test.go) copied in. The candidate's Git blob IDs were `8a8ac02` (dashboard), `1043108` (journal store) and `1214683` (pending index). Each build ran as a separate compiled Go test binary. One preparation run created and read the histories. The binaries then alternated order within five pairs with `-test.benchtime=1x`. Instrumentation and race tests ran outside the timed samples.

First-row samples in milliseconds, in pair order:

| Terminal journals | Store | Before | After |
| ---: | --- | --- | --- |
| 0 | cold | 1.034, 1.03, 1.033, 1.03, 1.033 | 1.545, 1.034, 0.5051, 1.035, 0.5144 |
| 0 | repeat | 0.5118, 0.5159, 0.5156, 0.5063, 0.514 | 0.5138, 0, 0.5126, 0.6244, 0.5147 |
| 100 | cold | 15.99, 14.92, 14.96, 15.44, 15.15 | 15.09, 14.36, 14.41, 15.61, 14.4 |
| 100 | repeat | 14.93, 14.5, 14.2, 14.16, 15.34 | 6.597, 6.027, 5.593, 7.035, 5.503 |
| 1000 | cold | 140.4, 142.4, 139.9, 141, 139.8 | 136.4, 156.9, 143.6, 143.6, 137.3 |
| 1000 | repeat | 138.3, 138.8, 135.2, 130.9, 143.6 | 57.08, 60.37, 59.66, 57.68, 56.88 |

The zero-terminal control is near this host's timer resolution. A zero observation means the timer did not advance between the two reads. It is unsuitable for estimating small differences between the builds.

## Verification

On Windows, `go test -race ./... -count=1 -timeout=25m` passed with `CGO_ENABLED=1` and `BEARFORGE_SCALE_TEST=1`, including the 60-repository lifecycle test. The run recorded 1,209 test and subtest passes, five platform-dependent skips and zero failures. The package tests cover same-size edits with preserved timestamps, corrupt or deleted journals, root changes, cache eviction, and concurrent reads during replacement. `go vet ./...` and the Windows build also passed.
