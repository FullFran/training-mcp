"""Deterministic tests for the external system-under-test interface (conformance/README.md, "System under test").

Run: python3 -B -m unittest discover -s conformance/runner -p test_sut.py
"""
import contextlib
import io
import json
import pathlib
import shutil
import sys
import tempfile
import unittest

import run
import sut

CLIENT_CASE = run.ROOT / "conformance/cases/HCS-0006/HCS-0006-R006/valid/problem-hcserror.json"
HOST_CASE = run.ROOT / "conformance/cases/HCS-0003/HCS-0003-R001/valid/resolve-without-execution.json"

# Each fake reads the request from stdin and behaves in one way.
PRELUDE = "import json, sys\nreq = json.load(sys.stdin)\nV = req['protocolVersion']\n"
FAKES = {
    "pass": "print(json.dumps({'protocolVersion': V, 'status': 'ok', 'observed': req['case']['expect']}))",
    "wrong": "print(json.dumps({'protocolVersion': V, 'status': 'ok', 'observed': {'hcsError': None}}))",
    "crash": "sys.stderr.write('boom'); sys.exit(3)",
    "garbage": "print('this is not json')",
    "hang": "import time; time.sleep(60)",
    "not-implemented": "print(json.dumps({'protocolVersion': V, 'status': 'not-implemented', 'reason': 'no client level'}))",
    "not-applicable": "print(json.dumps({'protocolVersion': V, 'status': 'not-applicable'}))",
    "wrong-version": "print(json.dumps({'protocolVersion': '9.9', 'status': 'ok', 'observed': {}}))",
    "missing-observed": "print(json.dumps({'protocolVersion': V, 'status': 'ok'}))",
    "two-documents": "print('{}'); print('{}')",
}


class Target(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = pathlib.Path(tempfile.mkdtemp())
        cls.cases = cls.tmp / "cases"
        (cls.cases / "valid").mkdir(parents=True)
        shutil.copy(CLIENT_CASE, cls.cases / "valid" / "client.json")
        shutil.copy(HOST_CASE, cls.cases / "valid" / "host.json")
        cls.scripts = {}
        for name, body in FAKES.items():
            path = cls.tmp / (name + ".py")
            path.write_text(PRELUDE + body + "\n", encoding="utf-8")
            cls.scripts[name] = path

    @classmethod
    def tearDownClass(cls):
        shutil.rmtree(cls.tmp)

    def run_against(self, name, *extra):
        report = self.tmp / (name + ".report.json")
        argv = ["--cases", str(self.cases), "--timeout", "1", "--report", str(report), *extra,
                "--sut", sys.executable, str(self.scripts[name])]
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = sut.main(argv, run)
        counts = json.loads(report.read_text())["counts"]
        return code, counts, out.getvalue(), json.loads(report.read_text())

    def expect(self, name, code, **counts):
        got_code, got, out, _ = self.run_against(name)
        base = {"pass": 0, "fail": 0, "crash": 0, "not-implemented": 0, "not-applicable": 0, "pending": 1, "malformed": 0}
        base.update(counts)
        self.assertEqual(got, base, out)
        self.assertEqual(got_code, code, out)
        return out

    def test_pass(self):
        self.expect("pass", 0, **{"pass": 1})

    def test_wrong_result_is_a_failed_expectation(self):
        self.expect("wrong", 1, fail=1)

    def test_non_zero_exit_is_a_crash(self):
        self.expect("crash", 1, crash=1)

    def test_garbage_output_is_a_crash(self):
        self.expect("garbage", 1, crash=1)

    def test_timeout_is_a_crash(self):
        out = self.expect("hang", 1, crash=1)
        self.assertIn("timed out", out)

    def test_not_implemented_is_not_a_pass(self):
        self.expect("not-implemented", 0, **{"not-implemented": 1})
        code, _, _, _ = self.run_against("not-implemented", "--strict")
        self.assertEqual(code, 1)

    def test_not_applicable_is_not_a_pass(self):
        self.expect("not-applicable", 0, **{"not-applicable": 1})

    def test_protocol_violations_are_crashes(self):
        for name in ("wrong-version", "missing-observed", "two-documents"):
            with self.subTest(name):
                self.expect(name, 1, crash=1)

    def test_stderr_is_shown_for_a_crash_and_kept_in_the_report(self):
        _, _, out, report = self.run_against("crash")
        self.assertIn("boom", out)
        crashed = [r for r in report["results"] if r["status"] == "crash"][0]
        self.assertEqual(crashed["stderr"], "boom")

    def test_summary_names_every_outcome(self):
        _, _, out, _ = self.run_against("pass")
        for word in ("pass 1", "fail 0", "crash 0", "not-implemented 0", "not-applicable 0", "pending(host) 1"):
            self.assertIn(word, out)

    def test_request_carries_the_case_as_is(self):
        self.assertEqual(sut.build_request({"level": "client"})["case"], {"level": "client"})


@unittest.skipUnless(shutil.which("node"), "node is not installed")
class Example(unittest.TestCase):
    def test_minimal_node_adapter_passes_what_it_implements_and_declines_the_rest(self):
        tmp = pathlib.Path(tempfile.mkdtemp())
        try:
            for src in (run.ROOT / "conformance/cases/HCS-0005/HCS-0005-R007/invalid/denial-wins-over-grant.json",
                        run.ROOT / "conformance/cases/HCS-0005/HCS-0005-R001/valid/granted.json", CLIENT_CASE):
                (tmp / src.parent.name).mkdir(exist_ok=True)
                shutil.copy(src, tmp / src.parent.name / src.name)
            report = tmp / "report.json"
            adapter = run.ROOT / "conformance/examples/minimal-sut.mjs"
            with contextlib.redirect_stdout(io.StringIO()):
                code = sut.main(["--cases", str(tmp), "--report", str(report), "--sut", "node", str(adapter)], run)
            counts = json.loads(report.read_text())["counts"]
        finally:
            shutil.rmtree(tmp)
        self.assertEqual((code, counts["pass"], counts["not-implemented"], counts["fail"] + counts["crash"]), (0, 2, 1, 0))


class Reference(unittest.TestCase):
    def test_every_case_through_the_reference_adapter_matches_the_in_process_run(self):
        adapter = pathlib.Path(run.__file__).with_name("reference_sut.py")
        report = pathlib.Path(tempfile.mkdtemp()) / "report.json"
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = sut.main(["--timeout", "60", "--report", str(report), "--sut", sys.executable, str(adapter)], run)
        self.assertEqual(code, 0, out.getvalue())
        results = json.loads(report.read_text())["results"]
        shutil.rmtree(report.parent)
        self.assertGreater(len(results), 300)
        for item in results:
            path = run.ROOT / item["case"]
            case = json.loads(path.read_text(encoding="utf-8"))
            self.assertEqual(item["status"], run.run_case(case, path.parent.name)[0], item["case"])


if __name__ == "__main__":
    unittest.main()
