package alligator

import "math"

type VisualStatus string

const (
	VisualCoil          VisualStatus = "coil"
	VisualPreBreakout   VisualStatus = "pre_breakout"
	VisualFreshBreakout VisualStatus = "fresh_breakout"
	VisualExtended      VisualStatus = "extended"
	VisualPullbackWatch VisualStatus = "pullback_watch"
	VisualTrendRun      VisualStatus = "trend_run"
)

func atr(candles []Candle, period int) float64 {
	if len(candles) < 2 || period <= 0 {
		return 0
	}
	start := len(candles) - period
	if start < 1 {
		start = 1
	}

	var sum float64
	var count int
	for i := start; i < len(candles); i++ {
		highLow := candles[i].High - candles[i].Low
		highPrevClose := math.Abs(candles[i].High - candles[i-1].Close)
		lowPrevClose := math.Abs(candles[i].Low - candles[i-1].Close)
		tr := math.Max(highLow, math.Max(highPrevClose, lowPrevClose))
		sum += tr
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func distanceATR(close, jaw, teeth, lips, atrValue float64) float64 {
	if atrValue <= 0 || close <= 0 || hasNaN(jaw, teeth, lips) {
		return 0
	}
	high := math.Max(jaw, math.Max(teeth, lips))
	low := math.Min(jaw, math.Min(teeth, lips))
	if close > high {
		return (close - high) / atrValue
	}
	if close < low {
		return (low - close) / atrValue
	}
	return 0
}

func bodyOutsideAge(candles []Candle, jaw, teeth, lips []float64, last int) int {
	direction := bodyOutsideDirection(candles[last], jaw[last], teeth[last], lips[last])
	if direction == BreakoutNone {
		return 0
	}

	age := 0
	for i := last; i >= 0; i-- {
		if bodyOutsideDirection(candles[i], jaw[i], teeth[i], lips[i]) != direction {
			break
		}
		age++
	}
	return age
}

func bodyOutsideDirection(candle Candle, jaw, teeth, lips float64) BreakoutDirection {
	if candle.Open <= 0 || candle.Close <= 0 || hasNaN(jaw, teeth, lips) {
		return BreakoutNone
	}
	high := math.Max(jaw, math.Max(teeth, lips))
	low := math.Min(jaw, math.Min(teeth, lips))
	bodyHigh := math.Max(candle.Open, candle.Close)
	bodyLow := math.Min(candle.Open, candle.Close)
	if bodyLow > high {
		return BreakoutUp
	}
	if bodyHigh < low {
		return BreakoutDown
	}
	return BreakoutNone
}

func touchLineCount(candles []Candle, jaw, teeth, lips []float64, last, lookback int) int {
	if lookback <= 0 {
		return 0
	}
	start := last - lookback + 1
	if start < 0 {
		start = 0
	}

	count := 0
	for i := start; i <= last; i++ {
		if touchesLineArea(candles[i], jaw[i], teeth[i], lips[i]) {
			count++
		}
	}
	return count
}

func touchesLineArea(candle Candle, jaw, teeth, lips float64) bool {
	if candle.High <= 0 || candle.Low <= 0 || hasNaN(jaw, teeth, lips) {
		return false
	}
	high := math.Max(jaw, math.Max(teeth, lips))
	low := math.Min(jaw, math.Min(teeth, lips))
	return candle.Low <= high && candle.High >= low
}

func classifyVisualStatus(state State, window WindowStatus, breakoutAge, bodyAge, touchCount int, distanceATRValue float64) VisualStatus {
	if (state == StateBullish || state == StateBearish) && (bodyAge >= 4 || distanceATRValue >= 1.8) {
		return VisualTrendRun
	}
	if bodyAge >= 3 && distanceATRValue >= 1.2 {
		return VisualExtended
	}
	if breakoutAge >= 3 && touchCount <= 1 && distanceATRValue >= 1.0 {
		return VisualExtended
	}
	if window == WindowMissed && touchCount >= 2 && distanceATRValue <= 1.0 {
		return VisualPullbackWatch
	}
	if bodyAge >= 1 && bodyAge <= 2 && distanceATRValue <= 1.2 {
		return VisualFreshBreakout
	}
	if window == WindowBreaking {
		return VisualFreshBreakout
	}
	if window == WindowCompressed && touchCount >= 3 {
		return VisualCoil
	}
	if window == WindowCompressed {
		return VisualPreBreakout
	}
	if window == WindowTrend {
		return VisualTrendRun
	}
	if window == WindowMissed {
		return VisualExtended
	}
	return VisualPreBreakout
}
