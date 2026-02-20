package models

// --- Request types ---

type SplitRequest struct {
	AmountDecimalPrecision string `json:"amountDecimalPrecision"`
	UnitDecimalPrecision   string `json:"unitDecimalPrecision"`
	VolatilityBuffer       string `json:"volatilityBuffer"`
	Goals                  []Goal `json:"goals"`
}

type Goal struct {
	GoalID                string      `json:"goalId"`
	GoalDetails           []Holding   `json:"goalDetails,omitempty"`
	OrderAmount           string      `json:"orderAmount"`
	OrderType             string      `json:"orderType"`
	ModelPortfolioID      string      `json:"modelPortfolioId"`
	ModelPortfolioDetails []ModelItem `json:"modelPortfolioDetails"`
}

type Holding struct {
	Ticker                    string `json:"ticker"`
	Units                     string `json:"units"`
	MarketPrice               string `json:"marketPrice"`
	Value                     string `json:"value"`
	MinInitialInvestmentAmt   string `json:"minInitialInvestmentAmt"`
	MinInitialInvestmentUnits string `json:"minInitialInvestmentUnits"`
	MinTopupAmt               string `json:"minTopupAmt"`
	MinTopupUnits             string `json:"minTopupUnits"`
	MinRedemptionAmt          string `json:"minRedemptionAmt"`
	MinRedemptionUnits        string `json:"minRedemptionUnits"`
	MinHoldingAmt             string `json:"minHoldingAmt"`
	MinHoldingUnits           string `json:"minHoldingUnits"`
	TransactionFee            string `json:"transactionFee"`
}

type ModelItem struct {
	Ticker                    string `json:"ticker"`
	Weight                    string `json:"weight"`
	MarketPrice               string `json:"marketPrice"`
	MinInitialInvestmentAmt   string `json:"minInitialInvestmentAmt"`
	MinInitialInvestmentUnits string `json:"minInitialInvestmentUnits"`
	MinTopupAmt               string `json:"minTopupAmt"`
	MinTopupUnits             string `json:"minTopupUnits"`
	MinRedemptionAmt          string `json:"minRedemptionAmt"`
	MinRedemptionUnits        string `json:"minRedemptionUnits"`
	MinHoldingAmt             string `json:"minHoldingAmt"`
	MinHoldingUnits           string `json:"minHoldingUnits"`
	TransactionFee            string `json:"transactionFee"`
}

// --- Response types ---

type GoalResult struct {
	GoalID             string              `json:"goalId"`
	TransactionType    string              `json:"transactionType"`
	TransactionDetails []TransactionDetail `json:"transactionDetails"`
}

type TransactionDetail struct {
	Ticker    string      `json:"ticker"`
	Direction string      `json:"direction"`
	Value     string      `json:"value"`
	Units     string      `json:"units"`
	Error     *TradeError `json:"error,omitempty"`
}

type TradeError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}

type ErrorResponse struct {
	Message    string `json:"message"`
	Error      string `json:"error"`
	StatusCode int    `json:"statusCode"`
}
