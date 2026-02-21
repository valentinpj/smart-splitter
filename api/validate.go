package api

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/valentinpj/smart-splitter/models"
)

var (
	decZero = decimal.Zero
	decOne  = decimal.NewFromInt(1)
)

// validateRequest validates all fields in the incoming request.
// On success it returns the parsed amountDecimalPrecision and unitDecimalPrecision.
func validateRequest(req *models.SplitRequest) (amountPrec, unitPrec int, err error) {
	amountPrec, err = parseNonNegInt(req.AmountDecimalPrecision, "amountDecimalPrecision")
	if err != nil {
		return
	}
	unitPrec, err = parseNonNegInt(req.UnitDecimalPrecision, "unitDecimalPrecision")
	if err != nil {
		return
	}
	if req.VolatilityBuffer != "" {
		if err = validateRateField(req.VolatilityBuffer, "volatilityBuffer"); err != nil {
			return
		}
	}
	if len(req.Goals) == 0 {
		err = fmt.Errorf("goals must not be empty")
		return
	}
	for _, goal := range req.Goals {
		if err = validateGoal(goal, amountPrec, unitPrec); err != nil {
			return
		}
	}
	return
}

func validateGoal(g models.Goal, amtP, unitP int) error {
	if strings.TrimSpace(g.GoalID) == "" {
		return fmt.Errorf("goalId must not be empty")
	}
	if strings.TrimSpace(g.ModelPortfolioID) == "" {
		return fmt.Errorf("modelPortfolioId must not be empty")
	}
	if strings.TrimSpace(g.OrderType) == "" {
		return fmt.Errorf("orderType must not be empty")
	}
	if err := validateAmountField(g.OrderAmount, "orderAmount", true, amtP); err != nil {
		return err
	}
	if strings.ToLower(g.OrderType) == "redemption" && len(g.GoalDetails) == 0 {
		return fmt.Errorf("goalDetails must not be empty for redemption orders")
	}
	for _, h := range g.GoalDetails {
		if err := validateHolding(h, amtP, unitP); err != nil {
			return err
		}
	}
	if len(g.ModelPortfolioDetails) == 0 {
		return fmt.Errorf("modelPortfolioDetails must not be empty")
	}
	for _, mp := range g.ModelPortfolioDetails {
		if err := validateModelItem(mp, amtP, unitP); err != nil {
			return err
		}
	}
	return nil
}

func validateHolding(h models.Holding, amtP, unitP int) error {
	if strings.TrimSpace(h.Ticker) == "" {
		return fmt.Errorf("goalDetails: ticker must not be empty")
	}
	if err := validateAmountField(h.Units, "units ("+h.Ticker+")", false, unitP); err != nil {
		return err
	}
	if err := validatePriceField(h.MarketPrice, "marketPrice ("+h.Ticker+")"); err != nil {
		return err
	}
	if err := validateAmountField(h.Value, "value ("+h.Ticker+")", false, amtP); err != nil {
		return err
	}
	for _, f := range []struct{ v, name string }{
		{h.MinInitialInvestmentAmt, "minInitialInvestmentAmt (" + h.Ticker + ")"},
		{h.MinTopupAmt, "minTopupAmt (" + h.Ticker + ")"},
		{h.MinRedemptionAmt, "minRedemptionAmt (" + h.Ticker + ")"},
		{h.MinHoldingAmt, "minHoldingAmt (" + h.Ticker + ")"},
	} {
		if err := validateOptionalAmountField(f.v, f.name, amtP); err != nil {
			return err
		}
	}
	for _, f := range []struct{ v, name string }{
		{h.MinInitialInvestmentUnits, "minInitialInvestmentUnits (" + h.Ticker + ")"},
		{h.MinTopupUnits, "minTopupUnits (" + h.Ticker + ")"},
		{h.MinRedemptionUnits, "minRedemptionUnits (" + h.Ticker + ")"},
		{h.MinHoldingUnits, "minHoldingUnits (" + h.Ticker + ")"},
	} {
		if err := validateOptionalAmountField(f.v, f.name, unitP); err != nil {
			return err
		}
	}
	return validateOptionalRateField(h.TransactionFee, "transactionFee ("+h.Ticker+")")
}

