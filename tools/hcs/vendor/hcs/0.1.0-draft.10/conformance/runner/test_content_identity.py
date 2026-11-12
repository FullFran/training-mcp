"""Focused deterministic tests for the public integrity runner interface.

Run: python3 -B -m unittest discover -s conformance/runner -p test_content_identity.py
"""
import base64
import gzip
import hashlib
import unittest

import run


def digest(data, algorithm="sha256"):
    return algorithm + ":" + hashlib.new(algorithm, data).hexdigest()


def check(data, identity=None, **changes):
    entry = {"id": "dev.example.a", "size": len(data), "contentHash": digest(data)}
    if identity is not None:
        entry["contentId"] = identity
    entry.update(changes)
    return run.check_integrity({"entry": entry, "contentBase64": base64.b64encode(data).decode()})


class ContentIdentity(unittest.TestCase):
    def test_encodings_and_exact_tar_stream(self):
        tar = bytes(1024)
        for level in (1, 9):
            for algorithm in ("sha256", "sha512"):
                self.assertEqual(check(gzip.compress(tar, compresslevel=level, mtime=0), digest(tar, algorithm)), [])
        self.assertEqual(check(gzip.compress(tar + bytes(512), mtime=0), digest(tar)), [run.err("integrity_mismatch", "dev.example.a")])

    def test_rejection_and_absence(self):
        self.assertEqual(check(b"not gzip"), [])
        for data, identity in [(b"not gzip", digest(bytes(1024))), (gzip.compress(bytes(1024), mtime=0), digest(b"wrong"))]:
            self.assertEqual(check(data, identity), [run.err("integrity_mismatch", "dev.example.a")])
        self.assertEqual(check(b"not gzip", digest(b"wrong"), size=0), [run.err("integrity_mismatch", "dev.example.a")])

    def test_decompression_limit(self):
        # The runner's declared limit is injectable by an integrity fixture.
        data = gzip.compress(bytes(1024), mtime=0)
        inp = {"entry": {"id": "dev.example.a", "size": len(data), "contentHash": digest(data), "contentId": digest(bytes(1024))}, "contentBase64": base64.b64encode(data).decode(), "maxUncompressedBytes": 1023}
        self.assertEqual(run.check_integrity(inp), [run.err("integrity_mismatch", "dev.example.a")])
        inp["maxUncompressedBytes"] = 1024
        self.assertEqual(run.check_integrity(inp), [])


if __name__ == "__main__":
    unittest.main()
