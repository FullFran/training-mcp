---
name: exercise-selection
description: "Lay out options for choosing, substituting and varying exercises, free weights or machines, and muscle-length emphasis, from the person's equipment, history and preferences, for them to choose."
license: MIT
metadata:
  version: "0.1.0"
  author: "training-mcp maintainers"
  review_status: draft-unreviewed
  triggers: "choosing exercises for a muscle or a plan; replacing an exercise because a machine is taken, missing or uncomfortable; deciding whether to rotate or keep exercises between blocks; free weights versus machines; whether to emphasise stretched positions or partial range"
  limitations: "draft that no trainer has reviewed yet; the trials are few, short and mostly of young adults, many of one sex only, so they show possibilities, not one person's best exercise; not for pain during an exercise, injury, rehabilitation or any medical condition: do not suggest a pain-free variant, defer to a professional; machines that share a name differ, so it gives no load for a new machine"
  required_tools: "memory_context memory_search list_plans get_plan exercise_history list_exercise_groups list_exercise_notes volume_by_muscle"
---

# Exercise selection

Draft 0.1.0, not reviewed by a trainer. Say so if the person relies on it.

Many exercises train a muscle well. This skill lays out what is known about
choosing and changing them, so the person picks what fits their body, gym and
taste. It does not hold a list of best exercises.

## How to use it

1. Read the person first: `memory_context` and `memory_search` for equipment,
   gym, limitations and what they like or dislike; `list_plans` and `get_plan`
   for the current routine; `list_exercise_groups` for how their exercises
   are assigned to muscles (a weekly volume count depends on it);
   `list_exercise_notes` for setup notes.
2. Look at what an exercise has done for them with `exercise_history`. A set's
   `equipment` tells two machines with the same name apart; history on one
   machine says little about the load on another.
3. Offer two or three candidates with their costs and ask. Changing today's
   session or a plan goes through a write tool (for example
   `swap_session_exercise`) whose result is a proposal; say it is done only
   when its status is `accepted`.
4. For a new exercise or machine, there is no history: suggest starting
   conservatively and letting the first sets set the load. Do not carry a
   load across from a different machine or exercise.
5. Pain during an exercise, an injury or a medical condition: stop here.
   There is not enough evidence in this skill to choose a variant around it.
   Do not suggest a "safer" alternative; suggest they stop that exercise and
   see a qualified professional.

## Evidence

Each claim cites a source in `references/sources.md` by key. Group results.

- **Free weights or machines.** A meta-analysis of 13 studies (1016
  participants, 5 studies measuring growth) found no difference in growth
  between free-weight and machine training; strength gains were specific to
  the kind of equipment trained on, and the authors left the choice to
  preference and goals [ref:haugen-2023-machines].
- **Varying exercises.** A systematic review of 8 studies (241 young men)
  concluded that some systematic variation may favour growth in more regions
  of a muscle and strength, while excessive, random rotation may hinder
  gains [ref:kassiano-2022-variation]. In one 12-week trial of 49 people,
  groups that varied their lower-body exercises grew all four quadriceps heads,
  whereas some heads did not grow significantly in groups with a fixed
  exercise, and whole-quadriceps growth was similar across groups
  [ref:fonseca-2014-variation].
- **Muscle length.** In 20 adults training one leg with a seated and the
  other with a prone leg curl for 12 weeks, the seated leg, where the
  hamstrings work more stretched, grew more (+14% versus +9%)
  [ref:maeo-2021-length]. In 45 untrained women, knee extensions through the
  bent-knee part of the range grew some regions of the thigh more than the
  full range [ref:pedrosa-2022-rom]. These are single trials on single
  exercises; they suggest, not prove, a general rule.

## Heuristics

Common practice. No verified source in this skill supports these as rules;
present them as options, never as facts.

- **Stimulus-to-fatigue ratio.** Choosing exercises that feel like they train
  the target muscle hard while tiring the rest of the body little. Popular
  and not measured in any study cited here.
- **Stability near failure.** Machines and supported exercises are often
  chosen for sets taken close to failure because balance and technique limit
  them less. Plausible; not established here.
- **Substitution.** Replacing an exercise with one of the same movement
  pattern, or one that loads the muscle in a similar position, is the usual
  approach. No verified rule here says which substitute is equivalent.
- **Rotation cadence.** Some keep exercises for a whole block to track
  progress; others rotate some each block. The review above argues against
  random, frequent change, without fixing a cadence.
- **Exercises per muscle.** One, two or more exercises per muscle per week
  are all practised.

## Person-specific assumptions

Never assume these; read them or ask.

- Equipment and gym: what is available, and which machines they mean (the
  `equipment` on a set).
- Preferences and what feels right: comfort, enjoyment and confidence in an
  exercise shape whether they keep doing it.
- Their limitations, from memory; a limitation sends you to step 5.
- How their exercises map to muscles (`list_exercise_groups`), and whether
  the change would move weekly volume per muscle (`volume_by_muscle`).
- History: how they progressed on the current exercise (`exercise_history`)
  and whether they want to keep tracking it.

## Decisions

The person's to make. Offer the options that fit, with their costs, and ask.

- **Keep or change an exercise.** Keeping it makes progress easy to track;
  changing it may add variety or fit better, and resets the history.
- **Free weights, machines or both.** Similar for growth in the evidence
  above; strength is specific to what they train; preference, access and
  comfort decide.
- **Emphasis on stretched positions or partial range.** An option some
  trials favour for certain muscles; not required.
- **How often to rotate.** Never, each block, or some exercises each block.
- **Today's substitute.** When a machine is taken: wait, use another
  exercise for the same muscle, or use another machine of the same kind,
  starting conservatively on the new one.
