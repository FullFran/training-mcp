---
name: fatigue-autoregulation
description: "Interpret logged RPE, reps, rest and set-to-set drop-off without treating them as measured fatigue, and lay out possible adjustments, including easier weeks, for the person to choose."
license: MIT
metadata:
  version: "0.1.0"
  author: "training-mcp maintainers"
  review_status: draft-unreviewed
  triggers: "reps or load fall from one set to the next; deciding whether to adjust load, sets or rest mid-session; questions about RPE, reps in reserve or effort ratings; feeling tired, flat or under-recovered; whether and when to take an easier week (deload)"
  limitations: "draft that no trainer has reviewed yet; vibeTraining measures no velocity, force or physiological fatigue, so every fatigue reading is a proxy from logged numbers; RPE and reps in reserve are self-reports that people misjudge; not for pain, illness, injury, overtraining symptoms or any medical concern: defer to a professional; explains adjustments, never applies them"
  required_tools: "memory_context memory_search live_workout get_session session_progress performance_dropoff exercise_history suggest_load weekly_volume volume_recommendation"
---

# Fatigue and autoregulation

Draft 0.1.0, not reviewed by a trainer. Say so if the person relies on it.

Autoregulation means adjusting training from how the person performs or feels.
This skill helps read the signals vibeTraining has and lay out what could be
done. It decides nothing: the person does.

## How to use it

1. Get the facts of the session before interpreting anything: `live_workout`
   during a workout (sets, plan versus done, rest since the last set against
   their target), or `get_session` and `session_progress` afterwards. A set's
   `rest_after_seconds` is there only when a rest was recorded.
2. For a drop between sets, call `performance_dropoff`. Read which sets it
   compared, which it excluded and why (warm-up, technique, different
   equipment, probable warm-up), each model's confidence, and whether it
   abstained. Report its numbers; do not recompute them.
3. Name what each thing is when you answer:
   - observed: the logged load, reps, stated RPE and recorded rest;
   - estimated: the drop-off and e1RM from `performance_dropoff`, a
     device-free proxy that is not velocity, force or fatigue;
   - hypothesis: why it happened (fatigue is one candidate among several);
   - decision: what the person chooses to do.
4. Check the ordinary explanations before fatigue: a shorter rest than usual,
   an earlier set taken closer to failure, a set stopped on purpose, a
   different machine, a technique set, a kg/lb slip, an unstated RPE. Ask about
   a deliberate stop before reading a drop as fatigue.
5. Offer the adjustments that fit with their costs, and ask. Any change to a
   plan or today's session goes through a write tool whose result is a
   proposal; say it is done only when its status is `accepted`.
6. Pain, dizziness, illness, unusual or lasting exhaustion, or an injury:
   stop interpreting. There is not enough evidence here to adapt training
   around it. Suggest they stop the exercise if it hurts and see a qualified
   professional. Do not diagnose.

## Evidence

Each claim cites a source in `references/sources.md` by key. Group results.

- **Effort scales.** An RPE scale anchored to reps in reserve (RPE 10 = no rep
  left, 9 = one, and so on) tracked bar velocity closely in 29 squatters, and
  experienced lifters rated a maximal single higher than novices
  [ref:zourdos-2016-rir-rpe]. A practitioner article describes how to use the
  scale to set loads set by set [ref:helms-2016-rir-application]; it is
  guidance, not a trial.
- **How accurate reps in reserve are.** Across 12 studies (414 participants),
  people underestimated the reps they had left by about one on average, with
  very large differences between studies. Predictions were better closer to
  failure, with heavier loads (12 reps or fewer) and in later sets; training
  experience did not seem to matter [ref:halperin-2022-rir-accuracy]. A stated
  RPE is useful and imprecise.
- **What autoregulation means.** A review found the field uses terms such as
  readiness, fatigue and response inconsistently, and proposed a framework to
  define them [ref:greig-2020-autoregulation]. Be precise about which one you
  mean.
