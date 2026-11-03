# m5stack-core2-tagtest

Phase 1 test firmware from the [build plan](../../docs/home-table/build-plan.md). It answers one question: **can a single RFID Unit 2 reliably read two stacked cards?**

- **Pass (Path A):** each seat needs only one reader.
- **Fail (Path B):** each seat needs two readers.

## What it does

The firmware reads every card on the reader, not just the first. It works through ISO 14443-3 anticollision:
1. Switch the RF field off for 5 ms and back on, so every card restarts fresh.
2. Send REQA, select one card through anticollision, and halt it.
3. Repeat until no card answers. Halted cards ignore REQA, so each card is read once.
4. If a select fails, reset the field and try again. A failed anticollision leaves the cards ignoring REQA.

The first version woke cards with WUPA instead of resetting the field. Halted cards stayed asleep after their first read, and a resting stack was read in only about half of scans.

At boot the firmware also recovers the I2C bus. If the Core2 restarted mid-transfer, for example when a serial monitor opened the port, the reader can hold the data line low. The firmware clocks it free, retries for 2 s, and power-cycles Port A if needed.

**Timing:** each placement of the stack is timed from the first card seen until both cards are read in the same scan.

- A trial **passes** if that takes ≤ 1 s.
- After a pass, the firmware keeps scanning for 2 s. **Steady** is the share of those scans that saw both cards, which is what the real seat firmware will depend on.

**The library:** `lib/MFRC522_I2C` is a vendored copy of `kkloesener/MFRC522_I2C` (public domain), with one fix in `PICC_Select`. The original computed the collision bit and the byte index from the same value. When two card UIDs first differ on a byte boundary, it set the bit in the wrong byte, and that pair of cards failed every time. The fix matches the one in miguelbalboa/rfid.

## Hardware

- **M5Stack Core2**, connected to your computer by USB-C.
- **One RFID Unit 2**, plugged straight into **Port A** (the red Grove port). No PaHub.
- **Two cards** from the deck.

## Flash

```bash
cd clients/m5stack-core2-tagtest
pio run -t upload
```

## Run the test

1. **Start a log (optional).** Opening the serial port restarts the Core2, so start this before you begin:
   ```bash
   pio device monitor -b 115200 --filter log2file
   ```
   This saves a `platformio-device-monitor-*.log` file in the current directory.
2. **Do 50 trials for placement 1.** For each trial, lift the stack fully off the reader, wait for **"Place the 2-card stack"**, then set it down and hold still until **"Lift the cards off"**.
3. **Press B (Next)** and repeat for placement 2, then placement 3.

Suggested placements:

| Placement | Position |
|---|---|
| 1 | Centred on the reader, cards aligned |
| 2 | Offset about 1 cm, or with the cards slightly fanned the way a player leaves them |
| 3 | Through the felt or table surface you plan to use |

**Buttons:**

| Button | Action |
|---|---|
| A (Undo) | Remove the last trial, e.g. if the stack was knocked |
| B (Next) | Move to the next placement |
| C (Reset) | Clear the current placement |

**Screen:** the headline shows passes / trials and the pass rate. It turns green at ≥ 98%.

**Serial log:** every trial prints one CSV line:
```
trial,<n>,<placement>,<pass|fail>,<ms_to_both>,<steady_pct>,<uid1 uid2>
```

**Phantom trials.** Lifting a stack away can carry one card through range for a single scan. The test counts that as a new placement, which then fails. These show in the log as a `fail` with no UID, right after a single one-card scan. Discount them when reading results; the per-second `scans,...` lines show what was really on the reader.

## Decision

- **Path A:** every placement reaches **≥ 98%** pass rate.
- **Path B:** otherwise.

Note the Steady figure too. If it's well below 100%, the seat firmware will need to smooth reads over a few scans before reporting a card as gone.

**Result (Oct 2026): Path A.** See the build plan's Phase 1 results.

| Test | Result |
|---|---|
| Resting stack, centred | 160/160 scans read both cards |
| Placement 1, centred | 41/41 real placements |
| Placement 2, offset / fanned | Skipped |
| Placement 3, through the surface | 25/26 real placements |
