#!/usr/bin/env python3
"""Extract HCS requirements from spec/*.md into requirements/requirements.json.

Usage: python3 tools/extract_requirements.py [--check]
--check exits 1 when the committed file differs from a fresh extraction.
"""
import json
import pathlib
import re
import sys

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "requirements" / "requirements.json"
LINE = re.compile(r"^\*\*\[(HCS-(\d{4})-R\d{3})\]\*\* (.+)$")
KEYWORDS = ["MUST NOT", "SHALL NOT", "SHOULD NOT", "NOT RECOMMENDED", "MUST", "REQUIRED",
            "SHALL", "SHOULD", "RECOMMENDED", "MAY", "OPTIONAL"]
KW = re.compile(r"\b(" + "|".join(k.replace(" ", r"\s") for k in KEYWORDS) + r")\b")


def extract():
    reqs, seen = [], set()
    for path in sorted((ROOT / "spec").glob("HCS-*.md")):
        for n, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
            m = LINE.match(line)
            if not m:
                continue
            rid, doc, text = m.group(1), m.group(2), m.group(3)
            if rid in seen:
                sys.exit(f"{path.name}:{n}: duplicate requirement id {rid}")
            if not path.name.startswith(f"HCS-{doc}-"):
                sys.exit(f"{path.name}:{n}: {rid} does not belong to this document")
            kw = KW.search(text)
            if not kw:
                sys.exit(f"{path.name}:{n}: {rid} has no BCP 14 keyword")
            seen.add(rid)
            reqs.append({"id": rid, "document": f"HCS-{doc}", "keyword": kw.group(1),
                         "text": text, "source": f"spec/{path.name}"})
    return {"generatedBy": "tools/extract_requirements.py", "requirements": reqs}


def main():
    data = json.dumps(extract(), indent=2, ensure_ascii=False) + "\n"
    if "--check" in sys.argv:
        current = OUT.read_text(encoding="utf-8") if OUT.exists() else ""
        if current != data:
            sys.exit("requirements/requirements.json is out of date; run tools/extract_requirements.py")
        print("requirements.json up to date")
        return
    OUT.parent.mkdir(exist_ok=True)
    OUT.write_text(data, encoding="utf-8")
    print(f"wrote {OUT.relative_to(ROOT)} ({len(json.loads(data)['requirements'])} requirements)")


if __name__ == "__main__":
    main()
