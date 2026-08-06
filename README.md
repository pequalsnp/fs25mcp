# fs25mcp

An MCP server for **Farming Simulator 25**: it reads your installed game
and your savegame, and answers questions about them with numbers rather
than recollection.

It exists because of a measured failure. Asked which production chain was
most profitable, an LLM with a good corpus quoted the Rice Box recipe
correctly — 100 l Rice in, 45 l Rice Box out, 10 cycles/hour — and the
output price correctly at 2.73/l, both verbatim from the game files. Then
it called that "a 23% markup". Rice costs 1.1/l, not the 1.0 it silently
assumed. The real figure is 11.68%.

Correct facts, wrong arithmetic, and an answer that read exactly like the
grounded ones. A model asked to divide two numbers it did not look up
will sometimes invent one of them. So the division happens here.

## What it reads

**Your install** (`internal/fs25data`) — the shop catalogue, crops, fill
types and production chains, from the game's own XML. Everything here is
base-game content by construction: DLC ships encrypted, and mods live in
a folder this never touches.

**Your savegame** (`internal/fs25save`) — money, loan, farmland owned,
vehicles, and every production point with the recipes actually switched
on and what is sitting in its storage.

The save is also the ONE place that knows a building came from a mod: it
records `modName` and a `$moddir$` filename per placeable. No store
document can tell you that, because mod content never appears in one.
Every production point is reported as `base game` or `mod: <name>`.

It deliberately does not read the `<players>` block in `farms.xml`. That
carries `uniqueUserId` and `lastNickname`, which are personal identifiers
with no bearing on any farming question.

## What it computes

`internal/plan` costs every recipe against the game's own fill-type
prices: input cost and output value per cycle, gross, net per hour after
running costs, margin, and how many in-game hours a factory runs before
it pays for itself. That last one matters more than the margin — a 330k
factory netting 126.5/hour takes **2,609 hours** to pay back, which is
the number that should lead a casual player's plan.

An unpriced ingredient drops the recipe rather than being costed at zero:
treating "the game did not say" as free turns a marginal chain into a
spectacular one.

## Status

Parsers and planning are done and tested. The MCP server and the
dial-out relay client are next.

MIT.
