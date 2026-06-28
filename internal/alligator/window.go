package alligator

import "math"

type WindowStatus string

const (
	WindowCompressed WindowStatus = "compressed"
	WindowBreaking   WindowStatus = "breaking"
	WindowMissed     WindowStatus = "missed"
	WindowTrend      WindowStatus = "trend"
	WindowMixedOnly  WindowStatus = "mixed_only"
)

type BreakoutDirection string

const (
	BreakoutNone BreakoutDirection = "none"
	BreakoutUp   BreakoutDirection = "up"
	BreakoutDown BreakoutDirection = "down"
)

func classifyWindow(state State, spread, distance float64, direction BreakoutDirection, age int, sleepThreshold float64) WindowStatus {
	lowSpread := sleepThreshold * 2
	breakingSpread := sleepThreshold * 8
	largeSpread := sleepThreshold * 20
	closeEnough := distance <= math.Max(spread*1.5, sleepThreshold*3)

	if (state == StateBullish || state == StateBearish) && (spread >= breakingSpread || age >= 3) {
		return WindowTrend
	}
	if spread <= lowSpread && closeEnough && age == 0 {
		return WindowCompressed
	}
	if direction != BreakoutNone && age <= 1 && spread <= breakingSpread {
		return WindowBreaking
	}
	if direction != BreakoutNone && (age >= 3 || distance >= math.Max(spread*2, sleepThreshold*8)) {
		return WindowMissed
	}
	if spread >= largeSpread {
		return WindowMissed
	}
	if spread <= breakingSpread && closeEnough {
		return WindowCompressed
	}
	if state == StateBullish || state == StateBearish {
		return WindowBreaking
	}
	return WindowMixedOnly
}

func breakoutInfo(closes, jaw, teeth, lips []float64, last int) (BreakoutDirection, int) {
	direction := breakoutDirection(closes[last], jaw[last], teeth[last], lips[last])
	if direction == BreakoutNone {
		return BreakoutNone, 0
	}

	age := 0
	for i := last; i >= 0; i-- {
		if breakoutDirection(closes[i], jaw[i], teeth[i], lips[i]) != direction {
			break
		}
		age++
	}
	return direction, age
}

func breakoutDirection(close, jaw, teeth, lips float64) BreakoutDirection {
	if close <= 0 || hasNaN(jaw, teeth, lips) {
		return BreakoutNone
	}
	high := math.Max(jaw, math.Max(teeth, lips))
	low := math.Min(jaw, math.Min(teeth, lips))
	if close > high {
		return BreakoutUp
	}
	if close < low {
		return BreakoutDown
	}
	return BreakoutNone
}

func distancePct(close, jaw, teeth, lips float64) float64 {
	if close <= 0 || hasNaN(jaw, teeth, lips) {
		return 0
	}
	mid := (jaw + teeth + lips) / 3
	return math.Abs(close-mid) / close
}
