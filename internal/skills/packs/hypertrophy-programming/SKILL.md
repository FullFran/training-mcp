---
name: hypertrophy-programming
description: "Lay out the options for weekly volume, frequency, effort, load and progression when a person plans or changes training for muscle growth, and help them choose."
license: MIT
metadata:
  version: "0.1.0"
  author: "training-mcp maintainers"
  review_status: draft-unreviewed
  triggers: "planning or changing a routine or block aimed at muscle growth; choosing weekly sets, frequency, rep ranges or how close to failure to train; deciding how to progress load, reps or sets; asking how much volume is enough or what MEV or MRV means for them"
  limitations: "draft that no trainer has reviewed yet; the studies are mostly of young, healthy adults and many of men only, so group averages do not predict one person's response; not for pain, injury, rehabilitation, pregnancy or any medical condition: defer to a professional; gives no number of its own: personal numbers come from vibeTraining tools"
  required_tools: "memory_context memory_search weekly_volume volume_by_muscle list_volume_landmarks volume_recommendation simulate_plan exercise_history suggest_load list_plans get_plan"
---

# Hypertrophy programming

Draft 0.1.0, not reviewed by a trainer. Say so if the person relies on it.

This skill does not pick a methodology. It lays out the positions that exist,
what each costs, and what the evidence can and cannot say, so the person
chooses. Their plan is theirs.

## How to use it

1. Read the person first: `memory_context` (goals, limitations, equipment,
   what worked), then the numbers the question needs: `weekly_volume` and
   `volume_by_muscle` for what they actually do, `list_volume_landmarks` for
   any boundaries they set themselves, `exercise_history` for progression,
   `list_plans` and `get_plan` for the current routine.
2. Get every number from a tool. `volume_recommendation` gives next week's
   set-count change from their own feedback, with its confidence; `simulate_plan`
   shows what a plan would do to a week before anything is proposed;
   `suggest_load` gives the next weight. Do not compute these yourself and do
   not quote a population figure as their target.
3. Keep four things apart when you answer: what was observed (their logs),
   what a tool estimated, what the research says about groups, and what is a
   heuristic. Label each.
4. Offer two or three options with their costs, ask which one they want, and
   only then propose it. Plan changes go through the write tools, whose results
   are proposals: report a change as done only when its status is `accepted`.
5. If pain, injury, a medical condition or rehabilitation is involved, stop
   here: there is not enough evidence in this skill to adapt training around
   it. Say so and suggest a qualified professional. Do not invent a
   progression or a diagnosis.

## Evidence

Each claim cites a source in `references/sources.md` by key. These are group
results; none sets an individual target.

- **Volume.** Across 15 studies, more weekly sets per muscle went with more
  growth, a graded dose-response [ref:schoenfeld-2017-volume]. A later
  meta-regression of 67 studies (2058 participants) again found growth rising
  with weekly sets, but with diminishing returns, and found that counting
  indirect sets (a muscle that assists, such as triceps in a press) as half a
  set fitted the data best [ref:pelland-2026-dose-response]. A smaller review
  restricted to trained young men found no difference between 12-20 and more
  than 20 weekly sets for quadriceps and biceps, but an advantage for more
  sets on triceps [ref:baz-valle-2022-volume]. So the research agrees that more
  volume tends to help up to some point, and disagrees on where returns stop
  being worth the time and fatigue.
- **Frequency.** With weekly volume equated, training a muscle more often did
  not meaningfully change growth [ref:schoenfeld-2019-frequency]. The 2026
  meta-regression found frequency's effect on growth compatible with
  negligible, while it did help strength [ref:pelland-2026-dose-response].
- **Load and rep range.** A narrative review argues growth can be obtained
  across a wide range of loads, against the older idea of a single
  "hypertrophy range" [ref:schoenfeld-2021-continuum]. It is a review and a
  position, not a new trial.
