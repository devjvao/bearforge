# Workspace pull-request lookup performance

A fresh lookup of 200 repositories with four jobs fell from **1.203 s to 349.2 ms** across four API hosts (71% shorter) and from **1.221 s to 493.7 ms** on one host (60% shorter). Both builds issued 200 HTTPS requests. On the four-host fixture, the median first lookup began at 7.71 ms before and 7.87 ms after. The improvement was in finishing the workspace.

## Fresh lookups

The tables show median wall time and the minimum–maximum range across five paired runs. First lookup is the median time from entering `Service.PullRequests` to the first call into a provider's `Lookup`. Peak jobs counts overlapping intervals from entering Git's `VerifyBare` through provider query resolution, and from entering `Lookup` through its return, including cache hits. The arrow shows before and after values.

| Repos | Hosts | Jobs | Mode | Before, ms | After, ms | First lookup, ms | HTTP requests | Peak jobs |
| ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: | ---: |
| 12 | 1 | 1 | fresh | 75.9 (71.94–77.12) | 78.12 (72.7–81.28) | 4.12 → 4.06 | 12 | 1 |
| 12 | 1 | 4 | fresh | 74.81 (71.43–77.14) | 35.34 (34.72–36.21) | 4.29 → 4.37 | 12 | 1 → 4 |
| 12 | 4 | 1 | fresh | 78.94 (74.88–80.05) | 81.2 (75.21–93.04) | 3.98 → 4.33 | 12 | 1 |
| 12 | 4 | 4 | fresh | 79.95 (75.22–84.97) | 23.26 (22.39–23.89) | 4.09 → 4.31 | 12 | 1 → 4 |
| 60 | 1 | 1 | fresh | 358.5 (350.5–376.6) | 369.9 (352.8–381.1) | 4.88 → 5.21 | 60 | 1 |
| 60 | 1 | 4 | fresh | 357.4 (343.3–369.7) | 150.6 (145.9–152.6) | 4.84 → 5.57 | 60 | 1 → 4 |
| 60 | 4 | 1 | fresh | 385.2 (374.7–406.8) | 382.1 (367.7–417.8) | 5.32 → 5.16 | 60 | 1 |
| 60 | 4 | 4 | fresh | 392 (365.8–423.4) | 109.7 (103–118.6) | 5.14 → 5.47 | 60 | 1 → 4 |
| 200 | 1 | 1 | fresh | 1258 (1200–1381) | 1245 (1209–1434) | 7.71 → 8.04 | 200 | 1 |
| 200 | 1 | 4 | fresh | 1221 (1137–1385) | 493.7 (486–516.4) | 7.88 → 7.92 | 200 | 1 → 4 |
| 200 | 4 | 1 | fresh | 1224 (1148–1384) | 1233 (1207–1390) | 7.89 → 8.22 | 200 | 1 |
| 200 | 4 | 4 | fresh | 1203 (1147–1333) | 349.2 (341.5–359.8) | 7.71 → 7.87 | 200 | 1 → 4 |

## Local preparation, warmed cache and refresh

A dry run (`plan`) measures local preparation through query resolution. With four jobs and 200 repositories it fell from 699.6 ms to 217.0 ms. The one-job control stayed within the baseline range.

For `warm` and `refresh`, an untimed lookup first populated the provider caches. The timed warm run used the ordinary cache policy. Explicit refresh bypassed cached observations. At 200 repositories across four hosts, 100 queries fit in each of the GitHub and Bitbucket clients' 128-entry caches. Warm runs made zero requests and refresh made 200. With one GitHub client, the 200-repository working set exceeded its cache. Both builds missed all 200 entries on every full repeat scan in this fixture.

