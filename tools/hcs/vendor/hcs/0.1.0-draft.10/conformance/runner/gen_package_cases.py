#!/usr/bin/env python3
"""Generate the archive member safety cases of HCS-0001 (R019 to R023).

Usage: python3 conformance/runner/gen_package_cases.py [--check]

Every archive is written byte by byte, because a tar library refuses to write absolute names,
`..` segments, NUL bytes, links and devices. The gzip layer uses stored deflate blocks, so the
bytes do not depend on the zlib release. `--check` exits 1 when committed cases differ.
"""
import base64
import hashlib
import json
import pathlib
import struct
import sys
import zlib

ROOT = pathlib.Path(__file__).resolve().parents[2]
CASES = ROOT / "conformance" / "cases" / "HCS-0001"
ENTRY_ID = "dev.example.a"
BLOCK = 512


def header(name, kind="0", size=0, prefix="", linkname="", raw_name=None):
    h = bytearray(BLOCK)
    h[0:100] = (raw_name if raw_name is not None else name.encode())[:100].ljust(100, b"\0")
    h[100:108] = b"0000644\0"
    h[108:116] = b"0000000\0"
    h[116:124] = b"0000000\0"
    h[124:136] = b"%011o\0" % size
    h[136:148] = b"00000000000\0"
    h[148:156] = b" " * 8
    h[156:157] = kind.encode()
    h[157:157 + len(linkname)] = linkname.encode()
    h[257:263] = b"ustar\0"
    h[263:265] = b"00"
    h[345:345 + len(prefix)] = prefix.encode()
    h[148:156] = b"%06o\0 " % sum(h)
    return bytes(h)


def member(name, kind="0", data=b"", **kw):
    pad = b"\0" * (-len(data) % BLOCK)
    return header(name, kind, len(data), **kw) + data + pad


def tar(*members):
    return b"".join(members) + b"\0" * (2 * BLOCK)


def gz(data):
    body = zlib.compressobj(0, zlib.DEFLATED, -15)
    deflated = body.compress(data) + body.flush()
    return b"\x1f\x8b\x08\x00\0\0\0\0\x00\xff" + deflated + struct.pack("<II", zlib.crc32(data), len(data) & 0xFFFFFFFF)


def case(description, requirement, archive, errors=None, **extra):
    data = archive if extra.pop("raw", False) or archive.startswith(b"\x1f\x8b") else gz(archive)
    entry = {"id": ENTRY_ID, "size": len(data), "contentHash": "sha256:" + hashlib.sha256(data).hexdigest()}
    entry.update(extra.pop("entry", {}))
    inp = {"entry": entry, "contentBase64": base64.b64encode(data).decode(), "unpack": True}
    inp.update(extra)
    expect = {"outcome": "invalid", "errors": [{"code": c, "entry": ENTRY_ID} for c in errors]} if errors else {"outcome": "valid", "errors": []}
    return {"description": description, "requirements": [requirement], "level": "integrity", "input": inp, "expect": expect}


PI = ["package_invalid"]
GOOD = member("SKILL.md", data=b"# skill\n")
R19, R20, R21, R22, R23 = (f"HCS-0001-R0{n}" for n in (19, 20, 21, 22, 23))
FILE = member("ok", data=b"x")


def hidden_nul():
    return header("safe", raw_name=b"safe\0hidden")


