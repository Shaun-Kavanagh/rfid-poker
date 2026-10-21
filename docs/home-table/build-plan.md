# 10-player home table: build plan

This fork adapts rfid-poker for a **10-player** Texas Hold'em home game with a **live stream overlay** (OBS), running the server on a Mac.

## Progress

| Phase | Status |
|---|---|
| 0. Server and simulator on the Mac | Done |
| 1. Multi-card read test (Core2 + 1 reader) | Not started |
| 2. Buy hardware | Waiting on Phase 1 |
| 3. Firmware | Not started |
| 4. Bridge and server on macOS | Not started |
| 5. Table install | Not started |
| 6. Stream overlay | Not started |

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
- **Hardware constraint:** the RFID Unit 2 has a fixed I2C address (0x28). More than one reader per device therefore needs a PaHub (6 channels, address 0x70–0x77).

## Why the design changes for 10 players
- **Cabling:** a 10-seat table is about 2.5–3 m across, which is too far for I2C. Each seat gets its own small controller next to its reader(s), connected to the Mac over USB, which also powers it.
- **Reader count:**
  - The upstream approach (2 readers per seat) needs 20 seat readers + 5 board + 1 muck = **26 readers**.
  - One reader per seat that reads both stacked cards needs **16**, but it relies on reading several tags at once, which upstream doesn't do.
  - A test with hardware already on hand decides which way to go before buying anything.

## Architecture
```
 Seat 1..10: Atom + RFID reader(s)  ──USB──┐
 Board/muck: Core2 + PaHub + 6 readers ─USB─┤── powered USB hubs (under table) ──USB── Mac
                                                                                      ├─ wired-client bridge (serial → HTTP)
                                                                                      ├─ rfid-poker server :8080 + MySQL (docker compose)
                                                                                      └─ UI /broadcast ──► OBS Browser Source
```
- **Seat nodes (×10):** an M5Stack Atom Lite (or AtomS3 Lite, which needs a small port) under the felt at each seat. Either:
  - 1 reader directly on the Atom (Path A), or
  - a PaHub + 2 readers (Path B).
