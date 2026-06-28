package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"qww-okx-alligator/internal/alligator"
	"qww-okx-alligator/internal/okx"
)

type config struct {
	InstType       string   `json:"instType"`
	QuoteCurrency  string   `json:"quoteCurrency"`
	InstIDs        []string `json:"instIds"`
	Bars           []string `json:"bars"`
	CandleLimit    int      `json:"candleLimit"`
	MaxInstruments int      `json:"maxInstruments"`
	Concurrency    int      `json:"concurrency"`
	MinNotional24h float64  `json:"minNotional24h"`
	SleepThreshold float64  `json:"sleepThreshold"`
	OutputDir      string   `json:"outputDir"`
}

type report struct {
	GeneratedAtUTC  time.Time                 `json:"generatedAtUTC"`
	InstrumentCount int                       `json:"instrumentCount"`
	Config          config                    `json:"config"`
	Items           []alligator.Analysis      `json:"items"`
	Errors          []instrumentError         `json:"errors,omitempty"`
	Summary         map[string]map[string]int `json:"summary"`
}

type instrumentError struct {
	Bar    string `json:"bar"`
	InstID string `json:"instId"`
	Error  string `json:"error"`
}

type analysisTask struct {
	Bar         string
	InstID      string
	Notional24h float64
}

type analysisResult struct {
	Analysis alligator.Analysis
	Error    instrumentError
	OK       bool
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	cfg := loadConfig()
	client := okx.NewClient("")

	instIDs := cfg.InstIDs
	if len(instIDs) == 0 {
		instruments, err := client.Instruments(ctx, cfg.InstType)
		if err != nil {
			return fmt.Errorf("fetch instruments: %w", err)
		}
		instIDs = selectInstruments(instruments, cfg.QuoteCurrency)
	}
	notionalByInstID, err := fetchNotional24h(ctx, client, cfg)
	if err != nil {
		return err
	}
	instIDs = filterInstrumentsByNotional(instIDs, notionalByInstID, cfg.MinNotional24h, cfg.MaxInstruments)
	if len(instIDs) == 0 {
		return errors.New("no instruments selected")
	}

	rep := report{
		GeneratedAtUTC:  time.Now().UTC(),
		InstrumentCount: len(instIDs),
		Config:          cfg,
		Summary:         map[string]map[string]int{},
	}

	for _, bar := range cfg.Bars {
		rep.Summary[bar] = map[string]int{}
	}

	for result := range analyzeInstruments(ctx, client, cfg, instIDs, notionalByInstID) {
		if !result.OK {
			rep.Errors = append(rep.Errors, result.Error)
			continue
		}
		rep.Items = append(rep.Items, result.Analysis)
		rep.Summary[result.Analysis.Bar][string(result.Analysis.State)]++
	}

	sort.Slice(rep.Items, func(i, j int) bool {
		if rep.Items[i].Bar != rep.Items[j].Bar {
			return barRank(rep.Items[i].Bar) < barRank(rep.Items[j].Bar)
		}
		if rep.Items[i].WindowStatus != rep.Items[j].WindowStatus {
			return windowRank(rep.Items[i].WindowStatus) < windowRank(rep.Items[j].WindowStatus)
		}
		if rep.Items[i].State != rep.Items[j].State {
			return stateRank(rep.Items[i].State) < stateRank(rep.Items[j].State)
		}
		if rep.Items[i].WindowStatus == alligator.WindowCompressed {
			return rep.Items[i].SpreadPct < rep.Items[j].SpreadPct
		}
		return rep.Items[i].SpreadPct > rep.Items[j].SpreadPct
	})

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(cfg.OutputDir, "alligator-report.json"), rep); err != nil {
		return err
	}

	md := renderMarkdown(rep)
	if err := os.WriteFile(filepath.Join(cfg.OutputDir, "alligator-report.md"), []byte(md), 0o644); err != nil {
		return err
	}
	if summaryPath := os.Getenv("GITHUB_STEP_SUMMARY"); summaryPath != "" {
		if err := os.WriteFile(summaryPath, []byte(md), 0o644); err != nil {
			return err
		}
	}

	fmt.Printf("wrote %d analyses and %d errors to %s\n", len(rep.Items), len(rep.Errors), cfg.OutputDir)
	return nil
}

