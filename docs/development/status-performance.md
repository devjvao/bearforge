# Status inspection performance

Bounded inspection reduced median status time from 9.906 seconds to 2.940 seconds
for the 60-repository workspace below. The first completed Git observation reached
the progress callback after 203 milliseconds. Previously the dashboard waited for
the full 9.906-second result before it could show repository observations.

## Measurements

Measured on 2026-11-17 using Windows 11 Pro 10.0.26300, amd64, an Intel Core
i5-14600K (14 cores, 20 logical processors), Go 1.27.4 and Git
2.47.0.windows.1. Each entry is the median of five full-workspace samples. Runs
were serial, with other project test processes stopped during timed samples.

| Workspace members | Historical sequential total / first observation | New total, four workers | New first completed Git observation | Total speedup | First observation sooner |
| --- | ---: | ---: | ---: | ---: | ---: |
| 12 | 1,972 ms | 606.5 ms | 191.0 ms | 3.25x | 10.3x |
| 60 | 9,906 ms | 2,940 ms | 202.6 ms | 3.37x | 48.9x |

The new implementation was also measured with one worker on the same fixtures:

| Workspace members | One-worker total | One-worker first observation | Four-worker total speedup over one worker |
| --- | ---: | ---: | ---: |
| 12 | 1,857 ms | 174.0 ms | 3.06x |
| 60 | 9,858 ms | 181.7 ms | 3.35x |

One worker in the new build still streams results. Its first observation need not
wait for the whole workspace. Four workers improve total time; simultaneous Git
process launches can make the first individual result slightly slower than with
one worker.

These are application callback timings. They include definition loading,
repository verification and Git inspection, but exclude Bubble Tea message
scheduling and terminal painting. The historical dashboard's wait is its complete
status duration. Ratios describe these samples rather than a hardware-independent
performance guarantee.

## Workload and checks

`BenchmarkStatusWorkspace` creates disposable local bare repositories and linked
worktrees. Every repository starts with 256 tiny tracked text files across 16
directories. Each group of six workspace members contains a clean worktree, an
unstaged change, a staged change, an untracked file, a detached HEAD and a missing
worktree. Thus the 12-member case completes 10 Git status inspections plus two
unreadable observations; the 60-member case completes 50 inspections plus ten
unreadable observations.

The benchmark exercises the complete application status path, including catalog
and workspace loading, safe paths, bare-repository and remote validation,
common-directory verification, Git status parsing, ordered result collection and
progress delivery. It checks that every member completes, that a real Git status
arrives, and that final items match the one-worker result exactly, including
partial failures. No network or credentials are required.

Fixture creation and catalog updates are excluded from timing. Each worker count
gets an untimed warm-up scan. This is a local, warm-cache workload dominated by
process launches and filesystem metadata. It represents a workspace containing
many small repositories and mixed local states; it does not measure huge
monorepos, cold disks, network shares or remote provider latency.

## Repeat the benchmark

From the repository root:

```sh
go test ./internal/app -run '^$' -bench '^BenchmarkStatusWorkspace$' -benchtime=1x -count=5 -timeout=20m
```

This runs the current implementation with catalog `defaults.jobs` values of one
and four. The default is four. `total-ms` measures the complete status call;
`first-repo-ms` measures delivery of the first callback containing a successful Git
inspection. Cheap unreadable observations do not satisfy that metric. Normal test
runs do not execute the benchmark.

The historical baseline came from an archive of commit `87c77e7`, with the same
benchmark source copied into it. Its original `Status` implementation was
unchanged. A benchmark-only adapter provided the new method signature while
preserving the original batch delivery:

```go
func (s *Service) StatusWithProgress(ctx context.Context, name string, onItem func(Item)) (Result, error) {
    result, err := s.Status(ctx, name)
    if onItem != nil {
        for _, item := range result.Items {
            onItem(item)
        }
    }
    return result, err
}
```

That archive ran with `-bench '^BenchmarkStatusWorkspace$/repos=.*/jobs=1$'` and
the other options above. Both the historical and current runs passed. The
benchmark also prints Go's `ns/op`, whose timing includes validation after the
status call; the tables use the explicit `total-ms` metric.

## Correctness verification

The full test suite, `go vet ./...` and the CLI build passed on this machine.
Tests cover bounded active inspections, completion before a blocked repository,
ordered partial results, cancellation, immutable progress snapshots, early
dashboard rendering, stable selection, stale requests and shutdown while update
delivery is blocked. Independent review found no material issues.

The full Windows race run also passed on 2026-11-17 after installing WinLibs GCC
16.2.0 / MinGW-w64 14.0.0 through winget. The compiler resolved
`libsynchronization.a`, and the run used `CGO_ENABLED=1` with
`go test -race -json ./... -count=1 -timeout=20m`: 15 packages passed, with 1,176
test/subtest passes, no failures and no race reports. Five existing checks skipped
because symlink privileges or shell adapters were unavailable, or because the
scale test is opt-in. No code fixes were required. See the
[Windows race setup](README.md#tests-and-ci) for reproduction; Linux CI remains
separate platform coverage.

## Raw samples

Samples are in execution order, in milliseconds.

| Build | Members | Workers | Total samples (ms) | First completed Git observation samples (ms) |
| --- | ---: | ---: | --- | --- |
| Historical batch | 12 | 1 | 1637, 1774, 2192, 1972, 1975 | 1637, 1774, 2192, 1972, 1975 |
| Historical batch | 60 | 1 | 10096, 9906, 10188, 9577, 9519 | 10096, 9906, 10188, 9577, 9519 |
| Progressive | 12 | 1 | 1527, 1877, 1840, 1857, 1898 | 142.8, 175.2, 197.7, 163.9, 174.0 |
| Progressive | 12 | 4 | 659.8, 607.4, 578.1, 589.8, 606.5 | 185.8, 200.1, 191.0, 213.7, 190.7 |
| Progressive | 60 | 1 | 10372, 9858, 9518, 9691, 9862 | 211.8, 249.8, 179.4, 181.7, 179.2 |
| Progressive | 60 | 4 | 2814, 2903, 3095, 2940, 2986 | 180.8, 203.3, 187.3, 202.6, 260.3 |
