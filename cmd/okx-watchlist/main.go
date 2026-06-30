package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type report struct {
	GeneratedAtUTC  time.Time                 `json:"generatedAtUTC"`
	InstrumentCount int                       `json:"instrumentCount"`
	Items           []analysis                `json:"items"`
	Errors          []instrumentError         `json:"errors,omitempty"`
	Summary         map[string]map[string]int `json:"summary"`
}

type instrumentError struct {
	Bar    string `json:"bar"`
	InstID string `json:"instId"`
	Error  string `json:"error"`
}

type analysis struct {
	InstID            string  `json:"instId"`
	Bar               string  `json:"bar,omitempty"`
	Notional24h       float64 `json:"notional24h,omitempty"`
	Close             float64 `json:"close"`
	Jaw               float64 `json:"jaw"`
	Teeth             float64 `json:"teeth"`
	Lips              float64 `json:"lips"`
	SpreadPct         float64 `json:"spreadPct"`
	DistancePct       float64 `json:"distancePct"`
	DistanceATR       float64 `json:"distanceAtr"`
	BreakoutDirection string  `json:"breakoutDirection"`
	BreakoutAge       int     `json:"breakoutAge"`
	BodyOutsideAge    int     `json:"bodyOutsideAge"`
	TouchLineCount    int     `json:"touchLineCount"`
	WindowStatus      string  `json:"windowStatus"`
	VisualStatus      string  `json:"visualStatus"`
	State             string  `json:"state"`
	Signal            string  `json:"signal"`
}

type watchlist struct {
	GeneratedAtUTC time.Time        `json:"generatedAtUTC"`
	SourceReportUTC time.Time       `json:"sourceReportUTC"`
	Market          marketSnapshot  `json:"market"`
	Benchmark       []symbolCard    `json:"benchmark"`
	Active          []symbolCard    `json:"active"`
	Emerging        []symbolCard    `json:"emerging"`
	Reverse         []symbolCard    `json:"reverse"`
	Ignore          []symbolCard    `json:"ignore"`
	Summary         map[string]map[string]int `json:"summary"`
}

type marketSnapshot struct {
	Bias      map[string]string `json:"bias"`
	Readiness int              `json:"readiness"`
	Note      string           `json:"note"`
}

type symbolCard struct {
	Symbol      string          `json:"symbol"`
	Score       int             `json:"score"`
	Delta       int             `json:"delta,omitempty"`
	IsNew       bool            `json:"isNew,omitempty"`
	Window      string          `json:"window"`
	Lifecycle   string          `json:"lifecycle"`
	Reason      string          `json:"reason"`
	Timeframes  map[string]tfView `json:"timeframes"`
	Notional24h float64         `json:"notional24h,omitempty"`
}

type tfView struct {
	State    string  `json:"state"`
	Visual   string  `json:"visual"`
	Window   string  `json:"window"`
	Breakout string  `json:"breakout"`
	SpreadPct float64 `json:"spreadPct"`
	DistancePct float64 `json:"distancePct"`
}

func main() {
	outDir := envString("OUTPUT_DIR", "reports")
	reportPath := filepath.Join(outDir, "alligator-report.json")
	previousPath := filepath.Join(outDir, "watchlist.json")

	rep, err := readReport(reportPath)
	if err != nil {
		panic(err)
	}
	previous := readPrevious(previousPath)
	wl := buildWatchlist(rep, previous)

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}
	if err := writeJSON(filepath.Join(outDir, "watchlist.json"), wl); err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "watchlist.md"), []byte(renderWatchlistMarkdown(wl)), 0o644); err != nil {
		panic(err)
	}
	fmt.Printf("wrote dynamic watchlist to %s\n", outDir)
}

func readReport(path string) (report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return report{}, err
	}
	var rep report
	if err := json.Unmarshal(raw, &rep); err != nil {
		return report{}, err
	}
	return rep, nil
}

func readPrevious(path string) watchlist {
	raw, err := os.ReadFile(path)
	if err != nil {
		return watchlist{}
	}
	var wl watchlist
	_ = json.Unmarshal(raw, &wl)
	return wl
}