func loadConfig() config {
	return config{
		InstType:       envString("OKX_INST_TYPE", "SWAP"),
		QuoteCurrency:  envString("OKX_QUOTE_CCY", "USDT"),
		InstIDs:        splitCSV(os.Getenv("OKX_INST_IDS")),
		Bars:           envBars(),
		CandleLimit:    envInt("OKX_CANDLE_LIMIT", 200),
		MaxInstruments: envInt("OKX_MAX_INSTRUMENTS", 0),
		Concurrency:    max(1, envInt("OKX_CONCURRENCY", 2)),
		MinNotional24h: envFloat("OKX_MIN_NOTIONAL_24H", 500000),
		SleepThreshold: envFloat("ALLIGATOR_SLEEP_THRESHOLD", 0.0015),
		OutputDir:      envString("OUTPUT_DIR", "reports"),
	}
}

func analyzeInstruments(ctx context.Context, client *okx.Client, cfg config, instIDs []string, notionalByInstID map[string]float64) <-chan analysisResult {
	tasks := make(chan analysisTask)
	results := make(chan analysisResult)

	var wg sync.WaitGroup
	for i := 0; i < cfg.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range tasks {
				results <- analyzeOne(ctx, client, cfg, task)
			}
		}()
	}

	go func() {
		defer close(tasks)
		for _, bar := range cfg.Bars {
			for _, instID := range instIDs {
				tasks <- analysisTask{Bar: bar, InstID: instID, Notional24h: notionalByInstID[instID]}
			}
		}
	}()

	go func() {
		wg.Wait()
		close(results)
	}()

	return results
}

func analyzeOne(ctx context.Context, client *okx.Client, cfg config, task analysisTask) analysisResult {
	candles, err := client.Candles(ctx, task.InstID, task.Bar, cfg.CandleLimit)
	if err != nil {
		return analysisResult{Error: instrumentError{Bar: task.Bar, InstID: task.InstID, Error: err.Error()}}
	}

	analysis, err := alligator.Analyze(task.InstID, candles, alligator.Settings{
		SleepThreshold: cfg.SleepThreshold,
	})
	if err != nil {
		return analysisResult{Error: instrumentError{Bar: task.Bar, InstID: task.InstID, Error: err.Error()}}
	}
	analysis.Bar = task.Bar
	analysis.Notional24h = task.Notional24h
	return analysisResult{Analysis: analysis, OK: true}
}

func fetchNotional24h(ctx context.Context, client *okx.Client, cfg config) (map[string]float64, error) {
	tickers, err := client.Tickers(ctx, cfg.InstType)
	if err != nil {
		return nil, fmt.Errorf("fetch tickers: %w", err)
	}
	out := make(map[string]float64, len(tickers))
	for _, ticker := range tickers {
		out[ticker.InstID] = ticker.VolCcy24h * ticker.Last
	}
	return out, nil
}

func selectInstruments(instruments []okx.Instrument, quoteCurrency string) []string {
	var ids []string
	for _, inst := range instruments {
		if inst.State != "" && !strings.EqualFold(inst.State, "live") {
			continue
		}
		if quoteCurrency != "" {
			if !strings.EqualFold(inst.QuoteCurrency, quoteCurrency) && !strings.Contains(inst.InstID, "-"+quoteCurrency) {
				continue
			}
		}
		ids = append(ids, inst.InstID)
	}
	sort.Strings(ids)
	return ids
}

func filterInstrumentsByNotional(instIDs []string, notionalByInstID map[string]float64, minNotional24h float64, max int) []string {
	filtered := make([]string, 0, len(instIDs))
	for _, instID := range instIDs {
		if minNotional24h > 0 && notionalByInstID[instID] < minNotional24h {
			continue
		}
		filtered = append(filtered, instID)
	}
	sort.Slice(filtered, func(i, j int) bool {
		left := notionalByInstID[filtered[i]]
		right := notionalByInstID[filtered[j]]
		if left == right {
			return filtered[i] < filtered[j]
		}
		return left > right
	})
	if max > 0 && len(filtered) > max {
		filtered = filtered[:max]
	}
	return filtered
}

