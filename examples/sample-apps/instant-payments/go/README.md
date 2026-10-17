# Instant Payments Go Sample

This sample application demonstrates an instant-payments workflow built with Go, Amazon DynamoDB, and DynamoDB Streams. It lives at `examples/sample-apps/instant-payments/go` inside the [aws-dynamodb-examples](../../../README.md) repository.

The application exposes HTTP endpoints for creating, retrieving, processing, and listing outbound payments. It also includes a DynamoDB repository layer, table initialization, idempotency models, and a DynamoDB Streams worker that dispatches stream records to a bounded worker pool.

## What This Sample Demonstrates

- Go AWS SDK v2 clients for DynamoDB and DynamoDB Streams
- DynamoDB single-table item keys for accounts, payments, events, and idempotency records
- Idempotent payment creation using an idempotency key
- Payment state transitions: `RECEIVED`, `COMPLETED`, and `REJECTED`
- DynamoDB Streams shard processing with worker-pool dispatch
- Fx lifecycle management for HTTP and stream-processing components
- OpenAPI-generated HTTP server support and request examples

## Architecture

```text
HTTP request
    |
    v
HTTP handlers -> service.Payment -> in-memory payment ledger
                         |
                         +-> AWS Repository -> DynamoDB accounts and idempotency models

DynamoDB Streams -> StreamProcessor -> WorkerPool -> IdentifyJob
```

The sample intentionally contains two persistence paths. `pkg/aws.Repository` persists payment projections, accounts, and idempotency records in DynamoDB, while `pkg/service.Payment` keeps the workflow cache and seeded account balances in memory. The cache and balances are lost when the process stops; the DynamoDB payment records remain available for stream processing and inspection.

## Prerequisites

- Go 1.26 or later
- AWS credentials configured through the standard AWS SDK credential chain
- An AWS Region with permission to use DynamoDB and DynamoDB Streams
- DynamoDB Streams enabled for the table used by the sample

The default AWS Region is `eu-west-1` and the default table name is `instant-payments-go`.

## Configuration

The application reads these environment variables:

| Variable | Default | Description |
| --- | --- | --- |
| `HTTP_SERVER_ADDRESS` | `:8080` | HTTP listen address |
| `AWS_REGION` | `eu-west-1` | AWS Region used by the SDK |
| `TABLE_NAME` | `instant-payments-go` | DynamoDB table name |
| `WORKER_POOL_SIZE` | `10` | Number of worker goroutines |
| `JOB_POOL_SIZE` | `100` | Buffered job-channel capacity |

## Run

From this directory:

```bash
go mod download
go run ./cmd/main
```

The Makefile provides the same default workflow:

```bash
make run
```

The application initializes the configured DynamoDB table when needed, seeds example accounts, starts the HTTP server, and begins watching DynamoDB Streams.

## HTTP API

The main routes are:

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/v1/payments/outbound` | Create an outbound payment |
| `GET` | `/api/v1/payments/outbound/{paymentID}` | Get a payment |
| `POST` | `/api/v1/payments/outbound/{paymentID}/process` | Process a received payment |
| `GET` | `/api/v1/merchants/{merchantID}/payments` | List a merchant's payments |
| `GET` | `/api/v1/merchants/{merchantID}/payments/state/{state}` | List payments filtered by state |
| `GET` | `/api/v1/accounts/{accountID}` | Get an account and reservations |
| `POST` | `/api/v1/accounts/{accountID}/batch-get-reservations` | Get reservations by ID |
| `GET` | `/api/healthcheck` | Health check |
| `GET` | `/actuator/health` | Actuator health response |
| `GET` | `/api-docs` | OpenAPI document |

Example request:

```bash
curl -X POST http://localhost:8080/api/v1/payments/outbound \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: example-001' \
  -d '{
    "idempotencyKey": "example-001",
    "merchantId": "merchant-001",
    "debtorAccountId": "acc_usd_1",
    "creditorIban": "DE89370400440532013000",
    "creditorName": "Example Recipient",
    "amount": 10.50,
    "currency": "USD"
  }'
```

The repository also includes `instant-payments-verification.http` and a Postman collection with request examples.

## DynamoDB Data Model

The model types in `pkg/model` use `pk` and `sk` attributes:

| Entity | Partition key | Sort key |
| --- | --- | --- |
| Account | `ACCOUNT#<username>` | `ACCOUNT#<username>` |
| Payment | `PAYMENT#<paymentId>` | `PAYMENT#<paymentId>` |
| Payment event | `PAYMENT#<paymentId>` | `EVENT#<timestamp>` |
| Idempotency record | `IDEMPOTENCY#<key>` | `IDEMPOTENCY#<key>` |

Amounts in persisted model types use minor currency units (`int64`). The HTTP DTO accepts a `float64` amount for the sample API and validates the currency as an ISO 4217 code.

## Tests

Run the package tests from this directory:

```bash
go test -count=1 -cover ./pkg/...
```

The tests cover the AWS repository adapters, environment parsing, HTTP handlers, payment service behavior, validation, hashing, and worker jobs.

## Directory Guide

- `api/`: OpenAPI source documents.
- `cmd/main/`: Application entry point and DynamoDB table/bootstrap logic.
- `cmd/cli/`: CLI entry-point placeholder.
- `gen/`: Generated OpenAPI server and model code.
- `pkg/aws/`: AWS clients, repository adapter, and Streams processor.
- `pkg/dto/`: HTTP request and response data-transfer objects.
- `pkg/http/`: HTTP server, routes, health endpoints, and API handlers.
- `pkg/input/`: Generic request validation and JSON processing helpers.
- `pkg/model/`: DynamoDB-persisted model types.
- `pkg/service/`: Payment workflow and in-memory sample ledger.
- `pkg/workerpool/`: Worker pool and stream-processing jobs.

## Notes

This is an educational sample, not a production payment system. Before using a similar design in production, add durable payment persistence, transactional DynamoDB writes, authenticated authorization, secrets management, reliable event delivery, audit controls, and operational safeguards for monetary transactions.
