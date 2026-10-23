# Instant Payments

## Summary

Instant Payments models the transactional core of a payment workflow, not a complete banking platform. A single DynamoDB table holds payments, lifecycle events, accounts, reservations, ledger entries, and idempotency records. Idempotent payment acceptance atomically records the initial state and event; lifecycle processing conditionally reserves funds and settles a debit; abandoned holds are released automatically. Key-shaped read models support payment tracking, account inspection, reservation lookup, and merchant payment lists.

Stream-processing checkpoints are in memory only. A payment created around a restart might not be processed automatically; the manual payment `.../process` endpoint is the recovery path.

## Why DynamoDB?

DynamoDB transactions keep payment state, account balances, reservations, and ledger effects atomic. Conditional writes guard state transitions and concurrent retries, while idempotency records make acceptance retry-safe. Single-partition reads return payment state with its auditable event history. Global Secondary Indexes provide predictable merchant and state queries without scanning the table.

Streams trigger lifecycle processing and hold recovery; TTL removes temporary timers and expired idempotency records asynchronously, not at an exact deadline. A raw DynamoDB stream carries every table change without server-side filtering: the consumer filters records in code and incurs `GetRecords` cost for discarded account, ledger, and idempotency writes.

## Endpoints

### POST /api/v1/payments/outbound

Accept an outbound payment using a transaction that writes the payment head, initial event, and idempotency binding together. Matching retries replay the original acceptance; a conflicting request using the same key is rejected.

### GET /api/v1/payments/outbound/{paymentId}

Track a payment using a strongly consistent single-partition Query of its head and ordered lifecycle events.

### POST /api/v1/payments/outbound/{paymentId}/process

Manually run lifecycle processing, including guarded reservation and settlement transactions. Terminal-state retries do not post another debit; this is also the recovery path when automatic processing was missed.

### GET /api/v1/accounts/{accountId}

Read current and available balances and reservation audit history using consistent account reads and paginated reservation queries.

### POST /api/v1/accounts/{accountId}/batch-get-reservations

Fetch selected account reservations with `BatchGetItem`, retrying unprocessed keys. Missing reservation identifiers do not create records.

### GET /api/v1/merchants/{merchantId}/payments

Query a merchant Global Secondary Index for a chronologically ordered, paginated payment view. Continuation tokens bind the query parameters; index updates are eventually consistent.

### GET /api/v1/merchants/{merchantId}/payments/state/{state}

Query the merchant/state Global Secondary Index for a lifecycle-state-filtered payment view with chronological ordering and validated continuation tokens.