func barRank(bar string) int {
	switch strings.ToUpper(bar) {
	case "1H":
		return 0
	case "4H":
		return 1
	case "1D", "D":
		return 2
	default:
		return 100
	}
}

func windowRank(status alligator.WindowStatus) int {
	switch status {
	case alligator.WindowCompressed:
		return 0
	case alligator.WindowBreaking:
		return 1
	case alligator.WindowMissed:
		return 2
	case alligator.WindowMixedOnly:
		return 3
	case alligator.WindowTrend:
		return 4
	default:
		return 100
	}
}

func stateRank(state alligator.State) int {
	switch state {
	case alligator.StateBullish:
		return 0
	case alligator.StateBearish:
		return 1
	case alligator.StateSleeping:
		return 2
	default:
		return 3
	}
}

func renderMarkdown(rep report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# OKX Alligator Report\n\n")
	fmt.Fprintf(&b, "- Generated at (UTC): `%s`\n", rep.GeneratedAtUTC.Format("2006-01-02 15:04:05 UTC"))
	fmt.Fprintf(&b, "- Instrument type: `%s`\n", rep.Config.InstType)
	fmt.Fprintf(&b, "- Instruments: `%d`\n", rep.InstrumentCount)
	fmt.Fprintf(&b, "- Min 24h notional: `%.0f`\n", rep.Config.MinNotional24h)
	fmt.Fprintf(&b, "- Timeframes: `%s`\n", strings.Join(rep.Config.Bars, "`, `"))
	fmt.Fprintf(&b, "- Expected analyses: `%d`\n", rep.InstrumentCount*len(rep.Config.Bars))
	fmt.Fprintf(&b, "- Analyses: `%d`, errors: `%d`\n\n", len(rep.Items), len(rep.Errors))

	fmt.Fprintf(&b, "## State Summary\n\n")
	fmt.Fprintf(&b, "| Timeframe | Bullish | Bearish | Sleeping | Mixed |\n")
	fmt.Fprintf(&b, "| --- | ---: | ---: | ---: | ---: |\n")
	for _, bar := range rep.Config.Bars {
		summary := rep.Summary[bar]
		fmt.Fprintf(
			&b,
			"| `%s` | %d | %d | %d | %d |\n",
			bar,
			summary[string(alligator.StateBullish)],
			summary[string(alligator.StateBearish)],
			summary[string(alligator.StateSleeping)],
			summary[string(alligator.StateMixed)],
		)
	}

	fmt.Fprintf(&b, "\n## Watch List\n\n")
	fmt.Fprintf(&b, "| Timeframe | Instrument | 24h Notional | State | Window | Breakout | Age | Close | Lips | Teeth | Jaw | Spread | Distance | Signal |\n")
	fmt.Fprintf(&b, "| --- | --- | ---: | --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |\n")
	for _, item := range rep.Items {
		fmt.Fprintf(
			&b,
			"| `%s` | `%s` | %.0f | `%s` | `%s` | `%s` | %d | %.8g | %.8g | %.8g | %.8g | %.3f%% | %.3f%% | %s |\n",
			item.Bar,
			item.InstID,
			item.Notional24h,
			item.State,
			item.WindowStatus,
			item.BreakoutDirection,
			item.BreakoutAge,
			item.Close,
			item.Lips,
			item.Teeth,
			item.Jaw,
			item.SpreadPct*100,
			item.DistancePct*100,
			item.Signal,
		)
	}

	if len(rep.Errors) > 0 {
		fmt.Fprintf(&b, "\n## Fetch Errors\n\n")
		fmt.Fprintf(&b, "| Timeframe | Instrument | Error |\n| --- | --- | --- |\n")
		for _, item := range rep.Errors {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", item.Bar, item.InstID, strings.ReplaceAll(item.Error, "|", "\\|"))
		}
	}

	return b.String()
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

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBars() []string {
	if bars := splitCSV(os.Getenv("OKX_BARS")); len(bars) > 0 {
		return bars
	}
	if bar := strings.TrimSpace(os.Getenv("OKX_BAR")); bar != "" {
		return []string{bar}
	}
	return []string{"1H", "4H", "1D"}
}

func splitCSV(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
