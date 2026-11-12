"""Focused tests for the reference member check (HCS-0001-R019 to R023).

Run: python3 -B -m unittest discover -s conformance/runner -p test_package_members.py
"""
import base64
import unittest

import gen_package_cases as gen
import package


class PackageMembers(unittest.TestCase):
    def test_violation_names_the_requirement_of_the_case_directory(self):
        for rel, doc in gen.build().items():
            requirement, polarity, _ = rel.split("/")
            inp = doc["input"]
            found = package.check_archive(base64.b64decode(inp["contentBase64"]), inp.get("maxUnpackedBytes", 256 << 20), inp.get("maxMembers", 10000))
            if requirement == "HCS-0001-R022":  # ordering cases fail at the transport check, not here
                continue
            if polarity == "valid":
                self.assertIsNone(found, rel)
            else:
                self.assertEqual(found["requirement"], requirement, rel)

    def test_reason_and_member_are_reported(self):
        archive = gen.gz(gen.tar(gen.member("a/.wh.b")))
        self.assertEqual(package.check_archive(archive), {"requirement": "HCS-0001-R019", "reason": "whiteout", "member": "a/.wh.b"})
        archive = gen.gz(gen.tar(gen.member("a", data=b"1"), gen.member("a/b")))
        self.assertEqual(package.check_archive(archive)["reason"], "below regular file")

    def test_hidden_bytes_after_nul_are_not_echoed(self):
        found = package.check_archive(gen.gz(gen.tar(gen.hidden_nul())))
        self.assertEqual((found["requirement"], found["reason"]), ("HCS-0001-R019", "invalid path"))
        self.assertEqual(found["member"], "(name with hidden bytes)")


if __name__ == "__main__":
    unittest.main()
