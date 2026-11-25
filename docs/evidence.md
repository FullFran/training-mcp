# Evidence library

A local, citable library of training-science sources. The coach searches it
with `search_evidence` and reads a source whole with `get_evidence` (see
[tools.md](tools.md#evidence-library)). Search is deterministic SQLite FTS5
over a corpus compiled into the binary: no network, no vector store, no
tokens spent on retrieval.

The library is unopinionated on purpose. Where the literature disagrees
(weekly volume, frequency, proximity to failure, rest intervals, deloads,
periodization, partial range of motion) it holds sources on each side and
names the **debate**, so the coach presents every side instead of a single
prescription.

## What is in a source

One JSON file per source in `internal/evidence/corpus/sources/<id>.json`. The
file name is the id. Unknown fields are refused, so an abstract cannot slip in
under another name.

| Field | Meaning |
|---|---|
| `id` | Stable id: lower-case words joined by hyphens, first author, year, subject. Never reused or renamed. |
| `title`, `authors`, `year`, `publication`, `volume`, `issue`, `pages` | Copied from the live Crossref record, never typed from memory. `year` is Crossref's `issued` year, the first publication date, which can be the online-first year (Pelland et al. is 2025 online, 2026 in print). |
| `study_type` | Closed list: `meta_analysis`, `systematic_review`, `scoping_review`, `randomized_trial`, `controlled_trial`, `acute_study`, `narrative_review`, `position_stand`, `delphi_consensus`, `cross_sectional_survey`. |
| `doi`, `pmid` | At least one. The PMID comes from PubMed's own record for the DOI. |
| `url` | Canonical link: `https://doi.org/<doi>`. |
| `population` | Who was studied, as the abstract describes it. |
| `topics` | Closed list (see `evidence.Topics`): `volume`, `frequency`, `fractional_volume`, `proximity_to_failure`, `rir_rpe`, `rest_intervals`, `autoregulation`, `deload`, `exercise_selection`, `range_of_motion`, `periodization`, `load`, `hypertrophy`, `strength`, `fatigue`. Each topic adds English and Spanish search words to the index. |
| `debates` | Ids of contested questions defined in `corpus/manifest.json`. Every debate needs at least two sources. |
| `keywords` | Extra search words not in the summary. |
| `key_findings`, `methods`, `limitations` | Our own short summary. No sentence of the abstract or full text is copied. |
| `summary_basis` | `abstract`: the summary was written from the abstract of the verified record. `record_only`: no abstract was available, so the source states one finding that only describes what the record supports and says to read the original. |
| `verification` | `crossref` and `pubmed`: the dates the source was checked there. `retraction_check`: what was checked for retractions and corrections, and when. |
| `ingested_on` | Date the source entered the corpus. |
| `content_version` | Starts at 1; add one whenever the summary changes. |
| `review_status` | See below. |
| `reviewed_by`, `reviewed_on` | Only on a reviewed source: who reviewed it and when. |

## What `review_status` means

- `unreviewed`: the citation metadata was verified against the live record,
  but **no human expert has checked that the summary is faithful to the
  study**. Every source of the seed corpus is unreviewed. The tools return
  the status with every source, and the coach should say "this summary has
  not been reviewed by an expert yet" when it cites one.
- `reviewed`: a named person with the expertise to judge the study read it and
  confirmed the summary. Validation requires `reviewed_by` and `reviewed_on`
  with it, and forbids them on an unreviewed source. Only that person's
  explicit review moves a source here; an agent never does.

## Adding or updating a source

1. **Find the record.** Search Crossref
   (`https://api.crossref.org/works?query.bibliographic=...`) or PubMed. Read
   the abstract from the record itself. A source with no DOI or PMID does not
   enter the library.
2. **Check it is not retracted.** Crossref `updated-by` must list no
   retraction, and PubMed's publication types must not include "Retracted
   Publication". Barbalho et al. 2020 (IJSPP) was dropped from the seed for
   exactly this reason.
3. **Write the file.** Copy the bibliographic fields from the record
   (decode HTML entities such as `&amp;`). Write `population`, `key_findings`,
   `methods` and `limitations` in your own words, saying only what the abstract
   supports; when there is no abstract, use `summary_basis: record_only`. Set
   `ingested_on` and the `verification` dates to today, `content_version: 1`
   and `review_status: unreviewed`. When the source takes a side in a debate,
   add the debate id; when it opens a new disagreement, add the debate to
   `manifest.json` with every source on each side.
4. **Updating** a source: change its summary and add one to
   `content_version`. Never change its `id`; a retracted source is removed.
5. **Bump the corpus version** in `manifest.json`. On the next start, `Open`
   sees a new version (or digest: an edit that forgot the bump is still
   caught) and rebuilds the index in one transaction.
6. **Verify and test.**
   `python3 -I tools/evidence/verify_corpus.py` re-fetches every source's
   Crossref and PubMed record and fails on any difference in title, authors,
   year, publication, volume, issue, pages or PMID, or on a retraction. It
   needs the network, so CI does not run it; run it before every corpus
   change. `go test ./internal/evidence/ ./internal/sqlitestore/` validates
   the corpus and checks search precision on fixed queries.

## How search works

- The query is reduced to runs of letters and digits, lower-cased, without
  common English and Spanish words or words every source shares (`training`,
  `entrenamiento`, `study`), at most 16. Each word is matched as a quoted FTS5
  string, so nothing typed can be read as FTS syntax.
- The index uses Porter stemming and folds diacritics, so `deloads` finds
  `deload` and `autorregulacion` finds `autorregulación`. Spanish queries work
  through each topic's Spanish words.
- Every word must match. When no source has them all and the query has three
  or more words, sources with more than half of the words are returned and
  `match` says `most_terms`. A single shared word is never enough, so
  "creatine loading phase" is an explicit no-result, not a load study.
- Ranking is BM25 with column weights: title 10, topics 4, keywords 4,
  authors 3, findings 2, the rest 1; ties go to the newer source.
- Filters: `topic`, `study_type`, `year_from`, `year_to`.

`BenchmarkSearchEvidence` (five mixed queries, including Spanish and a
no-result one) measured 1.2 to 2.7 ms per search on an AMD Ryzen 5 5500
shared with other work (`go test ./internal/sqlitestore/ -run '^$' -bench
BenchmarkSearchEvidence -benchmem -count 3`, 2026-11-25).

## Not yet

- A ChatGPT `search`/`fetch` compatibility adapter over this library
  (follow-up of #116).
- Expert review of the seed summaries.
