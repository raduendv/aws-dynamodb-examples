package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"instantpayments/pkg/aws"
	"instantpayments/pkg/dto"
	"instantpayments/pkg/env"
	"instantpayments/pkg/model"
)

const (
	// transactionConflictAttempts bounds contention before the caller can retry.
	transactionConflictAttempts = 3
	// transactionConflictBackoff lets competing transactions finish before reloading.
	transactionConflictBackoff = 20 * time.Millisecond
	// partitionReadAttempts absorbs transient head/history skew without unbounded reads.
	partitionReadAttempts = 5
	// partitionReadBackoff yields between inconsistent aggregate reads.
	partitionReadBackoff = 10 * time.Millisecond
	// defaultIdempotencyRetention keeps accepted intents replayable for thirty days.
	defaultIdempotencyRetention = env.DefaultIdempotencyRetention
	// defaultReservationTimeout bounds abandoned holds to the specification's fifteen minutes.
	defaultReservationTimeout = env.DefaultReservationTimeout
	// defaultMerchantPageSize balances page payload size and query traffic.
	defaultMerchantPageSize = 50
	// maximumRequestItems bounds merchant pages and reservation batch size.
	maximumRequestItems = 100
)

// DomainError describes an HTTP-compatible business error.
type DomainError struct {
	Status int
	Code   string
	Text   string
}

// Error returns the human-readable domain error text.
func (e *DomainError) Error() string { return e.Text }

// ExpectedOutcome identifies routine not-found outcomes for background logging.
func (e *DomainError) ExpectedOutcome() bool {
	return e != nil && (e.Code == "PAYMENT_NOT_FOUND" || e.Code == "ACCOUNT_NOT_FOUND")
}

// payment is an event-replayed aggregate. Persistent requests own separate
// instances so no mutable cache is shared across network calls.
type payment struct {
	ID, CorrelationID, MerchantID, DebtorAccountID, CreditorIban, CreditorName, Currency, IdempotencyKey string
	Amount                                                                                               float64
	AmountExact                                                                                          string
	State                                                                                                string
	RejectReason                                                                                         string // Stores CURRENCY_MISMATCH or INSUFFICIENT_FUNDS
	CreatedAt, UpdatedAt                                                                                 time.Time
	Events                                                                                               []map[string]any
	LastSequence                                                                                         int64
}

// account carries a balance snapshot and exact stored decimals for guarded writes.
type account struct {
	Currency              string
	Status                string
	CurrentBalance        float64
	CurrentBalanceExact   string
	AvailableBalance      float64
	AvailableBalanceExact string
	Version               int64
	Rows                  []map[string]any
}

// paymentLock protects only the in-memory backend and supports cancellable acquisition.
type paymentLock chan struct{}

// Lock waits for exclusive access to in-memory state.
func (lock paymentLock) Lock() { lock <- struct{}{} }

// Unlock releases exclusive access to in-memory state.
func (lock paymentLock) Unlock() { <-lock }

// TryLock acquires in-memory state only when it is immediately available.
func (lock paymentLock) TryLock() bool {
	select {
	case lock <- struct{}{}:
		return true
	default:
		return false
	}
}

