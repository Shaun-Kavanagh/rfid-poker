# 10-player home table: build plan

This fork adapts rfid-poker for a **10-player** Texas Hold'em home game with a **live stream overlay** (OBS), running the server on a Mac.

## Progress

| Phase | Status |
|---|---|
| 0. Server and simulator on the Mac | Done |
| 1. Multi-card read test (Core2 + 1 reader) | Done: **Path A** (1 reader per seat) |
| 2. Buy hardware | Next (single-Core2 list below) |
| 2b. Long-cable test | When the cables arrive |
| 3. Table firmware (Core2) | Not started |
| 4. Bridge and server on macOS | Not started |
| 5. Table install | Not started |
| 6. Stream overlay | Not started |

## Decisions

Newest last. Each entry says what was decided, why, and what would make us revisit it.

| Date | Decision | Why | Revisit if |
|---|---|---|---|
| 2026-10-20 | Build on a fork of [whywaita/rfid-poker](https://github.com/whywaita/rfid-poker). Run the server on the Mac with Docker Compose, and show hands on stream with the UI's `/broadcast` page as an OBS browser source. | The upstream server, equity and overlay already do most of what's needed. | – |
| 2026-10-21 | Equity: sample preflop (20k run-outs), enumerate exactly from the flop, and share split pots. | Upstream's exhaustive preflop took ~40 s at 10-handed (Phase 0 results). | Preflop accuracy ever matters more than ±0.7%. |
| 2026-10-21 | **Path A: one reader per seat**, reading both stacked hole cards. | Phase 1: a resting stack read in 160/160 scans, and real placements read on the first scan. It needs 16 readers in total, not 26. | Players can't keep cards on a marked spot, or offset/fanned cards (untested) turn out unreliable. |
| 2026-11-03 | **One Core2 drives all 16 readers through 3 PaHubs.** Seats are identified by which hub channel they're plugged into. This replaces the earlier plan of an Atom controller per seat. | Roughly a quarter of the cost (see Phase 2). One device and one USB cable to the Mac, and no multi-device USB bridge to build. Each PaHub channel is an isolated I2C segment, so each long seat cable only has to work on its own. | The long-cable test (Phase 2b) fails and can't be fixed by slowing the bus. Then fall back to an Atom per seat (see Fallback design). |
| 2026-11-03 | The table firmware reports **seats, board and muck under separate device IDs**. | The server treats every reader on a device that owns a board antenna as part of the board, so seats and board can't share a device ID. Device IDs must also not contain `-`. | – |

**Still open:**
- **Fold rule:** a muck reader (upstream behaviour), or treat cards taken off a seat reader as a fold. One reader per seat makes the second easy.
- **Transport to the Mac:** USB serial through upstream's `cmd/wired-client` is the default. WiFi is possible later.

## Results

### Phase 0 results (Oct 2026)
- `docker compose up` runs MySQL and the server. `scripts/sim-table` deals full hands for N seats plus the board and times how long equity takes to reach WebSocket clients.
- **Equity was too slow for 10 players.** 10-handed preflop equity took about 40 s to reach the overlay, and the flop then waited minutes behind queued work. There were three causes:
  1. Every possible board was enumerated, up to 1.7M preflop.
  2. One recalculation queued per completed hand.
  3. The Docker image was built for amd64, so it ran emulated on Apple Silicon.

  The fix:
  - Preflop is now estimated from 20k sampled run-outs (about ±0.7%).
  - From the flop onwards, run-outs are enumerated exactly.
  - Calculations run in a single worker that collapses pending requests.
  - The image is built natively.
- **Timing after the fix** (5 hands, 10-handed, Docker on a 2-CPU Colima VM):

  | Street | Time to reach clients |
  |---|---|
  | Preflop | 0.30–0.37 s |
  | Flop | 11–23 ms |
  | Turn / river | 4–38 ms |
- **Split pots** are now shared between the tied players, so equities always sum to 100%. Upstream's poker-go gives each tied player full credit (one 10-handed deal summed to 109%).
- **`/broadcast` didn't fit 10 players.** At 1920×1080 seats 1–2 were cut off the top, and the page didn't render the board.
  - Fixed: up to 5 players stay in one left column. Larger tables split into left and right columns, keeping the centre clear for the table camera.
  - The board is shown bottom centre.
  - Checked at 2, 6 and 10 players.
  - The overlay now also empties when a game is cleared. It used to keep showing the last hand.
- **Muck bug:** a mucked hand whose cards share a rank or suit (e.g. A♠ A♥) isn't folded. `server_http_card.go` only counts the second card as new when both rank *and* suit differ. Still to fix.
- **Device IDs must not contain `-`.** The server splits antenna serials as `<device_id>-<pair_id>`.

### Phase 1 results (Oct 2026): Path A
Test firmware: [`clients/m5stack-core2-tagtest`](../../clients/m5stack-core2-tagtest/). It ran on a Core2 with one RFID Unit 2 on Port A, using two Ultralight EV1 cards.

**Resting stack, centred:** both cards were read in **160 of 160 scans** over 33 s, with no failed reads.

**Lift-and-place trials:**

| Placement | Real placements | Notes |
|---|---|---|
| 1. Centred | **41/41** | Both cards read in the first scan every time |
| 2. Offset / fanned | Not run | Skipped |
| 3. Through the table surface | **25/26** | One stack had a single card read for ~3 s and the other never |

The on-screen totals were 41/50 and 25/27. The difference is "phantom" trials: when a stack is lifted away, a card can pass through range for one scan and the test counts that as a new placement that then fails. These show in the log as one single-card scan followed by an empty reader.

**Decision:** Path A, one reader per seat. A resting stack reads reliably, and real placements are read on the first scan. The one real miss through the surface shows the signal margin is thinner there, so Phase 5 should:
- Keep the material over each reader thin, or recess the reader.
- Give each seat a marked card spot or pocket so cards land centred. This also covers the untested offset/fanned case.

**Firmware fixes found during the test.** Phase 3 must carry these over:
1. **Vendored library fix.** The vendored `MFRC522_I2C` has a fix in `PICC_Select`. The original set the collision bit in the wrong byte whenever two UIDs first differed on a byte boundary, so that pair of cards failed every time.
2. **Reset the field each scan.** Switch the RF field off for 5 ms and on for 5 ms at the start of every scan. Relying on WUPA to wake cards halted by the previous scan left them asleep after one read.
3. **Reset after a failed select.** A failed anticollision select leaves the cards half-selected, ignoring REQA, so the field is reset again and the read retried. Before this fix, a resting stack was read in only 51% of scans.
4. **I2C bus recovery at boot.** If the ESP32 resets mid-transfer (e.g. a serial monitor opening the port), the reader can hold SDA low. At boot, clock SCL until SDA is released, retry for 2 s, then power-cycle Port A.
5. **Scan rate.** About 5 scans/s with two cards present: each HALT and the final REQA wait out the 25 ms timeout. A single seat could live with that, but a 16-reader sweep can't (see Phase 3).

## Starting point
- **Hardware on hand:** 1× M5Stack Core2, 8× M5Stack RFID Unit 2 (WS1850S), 1 deck of MIFARE Ultralight EV1 cards.
- **Upstream server** (Go + Echo, :8080, MySQL):
  - Maps card UIDs to cards with a static `config.yaml`.
  - Assigns reads to player, board or muck "antennas" through the admin UI (`/admin`).
  - Pushes hands, board and equity over WebSocket (`/ws`).
  - The UI's `/broadcast` page is the OBS browser source.
- **Upstream firmware** (`clients/m5stack/` over WiFi, `clients/m5stack-wired/` over USB serial):
  - Targets the **M5Stack Atom**.
  - One device per station (a seat, the board or the muck), each with a **PaHub I2C mux at 0x70**.
  - **One reader per card**: 2 per seat and 5 for the board.
- **Wired mode:** the M5 sends JSON lines over USB serial to `cmd/wired-client`, which POSTs to the server.
- **Hardware constraint:** the RFID Unit 2 has a fixed I2C address (0x28). More than one reader on a bus therefore needs a PaHub (6 channels, address 0x70–0x77).

## Architecture
```
 Seat readers 1–10 ──── 1–2 m Grove ────┐
 Board readers 1–5 + muck ── short ─────┤
                                         ├─ PaHub A (0x70) ─┐
                                         ├─ PaHub B (0x71) ─┼─ 1-to-3 HUB ── Core2 Port A
                                         └─ PaHub C (0x72) ─┘
 Core2 ──USB serial──► Mac: wired-client ──► rfid-poker server :8080 + MySQL ──► UI /broadcast ──► OBS
```
- **One Core2 under the centre of the table** runs every reader. Its screen is the dealer console: reader health grid, new hand, card registration.
- **3 PaHubs (18 channels) for 16 readers**, joined to Port A by a 1-to-3 hub. The PaHub v2.1 sets its address with a DIP switch, so no soldering.
- **Proposed channel map:**

  | Hub | Channels | Readers |
  |---|---|---|
  | A (0x70) | ch0–4 / ch5 | Board 1–5 / muck |
  | B (0x71) | ch0–5 | Seats 1–6 |
  | C (0x72) | ch0–3 / ch4–5 | Seats 7–10 / spare |

  The board and muck readers sit near the centre, so they use short cables. Label each seat cable with its seat number.
- **Server identity:** the firmware reports seats under one device ID (pair_id = seat number), the board under another (pair_id 1–5), and the muck under a third.
- **Transport:** one USB cable from the Core2 to the Mac, read by `cmd/wired-client`.

### Why one Core2 instead of an Atom per seat
- **What changed:** the earlier plan put a controller at every seat because a 10-seat table is about 2.5–3 m across, and I2C is designed for short runs. On reflection that was too cautious:
  - Each PaHub channel is electrically separate, so each 1–2 m seat cable is its own short bus rather than one long shared one.
  - Runs that long at 100 kHz often work.
- **What it costs:** that's unverified, which is why Phase 2b tests it before anything is installed.
- **Trade-offs:**
  - **Speed:** readers are scanned one at a time, so the firmware has to keep each reader's scan short (Phase 3).
  - **Power and cross-talk:** 16 readers with their fields always on could draw more than Port A supplies, and neighbouring board readers could read each other's cards. Only the reader being scanned turns its field on.
  - **Single point of failure:** if the Core2 fails, the table is down. Keep a spare controller.
  - **Cabling:** about the same either way. It's 10 long cables from the seats to the centre: Grove here, USB in the Atom design.

### Fallback design: an Atom per seat
Use this if the long-cable test fails and slowing the bus doesn't fix it:
- Each seat gets an M5Stack Atom Lite or AtomS3 Lite with its reader on a short cable, connected to the Mac over USB through powered hubs.
- The Core2 keeps the board and muck.
- **Extra hardware:** 10 Atoms, 1–2 powered USB hubs and 11 USB-C cables. At the Oct 2026 estimates that's roughly $200 more.
- **Extra software:** `cmd/wired-client` would then need to read several serial ports at once.
- **Reusable:** the readers, PaHubs and cables bought for the single-Core2 design all carry over.

### Changes this fork carries
1. **Table firmware for the Core2** (Phase 3): all readers, multi-card reads with the Phase 1 fixes, dealer console, registration mode.
2. **`docker-compose.yml`** for one-command start on the Mac. **Done.**
3. **Equity speed and the `/broadcast` layout** for 10 players plus the board. **Done.**
4. **Muck bug fix** (see Phase 0 results).
5. **Optional:** treat cards removed from a seat reader as a fold.

Generic fixes (compose, equity, Dockerfile arch, the `MFRC522_I2C` anticollision fix) are candidates to send upstream.

## Phases

### Phase 0: Server running on the Mac (no hardware). Done
1. Run `docker compose up` (MySQL + server) and open the UI.
2. Use `scripts/sim-table` to fake 10 players plus the board.
3. Check that `/broadcast` fits 10 players at 1920×1080 and that equity updates in under ~1 s.

### Phase 1: Decision test with hardware on hand (Core2 + 1 reader). Done
1. Build Core2 test firmware that reads every card on one reader (see [`clients/m5stack-core2-tagtest`](../../clients/m5stack-core2-tagtest/)).
2. Test 2 stacked cards on 1 reader: 50 trials in each of 3 placements. **Pass = ≥98% of trials read both cards within 1 s.**

- **Pass:** Path A, 1 reader per seat.
- **Fail:** Path B, 2 readers per seat in a two-slot card holder.

**Outcome: Path A** (see Phase 1 results).

### Phase 2: Buy hardware
**Shopping list.** Prices are from shop.m5stack.com, checked 2026-11-03, in USD before shipping and any import duty:

| Item | Qty | Unit price | Cost | Link |
|---|---|---|---|---|
| RFID 2 Unit (WS1850S), SKU U031-B | 8 (16 total) | $4.95 | $39.60 | [shop](https://shop.m5stack.com/products/rfid-unit-2-ws1850s) |
| I2C Hub 1 to 6 Expansion Unit v2.1 with DIP Switch (PCA9548A), SKU U040-B-V21 | 3 | $5.95 | $17.85 | [shop](https://shop.m5stack.com/products/i2c-hub-1-to-6-expansion-unit-v2-1-with-dip-switch-pca9548a) |
| 1 to 3 HUB Expansion Unit, SKU U006 | 1 | $3.50 | $3.50 | [shop](https://shop.m5stack.com/products/mini-hub-module) |
| Unbuckled Grove Cable, 1 m (A034-D) | ~8 | $1.95 | $15.60 | [shop](https://shop.m5stack.com/products/4pin-buckled-grove-cable) |
| Unbuckled Grove Cable, 2 m (A034-E) | ~2–4 | not checked | – | same page, 2 m variant |
| Card spot or pocket per seat | 10 | DIY | $0–40 | felt markers, or 3D-printed |
| **Subtotal (excluding 2 m cables and card spots)** | | | **~$77** | |

**Notes:**
- **Hubs:** the older PaHub listings (TCA9548A, and PCA9548APW v2.0) are marked end-of-life. Buy v2.1.
- **Cable lengths:** measure, under the table, from where the Core2 will sit to each seat's card spot. Use 1 m where it reaches and 2 m where it doesn't. Buy at least one 2 m cable either way: it's the worst case for the long-cable test.
- **Short cables:** check whether each RFID unit and hub includes a short Grove cable (M5Stack units usually do). Those cover the board and muck readers and the hub-to-hub links.
- **Worth adding as spares:** 1 RFID unit ($4.95) and 1 hub ($5.95). One order avoids a second shipping charge and wait.
- **Superseded:** the Oct 2026 estimates for the Atom-per-seat design ($355–400) assumed ~$10 readers and ~$9 hubs. The prices above are the real ones.

### Phase 2b: Long-cable test (when the cables arrive)
Before committing to long runs, prove one works:
1. Connect one reader to the Core2 through a PaHub channel on the **longest cable** (2 m), with the I2C clock at **100 kHz**. This needs a small change to the tag-test firmware: select a hub channel, and use 100 kHz instead of 400 kHz.
2. Repeat the Phase 1 resting test and about 20 placements.
3. Leave it scanning for 10 minutes.

**Pass:** both cards in ≥99% of resting scans, real placements read on the first scan, and no I2C errors or reconnects in 10 minutes.

**If it fails**, try in order:
1. 50 kHz.
2. A shorter route or a 1 m cable.
3. An I2C bus extender.
4. The fallback design.

### Phase 3: Table firmware (Core2)
Build on `clients/m5stack-core2-tagtest`, carrying over all the Phase 1 fixes.

**Scanning:**
- Scan the 16 readers one at a time across the 3 hubs, deselecting the other hubs before each select.
- Turn each reader's field on only while it's scanned (power and cross-talk).
- Shorten the MFRC522 timer from 25 ms to a few ms, so a full sweep of all readers takes about **0.3–0.4 s** (estimate; target ≤ 0.5 s).

**Reporting:**
- Seats under one device ID, the board under another, and the muck under a third. No `-` in IDs.
- A seat reports once both cards are present.
- Board cards are reported as they land.

**Output:** JSON lines over USB serial in upstream's wired format, for `cmd/wired-client`.

**Dealer console on the Core2 screen:**
- Reader health grid (`VersionReg` per channel).
- Last read per seat.
- New hand.
- **Registration mode:** scan the deck in a fixed order (As, Ks … 2c). It prints `<uid>: <card>` lines to paste into `config.yaml` (52 entries plus spares).

**Before the hardware arrives**, most of this can be built and tested on the Core2 with the readers on hand.

### Phase 4: Bridge and server on macOS
1. Run `cmd/wired-client` on the Mac against the Core2's serial port. Check it builds on macOS and reconnects after an unplug.
2. In `/admin`, assign seats 1–10 as named players, then the board and the muck.
3. Tune `RFID_POKER_CLIENT_TIMEOUT_SECONDS` (the auto-reset between hands) to the dealing pace.

### Phase 5: Table install
- **Core2 and hubs:** under the centre of the table.
- **Cables:** route a labelled Grove cable to each seat.
- **Readers:** mount each under the felt at a consistent spot per seat, with a marked card spot or pocket. Keep the material over each reader thin (Phase 1 results), and the cards within about 1–3 cm of the antenna.
- **Fold rule:** decide between the muck reader (upstream) and removal = fold (fork item 5).

### Phase 6: Stream overlay
1. Add an OBS Browser Source pointing at `/broadcast?override-api-url=ws://<mac-ip>:<port>`, sized 1920×1080. Chroma-key the green background.
2. Set a stream delay if players can see the stream during play.

## Verification
- **Phase 0:** simulated 10-player hands show correctly on `/broadcast`, with equity in under 1 s.
- **Phase 1:** the measured multi-card read rate decides Path A or B.
- **Phase 2b:** a reader on the longest cable meets the pass criteria above.
- **Phase 3:** a full sweep of all 16 readers takes ≤ 0.5 s with cards on every seat.
- **Soak test:** all readers stay green on the Core2 grid for 1 hour, with no serial disconnects.
- **End to end:** deal 20 full 10-handed hands. The overlay shows the correct hole cards, board, folds and equity within about 1 s, and the game resets between hands.