func buildWatchlist(rep report, previous watchlist) watchlist {
	bySymbol := map[string][]analysis{}
	for _, item := range rep.Items {
		bySymbol[item.InstID] = append(bySymbol[item.InstID], item)
	}

	previousScores := map[string]int{}
	for _, bucket := range [][]symbolCard{previous.Active, previous.Emerging, previous.Reverse, previous.Benchmark} {
		for _, card := range bucket {
			previousScores[card.Symbol] = card.Score
		}
	}

	cards := make([]symbolCard, 0, len(bySymbol))
	for symbol, items := range bySymbol {
		card := makeCard(symbol, items)
		if previous.GeneratedAtUTC.IsZero() {
			card.IsNew = false
		} else if oldScore, ok := previousScores[symbol]; ok {
			card.Delta = card.Score - oldScore
		} else {
			card.IsNew = true
		}
		cards = append(cards, card)
	}

	sortCards(cards)
	benchmark := pickBenchmark(cards)
	active := pickActive(cards, benchmark, 6, 9)
	emerging := pickEmerging(cards, active, benchmark, 3)
	reverse := pickReverse(cards, active, benchmark, 4)
	ignore := pickIgnore(cards, 6)

	return watchlist{
		GeneratedAtUTC: time.Now().UTC(),
		SourceReportUTC: rep.GeneratedAtUTC,
		Market: buildMarket(rep, benchmark),
		Benchmark: benchmark,
		Active: active,
		Emerging: emerging,
		Reverse: reverse,
		Ignore: ignore,
		Summary: rep.Summary,
	}
}

func makeCard(symbol string, items []analysis) symbolCard {
	views := map[string]tfView{}
	scoreSum := 0.0
	weightSum := 0.0
	maxNotional := 0.0
	bestWindow := "❌"
	bestLifecycle := "Archived"
	bestReason := "窗口已结束"

	for _, item := range items {
		views[item.Bar] = tfView{
			State: item.State,
			Visual: item.VisualStatus,
			Window: item.WindowStatus,
			Breakout: fmt.Sprintf("%s/%d", item.BreakoutDirection, item.BreakoutAge),
			SpreadPct: item.SpreadPct,
			DistancePct: item.DistancePct,
		}
		if item.Notional24h > maxNotional {
			maxNotional = item.Notional24h
		}
		weight := timeframeWeight(item.Bar)
		scoreSum += float64(scoreAnalysis(item)) * weight
		weightSum += weight
	}

	score := 0
	if weightSum > 0 {
		score = clampInt(int(math.Round(scoreSum/weightSum)), 0, 100)
	}
	if oneD, ok := findBar(items, "1D"); ok && oneD.WindowStatus == "trend" {
		score = clampInt(score-8, 0, 100)
	}
	if compressedCount(items) >= 2 {
		score = clampInt(score+6, 0, 100)
	}
	if breakingCount(items) >= 1 && missedCount(items) == 0 {
		score = clampInt(score+3, 0, 100)
	}

	best := bestItem(items)
	if best != nil {
		bestWindow = windowEmoji(best.WindowStatus, best.VisualStatus)
		bestLifecycle = lifecycle(*best)
		bestReason = reason(*best, items)
	}

	return symbolCard{
		Symbol: trimSwapSuffix(symbol),
		Score: score,
		Window: bestWindow,
		Lifecycle: bestLifecycle,
		Reason: bestReason,
		Timeframes: views,
		Notional24h: maxNotional,
	}
}

func scoreAnalysis(item analysis) int {
	score := 0
	switch item.WindowStatus {
	case "compressed":
		score += 35
	case "breaking":
		score += 28
	case "mixed_only":
		score += 10
	case "missed":
		score -= 30
	case "trend":
		score -= 35
	}
	switch item.VisualStatus {
	case "coil":
		score += 25
	case "pre_breakout":
		score += 22
	case "fresh_breakout":
		score += 18
	case "pullback_watch":
		score -= 12
	case "extended":
		score -= 25
	case "trend_run":
		score -= 30
	}

	spreadPct := item.SpreadPct * 100
	distancePct := item.DistancePct * 100
	score += clampInt(int(math.Round(20-spreadPct*4)), 0, 20)
	score += clampInt(int(math.Round(15-distancePct*5)), 0, 15)
	score += clampInt(item.TouchLineCount, 0, 5)
	if item.BreakoutDirection == "none" {
		score += 3
	} else if item.BreakoutAge <= 2 {
		score += 5
	} else {
		score -= 10
	}
	if item.State == "mixed" || item.State == "sleeping" {
		score += 5
	}
	return clampInt(score, 0, 100)
}

