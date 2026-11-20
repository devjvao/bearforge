"""Replay first-use Bubble Tea writes at 100x32. Requires pyte 0.8.2."""

import base64
import csv
import json
from pathlib import Path
import re
import sys

import pyte


class DashboardScreen(pyte.Screen):
    # pyte 0.8.2 implements IND/RI, but not the CSI S/T forms Bubble Tea
    # uses to scroll a pane. These operations preserve the cursor position.
    def scroll_up(self, count=1):
        saved = self.cursor.y
        self.cursor.y = self.margins.bottom if self.margins else self.lines - 1
        for _ in range(count or 1):
            self.index()
        self.cursor.y = saved

    def scroll_down(self, count=1):
        saved = self.cursor.y
        self.cursor.y = self.margins.top if self.margins else 0
        for _ in range(count or 1):
            self.reverse_index()
        self.cursor.y = saved


class DashboardStream(pyte.ByteStream):
    csi = {**pyte.ByteStream.csi, "S": "scroll_up", "T": "scroll_down"}


def measure(path):
    screen = DashboardScreen(100, 32)
    stream = DashboardStream(screen)
    inspecting = completed = final = None
    for write in json.loads(path.read_text()):
        data = base64.b64decode(write["data"])
        # A bare LF after the title means the renderer expects ONLCR. With
        # explicit CRLF (Windows), a bare LF moves down in the same column.
        if b"Bearforge / Workspace dashboard\n" in data:
            screen.set_mode(pyte.modes.LNM)
        stream.feed(data)
        elapsed = write["elapsed_ns"] / 1e6
        # A terminal write can contain just a cursor move and a few changed
        # characters. Match reconstructed repository rows, excluding details.
        rows = []
        for line in screen.display:
            if "Details:" in line:
                break
            row = re.search(r"\bservice-(\d{3})\b.*?\[(inspecting|clean|changes)\]", line)
            if row:
                if ((row[2] == "clean" and int(row[1]) % 6 != 0) or
                        (row[2] == "changes" and int(row[1]) % 6 not in (1, 2, 3))):
                    raise ValueError(f"{path}: status on the wrong repository row: {line}")
                rows.append(row[2])
        if inspecting is None and "inspecting" in rows:
            inspecting = elapsed
        if completed is None and any(row in ("clean", "changes") for row in rows):
            completed = elapsed
        if any(re.search(r"Workspace: feature-review\s+\|\s+Observed \d\d:\d\d:\d\d", line)
               for line in screen.display):
            if completed is None or not any("60 repos | 10 clean | 20 need attention" in line
                                            for line in screen.display):
                raise ValueError(f"{path}: unexpected final dashboard")
            final = elapsed
            break
    if completed is None or final is None:
        raise ValueError(f"{path}: missing completed repository row or final observation")
    # The renderer may coalesce the initial inspecting frame with a later one.
    return ["" if value is None else f"{value:.3f}" for value in (inspecting, completed, final)]


if __name__ == "__main__":
    writer = csv.writer(sys.stdout, lineterminator="\n")
    writer.writerow(["comparison", "pair", "jobs", "commit", "inspecting_ms",
                     "first_completed_row_ms", "final_refresh_ms"])
    for path in sorted(Path(sys.argv[1]).glob("*.json")):
        comparison, pair, jobs, commit = path.stem.split("_")
        writer.writerow([comparison, pair, jobs, commit, *measure(path)])
