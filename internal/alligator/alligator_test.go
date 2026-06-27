package alligator

import (
	"testing"
	"time"
)

func TestAnalyzeBullishTrend(t *testing.T) {
	candles := make([]Candle, 80)
	for i := range candles {
		candles[i] = Candle{
			Time:  time.Unix(int64(i*3600), 0).UTC(),
			Close: 100 + float64(i),
		}
	}

	got, err := Analyze("BTC-USDT-SWAP", candles, Settings{SleepThreshold: 0.0001})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateBullish {
		t.Fatalf("state = %s, want %s", got.State, StateBullish)
	}
	if !(got.Lips > got.Teeth && got.Teeth > got.Jaw) {
		t.Fatalf("lines are not bullish: lips=%f teeth=%f jaw=%f", got.Lips, got.Teeth, got.Jaw)
	}
}

func TestAnalyzeSleepingTrend(t *testing.T) {
	candles := make([]Candle, 80)
	for i := range candles {
		candles[i] = Candle{
			Time:  time.Unix(int64(i*3600), 0).UTC(),
			Close: 100,
		}
	}

	got, err := Analyze("BTC-USDT-SWAP", candles, Settings{SleepThreshold: 0.0015})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateSleeping {
		t.Fatalf("state = %s, want %s", got.State, StateSleeping)
	}
}
