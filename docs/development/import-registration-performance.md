# Registering repositories during a batch import

Importing 60 local repositories into a 200-entry catalog with four jobs took **2.175 s**, down from **2.774 s** (22% shorter). The median wait from a finished clone to its success event fell from **55.70 ms to 33.86 ms**. With 1,000 existing entries, a four-job import of 12 repositories fell from **1.420 s to 906.1 ms** (36% shorter).

## Import time and progress

Batch time covers the confirmed import from entry through return. The last two columns measure each repository from the end of the clone adapter's Git commands to the application's `succeeded` event, including identity verification, time waiting behind other completions, catalog persistence and a durable journal checkpoint. With four jobs, more finished clones can wait for registration together.

| Initial entries | Imports | Jobs | Batch before, ms | Batch after, ms | Clone to success p50, ms (before → after) | Clone to success p95, ms (before → after) |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 60 | 12 | 1 | 810.9 (733.4–890.3) | 742.3 (686.0–845.1) | 12.92 → 9.82 | 15.07 → 12.28 |
| 60 | 12 | 4 | 346.9 (329.3–385.5) | 313.8 (297.6–367.7) | 22.55 → 15.77 | 37.79 → 29.99 |
| 200 | 60 | 1 | 4,933 (4,718–5,760) | 4,310 (4,200–5,418) | 27.13 → 16.68 | 30.35 → 18.01 |
| 200 | 60 | 4 | 2,774 (2,620–3,065) | 2,175 (2,028–2,388) | 55.70 → 33.86 | 104.4 → 65.88 |
| 1,000 | 12 | 1 | 1,882 (1,848–2,244) | 1,388 (1,331–1,461) | 82.95 → 41.07 | 91.91 → 43.90 |
| 1,000 | 12 | 4 | 1,420 (1,333–1,509) | 906.1 (815.5–971.5) | 194.4 → 109.3 | 350.3 → 188.1 |

Batch times show the median and range over five paired runs. For each batch the benchmark records the median repository delay and its nearest-rank 95th percentile. The table shows the median of each statistic across the five runs. With 12 imports, the nearest-rank 95th percentile is the longest clone-to-success delay in the batch. [All 60 samples, bytes and allocations](import-registration-samples.csv)

## Catalog work during import

Separate instrumented runs counted work inside the confirmed import. Both job limits gave the same counts at each batch size, including both initial catalog sizes for the 12-repository batch. The first two YAML columns count passes over the input bytes, one to build the comment-preserving tree and one to decode the strict schema. The output column counts the YAML library constructing the replacement node from the updated catalog. A durable write is a successful catalog replacement followed by a directory sync.

| Imports | Catalog reads | Input tree parses | Strict schema decodes | Output nodes built | Durable catalog writes |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 12 | 50 → 38 | 50 → 12 | 38 → 12 | 12 → 12 | 12 → 12 |
| 60 | 242 → 182 | 242 → 60 | 182 → 60 | 60 → 60 | 60 → 60 |

During the 60-repository import, input YAML passes fell from 424 to 120. Each previously unseen revision still needed a strict decode. Subsequent reads of the same bytes reused the validated catalog and parsed comments. Each repository had its own durable catalog write before its progress event.

## Method

Each sample started in a new process with an identical catalog, empty clone destinations and a fresh store for the confirmed operation. The catalog contained 90% active entries and 10% retired entries with operation IDs and fingerprints, plus repository comments and a nested extension. Every selected repository had its own one-commit local bare origin. The benchmark redirected each anonymous transfer to that origin and set the cloned repository's URL back to its reviewed HTTPS value. The clone timestamp was taken immediately after that Git command returned. This fixture measures local Git, registration and journaling costs. Production network transfer times depend on repository size and the remote server.

Fixture creation and the confirmation preview completed before timing. The operating system's file cache was warm from setup. After timing, the benchmark checked every persisted provider identity and origin, the succeeded journal steps, the operation outcome, the catalog size and the ordered results. [Import benchmark source](../../internal/app/import_registration_benchmark_test.go) · [Isolated catalog save measurements](catalog-save-performance.md)

## Reproduction

The baseline was `2461c63`. Both builds used import benchmark blob `2c0c25b`. The candidate's `internal/config/store.go` blob was `5744848`. The linux/amd64 binaries ran on Ubuntu 24.04 under WSL2 (kernel 6.6.114.1) using the native ext4 filesystem, an Intel Core i5-14600K with 20 logical CPUs, Git 2.43.0 and Go 1.27.4.

Run these commands from the candidate checkout on Linux. Each benchmark creates its files under the system temporary directory.

```sh
benchdir=$(mktemp -d)
mkdir "$benchdir/base"
git archive 2461c63 | tar -x -C "$benchdir/base"
cp internal/app/import_registration_benchmark_test.go "$benchdir/base/internal/app/"
(cd "$benchdir/base" && CGO_ENABLED=0 go test -c -o "$benchdir/before" ./internal/app)
CGO_ENABLED=0 go test -c -o "$benchdir/after" ./internal/app

for pair in 1 2 3 4 5; do
    case $pair in 1|3|5) order="before after" ;; *) order="after before" ;; esac
    for entries in 60 200 1000; do
        batch=12
        if [ "$entries" = 200 ]; then batch=60; fi
        for jobs in 1 4; do
            for build in $order; do
                echo "pair=$pair entries=$entries imports=$batch jobs=$jobs build=$build"
                "$benchdir/$build" '-test.run=^$' \
                    "-test.bench=^BenchmarkImportRegistration$/^catalog=$entries$/^batch=$batch$/^jobs=$jobs$" \
                    -test.benchtime=1x -test.benchmem
            done
        done
    done
done
```
