package splitter

import (
	"sort"

	"github.com/shopspring/decimal"
	"github.com/valentinpj/smart-splitter/models"
)

// ProcessRedemption splits a redemption order across portfolio holdings so that the
// resulting composition is as close to model weights as possible.
//
// Two-phase approach:
//   Phase 1 — Zero-weight / absent products are fully redeemed first (highest priority),
//             sorted ascending by value to maximise the count of full redemptions within budget.
//   Phase 2 — Remaining budget is distributed across model-portfolio products proportionally
//             to how overweight each one is relative to its post-redemption model target.
func ProcessRedemption(goal models.Goal, amountPrec, unitPrec int) models.GoalResult {
	orderAmount, _ := decimal.NewFromString(goal.OrderAmount)

	// Build holdings map: ticker -> Holding (only products with positive value)
	holdingsMap := make(map[string]models.Holding)
	vTotal := decimal.Zero
	for _, h := range goal.GoalDetails {
		val, _ := decimal.NewFromString(h.Value)
		if val.IsPositive() {
			holdingsMap[h.Ticker] = h
			vTotal = vTotal.Add(val)
		}
	}

	// Build model map: ticker -> ModelItem
	modelMap := make(map[string]models.ModelItem)
	for _, mp := range goal.ModelPortfolioDetails {
		modelMap[mp.Ticker] = mp
	}

	// -------------------------------------------------------------------------
	// Phase 1: Zero-weight / absent products
	// -------------------------------------------------------------------------
	type zwProduct struct {
		holding models.Holding
		value   decimal.Decimal
	}
	var zwProducts []zwProduct
	for _, h := range goal.GoalDetails { // iterate GoalDetails to preserve deterministic order
		val, _ := decimal.NewFromString(h.Value)
		if !val.IsPositive() {
			continue
		}
		mp, inModel := modelMap[h.Ticker]
		w := decimal.Zero
		if inModel {
			w, _ = decimal.NewFromString(mp.Weight)
		}
		if w.IsZero() {
			zwProducts = append(zwProducts, zwProduct{h, val})
		}
	}
	// Sort ascending by value so we maximise the number of fully-redeemed positions.
	sort.Slice(zwProducts, func(i, j int) bool {
		return zwProducts[i].value.LessThan(zwProducts[j].value)
	})

	remaining := orderAmount
	var details []models.TransactionDetail

	for _, zp := range zwProducts {
		if remaining.IsZero() {
			break
		}
		isFullRedemption := !zp.value.GreaterThan(remaining)
		redeemAmt := zp.value
		if !isFullRedemption {
			redeemAmt = remaining
		}
		redeemAmt = redeemAmt.Truncate(int32(amountPrec))

		price, _ := decimal.NewFromString(zp.holding.MarketPrice)
		var units decimal.Decimal
		if price.IsPositive() {
			units = redeemAmt.Div(price).Truncate(int32(unitPrec))
		}

		tradeErr := checkRedemptionMinimums(
			redeemAmt, units,
			isFullRedemption,
			zp.holding.Value, zp.holding.Units,
			zp.holding.MinRedemptionAmt, zp.holding.MinRedemptionUnits,
			zp.holding.MinHoldingAmt, zp.holding.MinHoldingUnits,
			amountPrec, unitPrec,
		)

		details = append(details, models.TransactionDetail{
			Ticker:    zp.holding.Ticker,
			Direction: "SELL",
			Value:     redeemAmt.StringFixed(int32(amountPrec)),
			Units:     units.StringFixed(int32(unitPrec)),
			Error:     tradeErr,
		})
		remaining = remaining.Sub(redeemAmt)
	}

	// -------------------------------------------------------------------------
	// Phase 2: Shortfall-based proportional redemption for model-portfolio products
	//
	// ideal_i = max(0, V_i - w_i * (V_total - orderAmount))
	// This naturally sums to exactly `remaining` (proved in design doc), so we
	// can always scale to match the budget without a fallback.
	// -------------------------------------------------------------------------
	postTotal := vTotal.Sub(orderAmount)

	type productAlloc struct {
		mp      models.ModelItem
		holding *models.Holding // nil if product not currently held
		ideal   decimal.Decimal
	}

	var allocs []productAlloc
	totalIdeal := decimal.Zero

	for _, mp := range goal.ModelPortfolioDetails {
		w, _ := decimal.NewFromString(mp.Weight)
		if w.IsZero() {
			continue // already handled in Phase 1
		}
		currentVal := decimal.Zero
		var hp *models.Holding
		if h, held := holdingsMap[mp.Ticker]; held {
			currentVal, _ = decimal.NewFromString(h.Value)
			hCopy := h
			hp = &hCopy
		}
		ideal := currentVal.Sub(w.Mul(postTotal))
		if ideal.LessThan(decimal.Zero) {
			ideal = decimal.Zero
		}
		allocs = append(allocs, productAlloc{mp: mp, holding: hp, ideal: ideal})
		totalIdeal = totalIdeal.Add(ideal)
	}

	for _, a := range allocs {
		redeemAmt := decimal.Zero
		if !totalIdeal.IsZero() && remaining.IsPositive() {
			redeemAmt = a.ideal.Div(totalIdeal).Mul(remaining).Truncate(int32(amountPrec))
		}

		price, _ := decimal.NewFromString(a.mp.MarketPrice)
		var units decimal.Decimal
		if price.IsPositive() && redeemAmt.IsPositive() {
			units = redeemAmt.Div(price).Truncate(int32(unitPrec))
		}

		var tradeErr *models.TradeError
		if redeemAmt.IsPositive() && a.holding != nil {
			currentVal, _ := decimal.NewFromString(a.holding.Value)
			isFullRedemption := redeemAmt.GreaterThanOrEqual(currentVal)
			tradeErr = checkRedemptionMinimums(
				redeemAmt, units,
				isFullRedemption,
				a.holding.Value, a.holding.Units,
				a.mp.MinRedemptionAmt, a.mp.MinRedemptionUnits,
				a.mp.MinHoldingAmt, a.mp.MinHoldingUnits,
				amountPrec, unitPrec,
			)
		}

		details = append(details, models.TransactionDetail{
			Ticker:    a.mp.Ticker,
			Direction: "SELL",
			Value:     redeemAmt.StringFixed(int32(amountPrec)),
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

// checkRedemptionMinimums validates both the minimum redemption size and the
// minimum remaining holding after a partial redemption.
// A full redemption (isFullRedemption=true) bypasses the min-holding check.
func checkRedemptionMinimums(
	redeemAmt, units decimal.Decimal,
	isFullRedemption bool,
	currentValStr, currentUnitsStr string,
	minRedAmtStr, minRedUnitsStr string,
	minHoldAmtStr, minHoldUnitsStr string,
	amountPrec, unitPrec int,
) *models.TradeError {
	// 1. Minimum redemption amount / units
	minRedAmt, _ := decimal.NewFromString(minRedAmtStr)
	minRedUnits, _ := decimal.NewFromString(minRedUnitsStr)
	if redeemAmt.LessThan(minRedAmt) || units.LessThan(minRedUnits) {
		return &models.TradeError{
			Message: "Cannot trade this ticker because it breaches the minimum redemption amount",
			Code:    "MIN_REDEMPTION_VIOLATION",
		}
	}

	// 2. Minimum holding after partial redemption (full redemption always allowed)
	if !isFullRedemption {
		currentVal, _ := decimal.NewFromString(currentValStr)
		currentUnits, _ := decimal.NewFromString(currentUnitsStr)
		remainingAmt := currentVal.Sub(redeemAmt)
		remainingUnits := currentUnits.Sub(units)
		minHoldAmt, _ := decimal.NewFromString(minHoldAmtStr)
		minHoldUnits, _ := decimal.NewFromString(minHoldUnitsStr)
		if remainingAmt.LessThan(minHoldAmt) || remainingUnits.LessThan(minHoldUnits) {
			return &models.TradeError{
				Message: "Cannot trade this ticker because the remaining holding would breach the minimum holding amount",
				Code:    "MIN_HOLDING_VIOLATION",
			}
		}
	}
	return nil
}
