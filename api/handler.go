package api

import (
	"encoding/json"
	"net/http"
	"strings"

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
		writeError(w, "Invalid request body: "+err.Error(), "Bad Request", http.StatusBadRequest)
		return
	}

	amountPrec, unitPrec, err := validateRequest(&req)
	if err != nil {
		writeError(w, err.Error(), "Bad Request", http.StatusBadRequest)
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
