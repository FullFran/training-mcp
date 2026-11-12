# HCS consumer kit

Non-normative tooling for a harness that adopts HCS. It adds no requirement: copy `hcs_kit.py` and `schemas/` into your repository, change them, or write your own. It needs Python 3.10+ and nothing else; the conformance runner it works with needs `jsonschema` (see [conformance/README.md](../../conformance/README.md)).

Three steps. `hcs_kit.py` is called `kit` below.

## 1. Pin and vendor a release

Take the version and the tarball digest from the release page (the `.sha256` asset, or the release notes in [docs/releases/](../../docs/releases/)):

```sh
python3 kit/hcs_kit.py sync 0.1.0-draft.7 <sha256 of hcs-0.1.0-draft.7.tar.gz>
# offline: add --tarball path/to/hcs-0.1.0-draft.7.tar.gz   (--url overrides the download URL)
```

`sync` refuses the release unless the tarball matches the digest you pinned and the unpacked tree matches the release's own `SHA256SUMS` (a changed, missing or extra file; a link, a path outside `hcs-<version>/`). It then writes `vendor/hcs/<version>/` and `vendor/hcs/PIN.json`:

```json
{"version": "0.1.0-draft.7", "url": "https://github.com/FullFran/hcs/releases/download/v0.1.0-draft.7/hcs-0.1.0-draft.7.tar.gz", "sha256": "..."}
```

Commit the vendored tree as it is. Add `python3 kit/hcs_kit.py verify` to your tests: it fails when any vendored file was edited, removed or added since `sync`. To upgrade, run `sync` for the new version (an existing version directory needs `--force`), then review the diff.

## 2. Run the conformance cases against your system

Write a small program that speaks the `--sut` process protocol ([conformance/README.md](../../conformance/README.md#system-under-test)) and run the vendored runner with a report:

```sh
python3 vendor/hcs/<version>/conformance/runner/run.py --report report.json --sut <your command>
```

## 3. Record a baseline and fail only on regression

State what you do differently in `divergences.json` ([schema](schemas/divergences.schema.json)):

```json
{
  "divergences": [
    {"requirement": "HCS-0003-R010", "reason": "we activate plugins lazily; the order is the same"}
  ],
  "notApplicable": [
    {"requirement": "HCS-0006-R006", "reason": "we only produce HCS errors; no client reads them",
     "cases": ["HCS-0006/HCS-0006-R006/valid/jsonrpc-data-hcserror.json"]}
  ]
}
```

- Every rule names a `requirement` and a `reason`. Without `cases` it covers every case of that requirement; `cases` lists case keys (the path below `cases/`).
- A rule turns a non-`pass` result of its cases into `divergence` or `not-applicable`. It never hides a `pass`.

Create the baseline once, review it, and commit it ([schema](schemas/baseline.schema.json)); it maps each case key to its accepted status:

```sh
python3 kit/hcs_kit.py baseline --report report.json --divergences divergences.json --out baseline.json
```

From then on, in CI:

```sh
python3 kit/hcs_kit.py compare --report report.json --baseline baseline.json --divergences divergences.json
```

`compare` exits 1 only on regression. Statuses rank as: `pass`, `pending`, `not-applicable`, `divergence` (good); `not-implemented`; `fail`, `crash`, `malformed` (failing). A case regresses when its status ranks worse than in the baseline, or when a case absent from the baseline is not good. Improvements, new passing cases and cases that left the run are reported and never fail. After a deliberate change (or an upgrade that adds cases), run `compare ... --update` and commit the new baseline. A `not-applicable` that your own system reports counts as good: the declaration is yours.

This is the runner's `--report` JSON (`results[].case` and `results[].status`), which is all the kit reads. The in-process `run.py --selftest --report` file has the same two members, so the kit also works with it.