| Repos | Hosts | Jobs | Mode | Before, ms | After, ms | First lookup, ms | HTTP requests | Peak jobs |
| ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: | ---: |
| 12 | 4 | 1 | plan | 41.01 (40.07–43.68) | 43.13 (40.09–46.14) | – | 0 | 1 |
| 12 | 4 | 4 | plan | 43.69 (40.94–50.47) | 12.57 (12.17–15.03) | – | 0 | 1 → 4 |
| 60 | 1 | 4 | refresh | 377.3 (335.8–404) | 152.8 (143–159.6) | 4.91 → 5.72 | 60 | 1 → 4 |
| 60 | 1 | 4 | warm | 209.2 (200.4–217.2) | 69.25 (67.64–79.03) | 4.78 → 5.47 | 0 | 1 → 4 |
| 60 | 4 | 1 | plan | 221.9 (205.4–238.8) | 221.5 (206.1–266) | – | 0 | 1 |
| 60 | 4 | 4 | plan | 212.2 (199.3–228.6) | 66.41 (62.12–89.42) | – | 0 | 1 → 4 |
| 60 | 4 | 4 | refresh | 387.6 (359.4–395) | 107.8 (103.2–113) | 5.22 → 5.32 | 60 | 1 → 4 |
| 60 | 4 | 4 | warm | 214.9 (206.3–233.8) | 72.63 (65.95–78.74) | 5.13 → 5.73 | 0 | 1 → 4 |
| 200 | 1 | 4 | refresh | 1320 (1189–1344) | 514.8 (508.2–518.5) | 7.94 → 8.44 | 200 | 1 → 4 |
| 200 | 1 | 4 | warm | 1280 (1246–1358) | 504.1 (489–516.6) | 8.38 → 8.76 | 200 | 1 → 4 |
| 200 | 4 | 1 | plan | 698.3 (677.5–823.5) | 712.1 (685.7–785.4) | – | 0 | 1 |
| 200 | 4 | 4 | plan | 699.6 (638.8–783.3) | 217 (207–243.4) | – | 0 | 1 → 4 |
| 200 | 4 | 4 | refresh | 1230 (1225–1244) | 350.9 (341.3–368.9) | 7.46 → 8.31 | 200 | 1 → 4 |
| 200 | 4 | 4 | warm | 680.5 (643.4–824.2) | 246.9 (211.5–252.1) | 7.61 → 8.32 | 0 | 1 → 4 |

## Method

The benchmark created 12, 60 and 200 bare repositories, each with a linked worktree, a committed file, a checked-out `review` branch and a distinct repository identity. Every measured lookup used the file-backed workspace and catalog, real Git subprocesses, the production provider dispatcher and clients, and the same worktrees for both binaries. The benchmark checked every returned ID, actual branch, query repository, outcome and HTTP request count.

The single-host fixture used one GitHub endpoint. The mixed fixture rotated between two GitHub and two Bitbucket endpoints on four loopback TLS ports. Each endpoint waited 2 ms and returned one empty result page. These timings represent a controlled provider delay and up-to-date local files. Actual provider latency, pagination and filesystem speed affect the totals.

Each timed sample started a new process. Fresh samples started with empty provider caches and the operating system's file cache remained warm. Fixture creation, catalog setup and cache priming finished before the timer started. The comparison alternated execution order by pair on Ubuntu 24.04 under WSL2 (kernel 6.6.114.1, native ext4, Intel Core i5-14600K, 20 logical CPUs), using Git 2.43.0 and Go 1.27.4 linux/amd64 binaries built with `CGO_ENABLED=0`. Baseline commit `8fe464a` and the candidate used benchmark blob `ff399a0`. The candidate's `internal/app/pull_requests.go` blob was `6d3f1a1`. At 200 repositories, four jobs and four hosts, median full-lookup allocations were 87.1 MB before and 89.5 MB after. [All 260 raw samples and allocation counts](pr-lookup-samples.csv)

## Reproduction

Run the following from the candidate checkout on Linux. Use a dedicated fixture directory because the benchmark updates its catalog to select the job count and the current test server endpoints. The preparation pass creates the repositories without running a lookup.

```sh
benchdir=$(mktemp -d)
export BEARFORGE_BENCH_PR_PARENT="$benchdir/fixtures"
mkdir "$benchdir/base" "$BEARFORGE_BENCH_PR_PARENT"
git archive 8fe464a | tar -x -C "$benchdir/base"
cp internal/app/pull_requests_benchmark_test.go "$benchdir/base/internal/app/"
(cd "$benchdir/base" && CGO_ENABLED=0 go test -c -o "$benchdir/before" ./internal/app)
CGO_ENABLED=0 go test -c -o "$benchdir/after" ./internal/app
"$benchdir/before" '-test.run=^$' '-test.bench=^BenchmarkWorkspacePRs$/./^prepare_only$' -test.benchtime=1x

for pair in 1 2 3 4 5; do
    case $pair in 1|3|5) order="before after" ;; *) order="after before" ;; esac
    for n in 12 60 200; do
        for hosts in 1 4; do
            for jobs in 1 4; do
                modes="fresh"
                if [ "$hosts" = 4 ]; then modes="$modes plan"; fi
                if [ "$jobs" = 4 ] && [ "$n" -ge 60 ]; then modes="$modes warm refresh"; fi
                for mode in $modes; do
                    for build in $order; do
                        echo "pair=$pair repos=$n hosts=$hosts jobs=$jobs mode=$mode build=$build"
                        "$benchdir/$build" '-test.run=^$' \
                            "-test.bench=^BenchmarkWorkspacePRs$/^repos=${n}$/^hosts=${hosts}$/^jobs=${jobs}$/^${mode}$" \
                            -test.benchtime=1x
                    done
                done
            done
        done
    done
done
```

To exercise 1,000 members, set `BEARFORGE_BENCH_1000=1` before the preparation pass and add `1000` to the loop. [Benchmark source](../../internal/app/pull_requests_benchmark_test.go)