func buildMarket(rep report, benchmark []symbolCard) marketSnapshot {
	bias := map[string]string{}
	readinessParts := []int{}
	for _, bar := range []string{"1H", "4H", "1D"} {
		summary := rep.Summary[bar]
		total := summary["bullish"] + summary["bearish"] + summary["sleeping"] + summary["mixed"]
		if total == 0 {
			bias[bar] = "n/a"
			continue
		}
		mixedPct := float64(summary["mixed"]+summary["sleeping"]) / float64(total)
		bullPct := float64(summary["bullish"]) / float64(total)
		bearPct := float64(summary["bearish"]) / float64(total)
		switch {
		case mixedPct >= 0.5:
			bias[bar] = "⚪ Mixed"
		case bullPct > bearPct:
			bias[bar] = "📈 Bullish"
		default:
			bias[bar] = "📉 Bearish"
		}
		readinessParts = append(readinessParts, clampInt(int(math.Round(mixedPct*100)), 0, 100))
	}
	readiness := averageInt(readinessParts)
	if bias["1D"] == "📉 Bearish" {
		readiness = clampInt(readiness-5, 0, 100)
	}
	note := "适合维护观察名单，不适合追逐已经走远的趋势。"
	if readiness < 45 {
		note = "多数窗口已离开压缩区，优先等待下一轮重新缠绕。"
	}
	return marketSnapshot{Bias: bias, Readiness: readiness, Note: note}
}

func renderWatchlistMarkdown(wl watchlist) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 🐊 OKX Alligator Dynamic Watchlist\n\n")
	fmt.Fprintf(&b, "> Report: `%s`  \n", wl.SourceReportUTC.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "> Generated: `%s`  \n", wl.GeneratedAtUTC.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "> Mode: `Dynamic Watchlist`\n\n")

	fmt.Fprintf(&b, "## 🌍 Market Snapshot\n\n")
	fmt.Fprintf(&b, "| Benchmark | Score | Δ | Window | Lifecycle | Note |\n")
	fmt.Fprintf(&b, "| --- | ---: | ---: | --- | --- | --- |\n")
	for _, card := range wl.Benchmark {
		fmt.Fprintf(&b, "| **%s** | %d | %s | %s | %s | %s |\n", card.Symbol, card.Score, deltaText(card), card.Window, card.Lifecycle, card.Reason)
	}
	fmt.Fprintf(&b, "\n| Cycle | Bias |\n| --- | --- |\n")
	for _, bar := range []string{"1H", "4H", "1D"} {
		fmt.Fprintf(&b, "| %s | %s |\n", bar, wl.Market.Bias[bar])
	}
	fmt.Fprintf(&b, "\n**Market Readiness:** `%d / 100`  \n", wl.Market.Readiness)
	fmt.Fprintf(&b, "📌 %s\n\n", wl.Market.Note)

	renderCardTable(&b, "## 🎯 Active Watchlist", wl.Active)
	renderCardTable(&b, "## ⚡ Emerging", wl.Emerging)
	renderCardTable(&b, "## 🔄 Reverse Watch", wl.Reverse)
	renderCardTable(&b, "## 🚫 Ignore", wl.Ignore)

	fmt.Fprintf(&b, "## 📊 Market Breadth\n\n")
	fmt.Fprintf(&b, "| TF | 📈 Bull | 📉 Bear | ⚪ Mixed | 💤 Sleeping |\n")
	fmt.Fprintf(&b, "| --- | ---: | ---: | ---: | ---: |\n")
	for _, bar := range []string{"1H", "4H", "1D"} {
		s := wl.Summary[bar]
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", bar, s["bullish"], s["bearish"], s["mixed"], s["sleeping"])
	}
	fmt.Fprintf(&b, "\n## 📌 Summary\n\n")
	fmt.Fprintf(&b, "重点看 Active Watchlist 的 6–9 个标的；BTC / ETH / SOL 只作为市场锚点，不因其进入 Ignore 就代表完全不看市场。\n")
	return b.String()
}

