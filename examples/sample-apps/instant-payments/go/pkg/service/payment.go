package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"instantpayments/pkg/aws"
	"instantpayments/pkg/dto"
	"instantpayments/pkg/model"
	"instantpayments/pkg/util"
)

// DomainError describes an HTTP-compatible business error.
type DomainError struct {
	Status int
	Code   string
	Text   string
}

// Error returns the human-readable domain error text.
func (e *DomainError) Error() string { return e.Text }

type payment struct {
	ID, MerchantID, DebtorAccountID, CreditorIban, CreditorName, Currency, IdempotencyKey string
	Amount                                                                                float64
	State                                                                                 string
	RejectReason                                                                          string // Stores CURRENCY_MISMATCH or INSUFFICIENT_FUNDS
	CreatedAt, UpdatedAt                                                                  time.Time
	Events                                                                                []map[string]any
}

type account struct {
	Currency string
	Balance  float64
	Rows     []map[string]any
}

// Payment implements the sample payment workflow with an in-memory cache and
// optional DynamoDB persistence.
type Payment struct {
	log         *zap.Logger
	repository  *aws.Repository
	mu          sync.Mutex
	payments    map[string]*payment
	idempotency map[string]string
	accounts    map[string]*account
	// TODO: Migrate accounts and payments from in-memory maps to DynamoDB repository
	// for data persistence across restarts. Currently all state is lost on shutdown.
}

// NewPayment creates a payment service with seeded demonstration accounts.
func NewPayment(log *zap.Logger, repository *aws.Repository) *Payment {
	return &Payment{
		log:         log,
		repository:  repository,
		payments:    make(map[string]*payment),
		idempotency: make(map[string]string),
		accounts: map[string]*account{
			"acc_usd_1": {Currency: "USD", Balance: 10000},
			"acc_usd_2": {Currency: "USD", Balance: 10000},
			"acc_usd_3": {Currency: "USD", Balance: 10000},
			"acc_usd_5": {Currency: "USD", Balance: 0},
			"acc_eur_5": {Currency: "EUR", Balance: 0},
		},
	}
}

func invalid(message string) error {
	return &DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: message}
}

func validate(input *dto.OutboundPaymentInput) error {
	if input == nil || strings.TrimSpace(input.IdempotencyKey) == "" || strings.TrimSpace(input.MerchantID) == "" || strings.TrimSpace(input.DebtorAccountID) == "" || strings.TrimSpace(input.CreditorIban) == "" || strings.TrimSpace(input.CreditorName) == "" {
		return invalid("required payment field is missing or blank")
	}

	if input.Amount <= 0 {
		return invalid("amount must be positive")
	}

	if len(input.Currency) != 3 || input.Currency != strings.ToUpper(input.Currency) {
		return invalid("currency must be an ISO 4217 code")
	}

	return nil
}

func sameRequest(p *payment, input *dto.OutboundPaymentInput) bool {
	return p.MerchantID == input.MerchantID && p.DebtorAccountID == input.DebtorAccountID && p.CreditorIban == input.CreditorIban && p.CreditorName == input.CreditorName && p.Currency == input.Currency && p.Amount == input.Amount
}

func createdResponse(p *payment) *dto.OutboundPaymentOutput {
	return &dto.OutboundPaymentOutput{
		PaymentID:    p.ID,
		State:        p.State,
		CreatedAtUTC: p.CreatedAt.UTC(),
	}
}

func requestHash(input *dto.OutboundPaymentInput) (string, error) {
	return util.Sha256Hash(input)
}

func (p *Payment) persist(row *payment) error {
	if p.repository == nil {
		return nil
	}

	payment := &model.Payment{
		Item: model.Item{
			PK: "PAYMENT#" + row.ID,
			SK: "PAYMENT#" + row.ID,
		},
		MerchantID:      row.MerchantID,
		DebtorAccountID: row.DebtorAccountID,
		CreditorIBAN:    row.CreditorIban,
		CreditorName:    row.CreditorName,
		Amount:          int64(math.Round(row.Amount * 100)),
		Currency:        row.Currency,
		Status:          row.State,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		LastSequence:    len(row.Events),
	}

	return p.repository.PutPayment(context.Background(), payment)
}

func paymentFromModel(value *model.Payment) *payment {
	if value == nil {
		return nil
	}
	return &payment{
		ID:              strings.TrimPrefix(value.PK, "PAYMENT#"),
		MerchantID:      value.MerchantID,
		DebtorAccountID: value.DebtorAccountID,
		CreditorIban:    value.CreditorIBAN,
		CreditorName:    value.CreditorName,
		Currency:        value.Currency,
		Amount:          float64(value.Amount) / 100,
		State:           value.Status,
		CreatedAt:       value.CreatedAt,
		UpdatedAt:       value.UpdatedAt,
		Events:          make([]map[string]any, value.LastSequence),
	}
}

