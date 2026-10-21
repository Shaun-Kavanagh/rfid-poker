package store

import (
	"fmt"
	"math/rand/v2"

	"github.com/whywaita/poker-go"
)

const (
	// exactRunoutLimit is the most run-outs enumerated exactly. Beyond it (preflop) run-outs are sampled.
	exactRunoutLimit = 50_000
	// equitySamples is the number of sampled run-outs, about ±0.7% (95% CI) per player.
	equitySamples = 20_000
)

// estimateEquity returns each player's share of the pot over the possible run-outs of board.
// Split pots are shared equally among the tied players, so the shares sum to 1.
// Run-outs are enumerated exactly when there are at most exactRunoutLimit of them (flop onwards),
// otherwise equitySamples random run-outs are drawn (preflop with any number of players).
func estimateEquity(hands [][]poker.Card, board []poker.Card, rng *rand.Rand) ([]float64, error) {
	if len(hands) < 2 {
		return nil, fmt.Errorf("need at least 2 hands, got %d", len(hands))
	}
	if len(board) > 5 {
		return nil, fmt.Errorf("board has %d cards", len(board))
	}

	seen := make(map[poker.Card]bool, 2*len(hands)+len(board))
	use := func(c poker.Card) error {
		if seen[c] {
			return fmt.Errorf("card %s of %s appears twice", c.Rank, c.Suit)
		}
		seen[c] = true
		return nil
	}
	for i, h := range hands {
		if len(h) != 2 {
			return nil, fmt.Errorf("hand %d has %d cards", i, len(h))
		}
		for _, c := range h {
			if err := use(c); err != nil {
				return nil, err
			}
		}
	}
	for _, c := range board {
		if err := use(c); err != nil {
			return nil, err
		}
	}

	deck := make([]poker.Card, 0, 52)
	for _, c := range poker.NewDeck().Cards {
		if !seen[c] {
			deck = append(deck, c)
		}
	}

	need := 5 - len(board)
	if need > len(deck) {
		return nil, fmt.Errorf("not enough cards left to complete the board")
	}

	// Each player's 7 cards: hole cards, known board, then the run-out (filled per iteration).
	sevens := make([][]poker.Card, len(hands))
	for i, h := range hands {
		sevens[i] = make([]poker.Card, 0, 7)
		sevens[i] = append(sevens[i], h...)
		sevens[i] = append(sevens[i], board...)
		sevens[i] = sevens[i][:7]
	}
	runoutAt := 2 + len(board)

	shares := make([]float64, len(hands))
	values := make([]int, len(hands))
	score := func(runout []poker.Card) {
		best := int(^uint(0) >> 1)
		for i := range sevens {
			copy(sevens[i][runoutAt:], runout)
			// MadeHand values rank hands from 1 (royal flush) upwards: lower is better.
			values[i] = poker.NewBestMadeHand(sevens[i]).Value
			if values[i] < best {
				best = values[i]
			}
		}
		winners := 0
		for _, v := range values {
			if v == best {
				winners++
			}
		}
		for i, v := range values {
			if v == best {
				shares[i] += 1 / float64(winners)
			}
		}
	}

	runout := make([]poker.Card, need)
	total := 0
	if binomial(len(deck), need) <= exactRunoutLimit {
		idx := make([]int, need)
		for i := range idx {
			idx[i] = i
		}
		for {
			for i, j := range idx {
				runout[i] = deck[j]
			}
			score(runout)
			total++
			if !nextCombination(idx, len(deck)) {
				break
			}
		}
	} else {
		for s := 0; s < equitySamples; s++ {
			// Partial Fisher-Yates: the first `need` cards of deck become a uniform random run-out.
			for i := 0; i < need; i++ {
				j := i + rng.IntN(len(deck)-i)
				deck[i], deck[j] = deck[j], deck[i]
				runout[i] = deck[i]
			}
			score(runout)
			total++
		}
	}

	for i := range shares {
		shares[i] /= float64(total)
	}
	return shares, nil
}

// nextCombination advances idx to the next k-combination of [0, n) in lexicographic order.
func nextCombination(idx []int, n int) bool {
	k := len(idx)
	for i := k - 1; i >= 0; i-- {
		if idx[i] < n-k+i {
			idx[i]++
			for j := i + 1; j < k; j++ {
				idx[j] = idx[j-1] + 1
			}
			return true
		}
	}
	return false
}

func binomial(n, k int) int {
	if k < 0 || k > n {
		return 0
	}
	r := 1
	for i := 1; i <= k; i++ {
		r = r * (n - k + i) / i
	}
	return r
}
