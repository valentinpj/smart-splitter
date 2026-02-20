package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"
	"github.com/valentinpj/smart-splitter/models"
	"github.com/valentinpj/smart-splitter/splitter"
)

func HandleSplit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req models.SplitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "Invalid request body", "Bad Request", http.StatusBadRequest)
		return
	}

	// Validate all goal order amounts before processing any
	for _, goal := range req.Goals {
		amt, err := decimal.NewFromString(goal.OrderAmount)
		if err != nil || !amt.IsPositive() {
			writeError(w, "Order amount: value must be greater than 0", "Bad Request", http.StatusBadRequest)
			return
		}
	}

	amountPrec, err := strconv.Atoi(req.AmountDecimalPrecision)
	if err != nil {
		writeError(w, "Invalid amountDecimalPrecision", "Bad Request", http.StatusBadRequest)
		return
	}
	unitPrec, err := strconv.Atoi(req.UnitDecimalPrecision)
	if err != nil {
		writeError(w, "Invalid unitDecimalPrecision", "Bad Request", http.StatusBadRequest)
		return
	}

	var results []models.GoalResult
	for _, goal := range req.Goals {
		switch strings.ToLower(goal.OrderType) {
		case "investment":
			results = append(results, splitter.ProcessInvestment(goal, amountPrec, unitPrec))
		default:
			writeError(w, "Unsupported order type: "+goal.OrderType, "Bad Request", http.StatusBadRequest)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func writeError(w http.ResponseWriter, message, errStr string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(models.ErrorResponse{
		Message:    message,
		Error:      errStr,
		StatusCode: statusCode,
	})
}
