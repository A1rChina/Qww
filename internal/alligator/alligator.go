package alligator

import (
	"errors"
	"fmt"
	"math"
	"time"
)

type State string

const (
	StateBullish  State = "bullish"
	StateBearish  State = "bearish"
	StateSleeping State = "sleeping"
	StateMixed    State = "mixed"
)

type Candle struct {
	Time  time.Time `json:"time"`
	Close float64   `json:"close"`
}

type Settings struct {
	SleepThreshold float64
}

type Analysis struct {
	InstID            string            `json:"instId"`
	Bar               string            `json:"bar,omitempty"`
	Notional24h       float64           `json:"notional24h,omitempty"`
	Time              time.Time         `json:"time"`
	Close             float64           `json:"close"`
	Jaw               float64           `json:"jaw"`
	Teeth             float64           `json:"teeth"`
	Lips              float64           `json:"lips"`
	SpreadPct         float64           `json:"spreadPct"`
	DistancePct       float64           `json:"distancePct"`
	BreakoutDirection BreakoutDirection `json:"breakoutDirection"`
	BreakoutAge       int               `json:"breakoutAge"`
	WindowStatus      WindowStatus      `json:"windowStatus"`
	State             State             `json:"state"`
	Signal            string            `json:"signal"`
}

type lineSpec struct {
	period int
	shift  int
}

var (
	jawSpec   = lineSpec{period: 13, shift: 8}
	teethSpec = lineSpec{period: 8, shift: 5}
	lipsSpec  = lineSpec{period: 5, shift: 3}
)

func Analyze(instID string, candles []Candle, settings Settings) (Analysis, error) {
	if settings.SleepThreshold <= 0 {
		settings.SleepThreshold = 0.0015
	}
	if len(candles) == 0 {
		return Analysis{}, errors.New("no candles")
	}
	minCandles := jawSpec.period + jawSpec.shift + 2
	if len(candles) < minCandles {
		return Analysis{}, fmt.Errorf("need at least %d candles, got %d", minCandles, len(candles))
	}

	closes := make([]float64, len(candles))
	for i, candle := range candles {
		if candle.Close <= 0 {
			return Analysis{}, fmt.Errorf("non-positive close at index %d", i)
		}
		closes[i] = candle.Close
	}

	jaw, err := shiftedSMMA(closes, jawSpec)
	if err != nil {
		return Analysis{}, err
	}
	teeth, err := shiftedSMMA(closes, teethSpec)
	if err != nil {
		return Analysis{}, err
	}
	lips, err := shiftedSMMA(closes, lipsSpec)
	if err != nil {
		return Analysis{}, err
	}

	last := len(candles) - 1
	current := classify(closes[last], jaw[last], teeth[last], lips[last], settings.SleepThreshold)
	previous := classify(closes[last-1], jaw[last-1], teeth[last-1], lips[last-1], settings.SleepThreshold)
	spread := spreadPct(closes[last], jaw[last], teeth[last], lips[last])
	distance := distancePct(closes[last], jaw[last], teeth[last], lips[last])
	breakoutDirection, breakoutAge := breakoutInfo(closes, jaw, teeth, lips, last)
	windowStatus := classifyWindow(current, spread, distance, breakoutDirection, breakoutAge, settings.SleepThreshold)

	signal := describeSignal(previous, current)
	return Analysis{
		InstID:            instID,
		Time:              candles[last].Time,
		Close:             closes[last],
		Jaw:               jaw[last],
		Teeth:             teeth[last],
		Lips:              lips[last],
		SpreadPct:         spread,
		DistancePct:       distance,
		BreakoutDirection: breakoutDirection,
		BreakoutAge:       breakoutAge,
		WindowStatus:      windowStatus,
		State:             current,
		Signal:            signal,
	}, nil
}

func shiftedSMMA(values []float64, spec lineSpec) ([]float64, error) {
	smma, err := smma(values, spec.period)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(values))
	for i := range out {
		source := i - spec.shift
		if source < 0 {
			out[i] = math.NaN()
			continue
		}
		out[i] = smma[source]
	}
	return out, nil
}

func smma(values []float64, period int) ([]float64, error) {
	if period <= 0 {
		return nil, errors.New("period must be positive")
	}
	if len(values) < period {
		return nil, fmt.Errorf("need at least %d values, got %d", period, len(values))
	}
	out := make([]float64, len(values))
	for i := 0; i < period-1; i++ {
		out[i] = math.NaN()
	}

	var sum float64
	for i := 0; i < period; i++ {
		sum += values[i]
	}
	out[period-1] = sum / float64(period)
	for i := period; i < len(values); i++ {
		out[i] = (out[i-1]*float64(period-1) + values[i]) / float64(period)
	}
	return out, nil
}

func classify(close, jaw, teeth, lips, sleepThreshold float64) State {
	if hasNaN(jaw, teeth, lips) {
		return StateMixed
	}
	if spreadPct(close, jaw, teeth, lips) <= sleepThreshold {
		return StateSleeping
	}
	if lips > teeth && teeth > jaw && close > lips {
		return StateBullish
	}
	if lips < teeth && teeth < jaw && close < lips {
		return StateBearish
	}
	return StateMixed
}

func describeSignal(previous, current State) string {
	if previous == current {
		switch current {
		case StateBullish:
			return "多头排列延续"
		case StateBearish:
			return "空头排列延续"
		case StateSleeping:
			return "纠缠休眠"
		default:
			return "方向未确认"
		}
	}
	if previous == StateSleeping && current == StateBullish {
		return "休眠后向上张口"
	}
	if previous == StateSleeping && current == StateBearish {
		return "休眠后向下张口"
	}
	if current == StateBullish {
		return "转为多头关注"
	}
	if current == StateBearish {
		return "转为空头关注"
	}
	if current == StateSleeping {
		return "进入纠缠休眠"
	}
	return "信号切换中"
}

func spreadPct(close, jaw, teeth, lips float64) float64 {
	if close <= 0 || hasNaN(jaw, teeth, lips) {
		return 0
	}
	high := math.Max(jaw, math.Max(teeth, lips))
	low := math.Min(jaw, math.Min(teeth, lips))
	return (high - low) / close
}

func hasNaN(values ...float64) bool {
	for _, value := range values {
		if math.IsNaN(value) {
			return true
		}
	}
	return false
}
