# Equity viewer for poker (No-limit Texas Hold'em)

![demo](./docs/media/demo.gif)

> **Home table fork.** This fork of [whywaita/rfid-poker](https://github.com/whywaita/rfid-poker) is being adapted for a 10-player home game streamed with OBS. See the [build plan](./docs/home-table/build-plan.md) for the hardware design, costs, phases and progress.
>
> Changes so far:
> - `docker compose` setup for running on a Mac.
> - A table simulator.
> - Faster equity: preflop is sampled, and recalculations are collapsed.
> - A native arm64 Docker image.

## Requirements

- M5Stack + RFID module
  - Players x N (N is the number of players) + 1 (for muck) + 1 (for board)
  - We tested with...
    - [M5Stack Core2](https://docs.m5stack.com/en/core/core2)
    - [Unit RFID2](https://docs.m5stack.com/en/unit/rfid2)
      - This unit has been confirmed by [Switch Science](https://www.switch-science.com/products/8301) to be compliant with Japan's Radio Law. ([ref](https://mag.switch-science.com/2022/05/24/m5stack-3/))
- Player cards with NFC chip (ISO/IEC 14443 Type A)
  - We tested player cards include MIFARE Ultralight EV1

## Setup

### Prepare a config file

```bash
$ cat config.yaml
card_ids:  ## UID of NEC card
  040e3bd2286b85: As
  040f43d2286b85: Qc
  04101b9a776b85: Kc
  ...
```

### Run the server with Docker Compose

`docker-compose.yml` starts MySQL and the server together. It runs migrations at startup and needs no other setup.

```bash
# Uses config.dev.yaml (synthetic card UIDs) by default
$ docker compose up -d --build

# Use your scanned deck, and/or a different host port (default 8080)
$ CARD_CONFIG=./config.yaml RFID_POKER_PORT=8081 docker compose up -d --build
```

| Variable | Default | Purpose |
|---|---|---|
| `CARD_CONFIG` | `./config.dev.yaml` | Card UID config mounted into the server |
| `RFID_POKER_PORT` | `8080` | Host port for the server and WebSocket (`ws://localhost:<port>/ws`) |
| `RFID_POKER_CLIENT_TIMEOUT_SECONDS` | `10` | Auto-clear timeout; `0` disables it |

### Simulate a table

`scripts/sim-table` needs no hardware. It plays a whole table against a running server that's using `config.dev.yaml`:
- registers N seats and a board,
- deals full hands,
- logs how long equity takes to reach WebSocket clients after each street.

```bash
$ go run ./scripts/sim-table -host http://localhost:8080 -players 10 -hands 5
```

It leaves the last hand on the table, so you can look at it in the UI, for example `/broadcast?override-api-url=ws://localhost:8080`. Start the server with `RFID_POKER_CLIENT_TIMEOUT_SECONDS=0`, or the hand is cleared after 10 seconds with no card reads.

### Run the server without Docker

Run the server using the following environment variables

```bash
# Set the path to the config file
# You can set path as `http://` or `https://` to get the config file from the server.
export RFID_POKER_CONFIG_PATH="./config.yaml"

# Set MySQL connection information
export RFID_POKER_MYSQL_USER=<your_mysql_user>
export RFID_POKER_MYSQL_PASS=<your_mysql_password>
export RFID_POKER_MYSQL_HOST=<your_mysql_host>
export RFID_POKER_MYSQL_PORT=<your_mysql_port>
export RFID_POKER_MYSQL_DATABASE=<your_mysql_database>

# Optional: Auto-clear game when players/board stop sending cards.
# 0 disables timeout. Default: 10
export RFID_POKER_CLIENT_TIMEOUT_SECONDS=10

# Run the server
$ go run ./cmd/server
```

## Components

### Server

The server is a golang application that runs on a server.

#### `GET /ws` (websocket)

The server will upgrade the connection to a websocket. The server send an info about players to the client.

How equity is calculated:
- **Preflop:** estimated from 20,000 sampled run-outs (about ±0.7%).
- **Flop onwards:** calculated exactly over every run-out.
- **Split pots:** shared between the tied players, so equities sum to 1.
- **Recalculation:** one runs at a time. Card reads that arrive during a calculation collapse into a single follow-up run.

The body of the message is as follows:

```json
{
  "board": [
    {
      "rank": "A",
      "suit": "hearts"
    },
    {
      "rank": "K",
      "suit": "hearts"
    },
    {
      "rank": "Q",
      "suit": "hearts"
    },
    {
      "rank": "J",
      "suit": "hearts"
    },
    {
      "rank": "T",
      "suit": "hearts"
    }
  ],
  "players": [
    {
      "name": "Player 1",
      "hand": [
        {
          "rank": "A",
          "suit": "spades"
        },
        {
          "rank": "K",
          "suit": "spades"
        }
      ],
      "equity": 0.5
    },
    {
      "name": "Player 2",
      "hand": [
        {
          "rank": "A",
          "suit": "clubs"
        },
        {
          "rank": "K",
          "suit": "clubs"
        }
      ],
      "equity": 0.5
    }
  ]
}
```

#### POST /device/boot

The device should POST this message when it boots.

```json
{
  "device_id": "device_id",  // as Mac address (in M5stack)
  "pair_ids": [1, 2, 3, ...] // antenna pair ids
}
```

#### POST /card

The device (or wired client) should POST this message when it reads a card.

```json
{
  "device_id": "device_id",    // as Mac address (in M5stack)
  "pair_id": 1,                // antenna pair id
  "uid": "040e3bd2286b85"      // as UID of NFC card (spaces are allowed)
}
```

### ui

This ui is a Next.js application that runs on a client.

[ui](./ui) directory is a Next.js application.

You can use the newest code in GitHub Pages ([https://whywaita.github.io/rfid-poker/](https://whywaita.github.io/rfid-poker/)).

### Client

This is a M5Stack application that runs on a M5Stack device.

[clients/m5stack](./clients/m5stack) directory is a M5Stack application.