func accountFromModel(value *model.Account) *account {
	if value == nil {
		return nil
	}
	return &account{Currency: value.Currency, Balance: float64(value.Balance) / 100, Rows: value.Rows}
}

func (p *Payment) loadPayment(id string) (*payment, error) {
	if row := p.payments[id]; row != nil {
		return row, nil
	}
	if p.repository == nil {
		return nil, nil
	}
	value, err := p.repository.GetPayment(context.Background(), id)
	if err != nil {
		return nil, err
	}
	row := paymentFromModel(value)
	if row != nil {
		p.payments[id] = row
	}
	return row, nil
}

func (p *Payment) loadAccount(id string) (*account, error) {
	if p.repository != nil {
		value, err := p.repository.GetAccount(context.Background(), "ACCOUNT#"+id, "ACCOUNT#"+id)
		if err != nil {
			return nil, err
		}
		row := accountFromModel(value)
		if row != nil {
			p.accounts[id] = row
		}
		return row, nil
	}
	return p.accounts[id], nil
}

func (p *Payment) persistAccount(id string, row *account) error {
	if p.repository == nil {
		return nil
	}
	return p.repository.PutAccount(context.Background(), &model.Account{
		Item:     model.Item{PK: "ACCOUNT#" + id, SK: "ACCOUNT#" + id},
		Balance:  int64(math.Round(row.Balance * 100)),
		Currency: row.Currency,
		Status:   model.AccountStatusActive,
		Rows:     row.Rows,
	})
}

