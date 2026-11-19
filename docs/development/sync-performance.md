# Time to the first fetch during sync

With four jobs and 200 local repositories, the median wait for the first fetch fell from **251.0 ms to 81.23 ms** (68%). The full sync went from **1.499 s to 1.315 s** (12%). At one job the first fetch took 249.1 ms before and 249.8 ms after. Both builds used 400 Git commands to validate the 200 repositories.

## Measurements

Each cell shows the median and the minimum–maximum range in milliseconds across five paired runs. A fresh run starts a new process against a prepared fixture. A repeat run calls sync a second time in the same process. The operating system's file cache was left warm throughout.

| Run | Repos | Jobs | First fetch before | First fetch after | Full sync before | Full sync after |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Fresh | 12 | 1 | 14.89 (13.97–15.80) | 14.84 (14.01–15.77) | 260.2 (243.4–266.6) | 259.5 (248.5–260.4) |
| Fresh | 12 | 4 | 15.49 (14.09–15.61) | 5.03 (4.29–6.15) | 89.29 (82.36–93.97) | 77.98 (72.64–80.87) |
| Fresh | 60 | 1 | 83.25 (75.31–120.4) | 76.12 (67.94–91.97) | 1,363 (1,343–1,555) | 1,349 (1,156–1,589) |
| Fresh | 60 | 4 | 75.27 (69.21–79.15) | 25.22 (22.19–26.52) | 473.3 (407.0–496.0) | 398.1 (357.3–416.8) |
| Fresh | 200 | 1 | 249.1 (232.4–264.7) | 249.8 (233.3–267.1) | 4,381 (4,043–4,530) | 4,414 (4,004–4,587) |
| Fresh | 200 | 4 | 251.0 (230.4–257.6) | 81.23 (75.64–90.52) | 1,499 (1,376–1,531) | 1,315 (1,251–1,351) |
| Repeat | 60 | 4 | 72.60 (71.84–75.82) | 24.14 (23.28–24.82) | 441.9 (425.1–453.8) | 383.8 (377.0–397.7) |
| Repeat | 200 | 4 | 245.6 (240.8–307.6) | 78.95 (78.14–80.84) | 1,483 (1,442–1,632) | 1,278 (1,252–1,347) |

The clock starts immediately before `Service.Sync`. First fetch is entry to the first real Git client's `Fetch` method, after the mutation lock, catalog load and all repository identity checks. The method then validates the fetch configuration, contacts the local origin, runs Git's transfer and verifies the resulting refs. Full sync ends when all results are returned and the lock has been released. Every sample checked that every repository had a successful result in sorted order.

Each repository had its own local bare origin, one commit on `main`, and an up-to-date `origin/main`. In separate four-job runs, Git tracing counted one `rev-parse --is-bare-repository` and one `config --get remote.origin.url` for every repository before the first fetch began. That gives 24, 120 and 400 Git probes in both builds. The time saving comes from running independent repository checks concurrently. At 200 repositories and four jobs, median Go allocations for the full sync were 406.1 MB before and 406.3 MB after.

## Raw samples

Times are in milliseconds and listed in pair order.

