"""Reference check of package archive members (HCS-0001-R019 to R023).

The tar is read by hand rather than with `tarfile`, which normalizes names and skips entries a
host must refuse. `check_archive` returns None when the archive is acceptable, otherwise a
violation `{"requirement", "reason", "member"}`. Reasons are local strings, not a vocabulary.
"""
import posixpath
import zlib

BLOCK = 512
REGULAR = {"0", "\0"}
DIRECTORY = "5"


class Malformed(Exception):
    pass


def _field(header, start, size):
    raw = header[start:start + size]
    nul = raw.find(b"\0")
    if nul >= 0 and any(raw[nul + 1:]):
        return None  # bytes hidden after a NUL
    return raw[:nul if nul >= 0 else size].decode("utf-8", "surrogateescape")


def _octal(header, start, size):
    text = header[start:start + size].split(b"\0")[0].decode("ascii", "replace").strip()
    if text and not all(c in "01234567" for c in text):
        raise Malformed("numeric field")
    return int(text, 8) if text else 0


def read_members(tar):
    """Parse a tar into [(name or None when the name hides bytes after a NUL, type, size)]."""
    if len(tar) % BLOCK:
        raise Malformed("truncated")
    members, offset = [], 0
    while offset + BLOCK <= len(tar):
        header = tar[offset:offset + BLOCK]
        if not any(header):
            if offset + 2 * BLOCK > len(tar) or any(tar[offset:]):
                raise Malformed("terminator")
            return members
        if _octal(header, 148, 8) != sum(header[:148]) + 32 * 8 + sum(header[156:]):
            raise Malformed("checksum")
        name, prefix = _field(header, 0, 100), _field(header, 345, 155)
        size = _octal(header, 124, 12)
        start = offset + BLOCK
        if start + -(-size // BLOCK) * BLOCK > len(tar):
            raise Malformed("truncated member")
        full = None if name is None or prefix is None else (prefix + "/" + name if prefix else name)
        members.append((full, chr(header[156] or 48), size))
        offset = start + -(-size // BLOCK) * BLOCK
    raise Malformed("terminator")


def unpack_cap(max_bytes, max_members):
    return min(max_bytes + max_members * 1023 + 1024, 0x7FFFFFFF)


def gunzip(data, cap):
    """Decompress at most `cap` bytes; raises Malformed on bad gzip data or when the cap is exceeded."""
    stream = zlib.decompressobj(31)
    try:
        out = stream.decompress(data, cap + 1)
        if stream.unconsumed_tail or len(out) > cap or not stream.eof:
            raise Malformed("decompression")
    except zlib.error:
        raise Malformed("gzip")
    return out


def _violation(requirement, reason, member):
    return {"requirement": requirement, "reason": reason, "member": member}


def check_archive(data, max_bytes=256 * 1024 * 1024, max_members=10000):
    try:
        members = read_members(gunzip(data, unpack_cap(max_bytes, max_members)))
    except Malformed as exc:
        return _violation("HCS-0001-R023", "malformed " + str(exc), None)
    if len(members) > max_members:
        return _violation("HCS-0001-R023", "member limit", None)
    total, seen, files, parents = 0, set(), set(), set()
    for name, kind, size in members:
        shown = name if name is not None else "(name with hidden bytes)"
        if name is None or "\0" in name or "\\" in name or name.startswith("/") or ".." in name.split("/"):
            return _violation("HCS-0001-R019", "invalid path", shown)
        normal = posixpath.normpath(name) if name else "."
        if normal == ".":
            return _violation("HCS-0001-R019", "empty path", shown)
        if any(part.startswith(".wh.") for part in name.split("/")):
            return _violation("HCS-0001-R019", "whiteout", shown)
        if kind not in REGULAR and kind != DIRECTORY:
            return _violation("HCS-0001-R020", "unsupported type", shown)
        if kind == DIRECTORY and size:
            return _violation("HCS-0001-R020", "directory data", shown)
        if normal in seen:
            return _violation("HCS-0001-R021", "duplicate path", shown)
        seen.add(normal)
        pieces = normal.split("/")
        ancestors = {"/".join(pieces[:i]) for i in range(1, len(pieces))}
        if ancestors & files or (kind != DIRECTORY and normal in parents):
            return _violation("HCS-0001-R021", "below regular file", shown)
        parents |= ancestors
        if kind != DIRECTORY:
            files.add(normal)
        total += size
        if total > max_bytes:
            return _violation("HCS-0001-R023", "size limit", None)
    return None
