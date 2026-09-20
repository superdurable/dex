# Canvas layout: principles, not per-screenshot tuning

A view here places boxes by arithmetic over a ranked graph. Every spacing constant is therefore a
claim about every Flow, not just the one on screen. This directory holds the machinery that keeps
that claim honest.

## The loop

Change a layout constant or rule the same way every time:

1. **Measure first.** `layoutQuality.test.ts` runs all shipped Flow definitions in
   `docs/src/data/flow-definitions/` through every detail and direction. Run it before touching
   anything and keep the numbers.
2. **Find the worst case by number, not by eye.** The report names the flow and mode holding each
   worst metric. That is where to look, and it is often not the drawing you were shown.
3. **Change the rule, then re-measure the whole corpus.** A constant that improves one graph and
   worsens four is a regression the screenshot cannot show you.
4. **Break the guardrail on purpose.** Revert the fix and confirm the test fails. A guardrail that
   passes both ways guards nothing — three attempts at one in this directory were vacuous before
   anyone noticed.
5. **Set the limit from the gap between the two runs.** With the merge guard removed, the region-hole
   metric reads 1.09; an ordinary gap between fan members reads 0.65. The limit sits at 0.85 because
   the data put it there.

## The principles

Declared in `layoutQuality.ts` with their metric and limit, so each one can fail a test rather than
only guide a reader. Current corpus worst is in the report the test prints.

| Principle | Reading |
| --- | --- |
| `no-collision` | Two cards never occupy the same space. |
| `tight-regions` | A region has no gap inside it wide enough to hold a card. |
| `group-cohesion` | A group sits in one place where the graph allows it. |
| `short-edges` | An arrow spans about a rank, not the whole drawing. |
| `reading-order` | The first card is the start of the Flow. |

They pull against each other on purpose. Cohesion moves a card toward its group and lengthens its
edges; `short-edges` is where that stops. Do not relax a limit to let a change through without saying
which principle you are trading away and what the corpus number becomes.

## Metric traps found the hard way

- **Area is not tightness.** Five failure Steps at five ranks make a tall thin region that is 81%
  floor and entirely correct. Judging a region by how much of its area holds a card punished exactly
  the vertical strip a group is supposed to form.
- **Do not measure the band, measure the cards.** Uncovered-width charged every region for its own
  padding and label strip: 0.435 of a single-card band left-right, with no defect present.
- **Express a gap in cards, not pixels.** A hole reads as a hole because a missing box would fit. The
  same 240px means different things beside a 260px card and a 60px one.
- **Both axes exist.** Ranks run down the page top-down and across it left-right. Two metrics here
  assumed one axis and reported nonsense for half the corpus, including a 650 where the true value
  was 5.3.
- **Check the edge set reaches the function.** `cyclicRanks` takes `{from, to}`; `PocFlow`
  transitions carry `fromStepId`/`toStepId`. Passing them unmapped silently dropped every edge and
  returned rank 0 for all 25 Steps, which made an adjacency test vacuously true and moved cards that
  should not have moved.

## Bugs this machinery has caught

- **Region merging under-converged.** The greedy merge bounded its passes by the live `rects.length`,
  which shrinks on every merge while the counter rises. A group spanning seven rows stopped after
  four merges and shipped three regions where one was available.
- **A stylesheet import order made `stroke-width` dead.** `@xyflow/react`'s stylesheet loads after
  `canvas.css` at equal specificity, so an unqualified `.react-flow__edge-path` rule never applied and
  every edge sat at the library default.
- **`nodesep`/`ranksep` are inert here.** This layout takes only within-rank order from dagre.
  Setting them looks like a spacing fix and changes nothing; `rankBase` is the real lever.
