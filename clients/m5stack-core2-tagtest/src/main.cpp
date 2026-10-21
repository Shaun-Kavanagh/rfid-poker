// Phase 1 test firmware: can one RFID Unit 2 reliably read two stacked cards?
//
// Wiring: one RFID Unit 2 on the Core2's Port A (no PaHub).
// Procedure: lift the two-card stack off the reader and put it back, 50 times per placement.
// Each placement is timed from the first card seen until both cards are read in the same scan;
// a trial passes if that takes at most PASS_WINDOW_MS. For STEADY_WINDOW_MS after a pass the
// reader keeps scanning to measure how often both cards stay visible.
//
// Buttons: A = undo last trial, B = next placement, C = reset this placement.
// Every trial is also printed over USB serial (115200) as a CSV line.
#include <M5Unified.h>
#include <MFRC522_I2C.h>
#include <Wire.h>

namespace {

constexpr uint8_t RFID_ADDRESS = 0x28;
// The Unit RFID2 has no reset line on its Grove cable; PCD_Init still drives a pin, so give it
// an unused one (Port B).
constexpr uint8_t RFID_RESET_PIN_UNUSED = 26;
constexpr uint32_t I2C_CLOCK_HZ = 400000;
constexpr uint32_t RFID_PROBE_TIMEOUT_MS = 2000;

constexpr int MAX_TAGS = 4;
constexpr int PLACEMENTS = 3;
constexpr uint32_t PASS_WINDOW_MS = 1000;
constexpr uint32_t STEADY_WINDOW_MS = 2000;
constexpr uint32_t EMPTY_BEFORE_TRIAL_MS = 300;
// Field off long enough for cards to lose power, then on long enough for them to boot.
constexpr uint32_t FIELD_OFF_MS = 5;
constexpr uint32_t FIELD_SETTLE_MS = 5;
constexpr int MAX_SELECT_ATTEMPTS = 6;

MFRC522_I2C rfid(RFID_ADDRESS, RFID_RESET_PIN_UNUSED, &Wire);

struct TagScan {
  int count = 0;
  String uids[MAX_TAGS];
};

struct PlacementStats {
  int trials = 0;
  int passes = 0;
  uint32_t totalPassMs = 0;
  uint32_t steadyScans = 0;
  uint32_t steadyHits = 0;
};

enum class Phase { WaitEmpty, WaitFirstTag, Timing, Steady };

// The outcome of the most recent trial, kept so it can be undone.
struct LastTrial {
  bool valid = false;
  int placement = 0;
  bool passed = false;
  uint32_t ms = 0;
  uint32_t steadyScans = 0;
  uint32_t steadyHits = 0;
};

PlacementStats stats[PLACEMENTS];
int placement = 0;
int trialNumber = 0;
Phase phase = Phase::WaitEmpty;
uint32_t phaseStart = 0;
uint32_t emptySince = 0;
TagScan lastScan;
String lastResult = "-";
LastTrial lastTrial;
uint32_t trialSteadyScans = 0;
uint32_t trialSteadyHits = 0;
uint32_t trialPassMs = 0;
String trialUids;
uint32_t selectFailures = 0;
// Indexed by MFRC522_I2C status code (STATUS_COLLISION = 3, STATUS_TIMEOUT = 4, STATUS_CRC_WRONG = 8, ...).
constexpr int STATUS_SLOTS = 10;
uint32_t selectFailuresByStatus[STATUS_SLOTS] = {0};

String uidToHex(const MFRC522_I2C::Uid& uid) {
  String s;
  for (byte i = 0; i < uid.size; i++) {
    if (uid.uidByte[i] < 0x10) s += "0";
    s += String(uid.uidByte[i], HEX);
  }
  return s;
}

// scanAllTags reads every card in the field. The RF field is switched off and on first so every
// card restarts in IDLE: relying on WUPA to wake cards halted by the previous scan left them
// asleep in the first test run (one good read per placement, then nothing). Then: REQA, select
// one card through anticollision, halt it, and repeat until no card answers. A failed select
// (garbled anticollision) is retried rather than ending the scan.
void resetField() {
  rfid.PCD_AntennaOff();
  delay(FIELD_OFF_MS);
  rfid.PCD_AntennaOn();
  delay(FIELD_SETTLE_MS);
}

TagScan scanAllTags() {
  TagScan scan;
  resetField();

  byte atqa[2];
  for (int attempt = 0; attempt < MAX_SELECT_ATTEMPTS && scan.count < MAX_TAGS; attempt++) {
    byte atqaLen = sizeof(atqa);
    byte status = rfid.PICC_RequestA(atqa, &atqaLen);  // halted cards stay silent
    if (status != MFRC522_I2C::STATUS_OK && status != MFRC522_I2C::STATUS_COLLISION) {
      break;  // no card left to answer
    }
    MFRC522_I2C::Uid uid;
    byte selectStatus = rfid.PICC_Select(&uid);
    if (selectStatus != MFRC522_I2C::STATUS_OK) {
      selectFailures++;
      selectFailuresByStatus[selectStatus < STATUS_SLOTS ? selectStatus : 0]++;
      // A failed anticollision leaves the cards half-selected and they ignore REQA, so without a
      // reset the rest of the scan sees no cards. The reset also wakes cards already read in this
      // scan; they are read again and de-duplicated.
      resetField();
      continue;
    }
    String hex = uidToHex(uid);
    bool duplicate = false;
    for (int i = 0; i < scan.count; i++) {
      duplicate |= scan.uids[i] == hex;
    }
    if (!duplicate) {
      scan.uids[scan.count++] = hex;
    }
    rfid.PICC_HaltA();
  }
  return scan;
}

// logScanStats prints, once a second, how many scans ran and how often each card count was seen,
// so a resting stack can be checked for steady reads without timing trials.
void logScanStats(const TagScan& scan, uint32_t now) {
  static uint32_t windowStart = 0, scans = 0, seen[MAX_TAGS + 1] = {0}, lastFailures = 0;
  static uint32_t lastByStatus[STATUS_SLOTS] = {0};
  scans++;
  seen[scan.count]++;
  if (now - windowStart < 1000) return;
  auto delta = [](int status) {
    return (unsigned long)(selectFailuresByStatus[status] - lastByStatus[status]);
  };
  unsigned long known = delta(MFRC522_I2C::STATUS_COLLISION) + delta(MFRC522_I2C::STATUS_TIMEOUT) +
                        delta(MFRC522_I2C::STATUS_CRC_WRONG) + delta(MFRC522_I2C::STATUS_ERROR);
  unsigned long failures = selectFailures - lastFailures;
  Serial.printf("scans,%lu,zero=%lu,one=%lu,two=%lu,select_fail=%lu,collision=%lu,timeout=%lu,crc=%lu,error=%lu,other=%lu\n",
                (unsigned long)scans, (unsigned long)seen[0], (unsigned long)seen[1], (unsigned long)seen[2],
                failures, delta(MFRC522_I2C::STATUS_COLLISION), delta(MFRC522_I2C::STATUS_TIMEOUT),
                delta(MFRC522_I2C::STATUS_CRC_WRONG), delta(MFRC522_I2C::STATUS_ERROR), failures - known);
  windowStart = now;
  scans = 0;
  for (auto& s : seen) s = 0;
  lastFailures = selectFailures;
  for (int i = 0; i < STATUS_SLOTS; i++) lastByStatus[i] = selectFailuresByStatus[i];
}

void setPhase(Phase p) {
  phase = p;
  phaseStart = millis();
  emptySince = 0;
}

void recordTrial(bool passed, uint32_t ms) {
  PlacementStats& st = stats[placement];
  st.trials++;
  trialNumber++;
  if (passed) {
    st.passes++;
    st.totalPassMs += ms;
    st.steadyScans += trialSteadyScans;
    st.steadyHits += trialSteadyHits;
  }
  lastTrial = {true, placement, passed, ms, trialSteadyScans, trialSteadyHits};

  float steadyPct = trialSteadyScans ? 100.0f * trialSteadyHits / trialSteadyScans : 0;
  lastResult = passed ? String("PASS ") + ms + " ms" : String("FAIL");
  Serial.printf("trial,%d,%d,%s,%lu,%.1f,%s\n", trialNumber, placement + 1, passed ? "pass" : "fail",
                (unsigned long)ms, steadyPct, trialUids.c_str());
}

void undoLastTrial() {
  if (!lastTrial.valid) return;
  PlacementStats& st = stats[lastTrial.placement];
  st.trials--;
  trialNumber--;
  if (lastTrial.passed) {
    st.passes--;
    st.totalPassMs -= lastTrial.ms;
    st.steadyScans -= lastTrial.steadyScans;
    st.steadyHits -= lastTrial.steadyHits;
  }
  lastTrial.valid = false;
  lastResult = "undone";
  Serial.printf("undo,%d\n", trialNumber + 1);
}

void startSteady(const TagScan& scan, uint32_t msToBoth) {
  trialPassMs = msToBoth;
  trialUids = scan.uids[0] + " " + scan.uids[1];
  trialSteadyScans = trialSteadyHits = 0;
  setPhase(Phase::Steady);
}

void step(const TagScan& scan, uint32_t now) {
  switch (phase) {
    case Phase::WaitEmpty:
      // Require a short empty gap so one placement is never counted twice.
      if (scan.count > 0) {
        emptySince = 0;
      } else if (emptySince == 0) {
        emptySince = now;
      } else if (now - emptySince >= EMPTY_BEFORE_TRIAL_MS) {
        setPhase(Phase::WaitFirstTag);
      }
      break;
    case Phase::WaitFirstTag:
      trialSteadyScans = trialSteadyHits = 0;
      if (scan.count >= 2) {
        startSteady(scan, 0);
      } else if (scan.count == 1) {
        setPhase(Phase::Timing);
      }
      break;
    case Phase::Timing:
      if (scan.count >= 2) {
        startSteady(scan, now - phaseStart);
      } else if (now - phaseStart > PASS_WINDOW_MS) {
        trialUids = scan.count ? scan.uids[0] : "";
        recordTrial(false, 0);
        setPhase(Phase::WaitEmpty);
      }
      break;
    case Phase::Steady:
      trialSteadyScans++;
      trialSteadyHits += scan.count >= 2 ? 1 : 0;
      if (now - phaseStart >= STEADY_WINDOW_MS || scan.count == 0) {
        recordTrial(true, trialPassMs);
        setPhase(Phase::WaitEmpty);
      }
      break;
  }
}

const char* phasePrompt() {
  switch (phase) {
    case Phase::WaitEmpty: return "Lift the cards off";
    case Phase::WaitFirstTag: return "Place the 2-card stack";
    case Phase::Timing: return "Reading...";
    case Phase::Steady: return "Hold still...";
  }
  return "";
}

void draw() {
  auto& d = M5.Display;
  d.startWrite();
  d.fillScreen(TFT_BLACK);
  d.setTextColor(TFT_WHITE, TFT_BLACK);

  d.setTextSize(2);
  d.setCursor(4, 4);
  d.printf("Placement %d/%d", placement + 1, PLACEMENTS);

  const PlacementStats& st = stats[placement];
  float pct = st.trials ? 100.0f * st.passes / st.trials : 0;
  d.setTextSize(3);
  d.setCursor(4, 32);
  d.setTextColor(st.trials == 0 ? TFT_WHITE : (pct >= 98.0f ? TFT_GREEN : TFT_ORANGE), TFT_BLACK);
  d.printf("%d/%d  %.1f%%", st.passes, st.trials, pct);

  d.setTextColor(TFT_WHITE, TFT_BLACK);
  d.setTextSize(2);
  d.setCursor(4, 70);
  d.printf("Last: %s", lastResult.c_str());
  d.setCursor(4, 92);
  float steady = st.steadyScans ? 100.0f * st.steadyHits / st.steadyScans : 0;
  uint32_t avg = st.passes ? st.totalPassMs / st.passes : 0;
  d.printf("Avg %lums  Steady %.0f%%", (unsigned long)avg, steady);

  d.setCursor(4, 120);
  d.setTextColor(TFT_YELLOW, TFT_BLACK);
  d.print(phasePrompt());

  d.setTextColor(TFT_LIGHTGREY, TFT_BLACK);
  d.setTextSize(1);
  d.setCursor(4, 148);
  d.printf("Live: %d card(s)", lastScan.count);
  for (int i = 0; i < lastScan.count; i++) {
    d.setCursor(4, 162 + i * 12);
    d.print(lastScan.uids[i]);
  }
  d.setCursor(4, 206);
  for (int p = 0; p < PLACEMENTS; p++) {
    d.printf("P%d %d/%d   ", p + 1, stats[p].passes, stats[p].trials);
  }

  d.setTextSize(2);
  d.setCursor(20, 222);
  d.print("Undo");
  d.setCursor(118, 222);
  d.print("Next");
  d.setCursor(218, 222);
  d.print("Reset");
  d.endWrite();
}

// recoverI2CBus clocks SCL until the reader releases SDA, then sends a STOP. If the ESP32 resets
// mid-transfer (e.g. when a serial monitor opens the port), the reader can be left holding SDA
// low and will not answer until the bus is cleared.
void recoverI2CBus(int sda, int scl) {
  pinMode(sda, INPUT_PULLUP);
  pinMode(scl, OUTPUT_OPEN_DRAIN);
  digitalWrite(scl, HIGH);
  delayMicroseconds(5);
  for (int i = 0; i < 9 && digitalRead(sda) == LOW; i++) {
    digitalWrite(scl, LOW);
    delayMicroseconds(5);
    digitalWrite(scl, HIGH);
    delayMicroseconds(5);
  }
  // STOP condition: SDA rises while SCL is high.
  pinMode(sda, OUTPUT_OPEN_DRAIN);
  digitalWrite(sda, LOW);
  delayMicroseconds(5);
  digitalWrite(scl, HIGH);
  delayMicroseconds(5);
  digitalWrite(sda, HIGH);
  delayMicroseconds(5);
}

bool rfidAnswers() {
  Wire.beginTransmission(RFID_ADDRESS);
  return Wire.endTransmission() == 0;
}

// connectRfid clears the I2C bus and waits for the reader to answer. If it still doesn't, it
// power-cycles the Port A 5V output, which resets the reader, and tries once more.
bool connectRfid(int sda, int scl) {
  for (int round = 0; round < 2; round++) {
    if (round == 1) {
      Serial.println("info,power-cycling Port A");
      Wire.end();
      M5.Power.setExtOutput(false);
      delay(300);
      M5.Power.setExtOutput(true);
      delay(300);
    }
    recoverI2CBus(sda, scl);
    Wire.begin(sda, scl, I2C_CLOCK_HZ);
    for (uint32_t start = millis(); millis() - start < RFID_PROBE_TIMEOUT_MS; delay(50)) {
      if (rfidAnswers()) return true;
    }
    Wire.end();
  }
  return false;
}

}  // namespace

