# Dex Web Frame: One Bar, Focus on the Run

Status: Proposed
Date: 2026-11-12

## Summary

Dex Web stacks one bar per level today: the app header, the run's breadcrumb
and actions, a summary card with an event pager, and the run tabs. On a
1290×660 window the run tabs start at about 405px and the tab content at about
440px, so two thirds of the window is chrome before any work shows. A host that
embeds Dex Web adds its own bars above that.

This plan gives Dex Web one frame on every page, in two parts:

- **One bar.** A single 48px bar holds where you are (a trail from home), what
  this page is, its views and its actions.
- **Focus on the run.** The run page puts the Timeline under that bar at full
  height, with the run's facts in an inspector panel that folds.

The same frame serves `dexcli dev`, the standalone `dex-web` binary and a host
that serves Dex Web through `web/embedded`. A host opens Dex Web as its own page
and links back to it. It never wraps Dex Web in its own chrome.

## Why

- **The work starts too low.** People come to the run page to read the Timeline
  or act on the run. Chrome currently takes about 440px before either appears.
  Comparable durable-execution consoles take about 250px (the best) to 600px
  (the worst). The target here is under 100px.
- **Things repeat.** The run page shows four status words: "Dex server",
  Live, the run's status, and an agent session's status. The "Dex server" pill
  is a fixed label that checks nothing. Copy link copies the address of the
  frame it runs in, which is wrong when a host frames it. Refresh repeats what
  Live already does.
- **Nothing says where you are.** Each level adds its own bar: app, run
  identity, run summary, run views. None of them is "the" header.
- **Engine detail is promoted.** The event pager ("Latest #27 · Page 1 of 1",
  four page buttons, page size) is the widest control on the page. It pages the
  Timeline and Execution graph, not the run.

## Principles

1. **One bar per page.** The trail, the page's title, its views and its actions
   share one row. There is no separate app header.
2. **The trail replaces the levels.** Its root is home: the Dex mark locally,
   or the host's name with a link back when a host serves Dex Web. Each
   ancestor is one link, and the section segment switches between Dex Web's
   sections.
3. **The object is the focus.** Below the bar, the run page shows the selected
   view at full height. Facts, attributes and the selected event live in a right
   inspector that folds and never closes.
4. **One focal colour.** On a run page that is the run's status. State hues mean
   state only. The brand colour marks the primary action.
5. **Engine detail sits where it acts.** Event paging belongs in the toolbar of
   the views it pages. The run attempt shows only when there is more than one.
   IDs are copied from where they are shown.
6. **Removing a control names its new home.** Nothing becomes unreachable.

## Prior art

The UI notes for this work were checked against established durable-execution
consoles, and each note was kept, refined or challenged:

| Note | Common pattern | Decision |
| --- | --- | --- |
| Avoid borders; content first; type and colour carry the UI | Consoles separate regions with tint steps and spacing. They keep rules in data tables and on form controls. | **Refined.** Regions separate by tint and spacing. Tables keep row rules, and controls keep their outline. |
| Livelier, modern | The best console uses a sticky floating header tinted by the object's state. The weakest is criticised as chunky and low-density. | **Refined.** Liveliness comes from motion, the status tint and type contrast. It never comes from more padding. Data views keep their density. |
| Colour directs attention; one focal point | Every console uses fixed status hues. One tints the whole detail header by status. | **Kept.** Run status is the focal colour, and the bar carries a faint wash of it. |
| Icons where they help | Icons sit on navigation, actions and statuses. | **Kept.** Tabs keep their icons. At narrow widths, inactive tabs show only their icon and keep their name as an accessible label. |
| Smooth transitions | One console animates every running row, and users report the CPU cost. | **Refined.** Only state changes animate, for about 150ms, honouring `prefers-reduced-motion`. Nothing animates continuously except the one Live dot. |
| Keep only home at deep levels | Consoles keep a collapsible nav rail. The best one makes breadcrumbs a history trail that keeps your filters. | **Kept, as a trail.** Dex Web has one main section and three that appear when they apply, so a section switcher in the trail does the rail's job. The trail's section link returns to your last search. |
| Timeline before Execution graph | Consoles default to a timeline. The best has no graph view at all. | **Kept.** Timeline is first and is the default. |
| Merge Stop and Time travel into Operate | One console uses a split button whose primary action follows the state. Another shows its safe actions and puts destructive ones under "More". | **Kept, as a menu with no primary action.** Changing a run's state always takes two clicks. Unavailable items stay listed with the reason. |
| Remove Refresh and Copy link | Detail pages poll while the object is live. The address bar is the link. | **Kept for the run page.** Live covers Refresh. The run list keeps Search, which reruns the query. |

