package handlers

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"
)

// IVMS101Originator represents the IVMS 101 Originator payload.
type IVMS101Originator struct {
	LegalName           string `json:"legal_name"`
	NationalID          string `json:"national_id"`
	DateOfBirth         string `json:"date_of_birth,omitempty"`
	ResidentialAddress  string `json:"residential_address,omitempty"`
	OriginatorAccountID string `json:"originator_account_id"`
}

// IVMS101Beneficiary represents the IVMS 101 Beneficiary payload.
type IVMS101Beneficiary struct {
	LegalName            string `json:"legal_name"`
	BeneficiaryAccountID string `json:"beneficiary_account_id"`
}

// TravelRulePayload represents the FATF Recommendation 16 / SI 99 of 2026 data envelope.
type TravelRulePayload struct {
	TransactionID       string             `json:"transaction_id"`
	OriginatingVASP     string             `json:"originating_vasp"`
	BeneficiaryVASP     string             `json:"beneficiary_vasp"`
	AssetDenomination   string             `json:"asset_denomination"`
	Amount              float64            `json:"amount"`
	Originator          IVMS101Originator  `json:"originator"`
	Beneficiary         IVMS101Beneficiary `json:"beneficiary"`
	Timestamp           int64              `json:"timestamp"`
	ComplianceSignature string             `json:"compliance_signature"`
}

// TravelRuleValidationResponse represents response for Travel Rule compliance verification.
type TravelRuleValidationResponse struct {
	Valid          bool     `json:"valid"`
	RequiresReport bool     `json:"requires_report"` // true if >= 1000 USD threshold
	ThresholdUSD   float64  `json:"threshold_usd"`
	Errors         []string `json:"errors,omitempty"`
}

// HandleTravelRuleValidation processes and validates Travel Rule data headers for inter-VASP transfers.
func HandleTravelRuleValidation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload TravelRulePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}

	var validationErrors []string
	if strings.TrimSpace(payload.TransactionID) == "" {
		validationErrors = append(validationErrors, "transaction_id is required")
	}
	if strings.TrimSpace(payload.Originator.LegalName) == "" {
		validationErrors = append(validationErrors, "originator legal_name is required")
	}
	if strings.TrimSpace(payload.Originator.NationalID) == "" {
		validationErrors = append(validationErrors, "originator national_id is required")
	}
	if strings.TrimSpace(payload.Beneficiary.LegalName) == "" {
		validationErrors = append(validationErrors, "beneficiary legal_name is required")
	}
	if payload.Amount <= 0 {
		validationErrors = append(validationErrors, "amount must be positive")
	}

	requiresReport := payload.Amount >= 1000.0 // FATF 1,000 USD inter-VASP threshold

	res := TravelRuleValidationResponse{
		Valid:          len(validationErrors) == 0,
		RequiresReport: requiresReport,
		ThresholdUSD:   1000.0,
		Errors:         validationErrors,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// SanctionScreeningRequest represents a PEP / Sanction screening request.
type SanctionScreeningRequest struct {
	FullName   string `json:"full_name"`
	NationalID string `json:"national_id"`
	Country    string `json:"country"`
}

// SanctionScreeningResponse returns the screening verdict.
type SanctionScreeningResponse struct {
	ScreenedAt      int64    `json:"screened_at"`
	Matched         bool     `json:"matched"`
	MatchConfidence float64  `json:"match_confidence"`
	ListSource      string   `json:"list_source,omitempty"`
	RiskScore       string   `json:"risk_score"` // "LOW", "MEDIUM", "HIGH", "SANCTIONED"
	ActionsRequired []string `json:"actions_required,omitempty"`
}

// Mock known sanction patterns for Zimbabwe FIU / UN List simulation
var highRiskSanctions = []string{
	"SANCTIONED_TARGET_01",
	"ILLICIT_SYNDICATE_ZW",
	"PROHIBITED_ENTITY_LTD",
}

// HandleSanctionsScreening checks user credentials against regulatory watchlists.
func HandleSanctionsScreening(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SanctionScreeningRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request: "+err.Error(), http.StatusBadRequest)
		return
	}

	nameUpper := strings.ToUpper(strings.TrimSpace(req.FullName))
	idUpper := strings.ToUpper(strings.TrimSpace(req.NationalID))

	matched := false
	source := ""
	for _, s := range highRiskSanctions {
		if strings.Contains(nameUpper, s) || strings.Contains(idUpper, s) {
			matched = true
			source = "FIU_DESIGNATED_SANCTIONS_LIST_2026"
			break
		}
	}

	res := SanctionScreeningResponse{
		ScreenedAt: time.Now().Unix(),
		Matched:    matched,
	}

	if matched {
		res.MatchConfidence = 0.98
		res.ListSource = source
		res.RiskScore = "SANCTIONED"
		res.ActionsRequired = []string{
			"IMMEDIATE_ACCOUNT_FREEZE",
			"FILE_FIU_STR_WITHIN_24_HOURS",
			"PROHIBIT_OUTBOUND_TRANSFERS",
		}
	} else {
		res.MatchConfidence = 0.0
		res.RiskScore = "LOW"
		res.ActionsRequired = []string{"STANDARD_MONITORING"}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// TaxCalculationRequest represents input for calculating ZIMRA statutory deduction.
type TaxCalculationRequest struct {
	GrossAmount             float64 `json:"gross_amount"`
	IsRegisteredVATMerchant bool    `json:"is_registered_vat_merchant"`
}

// TaxCalculationResponse represents detailed statutory tax breakdown.
type TaxCalculationResponse struct {
	GrossAmount             float64 `json:"gross_amount"`
	IMTTRatePercent         float64 `json:"imtt_rate_percent"` // 2.0%
	IMTTAmount              float64 `json:"imtt_amount"`
	VATRatePercent          float64 `json:"vat_rate_percent"`  // 15.0%
	VATAmount               float64 `json:"vat_amount"`
	TotalTaxDeducted        float64 `json:"total_tax_deducted"`
	NetMerchantSettlement   float64 `json:"net_merchant_settlement"`
	ZIMRACollectionEscrow   string  `json:"zimra_collection_escrow"`
}

// HandleCalculateTax computes real-time ZIMRA 2% IMTT and 15% VAT for POS terminals.
func HandleCalculateTax(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req TaxCalculationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if req.GrossAmount <= 0 {
		http.Error(w, "Gross amount must be positive", http.StatusBadRequest)
		return
	}

	// 2% IMTT
	imtt := math.Round(req.GrossAmount*0.02*100) / 100
	// 15% VAT if registered
	vat := 0.0
	if req.IsRegisteredVATMerchant {
		vat = math.Round(req.GrossAmount*0.15*100) / 100
	}

	totalTax := imtt + vat
	netSettled := req.GrossAmount - totalTax
	if netSettled < 0 {
		netSettled = 0
	}

	res := TaxCalculationResponse{
		GrossAmount:           req.GrossAmount,
		IMTTRatePercent:       2.0,
		IMTTAmount:            imtt,
		VATRatePercent:        15.0,
		VATAmount:             vat,
		TotalTaxDeducted:      totalTax,
		NetMerchantSettlement: netSettled,
		ZIMRACollectionEscrow: "zwc1zimra_revenue_collection_escrow_mainnet",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}