// Create validates and creates an outbound payment, honoring idempotency keys.
func (p *Payment) Create(input *dto.OutboundPaymentInput) (*dto.OutboundPaymentOutput, int, error) {
	if err := validate(input); err != nil {
		return nil, 0, err
	}
	hash, err := requestHash(input)
	if err != nil {
		return nil, 0, fmt.Errorf("hash payment request: %w", err)
	}
	if p.repository != nil {
		record, err := p.repository.GetIdempotencyRecord(context.Background(), input.IdempotencyKey)
		if err != nil {
			return nil, 0, fmt.Errorf("load idempotency record: %w", err)
		}
		if record != nil && record.RequestHash != "" && record.RequestHash != hash {
			return nil, 0, &DomainError{Status: 409, Code: "IDEMPOTENCY_CONFLICT", Text: "idempotency key is already used by a different request"}
		}
		if record != nil && record.Response != "" {
			var response dto.OutboundPaymentOutput
			if err := json.Unmarshal([]byte(record.Response), &response); err != nil {
				return nil, 0, fmt.Errorf("decode idempotency response: %w", err)
			}
			if response.PaymentID != "" {
				p.mu.Lock()
				row, loadErr := p.loadPayment(response.PaymentID)
				p.mu.Unlock()
				if loadErr != nil {
					return nil, 0, fmt.Errorf("load idempotent payment: %w", loadErr)
				}
				if row != nil {
					return createdResponse(row), 200, nil
				}
			}
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if id, ok := p.idempotency[input.IdempotencyKey]; ok {
		existing := p.payments[id]
		if existing == nil {
			return nil, 0, &DomainError{Status: 409, Code: "IDEMPOTENCY_CONFLICT", Text: "idempotency key is already used"}
		}

		if !sameRequest(existing, input) {
			return nil, 0, &DomainError{Status: 409, Code: "IDEMPOTENCY_CONFLICT", Text: "idempotency key is already used by a different request"}
		}
		return createdResponse(existing), 200, nil
	}

	now := time.Now().UTC()
	id := "pay_" + uuid.NewString()
	row := &payment{ID: id, MerchantID: input.MerchantID, DebtorAccountID: input.DebtorAccountID, CreditorIban: input.CreditorIban, CreditorName: input.CreditorName, Currency: input.Currency, IdempotencyKey: input.IdempotencyKey, Amount: input.Amount, State: "RECEIVED", CreatedAt: now, UpdatedAt: now}
	row.Events = []map[string]any{{"eventKey": "evt_" + uuid.NewString(), "eventType": "OUTBOUND_PAYMENT_CREATED", "reasonCode": nil, "correlationId": nil}}
	if err := p.persist(row); err != nil {
		return nil, 0, fmt.Errorf("persist payment: %w", err)
	}

	p.payments[id], p.idempotency[input.IdempotencyKey] = row, id
	if p.repository != nil {
		record := &model.IdempotencyRecord{
			Item: model.Item{
				PK: "IDEMPOTENCY#" + input.IdempotencyKey,
				SK: "IDEMPOTENCY#" + input.IdempotencyKey,
			},
			RequestHash: hash,
			CreatedAt:   time.Now().UTC(),
			ExpiresAt:   time.Now().UTC().Add(24 * time.Hour),
		}
		response, err := json.Marshal(createdResponse(row))
		if err != nil {
			return nil, 0, fmt.Errorf("encode idempotency response: %w", err)
		}
		record.Response = string(response)
		if err := p.repository.PutIdempotencyRecord(context.Background(), record); err != nil {
			return nil, 0, fmt.Errorf("persist idempotency record: %w", err)
		}
	}

	return createdResponse(row), 201, nil
}

// Process advances a received payment and records its resulting event.
func (p *Payment) Process(id string) (map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	row, err := p.loadPayment(id)
	if err != nil {
		return nil, fmt.Errorf("load payment: %w", err)
	}
	if row == nil {
		return nil, &DomainError{Status: 404, Code: "PAYMENT_NOT_FOUND", Text: "payment not found"}
	}

	if row.State == "RECEIVED" {
		acct, err := p.loadAccount(row.DebtorAccountID)
		if err != nil {
			return nil, fmt.Errorf("load account: %w", err)
		}
		if acct == nil {
			return nil, &DomainError{Status: 404, Code: "ACCOUNT_NOT_FOUND", Text: "account not found"}
		}

		if acct.Currency != row.Currency {
			row.State = "REJECTED"
			row.RejectReason = "CURRENCY_MISMATCH"
			row.Events = append(row.Events, map[string]any{"eventKey": "evt_" + uuid.NewString(), "eventType": "REJECTED", "reasonCode": "CURRENCY_MISMATCH", "correlationId": nil})
		} else if acct.Balance < row.Amount {
			row.State = "REJECTED"
			row.RejectReason = "INSUFFICIENT_FUNDS"
			row.Events = append(row.Events, map[string]any{"eventKey": "evt_" + uuid.NewString(), "eventType": "REJECTED", "reasonCode": "INSUFFICIENT_FUNDS", "correlationId": nil})
		} else {
			acct.Balance -= row.Amount
			reservation := map[string]any{"reservationId": "res_" + row.ID, "paymentId": row.ID, "amount": row.Amount, "status": "CONSUMED", "createdAtUtc": time.Now().UTC().Format(time.RFC3339Nano)}
			acct.Rows = append(acct.Rows, reservation)
			row.State = "COMPLETED"
			row.Events = append(row.Events,
				map[string]any{"eventKey": "evt_" + uuid.NewString(), "eventType": "FUNDS_RESERVED", "reasonCode": nil, "correlationId": nil},
				map[string]any{"eventKey": "evt_" + uuid.NewString(), "eventType": "COMPLETED", "reasonCode": nil, "correlationId": nil})
		}
		row.UpdatedAt = time.Now().UTC()
		if err := p.persist(row); err != nil {
			return nil, fmt.Errorf("persist payment state: %w", err)
		}
		if row.State == "COMPLETED" {
			if err := p.persistAccount(row.DebtorAccountID, acct); err != nil {
				return nil, fmt.Errorf("persist account state: %w", err)
			}
		}
	}

	reason := any(nil)
	if row.State == "REJECTED" && row.RejectReason != "" {
		reason = row.RejectReason
	}
	return map[string]any{"paymentId": row.ID, "state": row.State, "reasonCode": reason}, nil
}

// Get returns a payment and its lifecycle details by ID.
func (p *Payment) Get(id string) (map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	row, err := p.loadPayment(id)
	if err != nil {
		return nil, fmt.Errorf("load payment: %w", err)
	}
	if row == nil {
		return nil, &DomainError{Status: 404, Code: "PAYMENT_NOT_FOUND", Text: "payment not found"}
	}

	reason := any(nil)
	if row.State == "REJECTED" && row.RejectReason != "" {
		reason = row.RejectReason
	}
	return map[string]any{"paymentId": row.ID, "state": row.State, "correlationId": nil, "createdAtUtc": row.CreatedAt.Format(time.RFC3339Nano), "updatedAtUtc": row.UpdatedAt.Format(time.RFC3339Nano), "debtorAccountId": row.DebtorAccountID, "creditorIban": row.CreditorIban, "creditorName": row.CreditorName, "amount": row.Amount, "currency": row.Currency, "idempotencyKey": row.IdempotencyKey, "reasonCode": reason, "version": len(row.Events), "events": row.Events}, nil
}

// Account returns an account and its payment reservations by ID.
func (p *Payment) Account(id string) (map[string]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	row, err := p.loadAccount(id)
	if err != nil {
		return nil, fmt.Errorf("load account: %w", err)
	}
	if row == nil {
		return nil, &DomainError{Status: 404, Code: "ACCOUNT_NOT_FOUND", Text: "account not found"}
	}

	reservations := row.Rows
	if reservations == nil {
		reservations = []map[string]any{}
	}
	return map[string]any{"accountId": id, "status": "ACTIVE", "currency": row.Currency, "currentBalance": row.Balance, "availableBalance": row.Balance, "reservations": reservations}, nil
}

// Batch returns matching reservations and the requested IDs that are missing.
func (p *Payment) Batch(accountID string, ids []string) (map[string]any, error) {
	if len(ids) == 0 || len(ids) > 100 {
		return nil, invalid("reservationIds must contain between 1 and 100 values")
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	acct, err := p.loadAccount(accountID)
	if err != nil {
		return nil, fmt.Errorf("load account: %w", err)
	}
	if acct == nil {
		return nil, &DomainError{Status: 404, Code: "ACCOUNT_NOT_FOUND", Text: "account not found"}
	}

	known, found, missing := make(map[string]map[string]any), []any{}, []string{}
	for _, row := range acct.Rows {
		reservationID, ok := row["reservationId"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid reservation record")
		}
		known[reservationID] = row
	}
	seen := make(map[string]bool)
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return nil, invalid("reservationIds cannot contain blank values")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if row := known[id]; row != nil {
			found = append(found, row)
		} else {
			missing = append(missing, id)
		}
	}

	return map[string]any{"reservations": found, "missingReservationIds": missing}, nil
}

type pageToken struct {
	Merchant, State, Route string
	Offset                 int
}

// List returns a paginated list of payments for a merchant.
func (p *Payment) List(merchant, state, route, token string, limit int, forward bool) (map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := 0
	if token != "" {
		payload, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			return nil, &DomainError{Status: 400, Code: "INVALID_PAGINATION_TOKEN", Text: "invalid pagination token"}
		}
		var decoded pageToken
		if err := json.Unmarshal(payload, &decoded); err != nil || decoded.Merchant != merchant || decoded.State != state || decoded.Route != route {
			return nil, &DomainError{Status: 400, Code: "INVALID_PAGINATION_TOKEN", Text: "invalid pagination token"}
		}
		offset = decoded.Offset
	}
	var persisted []*model.Payment
	if p.repository != nil {
		var err error
		persisted, err = p.repository.ListPayments(context.Background(), merchant, state)
		if err != nil {
			return nil, fmt.Errorf("list payments: %w", err)
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	rows := make([]*payment, 0)
	for _, value := range persisted {
		row := paymentFromModel(value)
		p.payments[row.ID] = row
		rows = append(rows, row)
	}
	for _, row := range p.payments {
		if persisted != nil {
			continue
		}
		if row.MerchantID == merchant && (state == "" || row.State == state) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if forward {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if offset > len(rows) {
		return nil, &DomainError{Status: 400, Code: "INVALID_PAGINATION_TOKEN", Text: "invalid pagination token"}
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	items := make([]any, 0, end-offset)
	for _, row := range rows[offset:end] {
		reason := any(nil)
		if row.State == "REJECTED" && row.RejectReason != "" {
			reason = row.RejectReason
		}
		items = append(items, map[string]any{"paymentId": row.ID, "state": row.State, "version": len(row.Events), "merchantId": row.MerchantID, "correlationId": nil, "amount": row.Amount, "currency": row.Currency, "createdAtUtc": row.CreatedAt.Format(time.RFC3339Nano), "updatedAtUtc": row.UpdatedAt.Format(time.RFC3339Nano), "reasonCode": reason})
	}
	result := map[string]any{"items": items}
	if end < len(rows) {
		encoded, err := json.Marshal(pageToken{Merchant: merchant, State: state, Route: route, Offset: end})
		if err != nil {
			p.log.Error("failed to marshal pagination token", zap.Error(err))
		} else {
			result["nextToken"] = base64.RawURLEncoding.EncodeToString(encoded)
		}
	}

	return result, nil
}

func (p *Payment) InitiateOutbound(input *dto.OutboundPaymentInput) (dto.OutboundPaymentOutput, error) {
	output, _, err := p.Create(input)
	if err != nil {
		return dto.OutboundPaymentOutput{}, err
	}

	return *output, nil
}

func (p *Payment) String() string { return fmt.Sprintf("payments=%d", len(p.payments)) }
