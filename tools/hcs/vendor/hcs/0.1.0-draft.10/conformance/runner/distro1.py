"""Reference semantics for HCS-0007 (DISTRO-1): canonical form, signature input, Ed25519, verification.

Written for the reference runner only (ADR-0015): readable and dependency-free, not constant-time
and not meant for production signing. Ed25519 follows RFC 8032 section 5.1 (pure Ed25519, cofactorless
verification equation); canonical JSON follows RFC 8785 restricted to the HCS-0007 value domain
(no non-integer numbers); the signature input is the DSSE v1 pre-authentication encoding.
"""
import base64
import datetime
import hashlib
import json

# ---------- Ed25519 (RFC 8032 section 5.1) ----------

P = 2 ** 255 - 19
L = 2 ** 252 + 27742317777372353535851937790883648493
D = -121665 * pow(121666, P - 2, P) % P
SQRT_M1 = pow(2, (P - 1) // 4, P)


def _recover_x(y, sign):
    if y >= P:
        return None
    x2 = (y * y - 1) * pow(D * y * y + 1, P - 2, P) % P
    if x2 == 0:
        return None if sign else 0
    x = pow(x2, (P + 3) // 8, P)
    if (x * x - x2) % P:
        x = x * SQRT_M1 % P
    if (x * x - x2) % P:
        return None
    if x & 1 != sign:
        x = P - x
    return x


_BY = 4 * pow(5, P - 2, P) % P
_BX = _recover_x(_BY, 0)
BASE = (_BX, _BY, 1, _BX * _BY % P)
IDENTITY = (0, 1, 1, 0)


def _add(p1, p2):
    x1, y1, z1, t1 = p1
    x2, y2, z2, t2 = p2
    a = (y1 - x1) * (y2 - x2) % P
    b = (y1 + x1) * (y2 + x2) % P
    c = 2 * t1 * t2 * D % P
    d = 2 * z1 * z2 % P
    e, f, g, h = b - a, d - c, d + c, b + a
    return (e * f % P, g * h % P, f * g % P, e * h % P)


def _mul(s, point):
    acc = IDENTITY
    while s:
        if s & 1:
            acc = _add(acc, point)
        point = _add(point, point)
        s >>= 1
    return acc


def _encode(point):
    x, y, z, _ = point
    zi = pow(z, P - 2, P)
    x, y = x * zi % P, y * zi % P
    return (y | ((x & 1) << 255)).to_bytes(32, "little")


def _decode(data):
    if len(data) != 32:
        return None
    n = int.from_bytes(data, "little")
    sign, y = n >> 255, n & ((1 << 255) - 1)
    x = _recover_x(y, sign)
    if x is None:
        return None
    return (x, y, 1, x * y % P)


def _expand(seed):
    h = hashlib.sha512(seed).digest()
    a = int.from_bytes(h[:32], "little")
    a &= (1 << 254) - 8
    a |= 1 << 254
    return a, h[32:]


def public_key(seed):
    a, _ = _expand(seed)
    return _encode(_mul(a, BASE))


def sign(seed, message):
    a, prefix = _expand(seed)
    pub = _encode(_mul(a, BASE))
    r = int.from_bytes(hashlib.sha512(prefix + message).digest(), "little") % L
    big_r = _encode(_mul(r, BASE))
    k = int.from_bytes(hashlib.sha512(big_r + pub + message).digest(), "little") % L
    return big_r + ((r + k * a) % L).to_bytes(32, "little")


def verify(pub, message, signature):
    if len(signature) != 64:
        return False
    a_point, r_point = _decode(pub), _decode(signature[:32])
    s = int.from_bytes(signature[32:], "little")
    if a_point is None or r_point is None or s >= L:
        return False
    k = int.from_bytes(hashlib.sha512(signature[:32] + pub + message).digest(), "little") % L
    return _encode(_mul(s, BASE)) == _encode(_add(r_point, _mul(k, a_point)))


# ---------- canonical form (RFC 8785 restricted) and signature input (DSSE PAE) ----------

SAFE_INT = 2 ** 53 - 1
ENVELOPE_PAYLOAD_TYPE = "application/vnd.hcs.catalog-envelope+json"
KEY_SET_PAYLOAD_TYPE = "application/vnd.hcs.key-set+json"
_ESCAPES = {'"': '\\"', "\\": "\\\\", "\b": "\\b", "\f": "\\f", "\n": "\\n", "\r": "\\r", "\t": "\\t"}


class CanonicalError(ValueError):
    pass


def _string(s):
    out = []
    for ch in s:
        if 0xD800 <= ord(ch) <= 0xDFFF:
            raise CanonicalError("lone surrogate")
        if ch in _ESCAPES:
            out.append(_ESCAPES[ch])
        elif ord(ch) < 0x20:
            out.append("\\u%04x" % ord(ch))
        else:
            out.append(ch)
    return '"' + "".join(out) + '"'


def canonical(value):
    """RFC 8785 serialization of a value in the HCS-0007 domain, as text."""
    if value is None:
        return "null"
    if value is True:
        return "true"
    if value is False:
        return "false"
    if isinstance(value, float):
        if not value.is_integer():
            raise CanonicalError("non-integer number")
        value = int(value)
    if isinstance(value, int):
        if abs(value) > SAFE_INT:
            raise CanonicalError("integer outside the I-JSON safe range")
        return str(value)
    if isinstance(value, str):
        return _string(value)
    if isinstance(value, list):
        return "[" + ",".join(canonical(v) for v in value) + "]"
    if isinstance(value, dict):
        keys = sorted(value, key=lambda k: k.encode("utf-16-be", "surrogatepass"))
        return "{" + ",".join(_string(k) + ":" + canonical(value[k]) for k in keys) + "}"
    raise CanonicalError(f"unsupported value {type(value).__name__}")




class StrictJsonError(ValueError):
    """The received text has no canonical form. `requirement` names the rule; messages never quote the text."""

    def __init__(self, message, requirement):
        super().__init__(message)
        self.requirement = requirement


def parse_text(data):
    """Parse the exact bytes of a received document in two stages (HCS-0007 section 5.2).

    Returns (value, violation). Stage 1 raises StrictJsonError (R003) for what is not readable: invalid
    UTF-8, a syntax error, a duplicated member name or an unpaired surrogate. Stage 3 is the number
    rule: `violation` is the first StrictJsonError (R004) for a number token that is not
    `-?(0|[1-9][0-9]*)` or lies outside the safe range, or None. It is returned, not raised, so that
    the caller reads hcsVersion (stage 2) before it reports the number. Python's json module is the one
    parser; its hooks see each member name and each number token as written.
    """
    first = []

    def note(message):
        if not first:
            first.append(StrictJsonError(message, "HCS-0007-R004"))
        return 0

    def integer(token):
        if token == "-0":
            return note("number token is not spelled as a plain integer")
        if len(token) > 20 or abs(int(token)) > SAFE_INT:  # a long token is out of range; never convert it
            return note("integer outside the I-JSON safe range")
        return int(token)

    def duplicates(pairs):
        out = {}
        for key, value in pairs:
            if key in out:
                raise StrictJsonError("duplicated member name", "HCS-0007-R003")
            out[key] = value
        return out

    def constant(name):
        raise StrictJsonError("not a JSON number", "HCS-0007-R003")

    try:
        value = json.loads(data.decode("utf-8"), object_pairs_hook=duplicates, parse_int=integer,
                           parse_float=lambda token: note("number token has a fraction or an exponent"),
                           parse_constant=constant)
        canonical(value)  # an unpaired surrogate; numbers noted above were replaced by 0
    except StrictJsonError:
        raise
    except (UnicodeDecodeError, ValueError, RecursionError):  # includes json.JSONDecodeError and CanonicalError
        raise StrictJsonError("document is not canonical JSON text", "HCS-0007-R003") from None
    return value, (first[0] if first else None)


def unsigned_bytes(doc):
    return canonical({k: v for k, v in doc.items() if k != "signatures"}).encode("utf-8")


def pae(payload_type, body):
    t = payload_type.encode("utf-8")
    return b"DSSEv1 %d %s %d %s" % (len(t), t, len(body), body)


def signing_input(payload_type, doc):
    return pae(payload_type, unsigned_bytes(doc))


def content_digest(doc):
    return "sha256:" + hashlib.sha256(unsigned_bytes(doc)).hexdigest()


# ---------- verification (HCS-0007 section 5) ----------

def _counting_keys(doc, payload_type, keys, threshold):
    """Distinct public keys whose signature over doc verifies. keys: keyid -> public bytes."""
    msg = signing_input(payload_type, doc)
    good = set()
    for s in doc.get("signatures", []):
        pub = keys.get(s["keyid"])
        if pub is not None and verify(pub, msg, _b64(s["sig"])):
            good.add(pub)
    return len(good) >= threshold


def _b64(s):
    return base64.b64decode(s, validate=True)


def instant(s):
    """RFC 3339 UTC timestamp ending in Z, optional fractional seconds."""
    return datetime.datetime.fromisoformat(s.replace("Z", "+00:00"))


def key_valid_at(key, now):
    return instant(key["notBefore"]) <= instant(now) < instant(key["notAfter"])


def check_key_set(key_set, anchor, last, now):
    """Returns (error code or None). last: {"version", "digest"} of the last accepted key set or None."""
    roots = {k["keyid"]: _b64(k["public"]) for k in anchor["keys"]}
    if not _counting_keys(key_set, KEY_SET_PAYLOAD_TYPE, roots, anchor.get("threshold", 1)):
        return "signature_invalid"
    if last is not None:
        if key_set["version"] < last["version"]:
            return "rollback_rejected"
        if key_set["version"] == last["version"] and content_digest(key_set) != last["digest"]:
            return "rollback_rejected"
    if not any(key_valid_at(k, now) for k in key_set["keys"]):
        return "signature_invalid"
    return None


def check_envelope(envelope, key_set, assignment, last, now):
    """Steps 2 to 5 of HCS-0007 section 5.2; returns an error code or None."""
    online = {k["keyid"]: _b64(k["public"]) for k in key_set["keys"] if key_valid_at(k, now)}
    if not _counting_keys(envelope, ENVELOPE_PAYLOAD_TYPE, online, key_set.get("threshold", 1)):
        return "signature_invalid"
    if envelope["binding"] != assignment:
        return "permission_denied"
    if not instant(now) < instant(envelope["expires"]):
        return "signature_invalid"
    if last is not None:
        if envelope["sequence"] < last["sequence"]:
            return "rollback_rejected"
        if envelope["sequence"] == last["sequence"] and content_digest(envelope) != last["digest"]:
            return "rollback_rejected"
    return None


def selftest():
    # RFC 8032 section 7.1, TEST 1 (empty message)
    seed = bytes.fromhex("9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60")
    pub = bytes.fromhex("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
    sig = bytes.fromhex("e5564300c360ac729086e2cc806e828a84877f1eb8e5d974d873e065224901555fb8821590a33bacc61e39701cf9b46bd25bf5f0595bbe24655141438e7a100b")
    assert public_key(seed) == pub and sign(seed, b"") == sig and verify(pub, b"", sig)
    assert not verify(pub, b"x", sig)
    # RFC 8785 ordering by UTF-16 code units: U+1F600 (D83D DE00) sorts before U+E000
    assert canonical({"\ue000": 1, "\U0001f600": 2, "a": [True, None, "\u000f"]}) == \
        '{"a":[true,null,"\\u000f"],"\U0001f600":2,"\ue000":1}'
    assert pae("t", b"ab") == b"DSSEv1 1 t 2 ab"
    for text, requirement in ((b'{"a":1,"a":2}', "HCS-0007-R003"), (b'{"a":"\\ud800"}', "HCS-0007-R003"),
                              (b'{"a":\xff}', "HCS-0007-R003"), (b'{"a":NaN}', "HCS-0007-R003"),
                              (b'{"a":01}', "HCS-0007-R003"), (b'{"a":-01}', "HCS-0007-R003"), (b'{"a":+1}', "HCS-0007-R003"),
                              (b'{"a":.5}', "HCS-0007-R003"), (b'{"a":1,"a":1.5}', "HCS-0007-R003")):
        try:
            parse_text(text)
            raise AssertionError("accepted " + repr(text))
        except StrictJsonError as e:
            assert e.requirement == requirement, (text, e.requirement)
    big = b"1" + b"0" * 399
    for token in (b"1.0", b"1e2", b"100e-2", b"-0", b"9007199254740992", b"-9007199254740992", big, b"-" + big):
        value, violation = parse_text(b'{"hcsVersion":"0.2","a":' + token + b"}")  # readable: the version stays available
        assert value["hcsVersion"] == "0.2" and violation.requirement == "HCS-0007-R004", token
    assert parse_text(b'{"a":[0,-12,9007199254740991],"b":{"a":"\\ud83d\\ude00"}}') == (
        {"a": [0, -12, 9007199254740991], "b": {"a": "\U0001f600"}}, None)
    for bad in (1.5, 2 ** 53):
        try:
            canonical(bad)
            raise AssertionError("accepted " + json.dumps(bad))
        except CanonicalError:
            pass
    print("selftest: Ed25519 (RFC 8032 test 1), RFC 8785 ordering, DSSE PAE and strict text parsing pass")
