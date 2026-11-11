# Performance engine

A device-free estimate of **apparent** performance from what was logged: load,
reps and, when stated, RPE. It lives in `internal/training/performance.go` as pure
Go (no store, no clock), is exposed through the read-only MCP tool
`performance_dropoff`, and costs no model tokens: the agent asks for the numbers
and explains them.

It never measures bar velocity, absolute force or physiological fatigue, never
changes a load or a plan, and abstains when the data cannot support a
comparison. Nothing it computes is stored. SI (`max(0, 0.2*RPE - 0.6)`, see
[si-formula.md](si-formula.md)) is unchanged and separate: SI counts
effort-weighted sets, the engine compares sets.

## e1RM convention (issue #20)

`training.Epley1RM` and the estimator `epley_v1` use Epley, `w*(1+reps/30)`,
rounded to 0.1 kg, with one rule: **a single (1 rep) is the lifted weight**. The
raw formula would report a 100 kg single as 103.3 kg, a heavier lift than the one
recorded. The best set in `exercise_history` (and the web app's record badge,
which compares against it) is ranked by the same rule in SQL; the two must change
together.

Visible effect of adopting the rule: only sets of exactly 1 rep change; their
estimate drops by 3.3%, and an exercise whose record was a single may now show a
multi-rep set as its record. Nothing stored changes, because the estimate is
computed on read.

Trusted range: 1-12 reps. Beyond 12, `epley_v1` still answers with `low`
confidence. An estimate is a formula convention, never an observed maximum.

## Estimators

| Id | Formula | Requires | Range |
|---|---|---|---|
| `epley_v1` | `w*(1+reps/30)`; `w` when reps = 1 | load and reps | 1-12 reps; beyond: `low` |
| `rir_epley_v1` | `w*(1+(reps+10-RPE)/30)` | an **observed** RPE from 6 to 10 (RIR 0-4) | reps + RIR 1-12; beyond: `low` |
| `brzycki_v1` | `w*36/(37-reps)` | load and reps | 1-12 reps; beyond: refused |

Epley and Brzycki are distinct estimators, not variants of one another; the
"Bilbo" `0.03*reps` progression is a different idea and is not implemented. A
formula change gets a new id (`_v2`), so every number traces to its formula.
Estimates are in kg rounded to 0.1. Confidence is `none`, `low` or `moderate`:
there is no `high`, because without a measuring device every value is a proxy.

`rir_epley_v1` reads RPE as reps in reserve (RIR = 10 - RPE). It refuses an RPE
nobody stated (`rpe_imputed`, the RPE 8 placeholder): a placeholder is not an
observation. It also refuses RPE below 6, where people misjudge reserve too much
to use it. Its drop carries `dropoff_range_pct`: the drop if the stated RPE were
one point off either way (RIR kept within 0-4). One RIR moves the estimate by
`w/30`, about 2.5% of the estimate at a 10-rep set, which is often as large as the drop itself.

## Comparable sets (issue #21)

Sets are compared only within one exercise of one session, in logged order. A set
is **comparable** when all of these hold; otherwise it is listed with an
`excluded_reason` and no estimates:

| Rule | `excluded_reason` |
|---|---|
| Working set: not recorded as a warm-up (`category` `warmup`) | `warmup` |
| Straight set: no intensity technique (drop set, rest-pause, myo-reps, parciales, asistida, ...) | `technique` |
| Load and reps are both positive | `no_load_or_reps` |
| Not a probable warm-up: logged before the session's top straight working load and lighter than 60% of it | `probable_warmup` |

What the contract derives instead of storing:

- **Equipment and configuration**: the exercise name is the identity. Two
  machines with the same display name are not comparable, so they must be logged
  under different names (`aperturas máquina`, `aperturas polea`). Setup details
  (seat, pin, grip, straps) go in `set_exercise_note`, which needs no tap while
  logging. Stored equipment and setup fields are milestone M2 (#24), not this
  engine.
- **Technique compatibility**: the existing per-set `technique` field.
- **Units**: weights are kilograms as stored. The historical lb/kg audit is not
  finished, so the engine compares within a session only and makes no historical
  calibration; that audit blocks calibration (#25), not this comparison.
- **RPE provenance**: `rpe_observed` is false for an unstated RPE. Only observed
  RPE feeds `rir_epley_v1`.

No migration: every input above already exists on a stored set.

## Drop-off

The **reference** is the first comparable set. For each later comparable set and
each estimator that can value both:

```
D = 100 * (1 - P_set / P_reference)    rounded to 0.1 %
```

Positive is lower apparent performance than the reference; negative is higher.
Each model summary reports how many sets it compared (reference included), its
largest drop and its confidence. A model **abstains** (confidence `none`, no
drop) when fewer than two comparable sets remain for it, including when the
reference itself cannot be valued (for example an unstated RPE on the reference
for `rir_epley_v1`): it never borrows a later set as the reference. Every
analysis states its assumptions and an explanation the agent can relay.

Example, from a logged session: row 130 lb (59 kg) x10, x10, x9, all @8. Equal
load and effort with one rep fewer reads as `epley_v1` 2.5% and `rir_epley_v1`
2.4%, with an RIR sensitivity of 0% to 4.8%. Chest fly 220 lb x6 @8 then 190 lb
x6 @8 reads as a 13.6% apparent drop; a third set with no stated RPE is valued
by Epley and Brzycki but not by the RIR model.

Not yet done: comparing the models on held-out verified sets (#25). Until then no
estimator is claimed to be more accurate than another.