// LockContext waits for in-memory state until acquired or the caller cancels.
func (lock paymentLock) LockContext(ctx context.Context) error {
	select {
	case lock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Payment implements the sample workflow using either isolated persisted aggregates
// and conditional transactions or an explicitly locked in-memory demonstration backend.
type Payment struct {
	log                *zap.Logger
	repository         *aws.Repository
	mu                 paymentLock
	payments           map[string]*payment
	idempotency        map[string]string
	accounts           map[string]*account
	idempotencyTTL     time.Duration
	reservationTimeout time.Duration
}

// NewPayment creates a payment service with seeded demonstration accounts.
func NewPayment(log *zap.Logger, repository *aws.Repository) *Payment {
	return NewPaymentWithEnv(log, repository, &env.Env{})
}

// modelPayment maps a replayed aggregate to the persistence transition input.
func modelPayment(row *payment) *model.Payment {
	return &model.Payment{
		Item: model.Item{EntityType: "PAYMENT_STREAM_HEAD"}, PaymentKeys: model.PaymentKeys{PK: model.PaymentKey(row.ID), SK: model.PaymentHeadSK},
		CorrelationID:   row.CorrelationID,
		MerchantID:      row.MerchantID,
		DebtorAccountID: row.DebtorAccountID,
		CreditorIBAN:    row.CreditorIban,
		CreditorName:    row.CreditorName,
		Amount:          row.Amount,
		AmountExact:     row.AmountExact,
		Currency:        row.Currency,
		Status:          row.State,
		ReasonCode:      row.RejectReason,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		LastSequence:    row.LastSequence,
	}
}

// NewPaymentWithEnv configures the payment service from validated runtime settings.
func NewPaymentWithEnv(log *zap.Logger, repository *aws.Repository, settings *env.Env) *Payment {
	if settings == nil {
		settings = &env.Env{}
	}

	idempotencyTTL := time.Duration(settings.IdempotencyTTL) * time.Second
	if settings.IdempotencyTTL == 0 {
		idempotencyTTL = defaultIdempotencyRetention
	}
	reservationTimeout := time.Duration(settings.ReservationTimeout) * time.Second
	if settings.ReservationTimeout == 0 {
		reservationTimeout = defaultReservationTimeout
	}
	return &Payment{
		log:                log,
		repository:         repository,
		mu:                 make(paymentLock, 1),
		idempotencyTTL:     idempotencyTTL,
		reservationTimeout: reservationTimeout,
		payments:           make(map[string]*payment),
		idempotency:        make(map[string]string),
		accounts: map[string]*account{
			"acc_usd_1": {Currency: "USD", CurrentBalance: 10000, AvailableBalance: 10000},
			"acc_usd_2": {Currency: "USD", CurrentBalance: 5000, AvailableBalance: 5000},
			"acc_usd_3": {Currency: "USD", CurrentBalance: 500, AvailableBalance: 500},
			"acc_usd_4": {Currency: "USD", CurrentBalance: 100, AvailableBalance: 100},
			"acc_usd_5": {Currency: "USD"},
			"acc_eur_1": {Currency: "EUR", CurrentBalance: 10000, AvailableBalance: 10000},
			"acc_eur_2": {Currency: "EUR", CurrentBalance: 5000, AvailableBalance: 5000},
			"acc_eur_3": {Currency: "EUR", CurrentBalance: 500, AvailableBalance: 500},
			"acc_eur_4": {Currency: "EUR", CurrentBalance: 100, AvailableBalance: 100},
			"acc_eur_5": {Currency: "EUR"},
		},
	}
}

// invalid constructs a client-correctable validation outcome.
func invalid(message string) error {
	return &DomainError{Status: 400, Code: "VALIDATION_ERROR", Text: message}
}

// validate checks required business fields before hashing or persistence.
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

// sameRequest compares normalized business intent for the in-memory replay backend.
func sameRequest(p *payment, input *dto.OutboundPaymentInput) bool {
	return p.MerchantID == input.MerchantID && p.DebtorAccountID == input.DebtorAccountID && p.CreditorIban == input.CreditorIban && p.CreditorName == input.CreditorName && p.Currency == input.Currency && p.AmountExact == input.AmountExact
}

// createdResponse formats the creation outcome without exposing persistence keys.
func createdResponse(p *payment) *dto.OutboundPaymentOutput {
	return &dto.OutboundPaymentOutput{
		PaymentID:     p.ID,
		State:         p.State,
		CorrelationID: &p.CorrelationID,
		CreatedAtUTC:  p.CreatedAt.UTC(),
	}
}

// responseSnapshot freezes the accepted response for durable idempotency replay.
func responseSnapshot(row *payment) map[string]any {
	return map[string]any{
		"paymentId": row.ID, "state": row.State,
		"correlationId": row.CorrelationID,
		"createdAtUtc":  model.FormatTimestamp(row.CreatedAt),
	}
}

// decodeResponseSnapshot restores a stored creation response or reports corrupt data.
func decodeResponseSnapshot(snapshot map[string]any) (*dto.OutboundPaymentOutput, error) {
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	var response dto.OutboundPaymentOutput
	if err := json.Unmarshal(encoded, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

// requestHash fingerprints exact normalized intent using character-length-prefixed UTF-8 fields.
func requestHash(input *dto.OutboundPaymentInput) (string, error) {
	amount, err := model.AmountDecimal(input.Amount, input.AmountExact)
	if err != nil {
		return "", err
	}
	fields := []string{
		input.IdempotencyKey,
		input.MerchantID,
		input.DebtorAccountID,
		input.CreditorIban,
		input.CreditorName,
		amount,
		input.Currency,
	}
	var canonical strings.Builder
	for _, field := range fields {
		canonical.WriteString(strconv.Itoa(utf8.RuneCountInString(field)))
		canonical.WriteByte(':')
		canonical.WriteString(field)
	}
	digest := sha256.Sum256([]byte(canonical.String()))
	return hex.EncodeToString(digest[:]), nil
}

// persist writes a head only for the legacy facade. Full durable creation uses CreatePayment.
func (p *Payment) persist(row *payment) error {
	if p.repository == nil {
		return nil
	}
	return p.repository.PutPayment(context.Background(), modelPayment(row))
}

// paymentFromModel replays ordered history and rejects sequence, correlation or state disagreement.
func paymentFromModel(value *model.Payment) (*payment, error) {
	if value == nil {
		return nil, nil
	}
	if int64(len(value.Events)) != value.LastSequence {
		return nil, fmt.Errorf("payment %s has %d events but head sequence is %d", value.PK, len(value.Events), value.LastSequence)
	}
	state := ""
	for index, event := range value.Events {
		expectedSequence := int64(index + 1)
		if event.SequenceNumber != expectedSequence || event.CorrelationID != value.CorrelationID {
			return nil, fmt.Errorf("payment %s event %d does not match its stream head", value.PK, expectedSequence)
		}
		switch event.EventType {
		case "OUTBOUND_PAYMENT_CREATED":
			if index != 0 {
				return nil, fmt.Errorf("payment %s has a creation event after sequence one", value.PK)
			}
			state = "RECEIVED"
		case "FUNDS_RESERVED":
			if state != "RECEIVED" {
				return nil, fmt.Errorf("payment %s has invalid reservation transition", value.PK)
			}
			state = "FUNDS_RESERVED"
		case "COMPLETED":
			if state != "FUNDS_RESERVED" {
				return nil, fmt.Errorf("payment %s has invalid completion transition", value.PK)
			}
			state = "COMPLETED"
		case "REJECTED":
			if state != "RECEIVED" && !(state == "FUNDS_RESERVED" && event.ReasonCode == "RESERVATION_EXPIRED") {
				return nil, fmt.Errorf("payment %s has invalid rejection transition", value.PK)
			}
			state = "REJECTED"
		default:
			return nil, fmt.Errorf("payment %s has unknown event type %q", value.PK, event.EventType)
		}
	}
	if state != value.Status {
		return nil, fmt.Errorf("payment %s replay state %q does not match head state %q", value.PK, state, value.Status)
	}
	row := &payment{
		ID:            string(value.PK),
		CorrelationID: value.CorrelationID,
		State:         state,
		CreatedAt:     value.CreatedAt,
		UpdatedAt:     value.UpdatedAt,
		LastSequence:  value.LastSequence,
	}
	for index, event := range value.Events {
		if index == 0 {
			row.CorrelationID = event.CorrelationID
			row.MerchantID = event.MerchantID
			row.DebtorAccountID = event.DebtorAccountID
			row.CreditorIban = event.CreditorIBAN
			row.CreditorName = event.CreditorName
			row.Amount = event.Amount
			row.AmountExact = event.AmountExact
			row.Currency = event.Currency
			row.IdempotencyKey = event.IdempotencyKey
		}
		var reason any
		if event.ReasonCode != "" {
			reason = event.ReasonCode
			row.RejectReason = event.ReasonCode
		}
		row.Events = append(row.Events, map[string]any{
			"eventKey": event.SK.String(), "eventType": event.EventType,
			"reasonCode": reason, "correlationId": event.CorrelationID,
		})
	}
	return row, nil
}

// paymentFromProjection maps a GSI head without loading event history.
func paymentFromProjection(value *model.Payment) *payment {
	if value == nil {
		return nil
	}
	return &payment{
		ID: string(value.PK), CorrelationID: value.CorrelationID,
		MerchantID: value.MerchantID, Currency: value.Currency, Amount: value.Amount, AmountExact: value.AmountExact,
		State: value.Status, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
		RejectReason: value.ReasonCode, LastSequence: value.LastSequence,
	}
}

// accountFromModel retains exact balance strings alongside the account version snapshot.
func accountFromModel(value *model.Account) *account {
	if value == nil {
		return nil
	}
	return &account{
		Currency:              value.Currency,
		Status:                string(value.Status),
		CurrentBalance:        value.CurrentBalance,
		CurrentBalanceExact:   value.CurrentBalanceExact,
		AvailableBalance:      value.AvailableBalance,
		AvailableBalanceExact: value.AvailableBalanceExact,
		Version:               value.Version,
		Rows:                  value.Rows,
	}
}

// loadPayment loads and reconciles a payment for non-cancellable read facades.
func (p *Payment) loadPayment(id string) (*payment, error) {
	return p.loadPaymentContext(context.Background(), id)
}

// loadPaymentContext retries transient head/history skew before reporting corruption.
// Persisted results are request-local and never published into shared mutable caches.
func (p *Payment) loadPaymentContext(ctx context.Context, id string) (*payment, error) {
	if p.repository == nil {
		return p.payments[id], nil
	}
	var lastReplayErr error
	for attempt := 1; attempt <= partitionReadAttempts; attempt++ {
		value, err := p.repository.GetPayment(ctx, id)
		if err != nil {
			return nil, err
		}
		row, err := paymentFromModel(value)
		if err == nil {
			return row, nil
		}
		lastReplayErr = err
		if attempt < partitionReadAttempts {
			if err := waitForRetry(ctx, time.Duration(attempt)*partitionReadBackoff); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("replay payment partition after five reads: %w", lastReplayErr)
}

// loadAccount reads an eventual account snapshot or the locked in-memory backend.
func (p *Payment) loadAccount(id string) (*account, error) {
	if p.repository != nil {
		value, err := p.repository.GetAccount(context.Background(), model.AccountKey(id), model.AccountKey(id))
		if err != nil {
			return nil, err
		}
		row := accountFromModel(value)
		return row, nil
	}
	return p.accounts[id], nil
}

// loadAccountForUpdate loads an eventual version snapshot for conditional balance writes.
func (p *Payment) loadAccountForUpdate(ctx context.Context, id string) (*account, error) {
	if p.repository == nil {
		return p.accounts[id], nil
	}
	value, err := p.repository.GetAccountForUpdate(ctx, model.AccountKey(id), model.AccountKey(id))
	if err != nil {
		return nil, err
	}
	row := accountFromModel(value)
	return row, nil
}

// persistAccount adapts an account for the legacy direct-write facade.
// Settlement uses guarded repository transactions instead.
func (p *Payment) persistAccount(id string, row *account) error {
	if p.repository == nil {
		return nil
	}
	return p.repository.PutAccount(context.Background(), &model.Account{
		AccountKeys:      model.AccountKeys{PK: model.AccountKey(id), SK: model.AccountKey(id)},
		AccountID:        id,
		CurrentBalance:   row.CurrentBalance,
		AvailableBalance: row.AvailableBalance,
		Currency:         row.Currency,
		Status:           model.AccountStatus(row.Status),
		Rows:             row.Rows,
	})
}

// Create validates and creates an outbound payment, honoring idempotency keys.
func (p *Payment) Create(input *dto.OutboundPaymentInput) (*dto.OutboundPaymentOutput, int, error) {
	if err := validate(input); err != nil {
		return nil, 0, err
	}
	normalized := *input
	amount, err := model.AmountDecimal(input.Amount, input.AmountExact)
	if err != nil {
		return nil, 0, invalid("amount must be a valid decimal")
	}
	normalized.AmountExact = amount
	input = &normalized
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
		if record != nil && len(record.Response) > 0 {
			response, err := decodeResponseSnapshot(record.Response)
			if err != nil {
				return nil, 0, fmt.Errorf("decode idempotency response: %w", err)
			}
			if response.PaymentID != "" {
				return response, 200, nil
			}
		}
	}

	if p.repository == nil {
		p.mu.Lock()
		defer p.mu.Unlock()
	}
	if id, ok := p.idempotency[input.IdempotencyKey]; p.repository == nil && ok {
		existing := p.payments[id]
		if existing == nil {
			return nil, 0, &DomainError{Status: 409, Code: "IDEMPOTENCY_CONFLICT", Text: "idempotency key is already used"}
		}

		if !sameRequest(existing, input) {
			return nil, 0, &DomainError{Status: 409, Code: "IDEMPOTENCY_CONFLICT", Text: "idempotency key is already used by a different request"}
		}
		replay := createdResponse(existing)
		replay.State = "RECEIVED"
		return replay, 200, nil
	}

	now := time.Now().UTC()
	id := "pay_" + uuid.NewString()
	row := &payment{
		ID: id, CorrelationID: "corr_" + uuid.NewString(),
		MerchantID: input.MerchantID, DebtorAccountID: input.DebtorAccountID,
		CreditorIban: input.CreditorIban, CreditorName: input.CreditorName,
		Currency: input.Currency, IdempotencyKey: input.IdempotencyKey,
		Amount: input.Amount, AmountExact: input.AmountExact, State: "RECEIVED", CreatedAt: now, UpdatedAt: now, LastSequence: 1,
	}
	row.Events = []map[string]any{{"eventKey": model.EventKey(1).String(), "eventType": "OUTBOUND_PAYMENT_CREATED", "reasonCode": nil, "correlationId": row.CorrelationID}}

	if p.repository != nil {
		paymentRecord := &model.Payment{
			Item: model.Item{EntityType: "PAYMENT_STREAM_HEAD"}, PaymentKeys: model.PaymentKeys{PK: model.PaymentKey(id), SK: model.PaymentHeadSK},
			CorrelationID: row.CorrelationID, MerchantID: row.MerchantID,
			DebtorAccountID: row.DebtorAccountID, CreditorIBAN: row.CreditorIban,
			CreditorName: row.CreditorName, Amount: row.Amount, AmountExact: row.AmountExact,
			Currency: row.Currency, Status: row.State, CreatedAt: now, UpdatedAt: now,
			LastSequence: row.LastSequence,
		}
		firstEvent := &model.PaymentEvent{
			Item: model.Item{EntityType: "PAYMENT_EVENT"}, PaymentEventKeys: model.PaymentEventKeys{PK: model.PaymentKey(id), SK: 1},
			EventType: model.PaymentEventType("OUTBOUND_PAYMENT_CREATED"), SequenceNumber: 1,
			CorrelationID: row.CorrelationID, OccurredAt: model.FormatTimestamp(now), PaymentID: id,
			MerchantID: row.MerchantID, DebtorAccountID: row.DebtorAccountID,
			CreditorIBAN: row.CreditorIban, CreditorName: row.CreditorName,
			Amount: row.Amount, AmountExact: row.AmountExact, Currency: row.Currency, IdempotencyKey: input.IdempotencyKey,
		}
		record := &model.IdempotencyRecord{
			Item: model.Item{EntityType: "IDEMPOTENCY"}, IdempotencyKeys: model.IdempotencyKeys{PK: model.IdempotencyKey(input.IdempotencyKey), SK: model.IdempotencySK},
			RequestHash: hash, Response: responseSnapshot(row), CreatedAt: now,
			ExpiresAt: now.Add(p.idempotencyTTL),
		}
		if err := p.repository.CreatePayment(context.Background(), paymentRecord, firstEvent, record); err != nil {
			existing, lookupErr := p.repository.GetIdempotencyRecord(context.Background(), input.IdempotencyKey)
			if lookupErr != nil {
				return nil, 0, fmt.Errorf("create payment transaction failed: %w (lookup idempotency record: %v)", err, lookupErr)
			}
			if existing == nil {
				return nil, 0, fmt.Errorf("create payment transaction: %w", err)
			}
			if existing.RequestHash != hash {
				return nil, 0, &DomainError{Status: 409, Code: "IDEMPOTENCY_CONFLICT", Text: "idempotency key is already used by a different request"}
			}
			replay, decodeErr := decodeResponseSnapshot(existing.Response)
			if decodeErr != nil {
				return nil, 0, fmt.Errorf("decode idempotency response: %w", decodeErr)
			}
			return replay, 200, nil
		}
	} else {
		if err := p.persist(row); err != nil {
			return nil, 0, fmt.Errorf("persist payment: %w", err)
		}
	}

	if p.repository == nil {
		p.payments[id] = row
		p.idempotency[input.IdempotencyKey] = id
	}
	return createdResponse(row), 201, nil
}

// appendEvent advances the request-local or locked in-memory lifecycle after a successful write.
func appendEvent(row *payment, eventType, reason string) {
	sequence := row.LastSequence + 1
	event := map[string]any{
		"eventKey":      model.EventKey(sequence).String(),
		"eventType":     eventType,
		"reasonCode":    nil,
		"correlationId": row.CorrelationID,
	}
	if reason != "" {
		event["reasonCode"] = reason
		row.RejectReason = reason
	}
	row.Events = append(row.Events, event)
	row.LastSequence = int64(sequence)
}

// Process advances a received payment and records its resulting event.
func (p *Payment) Process(id string) (map[string]any, error) {
	return p.ProcessContext(context.Background(), id)
}

// ProcessContext advances a payment while honoring caller cancellation.
func (p *Payment) ProcessContext(ctx context.Context, id string) (map[string]any, error) {
	for attempt := 0; attempt < transactionConflictAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := p.processOnce(ctx, id)
		if err == nil || (!aws.IsTransactionConflict(err) && !errors.Is(err, aws.ErrConcurrentUpdate)) || attempt == transactionConflictAttempts-1 {
			return result, err
		}
		if err := waitForRetry(ctx, time.Duration(attempt+1)*transactionConflictBackoff); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("payment processing exhausted transaction conflict retries")
}

// waitForRetry bounds retry backoff by caller cancellation.
func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// rejectPayment conditionally appends a business rejection before updating local state.
func (p *Payment) rejectPayment(ctx context.Context, row *payment, reason string) error {
	if p.repository != nil {
		if err := p.repository.RejectPayment(ctx, modelPayment(row), reason); err != nil {
			return fmt.Errorf("reject payment: %w", err)
		}
	}
	row.State = "REJECTED"
	row.UpdatedAt = time.Now().UTC()
	appendEvent(row, "REJECTED", reason)
	return nil
}

// processOnce replays and attempts settlement once. Persisted version/head guards
// provide concurrency safety without holding a process-wide lock across I/O.
func (p *Payment) processOnce(ctx context.Context, id string) (map[string]any, error) {
	if p.repository == nil {
		if err := p.mu.LockContext(ctx); err != nil {
			return nil, err
		}
		defer p.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	row, err := p.loadPaymentContext(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("load payment: %w", err)
	}
	if row == nil {
		return nil, &DomainError{Status: 404, Code: "PAYMENT_NOT_FOUND", Text: "payment not found"}
	}

	if row.State == "RECEIVED" {
		acct, err := p.loadAccountForUpdate(ctx, row.DebtorAccountID)
		if err != nil {
			return nil, fmt.Errorf("load account: %w", err)
		}
		reason := ""
		if acct == nil {
			reason = "ACCOUNT_NOT_FOUND"
		} else if acct.Currency != row.Currency {
			reason = "CURRENCY_MISMATCH"
		} else {
			insufficient := acct.AvailableBalance < row.Amount
			if p.repository != nil {
				available, ok := new(big.Rat).SetString(acct.AvailableBalanceExact)
				if !ok {
					return nil, fmt.Errorf("invalid stored available balance")
				}
				amountText, err := model.AmountDecimal(row.Amount, row.AmountExact)
				if err != nil {
					return nil, fmt.Errorf("invalid payment amount: %w", err)
				}
				amount, ok := new(big.Rat).SetString(amountText)
				if !ok {
					return nil, fmt.Errorf("invalid payment amount")
				}
				insufficient = available.Cmp(amount) < 0
			}
			if insufficient {
				reason = "INSUFFICIENT_FUNDS"
			}
		}
		if reason != "" {
			if err := p.rejectPayment(ctx, row, reason); err != nil {
				return nil, err
			}
		} else {
			if p.repository != nil {
				deadline := time.Now().UTC().Add(p.reservationTimeout)
				if err := p.repository.ReservePayment(ctx, modelPayment(row), &model.Account{Version: acct.Version}, deadline); err != nil {
					return nil, fmt.Errorf("reserve payment funds: %w", err)
				}
			}
			acct.AvailableBalance -= row.Amount
			row.State = "FUNDS_RESERVED"
			row.UpdatedAt = time.Now().UTC()
			appendEvent(row, "FUNDS_RESERVED", "")
			if p.repository != nil {
				acct.Version++
			} else {
				acct.Rows = append(acct.Rows, map[string]any{
					"reservationId": "res_" + row.ID, "paymentId": row.ID, "amount": row.Amount,
					"status": "ACTIVE", "createdAtUtc": model.FormatTimestamp(row.UpdatedAt),
				})
			}
		}
	}

	if row.State == "FUNDS_RESERVED" {
		acct, err := p.loadAccountForUpdate(ctx, row.DebtorAccountID)
		if err != nil {
			return nil, fmt.Errorf("load reserved account: %w", err)
		}
		if acct == nil {
			return nil, &DomainError{Status: 404, Code: "ACCOUNT_NOT_FOUND", Text: "account not found"}
		}
		if p.repository != nil {
			if err := p.repository.CompletePayment(ctx, modelPayment(row), &model.Account{
				CurrentBalance:      acct.CurrentBalance,
				CurrentBalanceExact: acct.CurrentBalanceExact,
				Version:             acct.Version,
			}, time.Now().UTC()); err != nil {
				if !errors.Is(err, aws.ErrReservationExpired) {
					return nil, fmt.Errorf("complete payment: %w", err)
				}
				if err := p.rejectPayment(ctx, row, "RESERVATION_EXPIRED"); err != nil {
					return nil, err
				}
			}
		}
		if row.State == "FUNDS_RESERVED" {
			acct.CurrentBalance -= row.Amount
			if p.repository != nil {
				acct.Version++
			}
			for _, reservation := range acct.Rows {
				if reservation["reservationId"] == "res_"+row.ID {
					reservation["status"] = "CONSUMED"
				}
			}
			row.State = "COMPLETED"
			row.UpdatedAt = time.Now().UTC()
			appendEvent(row, "COMPLETED", "")
			if p.repository == nil {
				if err := p.persistAccount(row.DebtorAccountID, acct); err != nil {
					return nil, fmt.Errorf("persist account state: %w", err)
				}
			}
		}
	}

	if p.repository == nil && row.State == "REJECTED" {
		row.UpdatedAt = time.Now().UTC()
	}

	reason := any(nil)
	if row.State == "REJECTED" && row.RejectReason != "" {
		reason = row.RejectReason
	}

	return map[string]any{"paymentId": row.ID, "state": row.State, "reasonCode": reason}, nil
}

// Get returns a payment and its lifecycle details by ID.
func (p *Payment) Get(id string) (map[string]any, error) {
	if p.repository == nil {
		p.mu.Lock()
		defer p.mu.Unlock()
	}
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
	amount, err := model.DecimalJSON(row.Amount, row.AmountExact)
	if err != nil {
		return nil, fmt.Errorf("payment amount: %w", err)
	}
	return map[string]any{"paymentId": row.ID, "state": row.State, "correlationId": row.CorrelationID, "createdAtUtc": model.FormatTimestamp(row.CreatedAt), "updatedAtUtc": model.FormatTimestamp(row.UpdatedAt), "debtorAccountId": row.DebtorAccountID, "creditorIban": row.CreditorIban, "creditorName": row.CreditorName, "amount": amount, "currency": row.Currency, "idempotencyKey": row.IdempotencyKey, "reasonCode": reason, "version": len(row.Events), "events": row.Events}, nil
}

// Account returns current balances and all durable reservations, or
// ACCOUNT_NOT_FOUND. Persistent reads use one GetItem plus paged prefix Queries;
// they never scan timer or ledger rows and do not provide a cross-item snapshot.
func (p *Payment) Account(id string) (map[string]any, error) {
	if p.repository == nil {
		p.mu.Lock()
		defer p.mu.Unlock()
	}
	row := p.accounts[id]
	var reservations []map[string]any
	if p.repository != nil {
		persisted, rows, err := p.repository.GetAccountReservations(context.Background(), id)
		if err != nil {
			return nil, fmt.Errorf("load account reservations: %w", err)
		}
		row = accountFromModel(persisted)
		reservations = rows
	} else if row != nil {
		reservations = row.Rows
	}
	if row == nil {
		return nil, &DomainError{Status: 404, Code: "ACCOUNT_NOT_FOUND", Text: "account not found"}
	}

	if reservations == nil {
		reservations = []map[string]any{}
	}
	status := row.Status
	if status == "" {
		status = string(model.AccountStatusActive)
	}
	current, err := model.DecimalJSON(row.CurrentBalance, row.CurrentBalanceExact)
	if err != nil {
		return nil, fmt.Errorf("stored current balance: %w", err)
	}
	available, err := model.DecimalJSON(row.AvailableBalance, row.AvailableBalanceExact)
	if err != nil {
		return nil, fmt.Errorf("stored available balance: %w", err)
	}
	return map[string]any{"accountId": id, "status": status, "currency": row.Currency, "currentBalance": current, "availableBalance": available, "reservations": reservations}, nil
}

// Batch returns matching reservations and the requested IDs that are missing.
func (p *Payment) Batch(accountID string, ids []string) (map[string]any, error) {
	if len(ids) == 0 || len(ids) > maximumRequestItems {
		return nil, invalid("reservationIds must contain between 1 and 100 values")
	}

	if p.repository == nil {
		p.mu.Lock()
		defer p.mu.Unlock()
	}
	known, found, missing := make(map[string]map[string]any), []any{}, []string{}
	normalized := make([]string, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return nil, invalid("reservationIds cannot contain blank values")
		}
		if !seen[id] {
			seen[id] = true
			normalized = append(normalized, id)
		}
	}
	if len(normalized) == 0 {
		return nil, &DomainError{Status: 400, Code: "INVALID_BATCH_GET_RESERVATIONS_REQUEST", Text: "no distinct reservation ids"}
	}
	if p.repository != nil {
		reservations, err := p.repository.GetReservations(context.Background(), accountID, normalized)
		if err != nil {
			return nil, fmt.Errorf("batch get reservations: %w", err)
		}
		known = reservations
	} else {
		acct, err := p.loadAccount(accountID)
		if err != nil {
			return nil, fmt.Errorf("load account: %w", err)
		}
		if acct != nil {
			for _, row := range acct.Rows {
				reservationID, ok := row["reservationId"].(string)
				if !ok {
					return nil, fmt.Errorf("invalid reservation record")
				}
				known[reservationID] = row
			}
		}
	}
	for _, id := range normalized {
		if row := known[id]; row != nil {
			found = append(found, row)
		} else {
			missing = append(missing, id)
		}
	}

	return map[string]any{"reservations": found, "missingReservationIds": missing}, nil
}

// ReleaseReservation restores available funds when a reservation timer expires.
func (p *Payment) ReleaseReservation(ctx context.Context, accountID, reservationID string) error {
	if p.repository != nil {
		return p.repository.ReleaseReservation(ctx, accountID, reservationID)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	acct := p.accounts[accountID]
	if acct == nil {
		return nil
	}
	for _, reservation := range acct.Rows {
		if reservation["reservationId"] != reservationID || reservation["status"] != "ACTIVE" {
			continue
		}
		amount, ok := reservation["amount"].(float64)
		if !ok {
			return fmt.Errorf("reservation %s has invalid amount", reservationID)
		}
		acct.AvailableBalance += amount
		reservation["status"] = "RELEASED"
		return nil
	}
	return nil
}

// pageToken binds an opaque continuation to a route, merchant and query shape.
type pageToken struct {
	Merchant string            `json:"merchant"`
	State    string            `json:"state"`
	Route    string            `json:"route"`
	Offset   int               `json:"offset,omitempty"`
	Forward  bool              `json:"forward"`
	Limit    int               `json:"limit"`
	LastKey  map[string]string `json:"lastKey,omitempty"`
}

// List returns a paginated list of payments for a merchant.
func (p *Payment) List(merchant, state, route, token string, limit int, forward bool) (map[string]any, error) {
	if limit <= 0 {
		limit = defaultMerchantPageSize
	}
	if limit > maximumRequestItems {
		limit = maximumRequestItems
	}
	decoded := pageToken{Merchant: merchant, State: state, Route: route, Forward: forward, Limit: limit}
	if token != "" {
		var err error
		decoded, err = p.decodePageToken(token, merchant, state, route, limit, forward)
		if err != nil {
			return nil, err
		}
	}
	if p.repository != nil {
		startKey := make(map[string]types.AttributeValue, len(decoded.LastKey))
		for key, value := range decoded.LastKey {
			startKey[key] = &types.AttributeValueMemberS{Value: value}
		}
		if len(startKey) == 0 {
			startKey = nil
		}
		persisted, lastKey, err := p.repository.ListPaymentPage(context.Background(), merchant, state, int32(limit), forward, startKey)
		if err != nil {
			return nil, fmt.Errorf("list payments: %w", err)
		}
		items := make([]any, 0, len(persisted))
		for _, value := range persisted {
			row := paymentFromProjection(value)
			if row == nil {
				continue
			}
			if row.MerchantID == "" {
				row.MerchantID = merchant
			}
			if row.State == "" {
				row.State = state
			}
			item, err := paymentListItem(row)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		result := map[string]any{"items": items}
		if len(lastKey) > 0 {
			stringKey := make(map[string]string, len(lastKey))
			for key, value := range lastKey {
				attribute, ok := value.(*types.AttributeValueMemberS)
				if !ok {
					return nil, fmt.Errorf("unsupported pagination key attribute %q", key)
				}
				stringKey[key] = attribute.Value
			}
			nextToken, err := encodePageToken(pageToken{
				Merchant: merchant, State: state, Route: route, Forward: forward, Limit: limit,
				LastKey: stringKey,
			})
			if err != nil {
				return nil, fmt.Errorf("encode pagination token: %w", err)
			}
			result["nextToken"] = nextToken
		}
		return result, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	rows := make([]*payment, 0)
	for _, row := range p.payments {
		if row.MerchantID == merchant && (state == "" || row.State == state) {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			if forward {
				return rows[i].ID < rows[j].ID
			}
			return rows[i].ID > rows[j].ID
		}
		if forward {
			return rows[i].CreatedAt.Before(rows[j].CreatedAt)
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	offset := decoded.Offset
	if offset > len(rows) {
		return nil, &DomainError{Status: 400, Code: "INVALID_PAGINATION_TOKEN", Text: "invalid pagination token"}
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	items := make([]any, 0, end-offset)
	for _, row := range rows[offset:end] {
		item, err := paymentListItem(row)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	result := map[string]any{"items": items}
	if end < len(rows) {
		nextToken, err := encodePageToken(pageToken{
			Merchant: merchant, State: state, Route: route, Offset: end, Forward: forward, Limit: limit,
		})
		if err != nil {
			return nil, fmt.Errorf("encode pagination token: %w", err)
		}
		result["nextToken"] = nextToken
	}

	return result, nil
}

// paymentListItem maps projected fields and preserves exact monetary JSON values.
func paymentListItem(row *payment) (map[string]any, error) {
	var reason any
	if row.State == "REJECTED" && row.RejectReason != "" {
		reason = row.RejectReason
	}
	amount, err := model.DecimalJSON(row.Amount, row.AmountExact)
	if err != nil {
		return nil, fmt.Errorf("payment projection amount: %w", err)
	}
	return map[string]any{
		"paymentId": row.ID, "state": row.State, "version": row.LastSequence,
		"merchantId": row.MerchantID, "correlationId": row.CorrelationID,
		"amount": amount, "currency": row.Currency,
		"createdAtUtc": model.FormatTimestamp(row.CreatedAt),
		"updatedAtUtc": model.FormatTimestamp(row.UpdatedAt), "reasonCode": reason,
	}, nil
}

// encodePageToken emits a reversible opaque continuation without claiming tamper resistance.
func encodePageToken(token pageToken) (string, error) {
	payload, err := json.Marshal(token)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

// decodePageToken rejects malformed or mismatched query scope before repository access.
func (p *Payment) decodePageToken(token, merchant, state, route string, limit int, forward bool) (pageToken, error) {
	invalidToken := &DomainError{Status: 400, Code: "INVALID_PAGINATION_TOKEN", Text: "invalid pagination token"}
	payload, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return pageToken{}, invalidToken
	}
	var decoded pageToken
	if err := json.Unmarshal(payload, &decoded); err != nil ||
		decoded.Merchant != merchant || decoded.State != state || decoded.Route != route ||
		decoded.Forward != forward || decoded.Limit != limit || decoded.Offset < 0 {
		return pageToken{}, invalidToken
	}
	if p.repository != nil {
		extraKey, extraValue := "paymentId", ""
		if state != "" {
			extraKey, extraValue = "aggregateState", state
		}
		var paymentKey model.PaymentKey
		keyErr := paymentKey.UnmarshalDynamoDBAttributeValue(&types.AttributeValueMemberS{Value: decoded.LastKey["PK"]})
		if state == "" {
			extraValue = string(paymentKey)
		}
		if keyErr != nil || decoded.Offset != 0 || len(decoded.LastKey) != 5 || decoded.LastKey["merchantId"] != merchant ||
			decoded.LastKey[extraKey] != extraValue ||
			decoded.LastKey["SK"] != model.PaymentHeadSK || decoded.LastKey["createdAtUtc"] == "" {
			return pageToken{}, invalidToken
		}
		for key := range decoded.LastKey {
			if key != "PK" && key != "SK" && key != "createdAtUtc" && key != "merchantId" && key != extraKey {
				return pageToken{}, invalidToken
			}
		}
	} else if decoded.Offset <= 0 || len(decoded.LastKey) != 0 {
		return pageToken{}, invalidToken
	}
	return decoded, nil
}

// InitiateOutbound adapts Create for callers that do not need its HTTP status.
func (p *Payment) InitiateOutbound(input *dto.OutboundPaymentInput) (dto.OutboundPaymentOutput, error) {
	output, _, err := p.Create(input)
	if err != nil {
		return dto.OutboundPaymentOutput{}, err
	}

	return *output, nil
}

// String reports the number of locally held in-memory payments, not persisted table size.
func (p *Payment) String() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return fmt.Sprintf("payments=%d", len(p.payments))
}
