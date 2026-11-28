import base64
import csv
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class CaptureReplayTest(unittest.TestCase):
    def test_capture_directory(self):
        script = Path(__file__).with_name("replay_dashboard_writer.py")
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary, "captures")

            def run():
                return subprocess.run([sys.executable, script, directory],
                                      capture_output=True, text=True)

            for state in ("missing", "empty"):
                with self.subTest(state=state):
                    if state == "empty":
                        directory.mkdir()
                    result = run()
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(result.stdout, "")

            frame = (b"service-000 [clean]\r\nWorkspace: feature-review | Observed 12:01:00\r\n"
                     b"60 repos | 10 clean | 20 need attention\r\n")
            capture = [{"data": base64.b64encode(frame).decode(), "elapsed_ns": 1000000}]
            (directory / "before_1_4_abcdef.json").write_text(json.dumps(capture))
            result = run()
            self.assertEqual(result.returncode, 0, result.stderr)
            rows = list(csv.reader(result.stdout.splitlines()))
            self.assertEqual(len(rows), 2)
            self.assertEqual(rows[1], ["before", "1", "4", "abcdef", "", "1.000", "1.000"])


if __name__ == "__main__":
    unittest.main()