## Design

### The bar

```text
[▶ | host ●] › Runs ▾ › [status] claude-sdk-…788fb4   Timeline  Graph  Overview  Streams  Agent·  Actions·        ● Live   Operate ▾   ⚙
```

- **Root.** Locally, the root is the Dex mark, which links to the run list. When a host
  serves Dex Web, the root is the host's name and links back to the host page. A
  dot beside it shows whether Dex Web reaches the Dex Server. The dot replaces
  the "Dex server" pill.
- **Section.** "Runs ▾" lists the sections that apply: Runs, then Work queue,
  Designs and Connectors under today's applicability rules. Each of the last
  three keeps a faint experimental dot with a tooltip. The section link
  returns to the last search (`?q=`, `size`, `page`).
- **Page.** On the run page this is the status chip and the Flow ID. Clicking
  the Flow ID copies it. On other pages it is the page's name.
- **Views.** These are the run tabs, in a new order: Timeline, Execution graph,
  Overview, Streams, then Agent session, Actions and Design. A tab still
  appears only when it applies. Experimental tabs carry a dot, not an uppercase
  badge. The tab ids and `?view=` values don't change.
- **Actions.** On the run page these are Live and Operate.
  - **Live** is a dot. Clicking it pauses and resumes following the run.
  - **Operate ▾** holds Stop… and Time travel…, which open today's dialogs.
    Stop is disabled with "The run has ended" once it is terminal.
- **⚙.** Holds Theme, Timezone, Docs and the Dex Web version.

The bar gives way in a fixed order as the window narrows:

1. Inactive tabs show only their icons.
2. The trail shortens to "‹ Runs".
3. The inspector becomes a drawer.

### The run page

- **The selected view fills the space under the bar.** The event pager moves
  into the Timeline and Execution graph toolbar as "Events #1–#27 ▾". Its page
  buttons show only when there is more than one page. Page size is an item in
  the same menu.
- **The inspector** is on the right, about 320px wide and resizable. It folds
  to a 40px rail and remembers its width and fold state per browser. Its
  contents, in order:
  - Summary: started, closed, duration, run attempt, run chain, Flow type, and
    Run ID with copy.
  - The selected event's panel, which today is the run sidebar.
  - On Overview, Streams, Agent session and Actions, only the summary shows.
- **Run attempt** shows as a chip beside the status only when the current
  attempt is above 1 ("Attempt 2 of 3 ▾"). The earlier-attempt banner stays.
- **Removed controls:**
  - Refresh: Live already does it.
  - Copy link: use the address bar.
  - The Flow ID and Run ID chips: the bar's Flow ID and the inspector's Run ID
    replace them.

### Other pages

The run list, Work queue, Designs and Connectors drop the app header and their
"Flows / …" breadcrumb, and gain the bar. Their contents don't change in this
plan.

### Host link

`web/embedded.Config` gains an optional `HostLink` (name and URL), which
defaults to none. When it is set, the served page carries
`<meta name="dex-web-host-name">` and `<meta name="dex-web-host-url">`,
injected the way `dex-web-base-path` already is. The SPA reads them to draw the
trail's root.

There is no chrome-hiding mode. A host that frames Dex Web shows the same
frame, so it should open Dex Web as a page of its own.

### Look

These rules apply to every surface the frame touches:

- Regions are separated by background steps (`--paper`, `--raised`, `--well`)
  and spacing, not outlines.
- The run's status hue washes the bar faintly.
- Type sizes stay as they are or grow, never shrink.
- Transitions run about 150ms on tab changes, the inspector fold, menus and
  the Live dot.

Inside each tab, the look pass comes after the frame. It is listed under Order
and PRs.

## As built

Review rounds against the real UI changed these parts of the design. The
implementation follows this section where it differs from the sections above.

- **No inspector column.** The run's facts open from the Flow ID button as
  **Run details**: started, closed, duration, run attempt or run chain, latest
  event, Flow type, child Flows with their status, and copy buttons for the
  Flow ID and Run ID. The selected event keeps its panel on the right.