def build():
    out = {}

    def add(requirement, polarity, slug, *args, **kw):
        out[f"{requirement}/{polarity}/{slug}.json"] = case(*args[:1], requirement, *args[1:], **kw)

    # R019 paths
    for slug, desc, mem in [
        ("absolute", "A member with an absolute path is refused.", member("/etc/passwd", data=b"x")),
        ("dotdot", "A member with a .. segment is refused.", member("a/../../b", data=b"x")),
        ("dotdot-leading", "A member whose path starts with .. is refused.", member("../b", data=b"x")),
        ("empty", "A member with an empty name is refused.", member("", data=b"x")),
        ("root-dot", "A directory member that names only the root is an empty path.", member("./", "5")),
        ("nul", "A name with bytes hidden after a NUL is refused.", hidden_nul()),
        ("backslash", "A backslash in a path is refused.", member("a\\b", data=b"x")),
        ("whiteout", "A path component that starts with .wh. is refused.", member("d/.wh.old", data=b"")),
        ("opaque-whiteout", "The OCI opaque whiteout name is refused.", member(".wh..wh..opq")),
    ]:
        add(R19, "invalid", slug, desc, tar(FILE, mem), PI)
    add(R19, "valid", "ordinary-paths", "Dots and wh inside a component are not whiteouts or traversal.",
        tar(member("./a.wh.b", data=b"x"), member("..a", data=b"y"), member("dir/", "5"), member("dir/.whx", data=b"z")))

    # R020 types
    for slug, desc, mem in [
        ("symlink", "A symbolic link member is refused, even when its target stays inside the root.", member("link", "2", linkname="ok")),
        ("hardlink", "A hard link member is refused.", member("link", "1", linkname="ok")),
        ("char-device", "A character device member is refused.", member("dev", "3")),
        ("block-device", "A block device member is refused.", member("dev", "4")),
        ("fifo", "A FIFO member is refused.", member("pipe", "6")),
        ("pax-header", "An extended header record is refused.", member("pax", "x", data=b"20 path=" + b"a" * 4 + b"\n")),
        ("directory-data", "A directory member that carries data is refused.", member("d/", "5", data=b"data")),
    ]:
        add(R20, "invalid", slug, desc, tar(FILE, mem), PI)
    add(R20, "valid", "files-and-directories", "Regular files, including the legacy NUL type flag, and directories are accepted.",
        tar(member("d/", "5"), member("d/f", data=b"x"), member("legacy", "\0", data=b"y")))

    # R021 duplicates and members below a file
    add(R21, "invalid", "duplicate-file", "A path equal to an earlier member's path is refused.", tar(member("a", data=b"1"), member("a", data=b"2")), PI)
    add(R21, "invalid", "duplicate-directory-and-file", "A directory and a file with the same path are duplicates.", tar(member("a/", "5"), member("a", data=b"x")), PI)
    add(R21, "invalid", "duplicate-after-dot", "Paths are compared after resolving . segments.", tar(member("a/b", data=b"1"), member("a/./b", data=b"2")), PI)
    add(R21, "invalid", "below-file", "A member below a regular file is refused.", tar(member("a", data=b"1"), member("a/b", data=b"2")), PI)
    add(R21, "invalid", "below-file-reversed", "A member below a regular file is refused whatever the order.", tar(member("a/b", data=b"2"), member("a", data=b"1")), PI)
    add(R21, "valid", "distinct-paths", "Distinct paths, with a directory before the files below it, are accepted.",
        tar(member("a/", "5"), member("a/b", data=b"1"), member("a2", data=b"2")))

    # R022 order and atomicity (the filesystem effect is a host-level check)
    unsafe = gz(tar(member("/abs", data=b"x")))
    add(R22, "invalid", "hash-mismatch-first", "An unsafe member does not hide a transport digest mismatch: integrity_mismatch comes first.",
        unsafe, ["integrity_mismatch"], entry={"contentHash": "sha256:" + "0" * 64})
    add(R22, "invalid", "size-mismatch-first", "An unsafe member does not hide a size mismatch.",
        unsafe, ["integrity_mismatch"], entry={"size": len(unsafe) + 1})
    add(R22, "invalid", "content-id-mismatch-first", "An unsafe member does not hide a declared contentId mismatch.",
        unsafe, ["integrity_mismatch"], entry={"contentId": "sha256:" + "0" * 64})
    plain = tar(GOOD)
    add(R22, "valid", "safe-package-with-content-id", "A safe package whose transport digest and contentId match is accepted.",
        plain, entry={"contentId": "sha256:" + hashlib.sha256(plain).hexdigest()})

    # R023 unparseable tar and host limits
    broken = bytearray(tar(GOOD))
    broken[0] ^= 1
    add(R23, "invalid", "bad-checksum", "A header whose checksum does not match is not a tar.", bytes(broken), PI)
    add(R23, "invalid", "truncated", "A tar cut inside a member is not a tar.", tar(GOOD)[:BLOCK + 3], PI)
    add(R23, "invalid", "missing-terminator", "A tar without the two zero blocks is not a tar.", member("a", data=b"x"), PI)
    add(R23, "invalid", "not-gzip-or-tar", "Verified bytes that are not a gzip stream cannot be unpacked.", b"hello hcz\n", PI, raw=True)
    add(R23, "invalid", "member-limit", "More members than the host's limit are refused.", tar(member("a"), member("b")), PI, maxMembers=1)
    add(R23, "invalid", "size-limit", "More unpacked bytes than the host's limit are refused.", tar(member("a", data=b"12345")), PI, maxUnpackedBytes=4)
    add(R23, "valid", "at-limits", "A package exactly at the host's limits is accepted.", tar(member("a", data=b"1234"), member("b")), maxMembers=2, maxUnpackedBytes=4)
    return out


def main(argv):
    cases, bad = build(), []
    for rel, doc in cases.items():
        path = CASES / rel
        text = json.dumps(doc, indent=2) + "\n"
        if "--check" in argv:
            if not path.exists() or path.read_text() != text:
                bad.append(str(path.relative_to(ROOT)))
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(text)
    if bad:
        print("drift:", *bad, sep="\n  ")
        return 1
    print(f"{len(cases)} package cases {'match' if '--check' in argv else 'written'}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
