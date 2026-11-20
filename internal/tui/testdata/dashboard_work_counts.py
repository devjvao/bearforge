"""Measure dashboard work in disposable, instrumented copies of the two releases."""
import csv
import io
from pathlib import Path
import subprocess
import sys
import tempfile
import zipfile


BASELINE = "9fa2c94"
CANDIDATE = "9a1f331"
HARNESS = "internal/tui/dashboard_benchmark_test.go"
FIELDS = (
    "initial_clones worker_input_clones status_result_writes progress_clones dashboard_row_writes filter_copies "
    "initial_payload completion_payload max_completion_payload final_payload frames rows_per_frame formatted_rows"
).split()


def replace_once(root, path, original, replacement):
    path = root / path
    content = path.read_text(encoding="utf-8")
    if content.count(original) != 1:
        raise ValueError(f"{path}: expected exactly one occurrence of {original!r}")
    path.write_text(content.replace(original, replacement), encoding="utf-8")


def instrument(root, revision):
    (root / "internal/app/dashboard_work_probe.go").write_text(
        """package app

// Each field has one writer. The benchmark reads them after the final message.
type DashboardBenchCounts struct {
    Initial, Workers, StatusResults, Progress, RowWrites, Filter, Formatted int
}
var DashboardBench DashboardBenchCounts
""", encoding="utf-8")

    def before(path, statement, count):
        replace_once(root, path, statement, count + "\n" + statement)

    status = "internal/app/status.go"
    before(status, "onStart(slices.Clone(r.Items))", "DashboardBench.Initial += len(r.Items)")
    before(status, "inputs := slices.Clone(r.Items)", "DashboardBench.Workers += len(r.Items)")
    before(status, "r.Items[result.index] = result.item", "DashboardBench.StatusResults++")
    old = revision == BASELINE
    source = "snapshot.Items" if old else "current.Items"
    before("internal/app/dashboard.go", f"update.Items = slices.Clone({source})",
           f"DashboardBench.Progress += len({source})")
    if old:
        before("internal/app/dashboard.go", "snapshot.Items[indices[item.Repository]] = item",
               "DashboardBench.RowWrites++")
        before("internal/tui/dashboard.go", "rows = append(rows, item)", "app.DashboardBench.Filter++")
    else:
        before("internal/tui/dashboard_repositories.go", "m.data.Items[i] = item",
               "app.DashboardBench.RowWrites++")
    before("internal/tui/dashboard_view.go", "line := prefix + padded(displayText(item.Repository), repoWidth)",
           "app.DashboardBench.Formatted++")

    # Use the same 100x32 benchmark and outcome/order checks on both revisions.
    # Counters cover a single refresh, starting after the benchmark's warm-up.
    replace_once(root, HARNESS, "for b.Loop() {", """for b.Loop() {
        app.DashboardBench = app.DashboardBenchCounts{}
        var initial, completions, maxCompletion, final, rowsPerFrame int""")
    row_payload = "" if old else "if msg.item != nil { n++ }"
    replace_once(root, HARNESS, "updated, cmd := m.Update(next())", """msg := next().(dashboardLoadedMsg)
        n := len(msg.snapshot.Items)
        """ + row_payload + """
        switch {
        case frames == 0:
            initial = n
        case msg.partial:
            completions += n
            maxCompletion = max(maxCompletion, n)
        default:
            final = n
        }
        updated, cmd := m.Update(msg)""")
    replace_once(root, HARNESS, "frame := m.View().Content", """previous := app.DashboardBench.Formatted
        frame := m.View().Content
        count := app.DashboardBench.Formatted - previous
        if frames == 0 {
            rowsPerFrame = count
        } else if count != rowsPerFrame {
            b.Fatalf("formatted %d rows in frame %d, initially %d", count, frames, rowsPerFrame)
        }""")
    before(HARNESS, "m.reader.close()", r"""c := app.DashboardBench
        fmt.Printf("WORK %s %d %d %d %d %d %d %d %d %d %d %d %d %d\n", b.Name(),
            c.Initial, c.Workers, c.StatusResults, c.Progress, c.RowWrites, c.Filter, initial,
            completions, maxCompletion, final, frames, rowsPerFrame, c.Formatted)""")


def main():
    root = Path(subprocess.check_output(
        ["git", "rev-parse", "--show-toplevel"], text=True).strip())
    harness = subprocess.check_output(["git", "show", f"{CANDIDATE}:{HARNESS}"], cwd=root)
    out = csv.writer(sys.stdout, lineterminator="\n")
    out.writerow(["revision", "repositories", *FIELDS])
    with tempfile.TemporaryDirectory() as tmp:
        for revision in [BASELINE, CANDIDATE]:
            files = subprocess.check_output(["git", "archive", "--format=zip", revision], cwd=root)
            tree = Path(tmp, revision)
            with zipfile.ZipFile(io.BytesIO(files)) as archive:
                archive.extractall(tree)
            (tree / HARNESS).write_bytes(harness)
            instrument(tree, revision)
            result = subprocess.run(["go", "test", "./internal/tui", "-run=^$",
                                     "-bench=^BenchmarkDashboardRefresh$", "-benchtime=1x",
                                     "-count=1", "-timeout=20m"], cwd=tree, stdout=subprocess.PIPE,
                                    stderr=subprocess.STDOUT, text=True)
            if result.returncode:
                raise RuntimeError(result.stdout)
            sizes = []
            for line in result.stdout.splitlines():
                if "WORK BenchmarkDashboardRefresh/repos=" in line:
                    label, *numbers = line.split("WORK ")[1].split()
                    size = int(label.rsplit("=", 1)[1])
                    if len(numbers) != len(FIELDS):
                        raise ValueError(line)
                    sizes.append(size)
                    out.writerow([revision, size, *numbers])
            if sizes != [12, 60, 200, 1000]:
                raise ValueError(f"unexpected benchmark output: {result.stdout}")


if __name__ == "__main__":
    main()
