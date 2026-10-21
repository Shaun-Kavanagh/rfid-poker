// sim-table simulates a full table (N player seats + board) against a running server
// and reports how long the server takes to publish equity after each street.
//
// It expects the server to load config.dev.yaml (card UIDs "dev<card>", e.g. devAs).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/whywaita/rfid-poker/pkg/server"
)

const (
	boardDeviceID = "simboard"
	ranks         = "AKQJT98765432"
	suits         = "shdc"
)

func main() {
	if err := run(); err != nil {
		log.Fatalln(err)
	}
}

func run() error {
	var (
		inHost    = flag.String("host", "http://localhost:8080", "base URL of server")
		inPlayers = flag.Int("players", 10, "number of player seats")
		inHands   = flag.Int("hands", 1, "number of hands to deal")
		inTimeout = flag.Duration("equity-timeout", 60*time.Second, "max wait for equity per street")
	)
	flag.Parse()

	base, err := url.Parse(*inHost)
	if err != nil {
		return fmt.Errorf("url.Parse(): %w", err)
	}
	ctx := context.Background()

	if err := setupAntennas(ctx, base, *inPlayers); err != nil {
		return fmt.Errorf("setupAntennas(): %w", err)
	}

	wsURL := *base
	wsURL.Scheme = strings.Replace(wsURL.Scheme, "http", "ws", 1)
	ws, _, err := websocket.Dial(ctx, wsURL.JoinPath("ws").String(), nil)
	if err != nil {
		return fmt.Errorf("websocket.Dial(): %w", err)
	}
	defer ws.CloseNow()
	ws.SetReadLimit(1 << 20)
	updates := make(chan server.Send, 64)
	go readUpdates(ctx, ws, updates)

	for h := 1; h <= *inHands; h++ {
		log.Printf("=== hand %d/%d ===", h, *inHands)
		if err := playHand(ctx, base, *inPlayers, updates, *inTimeout); err != nil {
			return fmt.Errorf("playHand(%d): %w", h, err)
		}
	}
	return nil
}

// setupAntennas registers one antenna per seat plus the board device, and assigns their types.
// A seat antenna needs one card read while "unknown" so the server creates its player.
func setupAntennas(ctx context.Context, base *url.URL, players int) error {
	if err := resetGame(ctx, base); err != nil {
		return err
	}
	if err := postJSON(ctx, base.JoinPath("device", "boot"), server.Device{DeviceID: boardDeviceID, PairIDs: []int{1, 2, 3, 4, 5}}, nil); err != nil {
		return err
	}
	// Cards are unique table-wide, so each seat gets a different one.
	deck := newDeck()
	for i := 1; i <= players; i++ {
		if err := postCard(ctx, base, seatDeviceID(i), 1, "dev"+deck[i]); err != nil {
			return err
		}
	}

	var resp server.GetAdminAntennaResponse
	if err := getJSON(ctx, base.JoinPath("admin", "antenna"), &resp); err != nil {
		return err
	}
	hasBoard := false
	for _, a := range resp.Antenna {
		if a.AntennaTypeName == "board" {
			hasBoard = true
		}
	}
	for _, a := range resp.Antenna {
		want := ""
		switch {
		case strings.HasPrefix(a.DeviceID, "simseat") && a.AntennaTypeName == "unknown":
			want = "player"
		case a.DeviceID == boardDeviceID && a.PairID == 1 && !hasBoard:
			want = "board"
		}
		if want == "" {
			continue
		}
		u := base.JoinPath("admin", "antenna", fmt.Sprint(a.ID))
		if err := postJSON(ctx, u, map[string]string{"antenna_type_name": want}, nil); err != nil {
			return fmt.Errorf("set antenna %d to %s: %w", a.ID, want, err)
		}
	}
	return resetGame(ctx, base)
}

func playHand(ctx context.Context, base *url.URL, players int, updates <-chan server.Send, timeout time.Duration) error {
	if err := resetGame(ctx, base); err != nil {
		return err
	}
	drain(updates)

	deck := newDeck()
	next := func() string { c := deck[0]; deck = deck[1:]; return c }

	// Deal two cards to every seat, in two passes like a real dealer.
	hands := make([][]string, players)
	for pass := 0; pass < 2; pass++ {
		for i := 0; i < players; i++ {
			hands[i] = append(hands[i], next())
		}
	}
	start := time.Now()
	for i := 0; i < players; i++ {
		for _, c := range hands[i] {
			if err := postCard(ctx, base, seatDeviceID(i+1), 1, "dev"+c); err != nil {
				return err
			}
		}
	}
	log.Printf("dealt %d hands in %s", players, time.Since(start).Round(time.Millisecond))
	if err := waitEquity(updates, players, 0, start, "preflop", timeout); err != nil {
		return err
	}

	streets := []struct {
		name  string
		cards int
	}{{"flop", 3}, {"turn", 1}, {"river", 1}}
	boardCount := 0
	for _, st := range streets {
		start := time.Now()
		for i := 0; i < st.cards; i++ {
			boardCount++
			if err := postCard(ctx, base, boardDeviceID, boardCount, "dev"+next()); err != nil {
				return err
			}
		}
		if err := waitEquity(updates, players, boardCount, start, st.name, timeout); err != nil {
			return err
		}
	}
	return nil
}

