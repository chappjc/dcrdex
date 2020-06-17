package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"decred.org/dcrdex/dex"
	"decred.org/dcrdex/server/asset/btc"
	"github.com/decred/slog"
)

func main() {
	logger := slog.NewBackend(os.Stdout).Logger("btc")
	btcAsset, err := btc.NewBackend("/home/jon/.bitcoin/bitcoin.conf", logger, dex.Mainnet)
	if err != nil {
		fmt.Println(err)
		return
	}
	btcBackend := btcAsset.(*btc.Backend)
	height, err := btcBackend.BestBlockHeight()
	if err != nil {
		fmt.Println(err)
		return
	}

	first := int64(320_000)
	type blockTime struct {
		t time.Time
		h int64
	}
	times := make([]*blockTime, 0, height-first)
	for i := height; i > first; i-- {
		ts, err := btcBackend.BlockTimeStamp(i)
		if err != nil {
			fmt.Println(err)
			return
		}
		times = append(times, &blockTime{ts, i})
		if i%10000 == 0 {
			fmt.Println(i)
		}
	}

	type delta struct {
		h, h0 int64
		delta time.Duration
	}
	stretch := 8
	deltas := make([]*delta, 0, len(times)-6)
	for i := range times {
		h := len(times) - i - 1
		if h-stretch < 0 {
			continue
		}
		deltas = append(deltas, &delta{
			h:     times[h].h,
			h0:    times[h-stretch].h,
			delta: times[h-stretch].t.Sub(times[h].t),
		})
	}

	fmt.Println(len(deltas))
	sort.Slice(deltas, func(i, j int) bool {
		return deltas[i].delta > deltas[j].delta
	})

	for _, d := range deltas[:12] {
		fmt.Printf("From %d to %d: %v\n", d.h, d.h0, d.delta)
	}
}