- **The Flow ID is the heaviest item in the bar.** Long IDs shorten in the
  middle, so both ends stay visible. The Flow type and copy buttons are in Run
  details, so the trail shows the same items at every desktop width.
- **Operate is a split button.** Its main action follows the run: **Stop**
  while it runs, **Time travel from #N** at the failed event on a failed run,
  and **Time travel** otherwise. The menu holds the other action with its full
  name.
- **Relations sit under the bar.** The run line names a child Flow's parent
  ("Child of …", linking to its current run) and a run's child Flows, and holds
  the attempt picker. Overview lists the relations under **Related Flows**, and
  the event row where a child Flow started links to it.
- **A state line under the bar.** A failed run states its error, and every
  other run states its newest event and Step type. Its button selects that
  event.
- **Desktop Timeline.** At 1280px and wider the Timeline shows its overview, a
  one-line event list in its own scroll box, and the selected event beside
  them. J, K and the arrow keys move the selection, and T opens time travel at
  it. The selected event is kept in the address as `event=`. A time travel
  fork reads as a divider, and the row where a child Flow started links to it.
  The labelled lane is behind **Show lane**.
- **Time travel from an event.** The selected event's panel and each event row
  open time travel set to that event.
- **Jump box.** ⌘K or Ctrl+K suggests Flows by ID prefix, opens a pasted run
  link, and lists recent runs and pages.
- **Reachability.** The dot reads `/readyz` every 30 seconds.
- **One bar for every run.** The bar shows status, duration and the Flow ID
  on every run, and every tab keeps its name from 1280px up. At laptop widths
  the tab text is a step smaller and the ⌘K hint hides. Below 1280px later tabs
  show only their icons, with tooltips.
- **Flows page.** All, Running, Failed and Completed search by status in one
  click, and a status-only search keeps the search card one row.
- **Known trade-off.** Without Refresh, a closed run that another client time
  travels shows the change after a reload.

## Order and PRs

1. This design (docs only).
2. **Frame:** the bar on every page, ⚙, the section switcher, the host link and
   the reachability dot. `AppHeader`, `PageBreadcrumb` and the run page's
   breadcrumb row go. Includes a `web` change-log fragment.
3. **Run page:** the views in the bar, Operate, the inspector, the pager in the
   view toolbar, the attempt chip, experimental dots and motion. Includes a
   `web` change-log fragment.
4. **Look pass per page:** borders to tints, focal colour, icons, for the run
   list, Overview, Streams, Work queue, Designs and Connectors.

PRs 2 and 3 may ship in one patch release. Neither changes an API, a URL or
the stored preferences.

## Tests

- Vitest component tests:
  - The bar renders the trail for both roots: local, and host-linked through
    the meta tags.
  - The section switcher lists only the applicable sections.
  - Operate opens the Stop and Time travel dialogs, and Stop is disabled
    with its reason on a terminal run.
  - The tab order and the Timeline default hold.
  - `?view=` deep links still open each tab.
  - The pager shows page buttons only for more than one page.
  - The attempt chip shows only above attempt 1.
- Update `RunDetailsPage.test.tsx` and `RunDetailsExperimentalTabs.test.tsx`
  for the removed controls and the new homes of each function.
- Go: `embedded` handler test that `HostLink` is injected as escaped meta, and
  is absent when it is unset.
- Browser checks before each PR leaves Draft. Run against a real `dexcli dev`
  run with more than one event page, an agent session and a time-traveled
  attempt, at 1280×720, 1024×640 and 390px wide, in light and dark. Attach
  screenshots.

## Documentation

- `docs/content/` pages that describe Dex Web's header, run page buttons or
  tab order are updated in English and Simplified Chinese in PRs 2 and 3.
- `web/embedded` doc comments describe `HostLink`, marked "Since" the next
  patch.
- `skills/`: N/A. A UI layout is not a stable rule or concept.

## UI/UX

This plan is the UI/UX change. Its acceptance bar:

- The run page's selected view starts within 100px of the top at 1280×720.
- Every removed control's function is reachable as listed above.
- The page has no horizontal scroll at 390px.
- Both themes are reviewed with before and after screenshots.

## Non-goals

- What each tab shows, its usability, and labels and copy.
- How a host designs its own pages, including where it puts the link that opens
  Dex Web.
- A chrome-hiding mode for framed hosts.
