package splitter

import (
	"sort"

	"github.com/shopspring/decimal"
	"github.com/valentinpj/smart-splitter/models"
)

type productAlloc struct {
	mp      models.ModelItem
	current decimal.Decimal
	ideal   decimal.Decimal
}

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

	// Pass 1: compute initial gross amounts (truncated down to amountDecimalPrecision).
	grossAmounts := make([]decimal.Decimal, len(allocs))
	for i := range allocs {
		grossAmounts[i] = feeAdjusted[i].Div(totalFeeAdjusted).Mul(orderAmount).Truncate(int32(amountPrec))
	}

	// Repair step: bump violating products up to their minimum requirement,
	// funded by proportionally reducing non-violating products.
	grossAmounts = repairViolations(allocs, grossAmounts, amountPrec, unitPrec)

	// Pass 2: build transaction details with updated gross amounts.
	var details []models.TransactionDetail
	for i, a := range allocs {
		gross := grossAmounts[i]

		price, _ := decimal.NewFromString(a.mp.MarketPrice)
		var units decimal.Decimal
		if price.IsPositive() {
			units = gross.Div(price).Truncate(int32(unitPrec))
		}

		// Compute net amount (after fee) for minimum requirement checks.
		// Minimums are expressed in terms of what actually enters the portfolio.
		fee, _ := decimal.NewFromString(a.mp.TransactionFee)
		net := gross.Mul(one.Sub(fee))
		var netUnits decimal.Decimal
		if price.IsPositive() {
			netUnits = net.Div(price).Truncate(int32(unitPrec))
		}

		// Check minimum requirements (flag-and-keep: violations are reported but allocation is preserved).
		var tradeErr *models.TradeError
		if gross.IsPositive() {
			if a.current.IsZero() {
				// First-time purchase: apply initial investment minimums against net amount.
				minAmt, _ := decimal.NewFromString(a.mp.MinInitialInvestmentAmt)
				minUnits, _ := decimal.NewFromString(a.mp.MinInitialInvestmentUnits)
				if net.LessThan(minAmt) || netUnits.LessThan(minUnits) {
					tradeErr = &models.TradeError{
						Message: "Cannot trade this ticker because it breaches the minimum initial investment amount",
						Code:    "MIN_INVESTMENT_VIOLATION",
					}
				}
			} else {
				// Subsequent purchase: apply top-up minimums against net amount.
				minAmt, _ := decimal.NewFromString(a.mp.MinTopupAmt)
				minUnits, _ := decimal.NewFromString(a.mp.MinTopupUnits)
				if net.LessThan(minAmt) || netUnits.LessThan(minUnits) {
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

// repairViolations attempts to clear minimum-requirement violations by bumping each
// violating product's gross allocation up to its required minimum.
//
// Two funding tiers, applied in order for each violation (cheapest bump first):
//
//  1. Safe slack: reduce non-violating products from their current gross down to their
//     own minimum floor (gross_j − reqGross_j). Never creates a new violation.
//
//  2. Zero-out: if safe slack alone is insufficient, additionally zero out non-violating
//     products entirely (smallest reqGross first), gaining their reqGross as extra slack.
//     A gross of 0 is always valid — it simply means no trade for that product.
//
// After deciding which violations to fix, non-zeroed products are reduced pro-rata by
// their safe slack to fund the bumps, keeping Σ gross == orderAmount exactly.
func repairViolations(allocs []productAlloc, grossAmounts []decimal.Decimal, amountPrec, unitPrec int) []decimal.Decimal {
	one := decimal.NewFromInt(1)

	type itemInfo struct {
		gross    decimal.Decimal
		reqGross decimal.Decimal // minimum gross to pass all checks; 0 if no minimum applies
	}

	items := make([]itemInfo, len(allocs))
	for i, a := range allocs {
		fee, _ := decimal.NewFromString(a.mp.TransactionFee)
		price, _ := decimal.NewFromString(a.mp.MarketPrice)

		var minAmt, minUnits decimal.Decimal
		if a.current.IsZero() {
			minAmt, _ = decimal.NewFromString(a.mp.MinInitialInvestmentAmt)
			minUnits, _ = decimal.NewFromString(a.mp.MinInitialInvestmentUnits)
		} else {
			minAmt, _ = decimal.NewFromString(a.mp.MinTopupAmt)
			minUnits, _ = decimal.NewFromString(a.mp.MinTopupUnits)
		}

		// requiredNet = max(minAmt, minUnits × price)
		requiredNet := minAmt
		if minUnitsCost := minUnits.Mul(price); minUnitsCost.GreaterThan(requiredNet) {
			requiredNet = minUnitsCost
		}

		// requiredGross = ⌈requiredNet / (1 − fee)⌉ at amountPrec decimal places.
		var reqGross decimal.Decimal
		if requiredNet.IsPositive() {
			if divisor := one.Sub(fee); divisor.IsPositive() {
				reqGross = ceilToPrec(requiredNet.Div(divisor), int32(amountPrec))
			}
		}

		items[i] = itemInfo{gross: grossAmounts[i], reqGross: reqGross}
	}

	// Identify violations: positive gross allocation that falls below reqGross.
	type violation struct {
		idx  int
		bump decimal.Decimal
	}
	var violations []violation
	for i, it := range items {
		if it.gross.IsZero() || it.reqGross.IsZero() {
			continue
		}
		if it.gross.LessThan(it.reqGross) {
			violations = append(violations, violation{idx: i, bump: it.reqGross.Sub(it.gross)})
		}
	}
	if len(violations) == 0 {
		return grossAmounts
	}

	// Sort violations cheapest-first to maximise the number fixed when resources are limited.
	sort.Slice(violations, func(i, j int) bool {
		return violations[i].bump.LessThan(violations[j].bump)
	})

	violatingSet := make(map[int]bool)
	for _, v := range violations {
		violatingSet[v.idx] = true
	}

	// Build slack info for non-violating products.
	type slackItem struct {
		idx       int
		safeSlack decimal.Decimal // gross − reqGross; can always be taken without creating a new violation
		reqGross  decimal.Decimal // additional slack available only if the product is zeroed entirely
	}
	var slackItems []slackItem
	totalSafeSlack := decimal.Zero
	for i, it := range items {
		if violatingSet[i] || it.gross.IsZero() {
			continue
		}
		safeSlack := it.gross.Sub(it.reqGross) // >= 0 since non-violating
		slackItems = append(slackItems, slackItem{idx: i, safeSlack: safeSlack, reqGross: it.reqGross})
		totalSafeSlack = totalSafeSlack.Add(safeSlack)
	}
	if len(slackItems) == 0 {
		return grossAmounts
	}

	// Zero-out candidates sorted by reqGross ascending: prefer zeroing products with
	// the smallest minimum so that we sacrifice as little as possible.
	zeroableSorted := make([]slackItem, len(slackItems))
	copy(zeroableSorted, slackItems)
	sort.Slice(zeroableSorted, func(i, j int) bool {
		return zeroableSorted[i].reqGross.LessThan(zeroableSorted[j].reqGross)
	})

	result := make([]decimal.Decimal, len(grossAmounts))
	copy(result, grossAmounts)

	zeroedSet := make(map[int]bool)
	remainingSlack := totalSafeSlack // tracks available pool across iterations
	totalBumpUsed := decimal.Zero

	for _, v := range violations {
		if v.bump.LessThanOrEqual(remainingSlack) {
			// Tier 1: safe slack is sufficient.
			result[v.idx] = items[v.idx].reqGross
			remainingSlack = remainingSlack.Sub(v.bump)
			totalBumpUsed = totalBumpUsed.Add(v.bump)
		} else {
			// Tier 2: try to bridge the gap by zeroing non-violating products.
			extraNeeded := v.bump.Sub(remainingSlack)
			extraGained := decimal.Zero
			var toZero []int
			for _, si := range zeroableSorted {
				if zeroedSet[si.idx] || si.reqGross.IsZero() {
					continue
				}
				toZero = append(toZero, si.idx)
				extraGained = extraGained.Add(si.reqGross)
				if extraGained.GreaterThanOrEqual(extraNeeded) {
					break
				}
			}
			if extraGained.GreaterThanOrEqual(extraNeeded) {
				result[v.idx] = items[v.idx].reqGross
				for _, idx := range toZero {
					result[idx] = decimal.Zero
					zeroedSet[idx] = true
				}
				// The zeroed products' reqGross values bridge the gap; update the pool.
				remainingSlack = remainingSlack.Add(extraGained).Sub(v.bump)
				totalBumpUsed = totalBumpUsed.Add(v.bump)
			}
			// else: insufficient resources even with zeroing — leave this violation unfixed.
		}
	}

	if totalBumpUsed.IsZero() {
		return grossAmounts
	}

	// Compute the net reduction still required from non-zeroed non-violating products.
	// (Zeroed products already contribute their full gross to balancing the sum.)
	zeroedContribution := decimal.Zero
	for idx := range zeroedSet {
		zeroedContribution = zeroedContribution.Add(items[idx].gross)
	}
	stillNeeded := totalBumpUsed.Sub(zeroedContribution)

	// Collect non-zeroed, non-violating products for pro-rata redistribution.
	var redistItems []slackItem
	redistSafeSlack := decimal.Zero
	for _, si := range slackItems {
		if zeroedSet[si.idx] {
			continue
		}
		redistItems = append(redistItems, si)
		redistSafeSlack = redistSafeSlack.Add(si.safeSlack)
	}

	unit := decimal.New(1, -int32(amountPrec))

	if stillNeeded.IsPositive() {
		// Reduce non-zeroed products pro-rata by their safe slack.
		if redistSafeSlack.IsPositive() {
			actualReduced := decimal.Zero
			reductions := make([]decimal.Decimal, len(redistItems))
			for i, si := range redistItems {
				reductions[i] = si.safeSlack.Div(redistSafeSlack).Mul(stillNeeded).Truncate(int32(amountPrec))
				actualReduced = actualReduced.Add(reductions[i])
			}
			for i, si := range redistItems {
				result[si.idx] = result[si.idx].Sub(reductions[i])
			}
			// Distribute any truncation residual one unit at a time.
			residual := stillNeeded.Sub(actualReduced)
			for _, si := range redistItems {
				if !residual.IsPositive() {
					break
				}
				if result[si.idx].Sub(items[si.idx].reqGross).GreaterThanOrEqual(unit) {
					result[si.idx] = result[si.idx].Sub(unit)
					residual = residual.Sub(unit)
				}
			}
		}
	} else if stillNeeded.IsNegative() {
		// We over-zeroed (last zeroed product's reqGross exceeded what was strictly needed).
		// Add the excess back to fixed-violation products, one unit at a time.
		excess := stillNeeded.Neg()
		var fixedIdxs []int
		for _, v := range violations {
			if result[v.idx].Equal(items[v.idx].reqGross) {
				fixedIdxs = append(fixedIdxs, v.idx)
			}
		}
		for excess.IsPositive() && len(fixedIdxs) > 0 {
			anyAdded := false
			for _, idx := range fixedIdxs {
				if !excess.IsPositive() {
					break
				}
				result[idx] = result[idx].Add(unit)
				excess = excess.Sub(unit)
				anyAdded = true
			}
			if !anyAdded {
				break
			}
		}
	}

	return result
}

// ceilToPrec rounds d up to the given number of decimal places.
func ceilToPrec(d decimal.Decimal, prec int32) decimal.Decimal {
	factor := decimal.New(1, prec) // 10^prec
	return d.Mul(factor).Ceil().Div(factor)
}