- **Drop-off thresholds come from velocity.** Research on stopping a set at a
  set performance loss measures bar velocity with a device. In 22 young men
  squatting for 8 weeks, allowing 20% versus 40% velocity loss gave similar
  strength, better jumps with 20%, and more growth of two thigh muscles with
  40% [ref:pareja-blanco-2017-velocity-loss]. A meta-analysis found no growth
  difference between high and moderate velocity-loss thresholds
  [ref:refalo-2023-failure]. vibeTraining has no velocity: these thresholds do
  not transfer to the rep-and-load proxy of `performance_dropoff`.
- **Rest between sets.** A Bayesian meta-analysis of 9 studies found a small
  growth benefit from resting more than 60 seconds and no appreciable
  difference beyond 90 seconds [ref:singer-2024-rest]. An earlier review of 6
  studies found both short and long rests useful, with a possible advantage of
  longer rests in trained people [ref:grgic-2017-rest]. A shorter rest is a
  plausible, ordinary cause of fewer reps.
- **Easier weeks (deloads).** In a Delphi study of strength and physique
  coaches (34 in the first round, 21 in the last), the coaches agreed on a
  definition: a period of reduced training stress to mitigate fatigue,
  promote recovery and prepare for further training
  [ref:bell-2023-deload-delphi]. That is expert agreement, not a trial. In 39
  trained young adults, a one-week break from training in the middle of a
  9-week programme left growth unchanged and gave smaller strength gains than
  training straight through [ref:coleman-2024-deload]. Evidence on how often,
  how and whether to deload is thin.

## Heuristics

Common practice. No verified source in this skill supports these as rules;
present them as options, never as facts.

- **Deload timing.** Some coaches schedule an easier week every few weeks
  (intervals from about 4 to 8 weeks are common), some take one only when
  performance or motivation drops for a while, and some never plan one.
  Coaches disagree, and this skill does not pick one.
- **Rep drop as a stopping rule.** Ending an exercise when reps fall by some
  share against the first set is practised; no threshold for a rep-based
  proxy has been validated, and `performance_dropoff` deliberately sets none.
- **Readiness checks.** Adjusting the day's load from sleep, stress or how
  the warm-up felt is common and unvalidated here.
- **Cutting sets before load.** When recovering, some reduce sets first and
  keep load; others reduce load. Both are practised.
- **Session drift.** Stated RPE often creeps up across a session at the same
  load; read a single higher rating with care.

## Person-specific assumptions

Never assume these; read them or ask.

- Whether they rate effort at all. An unstated RPE is stored as a placeholder
  (`rpe_imputed`) and never feeds the reps-in-reserve model; do not treat it
  as their rating.
- How they use the scale: some rate honestly near failure, others always say
  8. Compare their ratings with their own history (`exercise_history`), not
  with someone else's.
- Their rest target and how long they actually rested (`live_workout`,
  `rest_after_seconds`).
- Whether a stop was deliberate: a planned reserve or a time limit is not a
  drop in performance.
- Their history of how a fatiguing week went, their limitations and what
  worked, from `memory_context` and `memory_search`.
- Their recent volume (`weekly_volume`) and what their own feedback says
  about next week (`volume_recommendation`, with its confidence).

## Decisions

The person's to make. Offer the options that fit, with their costs, and ask.

- **During the session, after a drop.** Carry on as planned and accept the
  drop; rest longer before the next set; lower the load to stay in the
  prescribed reps; do fewer sets of this exercise; or stop the exercise. More
  rest costs time; lowering load or sets trades some work for less fatigue.
  `suggest_load` gives a weight against the prescription if they want one.
- **How to prescribe effort.** RPE, reps in reserve, a percentage of a
  maximum, or a fixed rep target. Coaches disagree; vibeTraining stores RPE 1-10.
- **Easier weeks.** Planned on a schedule, taken when signals persist, or not
  at all; and if taken, fewer sets, lighter loads, more reserve or full rest.
  The evidence above does not choose for them.
- **Week-to-week volume.** Keep, add or remove sets, informed by
  `volume_recommendation`, which reads their own feedback and declines on a
  single rating.
