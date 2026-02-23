package splitter

import (
	"github.com/shopspring/decimal"
	"github.com/valentinpj/smart-splitter/models"
)

// ProcessInvestment splits an investment order across model portfolio products,
// prioritising products that are furthest below their model weight (shortfall-based allocation).
// The output preserves the order of modelPortfolioDetails from the input.
func ProcessInvestment(goal models.Goal, amountPrec, unitPrec int) models.GoalResult {
	orderAmount, _ := decimal.NewFromString(goal.OrderAmount)

	// Build current-holdings map: ticker -> current value in portfolio
	holdingsMap := make(map[string]decimal.Decimal)
	vTotal := decimal.Zero
	for _, h := range goal.GoalDetails {
		val, _ := decimal.NewFromString(h.Value)
		holdingsMap[h.Ticker] = val
		vTotal = vTotal.Add(val)
	}

	postTotal := vTotal.Add(orderAmount)

	// Compute ideal (shortfall-based) allocation for each model product with weight > 0.
	// ideal_i = max(0, w_i * postTotal - currentValue_i)
	type productAlloc struct {
		mp      models.ModelItem
		current decimal.Decimal
		ideal   decimal.Decimal
	}

	var allocs []productAlloc
	totalIdeal := decimal.Zero
	totalWeight := decimal.Zero

	for _, mp := range goal.ModelPortfolioDetails {
		weight, _ := decimal.NewFromString(mp.Weight)
		if weight.IsZero() {
			continue
		}
		totalWeight = totalWeight.Add(weight)
		currentVal := holdingsMap[mp.Ticker]
		ideal := weight.Mul(postTotal).Sub(currentVal)
		if ideal.LessThan(decimal.Zero) {
			ideal = decimal.Zero
		}
		allocs = append(allocs, productAlloc{mp: mp, current: currentVal, ideal: ideal})
		totalIdeal = totalIdeal.Add(ideal)
	}

	// Fallback: if every product is already at or above its model weight (totalIdeal == 0),
	// distribute pro-rata by model weight.
	if totalIdeal.IsZero() {
		for i, a := range allocs {
			w, _ := decimal.NewFromString(a.mp.Weight)
			allocs[i].ideal = w.Div(totalWeight).Mul(orderAmount)
		}
		totalIdeal = orderAmount
	}

	// Apply transaction fee adjustment: to achieve a net investment equal to ideal_i,
	// the gross amount must be ideal_i / (1 - fee_i).
	// We then scale so that all gross amounts sum to orderAmount.
	one := decimal.NewFromInt(1)
	feeAdjusted := make([]decimal.Decimal, len(allocs))
	totalFeeAdjusted := decimal.Zero
	for i, a := range allocs {
		fee, _ := decimal.NewFromString(a.mp.TransactionFee)
		divisor := one.Sub(fee) // 1 - fee; fee is validated < 1, so divisor > 0
		feeAdjusted[i] = a.ideal.Div(divisor)
		totalFeeAdjusted = totalFeeAdjusted.Add(feeAdjusted[i])
	}

	// Scale each allocation so that gross amounts sum to orderAmount, then check minimums.
	var details []models.TransactionDetail
	for i, a := range allocs {
		// gross_i = (feeAdjusted_i / totalFeeAdjusted) * orderAmount, truncated to amountDecimalPrecision
		gross := feeAdjusted[i].Div(totalFeeAdjusted).Mul(orderAmount).Truncate(int32(amountPrec))

		// units = gross / marketPrice, truncated to unitDecimalPrecision
		price, _ := decimal.NewFromString(a.mp.MarketPrice)
		var units decimal.Decimal
		if price.IsPositive() {
			units = gross.Div(price).Truncate(int32(unitPrec))
		}

		// Check minimum requirements (flag-and-keep: violations are reported but allocation is preserved)
		var tradeErr *models.TradeError
		if gross.IsPositive() {
			if a.current.IsZero() {
				// First-time purchase: apply initial investment minimums
				minAmt, _ := decimal.NewFromString(a.mp.MinInitialInvestmentAmt)
				minUnits, _ := decimal.NewFromString(a.mp.MinInitialInvestmentUnits)
				if gross.LessThan(minAmt) || units.LessThan(minUnits) {
					tradeErr = &models.TradeError{
						Message: "Cannot trade this ticker because it breaches the minimum initial investment amount",
						Code:    "MIN_INVESTMENT_VIOLATION",
					}
				}
			} else {
				// Subsequent purchase: apply top-up minimums
				minAmt, _ := decimal.NewFromString(a.mp.MinTopupAmt)
				minUnits, _ := decimal.NewFromString(a.mp.MinTopupUnits)
				if gross.LessThan(minAmt) || units.LessThan(minUnits) {
					tradeErr = &models.TradeError{
						Message: "Cannot trade this ticker because it breaches the minimum topup amount",
						Code:    "MIN_TOPUP_VIOLATION",
					}
				}
			}
		}

		details = append(details, models.TransactionDetail{
			Ticker:    a.mp.Ticker,
			Direction: "BUY",
			Value:     gross.StringFixed(int32(amountPrec)),
			Units:     units.StringFixed(int32(unitPrec)),
			Error:     tradeErr,
		})
	}

	return models.GoalResult{
		GoalID:             goal.GoalID,
		TransactionType:    goal.OrderType,
		TransactionDetails: details,
	}
}
