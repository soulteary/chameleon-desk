"""Behavioral tests for the check runner; no Go, npm, or device access required."""

import csv
import os
from pathlib import Path
import shutil
import signal
import subprocess
import tempfile
import time
import unittest


SOURCE = Path(__file__).resolve().parent


@unittest.skipUnless(os.name == "posix", "the check runner targets POSIX hosts")
class CheckTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="check runner ")
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.scripts = self.root / "scripts"
        self.scripts.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        self.trace = self.root / "commands.txt"
        self.ready = self.root / "ready"
        self.summary = self.root / "summary.md"
        for name in ("check.sh", "build.sh"):
            shutil.copy2(SOURCE / name, self.scripts / name)
        # A strict PATH makes missing-tool cases independent of the host setup.
        for name in ("dirname", "mkdir", "date", "cat", "tail", "cp", "sleep"):
            target = shutil.which(name)
            self.assertIsNotNone(target, name)
            (self.bin / name).symlink_to(target)
        self.write_tool("go", r'''#!/bin/sh
printf 'go|%s|%s|%s|%s|%s|%s\n' "$*" "${GOPROXY:-}" "${GOSUMDB:-}" "${CHAMELEON_TEST_PORT:-}" "${CHAMELEON_TEST_NO_CARD:-}" "${CGO_ENABLED:-}" >> "$CHECK_TRACE"
if [ "${BLOCK_OPERATION:-}" = "$1" ]; then
  printf ready > "$CHECK_READY"
  while :; do sleep 1; done
fi
if [ "${FAIL_OPERATION:-}" = "$1" ]; then
  printf 'deliberate Go failure\n' >&2
  exit "${FAIL_STATUS:-37}"
fi
case "$1" in
  test) printf 'test result\n'; printf 'mode: atomic\n' > reports/coverage.out ;;
  tool) printf 'total: 100%%\n' ;;
  build) cp chameleon-template chameleon ;;
esac
''')
        self.write_tool("node", r'''#!/bin/sh
printf 'node|%s\n' "$*" >> "$CHECK_TRACE"
exit "${NODE_STATUS:-0}"
''')
        self.write_tool("npm", r'''#!/bin/sh
printf 'npm|%s\n' "$*" >> "$CHECK_TRACE"
printf 'web result\n'
exit "${NPM_STATUS:-0}"
''')
        self.write_file(self.root / "chameleon-template", r'''#!/bin/sh
printf 'version|%s\n' "$*" >> "$CHECK_TRACE"
printf 'test version\n'
exit "${VERSION_STATUS:-0}"
''')
        self.env = os.environ.copy()
        self.env.update({
            "PATH": str(self.bin),
            "CARD_GO": str(self.bin / "go"),
            "CHECK_TRACE": str(self.trace),
            "CHECK_READY": str(self.ready),
            "GITHUB_STEP_SUMMARY": str(self.summary),
            "GOPROXY": "https://should-not-be-used.invalid",
            "GOSUMDB": "should-not-be-used",
            "CHAMELEON_TEST_PORT": "must-be-cleared",
            "CHAMELEON_TEST_NO_CARD": "must-be-cleared",
        })
        for name in ("FAIL_OPERATION", "FAIL_STATUS", "BLOCK_OPERATION", "NODE_STATUS", "NPM_STATUS", "VERSION_STATUS", "CGO_ENABLED"):
            self.env.pop(name, None)

    def write_file(self, path, text):
        path.write_text(text)
        path.chmod(0o755)

    def write_tool(self, name, text):
        self.write_file(self.bin / name, text)

    def run_check(self, *arguments, env=None):
        return subprocess.run(
            ["/bin/sh", str(self.scripts / "check.sh"), *arguments],
            cwd=self.root,
            env=self.env if env is None else env,
            text=True,
            capture_output=True,
            timeout=10,
        )

    def commands(self):
        return self.trace.read_text().splitlines() if self.trace.exists() else []

    def timings(self):
        with (self.root / "reports/check-timings.tsv").open() as stream:
            rows = list(csv.DictReader(stream, delimiter="\t"))
        for row in rows:
            self.assertEqual(set(row), {"stage", "duration_seconds", "status"})
            self.assertTrue(row["duration_seconds"].isdigit(), row)
        return [(r["stage"], r["status"]) for r in rows]

    def test_default_and_all_run_the_original_checks_in_order(self):
        for arguments in ((), ("all",)):
            with self.subTest(arguments=arguments):
                result = self.run_check(*arguments)
                self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
                calls = self.commands()[-9:]
                self.assertEqual([line.split("|")[0] for line in calls], ["go", "go", "node", "go", "go", "go", "npm", "go", "version"])
                self.assertEqual(calls[0].split("|")[1], "mod verify")
                self.assertEqual(calls[1].split("|")[1], "list -mod=readonly -deps -test ./...")
                self.assertEqual(calls[3].split("|")[1], "test -mod=readonly -race -count=1 -p 1 -coverpkg=./... -coverprofile=reports/coverage.out ./...")
                self.assertEqual(calls[4].split("|")[1], "tool cover -func=reports/coverage.out")
                self.assertEqual(calls[5].split("|")[1], "vet -mod=readonly ./...")
                self.assertEqual(calls[6], "npm|run test:webui")
                self.assertEqual(calls[7].split("|")[1], "build -mod=readonly -trimpath -o chameleon ./cmd/chameleon")
                self.assertEqual(calls[7].split("|")[-1], "0")
                for line in calls:
                    if line.startswith("go|"):
                        self.assertEqual(line.split("|")[2:6], ["off", "off", "", ""])
                self.assertEqual(self.timings()[-5:], [(name, "success") for name in ("deps", "go", "vet", "webui", "build")])
        text = (self.root / "reports/check-timings.tsv").read_text()
        self.assertEqual(text.count("stage\tduration_seconds\tstatus\n"), 1)
        self.assertEqual(len(self.timings()), 10)
        self.assertIn("`go`:", self.summary.read_text())
        self.assertTrue((self.root / "reports/go-mod-verify.txt").exists())
        self.assertIn("PASS", (self.root / "reports/go-vet.txt").read_text())
        self.assertIn("web result", (self.root / "reports/webui-tests.txt").read_text())

    def test_go_stages_do_not_require_node_or_npm(self):
        (self.bin / "node").unlink()
        (self.bin / "npm").unlink()
        for stage in ("deps", "go", "vet", "build"):
            with self.subTest(stage=stage):
                result = self.run_check(stage)
                self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
                self.assertEqual(self.timings()[-1], (stage, "success"))
        self.assertFalse(any(line.startswith(("node|", "npm|")) for line in self.commands()))

    def test_all_checks_webui_tools_before_starting_go_tests(self):
        (self.bin / "node").unlink()
        result = self.run_check()
        self.assertEqual(result.returncode, 1)
        self.assertIn("npm ci", result.stderr)
        self.assertEqual(self.timings(), [("deps", "success")])
        self.assertEqual([line.split("|")[1] for line in self.commands()], ["mod verify", "list -mod=readonly -deps -test ./..."])
        self.assertFalse((self.root / "reports/unit-tests.txt").exists())

    def test_invalid_timing_path_fails_before_running_checks(self):
        path = self.root / "reports/check-timings.tsv"
        path.mkdir(parents=True)
        (path / "child").write_text("existing entry")
        result = self.run_check("vet")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("check-timings.tsv", result.stderr)
        self.assertEqual(self.commands(), [])

    def test_webui_has_no_go_prerequisite(self):
        (self.bin / "go").unlink()
        result = self.run_check("webui")
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertEqual([line.split("|")[0] for line in self.commands()], ["node", "npm"])
        self.assertEqual(self.timings(), [("webui", "success")])
        self.assertFalse((self.root / "reports/go-mod-verify.txt").exists())

    def test_go_failures_preserve_exit_code_and_stop_later_stages(self):
        for operation, stage, expected_count in (("mod", "deps", 1), ("list", "deps", 1), ("test", "go", 2), ("tool", "go", 2), ("vet", "vet", 3), ("build", "build", 5)):
            with self.subTest(operation=operation):
                timing = self.root / "reports/check-timings.tsv"
                timing.unlink(missing_ok=True)
                self.trace.unlink(missing_ok=True)
                env = dict(self.env, FAIL_OPERATION=operation, FAIL_STATUS="37")
                result = self.run_check(env=env)
                self.assertEqual(result.returncode, 37, result.stderr + result.stdout)
                rows = self.timings()
                self.assertEqual(len(rows), expected_count)
                self.assertEqual(rows[-1], (stage, "failure"))
                self.assertNotIn("interrupted", str(rows))
                self.assertIn("failure, exit 37", result.stdout)
                if stage != "go" or operation != "tool":
                    self.assertIn("deliberate Go failure", result.stdout + result.stderr)
                if operation == "test":
                    self.assertFalse(any("tool cover" in call for call in self.commands()))

    def test_webui_failure_and_build_version_failure_are_not_reported_as_success(self):
        for stage, variable, code in (("webui", "NPM_STATUS", 29), ("build", "VERSION_STATUS", 41)):
            with self.subTest(stage=stage):
                result = self.run_check(stage, env=dict(self.env, **{variable: str(code)}))
                self.assertEqual(result.returncode, code, result.stderr + result.stdout)
                self.assertEqual(self.timings()[-1], (stage, "failure"))

    def test_missing_webui_dependency_fails_only_the_webui_stage(self):
        env = dict(self.env, NODE_STATUS="17")
        result = self.run_check("webui", env=env)
        self.assertEqual(result.returncode, 1)
        self.assertIn("npm ci", result.stderr)
        self.assertEqual(self.timings(), [("webui", "failure")])
        self.assertFalse(any(line.startswith("npm|") for line in self.commands()))

    def test_summary_is_optional_and_failed_summary_write_does_not_mask_test_exit(self):
        env = dict(self.env)
        env.pop("GITHUB_STEP_SUMMARY")
        result = self.run_check("vet", env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertFalse(self.summary.exists())
        result = self.run_check("go", env=dict(self.env, FAIL_OPERATION="test", FAIL_STATUS="37", GITHUB_STEP_SUMMARY=str(self.root / "missing/summary.md")))
        self.assertEqual(result.returncode, 37)
        self.assertEqual(self.timings()[-1], ("go", "failure"))

    def test_invalid_selection_and_extra_arguments_do_not_start_checks(self):
        for arguments in (("unknown",), ("go", "webui")):
            with self.subTest(arguments=arguments):
                result = self.run_check(*arguments)
                self.assertEqual(result.returncode, 2)
                self.assertIn("Usage:", result.stderr)
                self.assertEqual(self.commands(), [])
                self.assertFalse((self.root / "reports").exists())
        result = self.run_check("--help")
        self.assertEqual(result.returncode, 0)
        self.assertIn("Usage:", result.stdout)

    def test_interrupted_stage_records_interruption_and_stops(self):
        env = dict(self.env, BLOCK_OPERATION="test")
        process = subprocess.Popen(
            ["/bin/sh", str(self.scripts / "check.sh"), "go"],
            cwd=self.root, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            text=True, start_new_session=True,
        )
        try:
            deadline = time.monotonic() + 5
            while not self.ready.exists() and process.poll() is None and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertTrue(self.ready.exists(), "stub did not start")
            os.killpg(process.pid, signal.SIGTERM)
            stdout, stderr = process.communicate(timeout=5)
            self.assertEqual(process.returncode, 143, stdout + stderr)
            self.assertEqual(self.timings(), [("go", "interrupted")])
            self.assertNotIn("success", stdout)
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.communicate()


if __name__ == "__main__":
    unittest.main()
