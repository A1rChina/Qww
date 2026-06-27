# OKX 鳄鱼线关注器

这是一个用 Go 编写的 OKX 标的关注器，适合放到 GitHub Actions 定时运行。它会动态获取 OKX 当前可交易标的，默认分析全部 USDT 永续合约，并分别按 `1H`、`4H`、`1D` 三个时间框架拉取 K 线，计算 Bill Williams 鳄鱼线，生成 Markdown 与 JSON 报告。

## 鳄鱼线规则

程序使用收盘价计算 SMMA：

- Jaw：13 周期，向前移 8 根
- Teeth：8 周期，向前移 5 根
- Lips：5 周期，向前移 3 根

报告会给出：

- `bullish`：Lips > Teeth > Jaw，且收盘价在 Lips 上方
- `bearish`：Lips < Teeth < Jaw，且收盘价在 Lips 下方
- `sleeping`：三条线纠缠，价差低于阈值
- `mixed`：其他状态

## 本地运行

```powershell
go run ./cmd/okx-alligator
```

常用环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `OKX_INST_TYPE` | `SWAP` | OKX 标的类型，如 `SPOT`、`SWAP` |
| `OKX_QUOTE_CCY` | `USDT` | 报价币过滤 |
| `OKX_INST_IDS` | 空 | 指定标的，逗号分隔；为空则动态获取 |
| `OKX_BARS` | `1H,4H,1D` | K 线周期，逗号分隔 |
| `OKX_CANDLE_LIMIT` | `200` | 每个标的、每个周期拉取 K 线数量 |
| `OKX_MAX_INSTRUMENTS` | `0` | 动态获取时最多分析多少个标的；`0` 表示不限制 |
| `OKX_CONCURRENCY` | `2` | 并发拉取 K 线的 worker 数；全量分析时建议保持较低，避免 OKX 限流 |
| `OKX_MIN_NOTIONAL_24H` | `500000` | 24h 成交金额过滤阈值，低于该值的标的不进入名单 |
| `ALLIGATOR_SLEEP_THRESHOLD` | `0.0015` | 三线纠缠阈值，按收盘价比例计算 |
| `OUTPUT_DIR` | `reports` | 报告输出目录 |

示例：

```powershell
$env:OKX_INST_IDS="BTC-USDT-SWAP,ETH-USDT-SWAP,SOL-USDT-SWAP"
$env:OKX_BARS="1H,4H,1D"
go run ./cmd/okx-alligator
```

全量 USDT 永续：

```powershell
$env:OKX_INST_TYPE="SWAP"
$env:OKX_QUOTE_CCY="USDT"
$env:OKX_MAX_INSTRUMENTS="0"
$env:OKX_CONCURRENCY="2"
$env:OKX_MIN_NOTIONAL_24H="500000"
go run ./cmd/okx-alligator
```

## GitHub Actions

仓库已包含 `.github/workflows/okx-alligator.yml`，默认每小时运行一次，也支持手动触发。手动触发时可以通过 `bars` 输入覆盖时间框架，例如：

```text
1H,4H,1D
```

Actions 输出：

- GitHub Step Summary：直接在运行页面查看三周期关注结果
- Artifact：`okx-alligator-report`，包含 `alligator-report.md` 和 `alligator-report.json`

## 免责声明

本工具只做行情计算和关注提醒，不构成投资建议。实盘前请结合自己的风险控制、交易成本、滑点和资金管理规则。