- **Board and muck node:** the Core2 with 1 PaHub, 5 board readers (one per card) and 1 muck reader. Its screen is the dealer console: reader health grid, new hand, card registration.
- **Transport:** USB serial (upstream's wired mode) through powered USB hubs. One cable goes to the Mac, with no WiFi and no per-seat power supplies.

### Changes this fork carries
1. Core2 build target and dealer console (firmware).
2. Multi-card read per reader, if Path A (firmware, `clients/lib/rfid-poker-common/src/rfid_core.cpp`).
3. `wired-client` on macOS against several serial ports at once (`cmd/wired-client`, `pkg/serial/`).
4. `docker-compose.yml` for one-command start on the Mac. **Done.**
5. Equity speed for 10 hands, and the `/broadcast` layout for 10 players plus the board. **Done.**
6. Optional: treat cards removed from a seat reader as a fold.

Generic fixes (Core2 target, macOS bridge, compose, equity, Dockerfile arch) are candidates to send upstream.

## Phases

### Phase 0: Server running on the Mac (no hardware)
1. Run `docker compose up` (MySQL + server) and open the UI.
2. Use `scripts/sim-table` to fake 10 players plus the board.
3. Check that `/broadcast` fits 10 players at 1920×1080 and that equity updates in under ~1 s.

### Phase 1: Decision test with hardware on hand (Core2 + 1 reader)
1. Add a `m5stack-core2` PlatformIO env, using M5Unified in place of M5Atom/FastLED and guarding the LED code with `#ifdef`.
2. Add `readAllTags()`: REQA → anticollision/select (7-byte UID, cascade level 2) → HALT, repeated until no tag answers, then WUPA on the next poll.
3. Test 2 stacked cards on 1 reader: 50 trials in each of 3 placements. **Pass = ≥98% of trials read both cards within 1 s.**

- **Pass:** Path A, 1 reader per seat.
- **Fail:** Path B, 2 readers per seat in a two-slot card holder.

### Phase 2: Buy hardware
| Item | Path A (1 reader/seat) | Path B (2 readers/seat) |
|---|---|---|
| Atom Lite / AtomS3 Lite | 10 | 10 |
| RFID Unit 2 (8 on hand; 5 board + 1 muck + seats) | +8 (16 total) | +18 (26 total) |
| PaHub2 | 1 (Core2) | 11 (Core2 + 1 per seat) |
| Powered USB hub, 7–10 port | 2 | 2 |
| USB-C cables, 1–2 m | 11 | 11 |
| Grove cables, 20–50 cm | ~16 | ~30 |
| Card holders/pockets per seat | 10 | 10 (two-slot) |

#### Projected cost (USD, estimates as of Oct 2026)
These are approximate list prices. Check current prices, and allow about 10–20% for shipping, tax and import duty (M5Stack ships from China).

**Unit prices used:**

| Item | Price |
|---|---|
| Atom Lite / AtomS3 Lite | ~$7.50 |
| RFID Unit 2 | ~$10 |
| PaHub2 | ~$9 |
| Powered USB hub | ~$40 |
| USB-C cable | ~$4 |
| Grove cable | ~$1 |

| Item | Path A qty | Path A cost | Path B qty | Path B cost |
|---|---|---|---|---|
| Atom Lite / AtomS3 Lite | 10 | $75 | 10 | $75 |
| RFID Unit 2 | 8 | $80 | 18 | $180 |
| PaHub2 | 1 | $9 | 11 | $99 |
| Powered USB hub | 2 | $80 | 2 | $80 |
| USB-C cables | 11 | $45 | 11 | $45 |
| Grove cables | ~16 | $20 | ~30 | $35 |
| Card holders (DIY felt → 3D-printed) | 10 | $0–40 | 10 | $0–60 |
| **Subtotal** | | **~$310–350** | | **~$515–575** |
| **With ~15% shipping/tax** | | **~$355–400** | | **~$590–660** |

Path A saves about $230, which is why the Phase 1 test (no cost) comes first.

**Ways to save:**
- Use one 10-port hub instead of two.
- Use generic USB cables.
- Make felt pockets instead of printed holders.
- In Path B, put 2 readers on the Atom's two I2C buses (Grove port plus the exposed pins, with a second `TwoWire`) instead of a PaHub. That saves about $90 but adds firmware work.

### Phase 3: Firmware
- **Seat firmware** (Atom, wired):
  - Reports `{device_id, pair_id, uid}` lines.
  - Path A: send both UIDs once 2 tags are present. Path B: upstream behaviour unchanged.
- **Core2 board/muck firmware:**
  - PaHub ch0–4 are board pair_ids 1–5; ch5 is the muck.
  - The screen shows a reader health grid (`VersionReg` check per channel), the last UID, and a registration mode.
- **Registration mode:** scan the deck in a fixed order (As, Ks … 2c). It prints `<uid>: <card>` lines to paste into `config.yaml` (52 entries plus spares).

### Phase 4: Bridge and server on macOS
1. Make `wired-client` accept several `/dev/cu.usbserial-*` / `/dev/cu.usbmodem-*` ports, or auto-discover them, and reconnect after an unplug.
2. In `/admin`, assign seats 1–10 as named players, then the board and the muck.
3. Tune `RFID_POKER_CLIENT_TIMEOUT_SECONDS` (the auto-reset between hands) to the dealing pace.

### Phase 5: Table install
- Mount readers under the felt at a consistent spot per seat, with a marked card pocket. Keep cards within about 1–3 cm of the antenna.
- Atoms go under the rail, with USB runs to hubs on the underside of the table centre. One USB cable goes to the Mac.
- Decide the fold rule: muck reader (upstream) or removal = fold (fork item 6).

### Phase 6: Stream overlay
1. Add an OBS Browser Source pointing at `/broadcast?override-api-url=ws://<mac-ip>:<port>`, sized 1920×1080. Chroma-key the green background.
2. Set a stream delay if players can see the stream during play.

## Verification
- **Phase 0:** simulated 10-player hands show correctly on `/broadcast`, with equity in under 1 s.
- **Phase 1:** the measured multi-card read rate decides Path A or B.
- **Soak test:** all readers stay green on the Core2 grid for 1 hour, with no bridge disconnects.
- **End to end:** deal 20 full 10-handed hands. The overlay shows the correct hole cards, board, folds and equity within about 1 s, and the game resets between hands.
