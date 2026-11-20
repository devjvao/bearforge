# Saving a catalog after registration

With 1,000 existing identities, the median time to save one new registration after loading its revision fell from **54.86 ms to 30.87 ms** (44% shorter). Including a fresh store's first load and edit, the same operation went from **70.58 ms to 47.50 ms** (33% shorter).

## Measurements

Each timing is the median in milliseconds, with the minimum and maximum from seven paired runs. The last column gives median Go heap allocations in decimal MB per operation. File synchronization is included.

| Existing identities | Timed operation | Before, ms | After, ms | Allocated MB, before → after |
| ---: | --- | ---: | ---: | ---: |
| 60 | Save | 6.10 (5.84–7.10) | 4.42 (3.64–6.83) | 3.19 → 2.28 |
| 60 | Load, edit, save | 7.10 (6.47–7.59) | 5.83 (5.69–6.38) | 3.86 → 3.00 |
| 200 | Save | 12.97 (12.39–13.08) | 8.92 (8.48–9.26) | 10.63 → 7.57 |
| 200 | Load, edit, save | 16.26 (15.39–18.15) | 11.99 (11.21–14.27) | 12.84 → 9.93 |
| 1,000 | Save | 54.86 (52.25–58.52) | 30.87 (29.56–31.96) | 69.14 → 54.02 |
| 1,000 | Load, edit, save | 70.58 (68.60–73.81) | 47.50 (44.56–70.70) | 80.05 → 65.73 |

The fixture uses 90% active repositories and 10% retired identities. Active repositories have an HTTPS origin, base branch and provider metadata. Retired records have an operation ID and fingerprint. The YAML includes key and value comments and a nested extension with a list. Each run starts with the same input bytes for its size and adds one active repository. After timing, a new store reopens the saved file to check the active and retired counts, registered origin, comments and extension marker. [Benchmark harness](../../internal/config/catalog_benchmark_test.go) · [All 84 samples, bytes and allocations](catalog-save-samples.csv)

## Work per registration

Counter runs gave the same counts at all three catalog sizes. The save-only window starts after the store has loaded and validated the revision. The full cycle starts with a fresh store.

| Timed operation | Build | Catalog file reads | Input tree parses | Strict catalog decodes | Output tree parses | Durable catalog writes |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Save | Before | 2 | 2 | 1 | 1 | 1 |
| Save | After | 1 | 0 | 0 | 1 | 1 |
| Load, edit, save | Before | 3 | 3 | 2 | 1 | 1 |
| Load, edit, save | After | 2 | 1 | 1 | 1 | 1 |

The final read hashes the file on disk immediately before replacement. At 1,000 identities, each build read a 189,575-byte input file and wrote a 201,902-byte replacement. The output SHA-256 matched between builds at every size. Each completed registration still serializes and replaces the full catalog. [Import batch measurements](import-registration-performance.md)

The counter runs used separate instrumented binaries. To reproduce the counts, record catalog reads in `config.read` and successful directory syncs in `Store.write` for `catalog.yaml`. In a private copy of `go.yaml.in/yaml/v3` v3.0.6, count each `yaml.Unmarshal` of input bytes, each successful `Decoder.Decode` into the strict catalog structure, and the internal parse in `Node.Encode` when it constructs the output tree. The EOF probe contributes zero decoded documents. Count events between the benchmark's timer start and stop so fixture setup and verification are excluded.

## Reproduction

Baseline `2461c63` and the candidate (`internal/config/store.go` blob `5744848`) used benchmark blob `93977a0`. Both binaries ran on Ubuntu 24.04.5 under WSL2, kernel 6.6.114.1, on the native ext4 temporary filesystem. The machine had an Intel Core i5-14600K with 20 logical CPUs. Go 1.27.4 built both linux/amd64 binaries with `CGO_ENABLED=0`. Each sample ran in a fresh process and created its fixture before starting the timer. The OS file cache was warm from fixture creation. In the save-only case the same store had already loaded the revision and registered the new repository in memory when timing started.

Run these commands from the candidate checkout on Linux. Set `TMPDIR` to a directory on the filesystem to be measured if needed.

```sh
benchdir=$(mktemp -d)
mkdir "$benchdir/base"
git archive 2461c63 | tar -x -C "$benchdir/base"
cp internal/config/catalog_benchmark_test.go "$benchdir/base/internal/config/"
(cd "$benchdir/base" && CGO_ENABLED=0 go test -c -o "$benchdir/before" ./internal/config)
CGO_ENABLED=0 go test -c -o "$benchdir/after" ./internal/config

for pair in 1 2 3 4 5 6 7; do
    case $pair in 1|3|5|7) order="before after" ;; *) order="after before" ;; esac
    for size in 60 200 1000; do
        for mode in save load-edit-save; do
            for build in $order; do
                echo "pair=$pair entries=$size mode=$mode build=$build"
                "$benchdir/$build" '-test.run=^$' \
                    "-test.bench=^BenchmarkCatalogRegistration$/^entries=$size$/^$mode$" \
                    -test.benchtime=1x -test.benchmem
            done
        done
    done
done
```
