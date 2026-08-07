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

## Running it

Two shapes, because they answer different questions. Neither needs a
config file, and neither needs a language model to set up — the model is
the consumer at the end of the pipe.

**With the game** (companion app). On Steam, Properties → Launch Options:

    fs25mcp play -- %command%

Steam expands `%command%` to the real launcher, Proton and all, so the
game starts exactly as it would have. The server runs for the length of
the play session and stops with it. Nothing to remember.

**All the time** (service). Better when something OTHER than the player
wants the state — an assistant answering questions while the game is
shut:

    systemctl --user enable --now fs25mcp

**Check it worked**, without involving a model:

    $ fs25mcp status
      install        .../steamapps/common/Farming Simulator 25
                     version 1.21.1.0, 550 store items, 25 crops, 176 fill types
      using          savegame4 — "My game save" on North Frisian 25 (a mod map)
      money          136203 (loan 250000)
      farmland       2 of 128 owned

It finds the game and the savegame by itself, including inside the Proton
prefix on Linux, which is where the save actually lives and where nobody
would think to look. It picks the savegame you played most recently.
`-install` / `-save` override that for a Steam library on another drive.

Starting before the game is installed is fine: it serves anyway and picks
things up when they appear, rather than exiting into a restart loop.

## Talking to it

`-addr` serves MCP over HTTP locally (default `127.0.0.1:14005`).

`--relay ws://host/relay/fs25` instead dials OUT to a relay and serves
through the tunnel, for the usual case where the gaming PC is behind a
firewall and the assistant is somewhere else on the LAN. No inbound port,
no firewall change, reconnects forever.

MIT.
