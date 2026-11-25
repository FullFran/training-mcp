#!/usr/bin/env python3
"""Re-check every evidence source against its live bibliographic record.

For each file in internal/evidence/corpus/sources (or the paths given), fetch
the Crossref record of its DOI and, when it has a PMID, the PubMed record, and
compare the citation metadata the corpus states: title, authors, year,
publication, volume, issue, pages and PMID. It also reports any retraction or
correction Crossref lists in updated-by, and a PubMed publication type of
"Retracted Publication".

It is a maintenance tool: it needs the network, so CI does not run it. Run it
before changing a source and before a corpus release; it exits 1 when any
source disagrees with its record. It sends a generic User-Agent and nothing
else about the caller.

Usage:
  python3 -I tools/evidence/verify_corpus.py [source.json ...]
"""
import html
import json
import os
import re
import sys
import time
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET

HEADERS = {"User-Agent": "evidence-corpus-verifier/1.0"}
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..")
SOURCES = os.path.join(ROOT, "internal", "evidence", "corpus", "sources")


def get(url, tries=4):
    for i in range(tries):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, headers=HEADERS), timeout=40) as r:
                return r.read()
        except Exception:
            if i == tries - 1:
                raise
            time.sleep(3 + 3 * i)


def clean(s):
    s = html.unescape(re.sub(r"<[^>]+>", " ", s or ""))
    s = s.replace("‐", "-").replace("‑", "-").replace(" ", " ")
    return re.sub(r"\s+", " ", s).strip()


def crossref(doi):
    m = json.loads(get("https://api.crossref.org/works/" + urllib.parse.quote(doi)))["message"]
    issued = (m.get("issued", {}).get("date-parts") or [[None]])[0][0]
    authors = [clean((a.get("given") or "") + " " + (a.get("family") or a.get("name") or "")) for a in m.get("author", [])]
    return {
        "title": clean((m.get("title") or [""])[0]).rstrip("."),
        "authors": [a for a in authors if a],
        "year": issued,
        "publication": clean((m.get("container-title") or [""])[0]),
        "volume": clean(m.get("volume")),
        "issue": clean(m.get("issue")),
        "pages": clean(m.get("page")),
        "updates": [(u.get("type"), u.get("DOI")) for u in m.get("updated-by", [])],
    }


def pubmed(pmid):
    root = ET.fromstring(get("https://eutils.ncbi.nlm.nih.gov/entrez/eutils/efetch.fcgi?db=pubmed&retmode=xml&id=" + urllib.parse.quote(pmid)))
    dois = [e.text for e in root.findall(".//ArticleIdList/ArticleId") if e.get("IdType") == "doi"]
    types = [p.text for p in root.findall(".//PublicationTypeList/PublicationType")]
    return {"dois": [d.lower() for d in dois if d], "types": types}


def check(path):
    src = json.load(open(path))
    problems = []
    if src.get("doi"):
        cr = crossref(src["doi"])
        for key in ("title", "year", "publication", "volume", "issue", "pages"):
            want = cr[key] or None
            have = src.get(key) or None
            if want != have:
                problems.append(f"{key}: corpus {have!r}, Crossref {want!r}")
        if cr["authors"] and cr["authors"] != src["authors"]:
            problems.append(f"authors: corpus {src['authors']!r}, Crossref {cr['authors']!r}")
        if cr["updates"]:
            problems.append(f"Crossref lists updates: {cr['updates']!r}")
        time.sleep(0.5)
    if src.get("pmid"):
        pm = pubmed(src["pmid"])
        if src.get("doi") and src["doi"].lower() not in pm["dois"]:
            problems.append(f"PubMed {src['pmid']} does not carry DOI {src['doi']} (it has {pm['dois']!r})")
        if "Retracted Publication" in pm["types"]:
            problems.append("PubMed marks it as a Retracted Publication")
        time.sleep(0.5)
    return src["id"], problems


def main():
    paths = sys.argv[1:] or sorted(os.path.join(SOURCES, n) for n in os.listdir(SOURCES) if n.endswith(".json"))
    failed = 0
    for path in paths:
        try:
            sid, problems = check(path)
        except Exception as e:
            sid, problems = os.path.basename(path), [f"could not verify: {e}"]
        if problems:
            failed += 1
            print(f"FAIL {sid}")
            for p in problems:
                print(f"     {p}")
        else:
            print(f"ok   {sid}")
    print(f"{len(paths) - failed} of {len(paths)} sources match their live records")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