- **Proximity to failure.** A meta-analysis of 15 studies found at most a
  trivial advantage for training to set failure over stopping short, and none
  for momentary failure specifically [ref:refalo-2023-failure]. A series of
  exploratory meta-regressions, with reps in reserve estimated from study
  descriptions, found growth increasing as sets ended closer to failure while
  strength did not change [ref:robinson-2024-rir]. The authors ask for caution
  because the reserve was estimated. Researchers do not agree on how close is
  enough.
- **Progression.** In 43 trained adults over 8 weeks, adding reps at a fixed
  load and adding load at fixed reps gave similar results
  [ref:plotkin-2022-progression]. Both are viable.
- **Individual response.** In 585 adults training the elbow flexors for 12
  weeks, size changes ranged from -2% to +59% [ref:hubal-2005-variability]. In
  20 trained men training each leg differently, the difference between people
  was about 40 times larger than the difference between protocols
  [ref:damas-2019-individual]. Who the person is matters more than which of
  these reasonable programmes they follow, and their own logs are the best
  evidence about them.

## Heuristics

Common coaching practice. No verified source in this skill supports these as
rules; present them as options, never as facts.

- **Volume landmarks (MEV, MAV, MRV).** A coaching model naming a minimum
  effective, a most adaptive and a maximum recoverable weekly volume. They are
  not measured quantities, and no study gives them for an individual. In
  vibeTraining they exist only when the person records their own with
  `set_volume_landmarks`; `volume_recommendation` and `simulate_plan` then
  respect them. Never fill one in from a chart.
- **Block length.** Blocks of anything from about 3 to 12 weeks are used, and
  some people never use blocks at all. No verified source in this skill
  settles a best length.
- **Volume over a block.** Some coaches add sets week to week and then reduce;
  others keep volume fixed and progress load or reps. Both are practised.
- **Double progression.** Add reps within a range, then add load and drop back
  to the bottom of the range. Widely used; the trial above supports reps or
  load progression, not this exact scheme.
- **Priorities.** Training one or two muscles with more volume while holding
  others at less is common; how little holds a muscle is not settled here.

## Person-specific assumptions

Never assume these; read them or ask.

- Goal, the time they have, how many days they can train, and what they
  enjoy (adherence beats a better programme they will not follow). Look in
  `memory_context` and `memory_search` before asking.
- Training history: what they actually did lately (`weekly_volume`), not what
  a plan said. Changes are safest as steps from that baseline.
- How they respond: their own feedback ratings drive `volume_recommendation`,
  which reports how many rated sessions it rests on and declines on one.
- Landmarks they set themselves, if any (`list_volume_landmarks`).
- Equipment and limitations from memory; a limitation sends you to step 5.
- How they rate effort: the app stores RPE 1-10. If they think in reps in
  reserve, RPE 8 is read as 2 in reserve; if they do not rate effort, do not
  invent RPE.

## Decisions

The person's to make. Offer the options that fit, with their costs, and ask.

- **Weekly sets per muscle.** Keep current volume, add a few sets, or reduce.
  More may add growth with diminishing returns and costs time and recovery;
  less saves time and may grow less. Show the effect with `simulate_plan`.
- **Frequency.** Once, twice or more per muscle per week. For growth it is
  mostly a question of schedule and session length when volume is equal.
- **How close to failure.** Stop with a few reps in reserve, take the last
  set close to failure, or go to failure on some exercises. Closer may help
  growth a little and costs more fatigue and discomfort; the evidence is mixed.
- **Rep ranges and loads.** Heavier and lower reps, lighter and higher reps,
  or a mix. Growth is possible across the range; preference, joint comfort
  and time per set decide.
- **Progression.** Add load, add reps, add sets, or a combination.
  `suggest_load` gives the next weight against the prescription.
- **Structure over time.** Fixed blocks with planned easier weeks, or open
  ended with changes when progress stalls or fatigue builds. For easier weeks
  and fatigue, load the `fatigue-autoregulation` skill.
- **How effort is prescribed.** RPE, reps in reserve or percentage of a
  maximum. Coaches disagree; vibeTraining records RPE.
