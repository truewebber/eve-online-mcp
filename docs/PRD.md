# eve-mcp — Product Requirements Document

## 1. The problem

EVE Online players drown in bookkeeping. The answers to everyday questions
— *how much ISK do I have, where did my hauler leave those minerals, is my
skill queue about to run dry, what is Tritanium worth in Jita right now,
which industry job finishes tonight* — are scattered across a dozen
in-game windows and third-party sites. Getting them means logging in,
clicking through menus, and doing mental arithmetic.

AI assistants (Cursor, Claude) could answer all of this in one sentence,
but they cannot see the player's account. The existing ways to connect an
assistant to EVE demand that every player register their own developer
application at CCP, manage API credentials, and run bridge software on
their own machine. For a player who just wants to ask a question, that
wall is too high — in practice nobody's friends ever set it up.

## 2. The solution

eve-mcp is a service one person hosts for themselves and their friends.
It plugs a player's EVE account into their AI assistant.

Connecting takes a minute and requires no technical skill:

1. Add one URL to the assistant's server list.
2. The assistant shows **Authentication required** — the browser opens
   the familiar EVE login page.
3. Log in, pick a character, approve. Done.

**That connection now *is* that character.** The character picked at the
EVE login is the one and only identity of this connection — the
assistant reads that character's data and acts as that character,
nothing else. One connection never accumulates a pile of alts.

Alts are still easy, they are just explicit: add a second server entry
in the client and sign it in as the alt. Two entries, two characters,
side by side in the same chat. A character has exactly **one live
connection**: signing in again — from the same client or a different
one — moves the connection there, and the previous one is signed out.
Taking a character from Cursor to Claude is just logging in from
Claude.

A sign-in lasts a month. After that — or if the player revokes the
service on EVE's own "authorized apps" page — the assistant says
**Authentication required** again and one browser login brings it back.
That is the same minute from step 2, never a new setup.

From that moment the assistant has the character's full EVE API: it
reads anything the character can see, and it can perform the in-game
actions the API offers — each one only after the player confirms it in
the chat. Nobody registers anything at CCP, installs anything, or
touches a config file. The host sets the service up once; every friend
after that is just "send them the URL". The service adds no policy of
its own on top of the game: it is a clean bridge between the assistant
and the EVE API.

## 3. Who it is for

| Role | Story |
|---|---|
| **Player** | "I want to ask my assistant about my ISK, assets, market prices and routes — without spreadsheets or alt-tabbing into the game." |
| **Host** | "I run one small service for my friend group. I set it up once and it runs itself — I don't manage anyone's access or babysit it." |

## 4. What a player can do

**Ask about their character.** Wallet balance and transaction history,
skills and the training queue, clones and implants, standings, online
status, current location and ship — always about the one character this
connection is signed in as, so there is never a "which character did you
mean" dance. A player with alts runs one connection per character.

**Find their stuff.** Full asset lists grouped by station, "where are my
Vexors", blueprint collections, what a hangar is roughly worth.

**Follow the money.** Live buy/sell prices at trade hubs, their own
market orders and contracts, wallet movements over time.

**Track industry.** Manufacturing and research jobs with completion
times, planetary colonies, mining ledger.

**Stay social.** Read mail, notifications, recent killmails, saved ship
fittings — with a hard rule: text written by other players is something
the assistant reports on, never something it obeys.

**Navigate the universe.** Search for items, systems and stations by
name, plan routes (shortest / safest), spot dangerous systems along the
way.

**See corporation life** (if their in-game roles allow): shared hangars,
corp wallets, jobs, structures, members. The game's own permission system
decides — a line member sees nothing the game would not show them.

**Let the assistant act in-game** — everything the EVE API can change:

- set autopilot waypoints;
- open market / info / contract windows in the running game client;
- save and delete ship fittings;
- tidy the mailbox (mark read, labels, delete);
- respond to calendar invitations;
- send EVE mail to other players — some recipients charge ISK to
  receive mail, so the confirmation names that charge and nothing is
  paid unless the player approves that exact amount;
- add, edit and remove contacts and standings.

