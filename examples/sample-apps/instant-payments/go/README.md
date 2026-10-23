# Instant Payments - Go

A Go implementation using the standard-library HTTP server, Uber Fx dependency injection, and AWS SDK for Go v2. See the [Instant Payments workload overview](../README.md) for scope, lifecycle, and endpoints.

## DynamoDB Features at a Glance

| Feature | Where it's used | Code pointers |
| --- | --- | --- |
| Single-table keys and consistent Query | Payment head and ordered lifecycle history; account reservations | [`pkg/model/keys.go`](pkg/model/keys.go), [`pkg/aws/repository.go`](pkg/aws/repository.go) |
| Transactions and conditional writes | Idempotent acceptance, guarded fund reservation, settlement, and release | [`pkg/aws/repository.go`](pkg/aws/repository.go), [`pkg/service/payment.go`](pkg/service/payment.go) |
| BatchGetItem | Selected reservations, with unprocessed-key retries | [`pkg/aws/repository.go`](pkg/aws/repository.go) |
| Global Secondary Indexes | Merchant and merchant/state pagination with continuation keys | [`cmd/main/table.go`](cmd/main/table.go), [`pkg/aws/repository.go`](pkg/aws/repository.go) |
| DynamoDB Streams | Lifecycle events and reservation timer deletion processing | [`pkg/aws/stream_processor.go`](pkg/aws/stream_processor.go), [`pkg/workerpool/job/`](pkg/workerpool/job/) |
| Time to Live (TTL) | Idempotency retention and abandoned-hold timers | [`pkg/model/idempotency_record.go`](pkg/model/idempotency_record.go), [`pkg/model/settlement.go`](pkg/model/settlement.go), [`cmd/main/table.go`](cmd/main/table.go) |

## Runbook

See the [Go Runbook](docs/Runbook.md) for prerequisites, build, execution modes, API documentation, and operational guidance.