void setup() {
  auto cfg = M5.config();
  M5.begin(cfg);
  Serial.begin(115200);
  M5.Display.setRotation(1);
  M5.Display.setTextSize(2);

  int sda = M5.getPin(m5::pin_name_t::port_a_sda);
  int scl = M5.getPin(m5::pin_name_t::port_a_scl);
  while (!connectRfid(sda, scl)) {
    M5.Display.fillScreen(TFT_RED);
    M5.Display.setCursor(4, 4);
    M5.Display.print("No RFID Unit on Port A");
    M5.Display.setCursor(4, 32);
    M5.Display.print("Check the cable. Retrying...");
    Serial.println("error,no RFID unit at 0x28 on Port A, retrying");
  }
  rfid.PCD_Init();
  byte version = rfid.PCD_ReadRegister(MFRC522_I2C::VersionReg);
  Serial.printf("ready,version=0x%02x,sda=%d,scl=%d\n", version, sda, scl);
  Serial.println("columns,trial,placement,result,ms_to_both,steady_pct,uids");
  setPhase(Phase::WaitEmpty);
}

void loop() {
  M5.update();
  if (M5.BtnA.wasPressed()) undoLastTrial();
  if (M5.BtnB.wasPressed()) {
    placement = (placement + 1) % PLACEMENTS;
    lastTrial.valid = false;
    lastResult = "-";
    setPhase(Phase::WaitEmpty);
  }
  if (M5.BtnC.wasPressed()) {
    stats[placement] = PlacementStats{};
    lastTrial.valid = false;
    lastResult = "reset";
    setPhase(Phase::WaitEmpty);
  }

  uint32_t now = millis();
  lastScan = scanAllTags();
  step(lastScan, now);
  logScanStats(lastScan, now);

  static uint32_t lastDraw = 0;
  if (now - lastDraw > 150) {
    draw();
    lastDraw = now;
  }
}