| Run | Repos / jobs | First before | First after | Total before | Total after |
| --- | --- | --- | --- | --- | --- |
| Fresh | 12 / 1 | 14.89, 13.97, 15.34, 15.80, 14.81 | 14.01, 14.25, 15.51, 14.84, 15.77 | 252.3, 243.4, 266.6, 260.2, 263.0 | 248.5, 250.9, 260.2, 260.4, 259.5 |
| Fresh | 12 / 4 | 15.49, 14.09, 15.28, 15.61, 15.51 | 5.027, 4.292, 5.426, 6.148, 4.982 | 91.68, 82.36, 89.29, 88.73, 93.97 | 79.06, 72.64, 77.41, 80.87, 77.98 |
| Fresh | 60 / 1 | 83.25, 120.4, 75.31, 84.06, 75.67 | 67.94, 69.94, 91.97, 76.56, 76.12 | 1343, 1555, 1363, 1444, 1350 | 1156, 1349, 1359, 1589, 1349 |
| Fresh | 60 / 4 | 69.64, 69.21, 76.59, 79.15, 75.27 | 22.19, 22.76, 25.23, 25.22, 26.52 | 426.3, 407.0, 496.0, 473.3, 477.7 | 357.3, 361.5, 398.1, 416.8, 409.3 |
| Fresh | 200 / 1 | 232.4, 239.4, 264.7, 249.1, 255.2 | 234.6, 233.3, 267.1, 255.3, 249.8 | 4043, 4161, 4526, 4381, 4530 | 4004, 4122, 4487, 4414, 4587 |
| Fresh | 200 / 4 | 230.4, 249.6, 251.0, 257.6, 255.0 | 75.64, 81.23, 90.52, 83.32, 80.40 | 1376, 1471, 1502, 1531, 1499 | 1251, 1305, 1334, 1351, 1315 |
| Repeat | 60 / 4 | 75.75, 75.82, 72.60, 71.90, 71.84 | 24.14, 24.18, 23.28, 23.28, 24.82 | 441.9, 453.8, 429.0, 425.1, 442.7 | 397.7, 383.8, 377.0, 377.3, 390.4 |
| Repeat | 200 / 4 | 262.4, 240.8, 241.5, 245.6, 307.6 | 79.82, 78.89, 78.14, 78.95, 80.84 | 1536, 1478, 1442, 1483, 1632 | 1296, 1267, 1252, 1278, 1347 |

## Reproduce

Measured on 2026-11-18 on Ubuntu 24.04 under WSL2 (kernel 6.6.114.1, native ext4), Intel Core i5-14600K, 20 logical CPUs, Go 1.27.4 linux/amd64 and Git 2.43.0. The baseline was `d4fea01`. The measured candidate's `internal/app/sync.go` Git blob was `bd2866c`. Both executables used the same benchmark source and accessed the same files within each comparison. Fixture preparation, test process startup, tracing and race tests were outside the timing interval. [Benchmark source](../../internal/app/sync_benchmark_test.go)

Run the benchmark on a single build with disposable fixtures.

```sh
go test ./internal/app -run '^$' -bench '^BenchmarkSyncWorkspace$' -benchtime=1x -count=5 -benchmem
```

To reproduce the fresh-process comparison on Linux, run the following from the candidate checkout. The preparation command sets up the repositories without running a sync. To exercise 1,000 repositories, also export `BEARFORGE_BENCH_1000=1` and add `1000` to the sizes in the loop.

```sh
benchdir=$(mktemp -d)
export BEARFORGE_BENCH_SYNC_PARENT="$benchdir/fixtures"
mkdir "$benchdir/base" "$BEARFORGE_BENCH_SYNC_PARENT"
git archive d4fea01 | tar -x -C "$benchdir/base"
cp internal/app/sync_benchmark_test.go "$benchdir/base/internal/app/"
(cd "$benchdir/base" && CGO_ENABLED=0 go test -c -o "$benchdir/before" ./internal/app)
CGO_ENABLED=0 go test -c -o "$benchdir/after" ./internal/app
"$benchdir/before" '-test.run=^$' '-test.bench=^BenchmarkSyncWorkspace$/./^prepare_only$' -test.benchtime=1x
for round in 1 2 3 4 5; do
    case $round in 1|3|5) order="before after" ;; *) order="after before" ;; esac
    for n in 12 60 200; do
        for j in 1 4; do
            for build in $order; do
                echo "round=$round repos=$n jobs=$j build=$build"
                "$benchdir/$build" '-test.run=^$' \
                    "-test.bench=^BenchmarkSyncWorkspace$/^repos=${n}$/^jobs=${j}$" -test.benchtime=1x
            done
        done
    done
done
```

For the repeat measurement, add `-test.count=2`, use 60 and 200 repositories at four jobs, and record the second result from each binary. For a separate command count, set `GIT_TRACE` to an empty, absolute log file and count the two identity probes before the first `built-in: git remote` line, which begins fetch configuration validation.
