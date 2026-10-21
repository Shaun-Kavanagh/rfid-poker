package store

import (
	"math"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/whywaita/poker-go"

	"github.com/whywaita/rfid-poker/pkg/playercards"
)

func cards(t testing.TB, s string) []poker.Card {
	t.Helper()
	var out []poker.Card
	for _, f := range strings.Fields(s) {
		c, err := playercards.UnmarshalPlayerCard(f)
		if err != nil {
			t.Fatalf("UnmarshalPlayerCard(%q): %v", f, err)
		}
		out = append(out, c)
	}
	return out
}

func testRand() *rand.Rand {
	return rand.New(rand.NewPCG(1, 2))
}

func TestEstimateEquityExact(t *testing.T) {
	tests := []struct {
		name  string
		hands []string
		board string
		want  []float64
	}{
		{
			name:  "river, aces win",
			hands: []string{"As Ah", "Ks Kh"},
			board: "2c 7d 9s Tc 3h",
			want:  []float64{1, 0},
		},
		{
			name:  "river, royal flush on board splits",
			hands: []string{"2c 3d", "4h 5d"},
			board: "As Ks Qs Js Ts",
			want:  []float64{0.5, 0.5},
		},
		{
			name:  "turn, kings have two outs of 44",
			hands: []string{"As Ah", "Ks Kh"},
			board: "2c 7d 9s Tc",
			want:  []float64{42.0 / 44, 2.0 / 44},
		},
		{
			name:  "turn, identical hands split and the third hand is drawing dead",
			hands: []string{"As Kd", "Ac Kh", "2s 3s"},
			board: "Ah 7d 9c Tc",
			want:  []float64{0.5, 0.5, 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hands := make([][]poker.Card, 0, len(tt.hands))
			for _, h := range tt.hands {
				hands = append(hands, cards(t, h))
			}
			got, err := estimateEquity(hands, cards(t, tt.board), testRand())
			if err != nil {
				t.Fatalf("estimateEquity(): %v", err)
			}
			for i := range tt.want {
				if math.Abs(got[i]-tt.want[i]) > 1e-9 {
					t.Errorf("player %d equity = %.6f, want %.6f (all: %v)", i, got[i], tt.want[i], got)
				}
			}
		})
	}
}

func TestEstimateEquityPreflopSampled(t *testing.T) {
	// AA vs KK (no shared suits) is about 82% to 18% preflop.
	got, err := estimateEquity([][]poker.Card{cards(t, "As Ah"), cards(t, "Kd Kc")}, nil, testRand())
	if err != nil {
		t.Fatalf("estimateEquity(): %v", err)
	}
	if got[0] < 0.80 || got[0] > 0.85 {
		t.Errorf("AA equity = %.4f, want about 0.82", got[0])
	}
	if math.Abs(got[0]+got[1]-1) > 1e-9 {
		t.Errorf("equities sum to %.6f, want 1", got[0]+got[1])
	}
}

func TestEstimateEquityTenPlayersSumToOne(t *testing.T) {
	hands := tenHands(t)
	for _, board := range []string{"", "Jc Jd 8h", "Jc Jd 8h 8s", "Jc Jd 8h 8s 4h"} {
		got, err := estimateEquity(hands, cards(t, board), testRand())
		if err != nil {
			t.Fatalf("board %q: estimateEquity(): %v", board, err)
		}
		sum := 0.0
		for _, e := range got {
			sum += e
		}
		if len(got) != 10 || math.Abs(sum-1) > 1e-9 {
			t.Errorf("board %q: got %d equities summing to %.6f, want 10 summing to 1", board, len(got), sum)
		}
	}
}

func TestEstimateEquityRejectsBadInput(t *testing.T) {
	tests := []struct {
		name  string
		hands [][]poker.Card
		board []poker.Card
	}{
		{"one player", [][]poker.Card{cards(t, "As Ah")}, nil},
		{"duplicate card", [][]poker.Card{cards(t, "As Ah"), cards(t, "As Kd")}, nil},
		{"hand on board", [][]poker.Card{cards(t, "As Ah"), cards(t, "Ks Kd")}, cards(t, "Ah 2c 3d")},
		{"three hole cards", [][]poker.Card{cards(t, "As Ah 2c"), cards(t, "Ks Kd")}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := estimateEquity(tt.hands, tt.board, testRand()); err == nil {
				t.Error("estimateEquity() succeeded, want error")
			}
		})
	}
}

func TestNextCombinationCountsAll(t *testing.T) {
	idx := []int{0, 1}
	n := 1
	for nextCombination(idx, 6) {
		n++
	}
	if n != binomial(6, 2) {
		t.Errorf("visited %d combinations, want %d", n, binomial(6, 2))
	}
}

func tenHands(t testing.TB) [][]poker.Card {
	var hands [][]poker.Card
	for _, h := range []string{"As Ah", "Ks Kh", "Qs Qh", "Js Jh", "Ts Th", "9s 9h", "8c 7c", "6d 5d", "4c 3c", "2d 2h"} {
		hands = append(hands, cards(t, h))
	}
	return hands
}

func BenchmarkEstimateEquityTenPlayersPreflop(b *testing.B) {
	hands := tenHands(b)
	rng := testRand()
	for b.Loop() {
		if _, err := estimateEquity(hands, nil, rng); err != nil {
			b.Fatal(err)
		}
	}
}
