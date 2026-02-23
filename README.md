# Smart Order Splitter API

A stateless Go API for robo-advisory order splitting. Given an investment or redemption amount and a model portfolio, it allocates the order across financial products so that the resulting portfolio composition is as close as possible to the model weights.

---

## Running the server

```bash
go run main.go
# Listening on :8080
```

---

## Endpoint

| Method | Path |
|--------|------|
| `POST` | `/split` |

Content-Type: `application/json`

---

## Input

### Top-level fields

| Field | Type | Validation | Description |
|-------|------|------------|-------------|
| `amountDecimalPrecision` | string (integer) | ≥ 0 | Number of decimal places for all monetary amounts |
| `unitDecimalPrecision` | string (integer) | ≥ 0 | Number of decimal places for all unit quantities |
| `volatilityBuffer` | string (decimal) | Optional; ≥ 0 and < 1 | When present, used to classify the redemption transaction type (see [Redemption transaction type](#redemption-transaction-type)) |
| `goals` | array | Non-empty | One or more goals to process (each processed independently) |

### Goal object

| Field | Type | Validation | Description |
|-------|------|------------|-------------|
| `goalId` | string | Non-empty | Unique identifier for the goal |
| `orderType` | string | `"Investment"` or `"Redemption"` | Type of order |
| `orderAmount` | string (decimal) | > 0, ≤ `amountDecimalPrecision` d.p.; for Redemption: ≤ total goal value | Gross amount to invest or redeem |
| `modelPortfolioId` | string | Non-empty | Identifier of the attached model portfolio |
| `goalDetails` | array of holdings | Optional for Investment; **required and non-empty for Redemption** | Current holdings in the goal |
| `modelPortfolioDetails` | array of model items | Non-empty | Target model portfolio |

### Holding object (`goalDetails` items)

| Field | Type | Validation | Description |
|-------|------|------------|-------------|
| `ticker` | string | Non-empty | Product identifier |
| `units` | string (decimal) | ≥ 0, ≤ `unitDecimalPrecision` d.p. | Current units held |
| `marketPrice` | string (decimal) | > 0 | Current market price per unit |
| `value` | string (decimal) | ≥ 0, ≤ `amountDecimalPrecision` d.p. | Current market value |
| `minInitialInvestmentAmt` | string (decimal) | ≥ 0, ≤ `amountDecimalPrecision` d.p. | Minimum first-time purchase amount |
| `minInitialInvestmentUnits` | string (decimal) | ≥ 0, ≤ `unitDecimalPrecision` d.p. | Minimum first-time purchase units |
| `minTopupAmt` | string (decimal) | ≥ 0, ≤ `amountDecimalPrecision` d.p. | Minimum subsequent purchase amount |
| `minTopupUnits` | string (decimal) | ≥ 0, ≤ `unitDecimalPrecision` d.p. | Minimum subsequent purchase units |
| `minRedemptionAmt` | string (decimal) | ≥ 0, ≤ `amountDecimalPrecision` d.p. | Minimum redemption amount |
| `minRedemptionUnits` | string (decimal) | ≥ 0, ≤ `unitDecimalPrecision` d.p. | Minimum redemption units |
| `minHoldingAmt` | string (decimal) | ≥ 0, ≤ `amountDecimalPrecision` d.p. | Minimum remaining value after partial redemption |
| `minHoldingUnits` | string (decimal) | ≥ 0, ≤ `unitDecimalPrecision` d.p. | Minimum remaining units after partial redemption |
| `transactionFee` | string (decimal) | ≥ 0 and < 1 | Fee rate applied by the broker on this product |

### Model item object (`modelPortfolioDetails` items)

Same fields as a holding **except** `units` and `value` are replaced by:

| Field | Type | Validation | Description |
|-------|------|------------|-------------|
| `weight` | string (decimal) | ≥ 0 and ≤ 1 | Target portfolio weight for this product |

All other fields (`ticker`, `marketPrice`, min requirements × 8, `transactionFee`) follow the same rules as the holding object.

---

## Output

### Success — HTTP 200

Returns an array of goal results (one per goal in the request).

```json
[
  {
    "goalId": "string",
    "transactionType": "Investment" | "Partial Redemption" | "Full Redemption" | "Small Redemption" | "Big Redemption",
    "transactionDetails": [
      {
        "ticker": "string",
        "direction": "BUY" | "SELL",
        "value": "string",
        "units": "string",
        "error": {
          "message": "string",
          "code": "string"
        }
      }
    ]
  }
]
```

- `value` — gross order amount for this product (what the broker receives), formatted to `amountDecimalPrecision` decimal places.
- `units` — `value / marketPrice`, truncated down to `unitDecimalPrecision` decimal places. Represents the approximate units traded before the broker deducts its fee.
- `error` — present only when a minimum requirement is violated (see [Minimum violations](#minimum-violations)). The allocation is **preserved** even when an error is present (flag-and-keep).

### Error — HTTP 400

```json
{
  "message": "human-readable description",
  "error": "Bad Request",
  "statusCode": 400
}
```

---

## Splitting logic

All amounts are processed in exact decimal arithmetic. The `orderAmount` is always the **gross** amount: for investments it is what the client sends; for redemptions it is what gets sold from the portfolio.

The `transactionFee` (a rate in [0, 1)) is applied per product:
- **Investment**: the fee reduces the net amount that actually enters the portfolio. The gross allocation is inflated by `1 / (1 − fee)` so that the net investment hits the shortfall target (e.g. shortfall $10, fee 1% → gross = $10 / 0.99 ≈ $10.10).
- **Redemption**: the fee reduces the proceeds from the sale but does not affect the splitting logic or minimum-requirement checks.

### Investment

**Objective:** allocate `orderAmount` across model-portfolio products so that the post-investment portfolio is as close as possible to the model weights.

**Algorithm:**

1. Compute the post-investment portfolio total:
   `postTotal = V_total + orderAmount`
   where `V_total` is the sum of all current holding values (including any holdings absent from the model portfolio).

2. For each product in `modelPortfolioDetails` with `weight > 0`, compute the **shortfall** — how much needs to be invested to bring it up to its model target:
   ```
   ideal_i = max(0,  w_i × postTotal  −  V_i)
   ```
   Products already at or above their model weight receive 0.

3. Apply the transaction fee to convert each net shortfall into the required gross allocation:
   ```
   feeAdjusted_i = ideal_i / (1 − transactionFee_i)
   ```
   A product with no fee (or fee = 0) is unchanged. This ensures the net amount invested after the broker deducts its fee equals the shortfall target.

4. Scale the fee-adjusted ideals so that gross allocations sum to `orderAmount`:
   ```
   gross_i = (feeAdjusted_i / Σ feeAdjusted_j) × orderAmount
   ```
   *Fallback:* if all ideals are 0 (every product already at or above model weight), distribute pro-rata by model weight (fee adjustment still applied).

5. Truncate `gross_i` to `amountDecimalPrecision` decimal places (round down).

6. Compute `units_i = gross_i / marketPrice_i`, truncated down to `unitDecimalPrecision` decimal places. Represents the approximate units traded before the broker deducts its fee.

7. Check minimum requirements and flag violations (see below).

8. Output preserves the order of `modelPortfolioDetails`. Products with `weight = 0` (e.g. CASH) are excluded from the output.

> **Note:** step 4 is a placeholder for a future call to the `generalsplitter` external API, which will eliminate rounding residuals entirely.

### Redemption

**Objective:** redeem `orderAmount` from portfolio holdings so that the post-redemption portfolio is as close as possible to the model weights.

**Phase 1 — Zero-weight / absent products (highest priority)**

Products held in `goalDetails` that are either absent from `modelPortfolioDetails` or have `weight = 0` are fully redeemed first, as they should not be in the portfolio at all.

- These products are sorted by current value **ascending** to maximise the number of positions fully closed within the budget.
- The API greedily fully redeems them one by one. If the budget runs out mid-list, the last product is partially redeemed for the remaining amount.
- Any unspent budget after Phase 1 is carried into Phase 2.

**Phase 2 — Model-portfolio products**

For each product in `modelPortfolioDetails` with `weight > 0`, compute the **overweight** — how much must be sold to bring it down to its model target after the full redemption:

```
ideal_i = max(0,  V_i  −  w_i × (V_total − orderAmount))
```

Products at or below their model weight receive 0. The sum of all positive ideals equals the Phase 2 budget exactly (a consequence of model weights summing to 1), so no additional scaling edge cases arise.

```
redemption_i = (ideal_i / Σ ideal_j) × remaining_budget
```

Truncation and unit calculation follow the same rules as investment.

Output order: Phase 1 products appear first (ascending value), followed by `modelPortfolioDetails` products in their input order.

---

## Redemption transaction type

The `transactionType` field in the response is determined by comparing `orderAmount` against the total goal value (`V_total = Σ goalDetails[i].value`) and the optional `volatilityBuffer`.

### Without `volatilityBuffer`

| Condition | `transactionType` |
|-----------|-------------------|
| `orderAmount < V_total` | `"Partial Redemption"` |
| `orderAmount = V_total` | `"Full Redemption"` |

### With `volatilityBuffer`

Let `threshold = V_total × (1 − volatilityBuffer)`.

| Condition | `transactionType` |
|-----------|-------------------|
| `orderAmount < threshold` | `"Small Redemption"` |
| `threshold ≤ orderAmount < V_total` | `"Big Redemption"` |
| `orderAmount = V_total` | `"Full Redemption"` |

**Example:** goal value = $10.00, `volatilityBuffer` = `"0.03"` → `threshold` = $9.70.
- `orderAmount` < $9.70 → `"Small Redemption"`
- $9.70 ≤ `orderAmount` < $10.00 → `"Big Redemption"`
- `orderAmount` = $10.00 → `"Full Redemption"`

> **Note:** `orderAmount` strictly greater than `V_total` is rejected with HTTP 400.

---

## Minimum violations

Violations are **flagged but not suppressed** — the calculated allocation is always included in the response alongside the error. This preserves full traceability of what the algorithm attempted.

| Code | Trigger | Applies to |
|------|---------|------------|
| `MIN_INVESTMENT_VIOLATION` | `gross_i < minInitialInvestmentAmt` or `units_i < minInitialInvestmentUnits` (first-time purchase, i.e. product not currently held) | Investment |
| `MIN_TOPUP_VIOLATION` | `gross_i < minTopupAmt` or `units_i < minTopupUnits` (product already held) | Investment |
| `MIN_REDEMPTION_VIOLATION` | `redemption_i < minRedemptionAmt` or `units_i < minRedemptionUnits` | Redemption |
| `MIN_HOLDING_VIOLATION` | Remaining value or units after a **partial** redemption fall below `minHoldingAmt` / `minHoldingUnits`. Full redemptions (remaining = 0) are always permitted. | Redemption |