func validateModelItem(mp models.ModelItem, amtP, unitP int) error {
	if strings.TrimSpace(mp.Ticker) == "" {
		return fmt.Errorf("modelPortfolioDetails: ticker must not be empty")
	}
	w, err := decimal.NewFromString(mp.Weight)
	if err != nil || w.LessThan(decZero) || w.GreaterThan(decOne) {
		return fmt.Errorf("weight (%s): must be a number between 0 and 1", mp.Ticker)
	}
	if err := validatePriceField(mp.MarketPrice, "marketPrice ("+mp.Ticker+")"); err != nil {
		return err
	}
	for _, f := range []struct{ v, name string }{
		{mp.MinInitialInvestmentAmt, "minInitialInvestmentAmt (" + mp.Ticker + ")"},
		{mp.MinTopupAmt, "minTopupAmt (" + mp.Ticker + ")"},
		{mp.MinRedemptionAmt, "minRedemptionAmt (" + mp.Ticker + ")"},
		{mp.MinHoldingAmt, "minHoldingAmt (" + mp.Ticker + ")"},
	} {
		if err := validateOptionalAmountField(f.v, f.name, amtP); err != nil {
			return err
		}
	}
	for _, f := range []struct{ v, name string }{
		{mp.MinInitialInvestmentUnits, "minInitialInvestmentUnits (" + mp.Ticker + ")"},
		{mp.MinTopupUnits, "minTopupUnits (" + mp.Ticker + ")"},
		{mp.MinRedemptionUnits, "minRedemptionUnits (" + mp.Ticker + ")"},
		{mp.MinHoldingUnits, "minHoldingUnits (" + mp.Ticker + ")"},
	} {
		if err := validateOptionalAmountField(f.v, f.name, unitP); err != nil {
			return err
		}
	}
	return validateOptionalRateField(mp.TransactionFee, "transactionFee ("+mp.Ticker+")")
}

// validateAmountField validates a decimal amount or unit quantity.
// mustBePositive=true enforces > 0 (e.g. orderAmount); otherwise >= 0 is required.
// maxPrec is the maximum allowed number of decimal places.
func validateAmountField(s, field string, mustBePositive bool, maxPrec int) error {
	s = strings.TrimSpace(s)
	d, err := decimal.NewFromString(s)
	if err != nil {
		return fmt.Errorf("%s: must be a valid decimal number", field)
	}
	if mustBePositive && !d.IsPositive() {
		return fmt.Errorf("%s: must be greater than 0", field)
	}
	if !mustBePositive && d.IsNegative() {
		return fmt.Errorf("%s: must be >= 0", field)
	}
	if places := decimalPlaces(s); places > maxPrec {
		return fmt.Errorf("%s: must have at most %d decimal place(s)", field, maxPrec)
	}
	return nil
}

// validatePriceField validates that s is a strictly positive decimal (no precision constraint).
func validatePriceField(s, field string) error {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil || !d.IsPositive() {
		return fmt.Errorf("%s: must be a number greater than 0", field)
	}
	return nil
}

// validateRateField validates that s is a decimal in [0, 1) — used for fees and volatilityBuffer.
func validateRateField(s, field string) error {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil || d.IsNegative() || d.GreaterThanOrEqual(decOne) {
		return fmt.Errorf("%s: must be a number >= 0 and < 1", field)
	}
	return nil
}

// validateOptionalAmountField validates a non-negative decimal with at most maxPrec decimal places,
// but treats an empty or absent field as valid (defaults to 0).
func validateOptionalAmountField(s, field string, maxPrec int) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return validateAmountField(s, field, false, maxPrec)
}

// validateOptionalRateField validates a decimal in [0, 1), but treats an empty or absent
// field as valid (defaults to 0).
func validateOptionalRateField(s, field string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return validateRateField(s, field)
}

// parseNonNegInt parses s as a non-negative integer.
func parseNonNegInt(s, field string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: must be a non-negative integer", field)
	}
	return n, nil
}

// decimalPlaces returns the number of digit characters after the decimal point in s.
func decimalPlaces(s string) int {
	if idx := strings.Index(s, "."); idx != -1 {
		return len(s) - idx - 1
	}
	return 0
}