func renderCardTable(b *strings.Builder, title string, cards []symbolCard) {
	fmt.Fprintf(b, "%s\n\n", title)
	if len(cards) == 0 {
		fmt.Fprintf(b, "_No candidates._\n\n")
		return
	}
	fmt.Fprintf(b, "| Symbol | Score | Δ | Window | Lifecycle | Reason |\n")
	fmt.Fprintf(b, "| --- | ---: | ---: | --- | --- | --- |\n")
	for _, card := range cards {
		name := card.Symbol
		if card.IsNew {
			name = "🆕 " + name
		}
		fmt.Fprintf(b, "| **%s** | %d | %s | %s | %s | %s |\n", name, card.Score, deltaText(card), card.Window, card.Lifecycle, card.Reason)
	}
	fmt.Fprintf(b, "\n")
}

func pickBenchmark(cards []symbolCard) []symbolCard {
	out := []symbolCard{}
	for _, symbol := range []string{"BTC", "ETH", "SOL"} {
		if card, ok := findCard(cards, symbol); ok {
			out = append(out, card)
		}
	}
	return out
}

func pickActive(cards, benchmark []symbolCard, minItems, maxItems int) []symbolCard {
	blocked := symbolSet(benchmark)
	out := []symbolCard{}
	for _, card := range cards {
		if blocked[card.Symbol] || card.Score < 50 || card.Window == "❌" {
			continue
		}
		out = append(out, card)
		if len(out) >= maxItems {
			break
		}
	}
	if len(out) < minItems {
		for _, card := range cards {
			if blocked[card.Symbol] || containsCard(out, card.Symbol) || card.Score < 42 {
				continue
			}
			out = append(out, card)
			if len(out) >= minItems {
				break
			}
		}
	}
	return out
}