// waitEquity waits for a WebSocket update with every player's hand, the expected board size,
// and equities that sum to ~1, then logs the latency since start.
func waitEquity(updates <-chan server.Send, players, boardCount int, start time.Time, street string, timeout time.Duration) error {
	deadline := time.After(timeout)
	last := "none"
	for {
		select {
		case s, ok := <-updates:
			if !ok {
				return fmt.Errorf("%s: websocket closed", street)
			}
			sum := 0.0
			for _, p := range s.Players {
				sum += p.Equity
			}
			last = fmt.Sprintf("players=%d board=%d equity_sum=%.3f", len(s.Players), len(s.Board), sum)
			if len(s.Players) != players || len(s.Board) != boardCount {
				continue
			}
			if sum < 0.99 || sum > 1.01 {
				continue
			}
			// Preflop nobody is drawing dead, so a 0% player means equity was computed
			// before every hand arrived.
			if boardCount == 0 && slices.ContainsFunc(s.Players, func(p server.SendPlayer) bool { return p.Equity == 0 }) {
				continue
			}
			log.Printf("%-7s equity published in %s (board: %s)", street, time.Since(start).Round(time.Millisecond), formatCards(s.Board))
			for _, p := range s.Players {
				log.Printf("    %-24s %s  %5.1f%%", p.Name, formatCards(p.Hand), p.Equity*100)
			}
			return nil
		case <-deadline:
			return fmt.Errorf("%s: no equity update within %s (last update: %s)", street, timeout, last)
		}
	}
}

func readUpdates(ctx context.Context, ws *websocket.Conn, out chan<- server.Send) {
	defer close(out)
	for {
		_, b, err := ws.Read(ctx)
		if err != nil {
			log.Printf("ws.Read(): %v", err)
			return
		}
		var s server.Send
		if err := json.Unmarshal(b, &s); err != nil {
			continue
		}
		out <- s
	}
}

func drain(ch <-chan server.Send) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

func newDeck() []string {
	deck := make([]string, 0, 52)
	for _, s := range suits {
		for _, r := range ranks {
			deck = append(deck, string(r)+string(s))
		}
	}
	rand.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
	return deck
}

// seatDeviceID must not contain "-": the server splits serials as "<device_id>-<pair_id>".
func seatDeviceID(seat int) string {
	return fmt.Sprintf("simseat%02d", seat)
}

func formatCards(cards []server.SendCard) string {
	parts := make([]string, 0, len(cards))
	for _, c := range cards {
		parts = append(parts, c.Rank+c.Suit)
	}
	return strings.Join(parts, " ")
}

func resetGame(ctx context.Context, base *url.URL) error {
	return do(ctx, http.MethodDelete, base.JoinPath("admin", "game"), nil, nil)
}

func postCard(ctx context.Context, base *url.URL, deviceID string, pairID int, uid string) error {
	return postJSON(ctx, base.JoinPath("card"), server.PostCardRequest{UID: uid, DeviceID: deviceID, PairID: pairID}, nil)
}

func getJSON(ctx context.Context, u *url.URL, out any) error {
	return do(ctx, http.MethodGet, u, nil, out)
}

func postJSON(ctx context.Context, u *url.URL, in, out any) error {
	return do(ctx, http.MethodPost, u, in, out)
}

func do(ctx context.Context, method string, u *url.URL, in, out any) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return fmt.Errorf("json.Encode(): %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), &body)
	if err != nil {
		return fmt.Errorf("http.NewRequestWithContext(): %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, u, err)
	}
	defer resp.Body.Close()
	// 304 means the server already had this card, which is fine for the simulator.
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		return fmt.Errorf("%s %s: status %d: %s", method, u, resp.StatusCode, strings.TrimSpace(b.String()))
	}
	if out != nil && resp.StatusCode != http.StatusNotModified {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("json.Decode(): %w", err)
		}
	}
	return nil
}
