# Evidence library

Objective: a local, citable, versioned training-science library that the coach searches with `search_evidence` and reads with `get_evidence`, every source verified against its live bibliographic record, presenting each side where the literature disagrees, and spending no tokens on retrieval.
Branch: `feat/evidence-library` from `main` (293e4abc). Delivery: one PR. Test runner: `go test ./internal/... ./cmd/...`. Refs #116, #47.

## Specs

- S1 (owner, verbatim, issue #47): "Everything deterministic is computed by tools, spending no tokens. The agent stays the center of the experience and calls them."
- S2 (owner, verbatim, issue #47): "Content is organized and structured (for example as skills or resources), with sources."
- S3 (owner, verbatim, 2026-11-25): "Quiero avanzar lo máximo posible en este proyecto. [...] busca precedentes, sigue la filosofía de HCS y esta tiene que ser la mejor app para planificar el entrenamiento de forma no opinada que existe. el objetivo es \"vibe entrenar\" auto regularse y planificarse el entrenamiento de la misma forma que usamos ia para programar."
- S4 (issue #116, MVP scope as dispatched): an embedded, versioned corpus with one record per source; SQLite FTS5 lexical search with filters; exactly two read-only tools; `review_status` returned with every source; output presented as untrusted data; no vector store, no runtime network; the ChatGPT `search`/`fetch` adapter left as a follow-up.

Assumptions: the corpus is global and read-only (no user id anywhere); `year` is Crossref's `issued` year, which can be the online-first year; a source without an abstract is `record_only` and states a single finding; every seed source is `unreviewed` until a named human expert reviews it; the index is rebuilt on start whenever the embedded corpus version or digest differs from the latest load.

## Tasks

- [x] T1 (S2, S4) inline: `internal/evidence`: source model, closed topic and study-type lists, strict corpus parsing and validation, digest, query sanitization, Library search rules over an `Index` port; seed corpus of 31 verified sources and 7 debates. Risk: none (new package, no data path). Commit: `6c8c5af5`.
- [x] T2 (S1, S4) inline: migration 033 (global evidence tables and `evidence_fts`), `SyncEvidence` rebuild on version or digest change, `Store.Evidence()`, `Service.SearchEvidence`/`GetEvidence`. Risk: item 1 (migration, additive only; no existing table touched). Commit: `7400b015`.
- [x] T3 (S1, S4) inline: MCP `search_evidence` and `get_evidence`, read-only annotations, untrusted-data fence, isolation cases, `docs/tools.md`. Risk: none (read-only tools). Commit: `4dcf8c19`.
- [x] T4 (S2) inline: `docs/evidence.md` (adding a source, `review_status`), `tools/evidence/verify_corpus.py`, this document. Risk: none. Commit: see the PR.

Route evidence: sequential units (T2 and T3 depend on T1); parent works inline as a delegated writer; no parallel units.

## Log

- L1 (2026-11-25, owner, verbatim): "Quiero avanzar lo máximo posible en este proyecto. [...] busca precedentes, sigue la filosofía de HCS y esta tiene que ser la mejor app para planificar el entrenamiento de forma no opinada que existe. el objetivo es \"vibe entrenar\" auto regularse y planificarse el entrenamiento de la misma forma que usamos ia para programar."
- L2 (2026-11-25): candidates found by Crossref `query.bibliographic` searches; each candidate's record fetched from `https://api.crossref.org/works/<DOI>` and from PubMed E-utilities (`esearch` by `<DOI>[doi]`, then `efetch`), with a generic User-Agent. Bibliographic fields were generated from those records, never typed; summaries were written from the abstracts read in them.
- L3 (2026-11-25): dropped: Barbalho et al. 2020, "Evidence of a Ceiling Effect for Training Volume in Muscle Hypertrophy and Strength in Trained Men" (doi:10.1123/ijspp.2018-0914). PubMed lists it as "Retracted Publication" and Crossref `updated-by` lists a retraction (doi:10.1123/ijspp.2020-0372). Its absence leaves the volume debate's "flat above a ceiling" side to Heaselgrave 2019 and Baz-Valle 2022.
- L4 (2026-11-25): per-source verification. All 31 records checked in Crossref on 2026-11-25 with no retraction or correction in `updated-by`; 29 also checked in PubMed on 2026-11-25 (PMID from PubMed's own record of the DOI, no "Retracted Publication" type). Summary basis is the abstract unless stated.
  - `schoenfeld-2016-weekly-volume-meta-analysis`: doi:10.1080/02640414.2016.1210197, PMID 27433992. Crossref + PubMed.
  - `schoenfeld-2019-volume-trained-men`: doi:10.1249/mss.0000000000001764, PMID 30153194. Crossref + PubMed.
  - `heaselgrave-2019-biceps-volume-frequency`: doi:10.1123/ijspp.2018-0427, PMID 30160627. Crossref + PubMed.
  - `bazvalle-2022-volume-systematic-review`: doi:10.2478/hukin-2022-0017, PMID 35291645. Crossref + PubMed.
  - `pelland-2025-volume-frequency-dose-response`: doi:10.1007/s40279-025-02344-w, PMID 41343037. Crossref + PubMed (issued 2025, print 2026).
  - `schoenfeld-2018-frequency-hypertrophy-meta-analysis`: doi:10.1080/02640414.2018.1555906, PMID 30558493. Crossref + PubMed.
  - `grgic-2018-frequency-strength-meta-analysis`: doi:10.1007/s40279-018-0872-x, PMID 29470825. Crossref + PubMed.
  - `refalo-2022-proximity-to-failure-hypertrophy`: doi:10.1007/s40279-022-01784-y, PMID 36334240. Crossref + PubMed.
  - `robinson-2024-proximity-to-failure-dose-response`: doi:10.1007/s40279-024-02069-2, PMID 38970765. Crossref + PubMed.
  - `grgic-2022-failure-vs-non-failure`: doi:10.1016/j.jshs.2021.01.007, PMID 33497853. Crossref + PubMed.
  - `helms-2016-rir-based-rpe-application`: doi:10.1519/ssc.0000000000000218, PMID 27531969. Crossref + PubMed.
  - `zourdos-2016-rir-rpe-scale`: doi:10.1519/jsc.0000000000001049, PMID 26049792. Crossref + PubMed.
  - `halperin-2021-predicting-repetitions-to-failure`: doi:10.1007/s40279-021-01559-x, PMID 34542869. Crossref + PubMed.
  - `schoenfeld-2016-rest-intervals-trained-men`: doi:10.1519/jsc.0000000000001272, PMID 26605807. Crossref + PubMed.
  - `grgic-2017-rest-intervals-systematic-review`: doi:10.1080/17461391.2017.1340524, PMID 28641044. Crossref + PubMed.
  - `singer-2024-rest-intervals-bayesian-meta-analysis`: doi:10.3389/fspor.2024.1429789, PMID 39205815. Crossref + PubMed.
  - `hickmott-2022-load-volume-autoregulation`: doi:10.1186/s40798-021-00404-9, PMID 35038063. Crossref + PubMed.
  - `larsen-2021-autoregulation-maximal-strength`: doi:10.7717/peerj.10663, PMID 33520457. Crossref + PubMed.
  - `bell-2023-deloading-delphi-consensus`: doi:10.1186/s40798-023-00633-0, PMID 37730925. Crossref + PubMed.
  - `coleman-2024-one-week-deload-trial`: doi:10.7717/peerj.16777, PMID 38274324. Crossref + PubMed.
  - `rogerson-2024-deloading-practices-survey`: doi:10.1186/s40798-024-00691-y, PMID 38499934. Crossref + PubMed.
  - `pallares-2021-range-of-motion-meta-analysis`: doi:10.1111/sms.14006, PMID 34170576. Crossref + PubMed.
  - `schoenfeld-2020-range-of-motion-review`: doi:10.1177/2050312120901559, PMID 32030125. Crossref + PubMed.
  - `wolf-2023-partial-vs-full-range-of-motion`: doi:10.47206/ijsc.v3i1.182. Crossref only (not indexed in PubMed); abstract from the Crossref record.
  - `maeo-2020-hamstrings-long-muscle-length`: doi:10.1249/mss.0000000000002523, PMID 33009197. Crossref + PubMed.
  - `kassiano-2022-exercise-variation-review`: doi:10.1519/jsc.0000000000004258, PMID 35438660. Crossref + PubMed.
  - `grgic-2018-periodization-hypertrophy-review`: doi:10.1016/j.scispo.2017.09.005. Crossref only (not indexed in PubMed); no abstract in either record, so `record_only`.
  - `moesgaard-2022-periodization-volume-equated`: doi:10.1007/s40279-021-01636-1, PMID 35044672. Crossref + PubMed.
  - `williams-2017-periodization-maximal-strength`: doi:10.1007/s40279-017-0734-y, PMID 28497285. Crossref + PubMed.
  - `acsm-2009-progression-models-position-stand`: doi:10.1249/mss.0b013e3181915670, PMID 19204579. Crossref + PubMed.
  - `schoenfeld-2017-low-vs-high-load`: doi:10.1519/jsc.0000000000002200, PMID 28834797. Crossref + PubMed.
- L5 (2026-11-25): re-verification of the committed files with `python3 -I tools/evidence/verify_corpus.py`: "31 of 31 sources match their live records" (title, authors, year, publication, volume, issue, pages, PMID's DOI, no retraction).
- L6 (2026-11-25): debates and their sides: `volume_dose_response` (Schoenfeld 2016, Schoenfeld 2019, Pelland 2025: more volume, more growth with diminishing returns; Heaselgrave 2019, Baz-Valle 2022: no significant gain past moderate volumes), `frequency_independent_effect` (Schoenfeld 2018 and Grgic 2018: no effect once volume is equated; Pelland 2025: strength rises with frequency), `proximity_to_failure` (Refalo 2022 and Grgic 2022: little or no difference; Robinson 2024: growth rises nearer failure), `rest_interval_length` (Schoenfeld 2016 and Singer 2024: longer rests help somewhat; Grgic 2017: both work; ACSM 2009: 1 to 2 minutes for hypertrophy), `deload_value` (Coleman 2024: a week off kept growth but cost strength; Bell 2023 and Rogerson 2024: coaches and athletes plan them), `periodization_value` (Moesgaard 2022 and Williams 2017: more strength, no more hypertrophy; Grgic 2018 record only), `partial_range_of_motion` (Pallarés 2021 and Schoenfeld 2020: full range better; Wolf 2023: trivial difference, possible benefit of long-length partials).
- L7 (2026-11-25): search precision is pinned by fixed queries in `internal/sqlitestore/evidence_test.go`, including Spanish queries, confusable terms (periodization versus "deload period", long rests versus long muscle lengths, load autoregulation versus low load) and contradictory sources that must both surface. A first version fell back to any single matching word; "creatine loading phase" then returned load studies, so the fallback now needs more than half of the words and only for queries of three or more.
- L8 (2026-11-25): `BenchmarkSearchEvidence`: 1.2 to 2.7 ms per search (3 runs, AMD Ryzen 5 5500, laptop shared with other workers).
- L9 (2026-11-25): follow-ups, not in this PR: the ChatGPT `search`/`fetch` compatibility adapter over the library; expert review of the 31 seed summaries.
