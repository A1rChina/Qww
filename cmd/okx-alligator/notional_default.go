package main

import "os"

const defaultMinNotional24h = "600000"

func init() {
	if os.Getenv("OKX_MIN_NOTIONAL_24H") == "" {
		_ = os.Setenv("OKX_MIN_NOTIONAL_24H", defaultMinNotional24h)
	}
}