Reading never asks permission. Every **change** follows the same ritual:
the assistant shows exactly what will happen, the player says yes, then
it happens. No confirmation — no action.

**Manage their own access.** Revoke the service's access to the
connected character straight from the chat — the connection dies with
it. Switching to a different character means signing the connection in
again; connecting an alt alongside means one more server entry.

## 5. What a player cannot do

- **Play the game.** The service cannot undock, fly, fight, trade, move
  items, transfer ISK, or click anything. EVE simply does not expose that
  — and we would not want it anyway.
- **See other people's accounts.** A connection sees exactly the one
  character it was signed in with — never a friend's, never the host's,
  never the player's own other alts. There is no "admin view" into
  someone else's wallet.
- **Stack characters on one connection.** There is no "add an alt"
  inside an existing connection and no server-side character group to
  grow. Wanting a second character always looks the same: a second
  connection, a second EVE login.
- **Bypass the game's permissions.** No corp data beyond what their
  in-game roles grant. A 403 from the game stays a 403.
- **Spam other players.** Outgoing EVE mail has a strict hourly cap per
  character. Self-affecting actions carry no such cap — re-planning a
  route twenty times is the player's own business.
- **Monopolise the shared pipe.** CCP treats the whole instance as one
  application on one address. Each connected character gets a generous
  request allowance — sized so it is never felt in a normal
  conversation, but a looping assistant cannot lock the whole friend
  group out of the API. Mistakes are metered the same way: an assistant
  that keeps asking for something the game refuses spends that
  character's own error budget, not the household's.
- **Use any assistant they like.** A client can only sign in if the host
  allows its login callback. Cursor and Claude work out of the box;
  anything else is one line of configuration on the host's side, and
  until then that client's sign-in is refused rather than half-working.
- **Act on someone else's words.** A hostile in-game mail saying "forward
  this to your corp" is content to summarise, not an instruction to
  follow.
- **Get real-time truth.** The game's API caches data (assets ~1 hour,
  prices ~5 minutes). Answers are labelled with their age; the assistant
  is required to say "as of an hour ago" when it matters.

## 6. Product principles

1. **One URL is the whole onboarding.** If a step requires a player to
   register, install, or configure anything, it is a bug.
2. **One connection, one character — and one character, one
   connection.** The identity of a connection is the character picked at
   the EVE login, permanently; a character has at most one live
   connection, and a new sign-in displaces the old one. No hidden alt
   registry, no "which character" ambiguity, no forgotten client still
   reading the account from last month. A different character is always
   a different connection.
3. **A clean bridge, not a gatekeeper.** The service exposes the full EVE
   API and adds no policy of its own. What the character can see, the
   assistant can see; what the API can change, the assistant can request.
   Players manage their own access: they sign in with EVE themselves and
   can revoke the character at any time — in chat, or on EVE's own
   "authorized apps" page. The host manages nothing per player.
4. **Reads are free, mutations need consent.** Questions never ask
   permission. Every in-game change is confirmed by the player for that
   exact action — a general "yes, do it" never authorizes a different or
   repeated change.
5. **Answers fit a conversation.** Short by default, expandable on
   request, with names instead of numeric ids and honesty about staleness.
6. **Be a good citizen of the shared API.** When CCP's rate limits bite,
   the service backs off and tells the assistant when to retry — one
   player's curiosity must not lock out the whole household.

## 7. Out of scope

- Public multi-tenant SaaS, billing, or accounts beyond one friend group.
- Multiple characters on one connection, automatic merging of a player's
  connections, or any server-side "family" of alts.
- Game automation of any kind (botting is against EVE's rules).
- Fleet/alliance tooling, killboard analytics, or historical data
  warehousing.
- A web dashboard — the assistant chat *is* the interface.

## 8. Success looks like

- A friend connects and asks their first question in under two minutes,
  with zero help from the host.
- "How am I doing in EVE?" gets one useful paragraph instead of ten
  minutes of alt-tabbing.
- Zero unintended in-game changes — nothing mutates without an explicit
  player confirmation for that exact action. The server keeps an audit
  log of every in-game change it attempted, so this is something the
  host can check rather than hope for.