func pickEmerging(cards, active, benchmark []symbolCard, limit int) []symbolCard {
	blocked := symbolSet(append(active, benchmark...))
	out := []symbolCard{}
	for _, card := range cards {
		if blocked[card.Symbol] || card.Score < 45 || card.Window == "❌" {
			continue
		}
		if card.IsNew || card.Delta >= 5 || card.Window == "🔥" {
			out = append(out, card)
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func pickReverse(cards, active, benchmark []symbolCard, limit int) []symbolCard {
	blocked := symbolSet(append(active, benchmark...))
	out := []symbolCard{}
	for _, card := range cards {
		if blocked[card.Symbol] || card.Score < 45 || card.Window == "❌" {
			continue
		}
		oneH, ok1 := card.Timeframes["1H"]
		fourH, ok4 := card.Timeframes["4H"]
		oneD, okD := card.Timeframes["1D"]
		lowUp := (ok1 && strings.HasPrefix(oneH.Breakout, "up/")) || (ok4 && strings.HasPrefix(fourH.Breakout, "up/"))
		highBear := okD && oneD.State == "bearish"
		if lowUp && highBear {
			card.Reason = "低周期向上修复，高周期仍未确认"
			out = append(out, card)
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func pickIgnore(cards []symbolCard, limit int) []symbolCard {
	copyCards := append([]symbolCard(nil), cards...)
	sort.Slice(copyCards, func(i, j int) bool {
		return ignoreRank(copyCards[i]) > ignoreRank(copyCards[j])
	})
	out := []symbolCard{}
	for _, card := range copyCards {
		if ignoreRank(card) <= 0 {
			continue
		}
		out = append(out, card)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func sortCards(cards []symbolCard) {
	sort.Slice(cards, func(i, j int) bool {
		if windowSortRank(cards[i].Window) != windowSortRank(cards[j].Window) {
			return windowSortRank(cards[i].Window) < windowSortRank(cards[j].Window)
		}
		if cards[i].Score != cards[j].Score {
			return cards[i].Score > cards[j].Score
		}
		return cards[i].Notional24h > cards[j].Notional24h
	})
}

func scoreSymbol(card symbolCard) int { return card.Score }

func bestItem(items []analysis) *analysis {
	if len(items) == 0 {
		return nil
	}
	best := items[0]
	for _, item := range items[1:] {
		if scoreAnalysis(item) > scoreAnalysis(best) {
			best = item
		}
	}
	return &best
}

func reason(best analysis, items []analysis) string {
	switch best.WindowStatus {
	case "compressed":
		return "仍在压缩，Close贴近三线"
	case "breaking":
		return "刚脱离混沌，距离仍可控"
	case "missed":
		return "窗口已过，只等回踩/回压"
	case "trend":
		return "趋势延续，不追"
	}
	if compressedCount(items) >= 2 {
		return "多周期未确认，仍可观察"
	}
	return "方向未确认，低优先观察"
}

func lifecycle(item analysis) string {
	if item.WindowStatus == "compressed" && (item.VisualStatus == "coil" || item.VisualStatus == "pre_breakout") {
		return "Watching"
	}
	if item.WindowStatus == "breaking" || item.VisualStatus == "fresh_breakout" {
		return "Preparing"
	}
	if item.WindowStatus == "trend" || item.VisualStatus == "trend_run" {
		return "Running"
	}
	if item.WindowStatus == "missed" || item.VisualStatus == "extended" || item.VisualStatus == "pullback_watch" {
		return "Missed"
	}
	return "Watching"
}

func windowEmoji(window, visual string) string {
	switch {
	case window == "compressed" && (visual == "coil" || visual == "pre_breakout"):
		return "🧩"
	case window == "breaking" || visual == "fresh_breakout":
		return "🔥"
	case window == "missed" || visual == "pullback_watch" || visual == "extended":
		return "⚠️"
	case window == "trend" || visual == "trend_run":
		return "❌"
	default:
		return "🧩"
	}
}

func ignoreRank(card symbolCard) int {
	rank := 0
	for _, tf := range card.Timeframes {
		if tf.Window == "missed" || tf.Visual == "extended" || tf.Visual == "pullback_watch" {
			rank += 3
		}
		if tf.Window == "trend" || tf.Visual == "trend_run" {
			rank += 2
		}
		if tf.SpreadPct*100 >= 5 || tf.DistancePct*100 >= 5 {
			rank += 2
		}
	}
	return rank
}

func findBar(items []analysis, bar string) (analysis, bool) {
	for _, item := range items {
		if strings.EqualFold(item.Bar, bar) {
			return item, true
		}
	}
	return analysis{}, false
}

func findCard(cards []symbolCard, symbol string) (symbolCard, bool) {
	for _, card := range cards {
		if card.Symbol == symbol {
			return card, true
		}
	}
	return symbolCard{}, false
}

func containsCard(cards []symbolCard, symbol string) bool {
	_, ok := findCard(cards, symbol)
	return ok
}

func symbolSet(cards []symbolCard) map[string]bool {
	out := map[string]bool{}
	for _, card := range cards {
		out[card.Symbol] = true
	}
	return out
}

func compressedCount(items []analysis) int {
	count := 0
	for _, item := range items {
		if item.WindowStatus == "compressed" || item.VisualStatus == "coil" || item.VisualStatus == "pre_breakout" {
			count++
		}
	}
	return count
}

func breakingCount(items []analysis) int {
	count := 0
	for _, item := range items {
		if item.WindowStatus == "breaking" || item.VisualStatus == "fresh_breakout" {
			count++
		}
	}
	return count
}

func missedCount(items []analysis) int {
	count := 0
	for _, item := range items {
		if item.WindowStatus == "missed" || item.VisualStatus == "extended" || item.VisualStatus == "pullback_watch" || item.VisualStatus == "trend_run" {
			count++
		}
	}
	return count
}

func timeframeWeight(bar string) float64 {
	switch strings.ToUpper(bar) {
	case "1H":
		return 0.40
	case "4H":
		return 0.45
	case "1D", "D":
		return 0.15
	default:
		return 0.10
	}
}

func windowSortRank(window string) int {
	switch window {
	case "🔥":
		return 0
	case "🧩":
		return 1
	case "⚠️":
		return 2
	default:
		return 3
	}
}

func deltaText(card symbolCard) string {
	if card.IsNew {
		return "🆕"
	}
	if card.Delta > 0 {
		return fmt.Sprintf("▲%d", card.Delta)
	}
	if card.Delta < 0 {
		return fmt.Sprintf("▼%d", -card.Delta)
	}
	return "—"
}

func trimSwapSuffix(symbol string) string {
	return strings.TrimSuffix(symbol, "-USDT-SWAP")
}

func averageInt(values []int) int {
	if len(values) == 0 {
		return 0
	}
	sum := 0
	for _, value := range values {
		sum += value
	}
	return int(math.Round(float64(sum) / float64(len(values))))
}

func clampInt(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}

func writeJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
