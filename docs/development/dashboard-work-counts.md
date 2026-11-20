# Dashboard copies and formatted rows

With 1,000 repositories, the full refresh copied or assigned **5,012,000** repository records at the measured slice and row-update sites on `9fa2c94` and **4,000** on `9a1f331`. At 100 columns by 32 lines, the repository list formatted **1,002,000** rows before and **9,018** after. The final build formatted the nine visible rows on every frame.

## Item copies

Each cell shows the measured baseline and final counts for one complete refresh.

| Repositories | Progress snapshot clones | Filtered list appends | Other Item copies and assignments | Total Item copies and assignments |
| ---: | ---: | ---: | ---: | ---: |
| 12 | 156 → 0 | 660 → 0 | 48 → 48 | 864 → 48 |
| 60 | 3,660 → 0 | 14,820 → 0 | 240 → 240 | 18,720 → 240 |
| 200 | 40,200 → 0 | 161,400 → 0 | 800 → 800 | 202,400 → 800 |
| 1,000 | 1,001,000 → 0 | 4,007,000 → 0 | 4,000 → 4,000 | 5,012,000 → 4,000 |

The progress counter sums the slice lengths at the dashboard's `slices.Clone` call. The baseline cloned the initial snapshot and another full snapshot per completion. The final TUI takes the row-update path. The filter counter counts each `app.Item` appended by the baseline's `repositoryRows` during selection, list rendering and detail rendering. The final model refers to positions in its existing item slice.

The four other counters each recorded one element per repository on both builds. Status cloned the initial inspecting list, cloned the worker inputs, and assigned the completed result into its ordered result slice. The dashboard also wrote the completed item into its own row, in the service's progress snapshot before and in the model after. These are logical struct copies and assignments. The counts exclude compiler temporaries, runtime slice growth, new inspecting records, and metadata such as strings and index entries.

## Stream payload and rendering

| Repositories | Items per completion message | Items across all completion messages | Frames | Repository rows formatted per frame | Repository rows formatted per refresh |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 12 | 12 → 1 | 144 → 12 | 14 | 12 → 9 | 168 → 126 |
| 60 | 60 → 1 | 3,600 → 60 | 62 | 60 → 9 | 3,720 → 558 |
| 200 | 200 → 1 | 40,000 → 200 | 202 | 200 → 9 | 40,400 → 1,818 |
| 1,000 | 1,000 → 1 | 1,000,000 → 1,000 | 1,002 | 1,000 → 9 | 1,002,000 → 9,018 |

The model also received the full repository count in the initial and final snapshots of every run. Payload counts describe what the model received. Passing the final result slice transfers its header and adds zero element copies. The formatting counter ran once per execution of the statement that builds a repository list row, including rows the baseline formatted offscreen. It excludes the workspace pane, column headers, and detail text.

## Reproduce

From a checkout containing both commits, run the following with Python 3, Go and Git available. The script instruments temporary Git archives, runs one refresh per size and revision, and prints the individual counters as CSV.

```sh
python internal/tui/testdata/dashboard_work_counts.py
```

The count run used Windows, Go 1.27.4 and the same deterministic `BenchmarkDashboardRefresh` source from `9a1f331` on both builds. It used one worker, an empty filter and one generated frame for each incoming message. Counters were reset after the fixture warm-up and read after processing the final snapshot. Each run checked every final repository ID and outcome, the expected frame count, and a constant number of formatted rows across its frames. Timing measurements used separate, uninstrumented binaries. [Counting script](../../internal/tui/testdata/dashboard_work_counts.py) · [Refresh benchmark](../../internal/tui/dashboard_benchmark_test.go)
