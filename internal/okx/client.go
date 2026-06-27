package okx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"qww-okx-alligator/internal/alligator"
)

const defaultBaseURL = "https://www.okx.com"

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type Instrument struct {
	InstID        string `json:"instId"`
	InstType      string `json:"instType"`
	QuoteCurrency string `json:"quoteCcy"`
	State         string `json:"state"`
}

type Ticker struct {
	InstID    string  `json:"instId"`
	Last      float64 `json:"-"`
	VolCcy24h float64 `json:"-"`
}

type rawTicker struct {
	InstID    string `json:"instId"`
	Last      string `json:"last"`
	VolCcy24h string `json:"volCcy24h"`
}

type response[T any] struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data T      `json:"data"`
}

func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *Client) Instruments(ctx context.Context, instType string) ([]Instrument, error) {
	values := url.Values{}
	values.Set("instType", instType)

	var out response[[]Instrument]
	if err := c.get(ctx, "/api/v5/public/instruments", values, &out); err != nil {
		return nil, err
	}
	if out.Code != "0" {
		return nil, fmt.Errorf("okx instruments code=%s msg=%s", out.Code, out.Msg)
	}
	return out.Data, nil
}

func (c *Client) Tickers(ctx context.Context, instType string) ([]Ticker, error) {
	values := url.Values{}
	values.Set("instType", instType)

	var out response[[]rawTicker]
	if err := c.get(ctx, "/api/v5/market/tickers", values, &out); err != nil {
		return nil, err
	}
	if out.Code != "0" {
		return nil, fmt.Errorf("okx tickers code=%s msg=%s", out.Code, out.Msg)
	}

	tickers := make([]Ticker, 0, len(out.Data))
	for _, raw := range out.Data {
		last, err := strconv.ParseFloat(raw.Last, 64)
		if err != nil {
			return nil, fmt.Errorf("parse ticker last for %s: %w", raw.InstID, err)
		}
		volCcy24h, err := strconv.ParseFloat(raw.VolCcy24h, 64)
		if err != nil {
			return nil, fmt.Errorf("parse ticker volCcy24h for %s: %w", raw.InstID, err)
		}
		tickers = append(tickers, Ticker{
			InstID:    raw.InstID,
			Last:      last,
			VolCcy24h: volCcy24h,
		})
	}
	return tickers, nil
}

func (c *Client) Candles(ctx context.Context, instID, bar string, limit int) ([]alligator.Candle, error) {
	values := url.Values{}
	values.Set("instId", instID)
	values.Set("bar", bar)
	values.Set("limit", strconv.Itoa(limit))

	var out response[[][]string]
	if err := c.get(ctx, "/api/v5/market/candles", values, &out); err != nil {
		return nil, err
	}
	if out.Code != "0" {
		return nil, fmt.Errorf("okx candles code=%s msg=%s", out.Code, out.Msg)
	}

	candles := make([]alligator.Candle, 0, len(out.Data))
	for i := len(out.Data) - 1; i >= 0; i-- {
		row := out.Data[i]
		if len(row) < 5 {
			continue
		}
		ts, err := strconv.ParseInt(row[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse candle timestamp for %s: %w", instID, err)
		}
		closePrice, err := strconv.ParseFloat(row[4], 64)
		if err != nil {
			return nil, fmt.Errorf("parse close for %s: %w", instID, err)
		}
		candles = append(candles, alligator.Candle{
			Time:  time.UnixMilli(ts).UTC(),
			Close: closePrice,
		})
	}
	return candles, nil
}

func (c *Client) get(ctx context.Context, path string, values url.Values, target any) error {
	endpoint := c.baseURL + path + "?" + values.Encode()

	var lastErr error
	for attempt := 1; attempt <= 6; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "qww-okx-alligator/1.0")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if !sleepBeforeRetry(ctx, attempt, "") {
				return lastErr
			}
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			defer resp.Body.Close()
			return json.NewDecoder(resp.Body).Decode(target)
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		lastErr = fmt.Errorf("okx http status %d", resp.StatusCode)
		if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return lastErr
		}
		if !sleepBeforeRetry(ctx, attempt, resp.Header.Get("Retry-After")) {
			return lastErr
		}
	}
	return lastErr
}

func sleepBeforeRetry(ctx context.Context, attempt int, retryAfter string) bool {
	delay := retryDelay(attempt, retryAfter)
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func retryDelay(attempt int, retryAfter string) time.Duration {
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	delay := time.Duration(attempt*attempt) * time.Second
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}
