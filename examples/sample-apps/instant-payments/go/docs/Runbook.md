# Runbook: DynamoDB Sample App Instant Payments

## Prerequisites

| Need | When |
| --- | --- |
| Go 1.26.0 or later, including the `go` build/test tool | Building, testing, or running on the host |
| Docker Engine/Desktop and Docker Compose plugin | Either DynamoDB Local execution mode; tagged tests automatically activate Local unless an explicit loopback test endpoint is supplied |
| curl with `--aws-sigv4` support | Host DynamoDB Local readiness checks, tagged-test endpoint probes, and manual HTTP verification |
| AWS credentials and access to the configured table/indexes/stream | Only when targeting a remote AWS DynamoDB endpoint; local runs use dummy credentials and require no AWS account |
| AWS CLI | The optional destructive table-deletion helper |
| JetBrains HTTP Client CLI or Postman | Optional HTTP verification collections |

## Tech stack

- Runtime/build: Go `1.26.0` in [`go.mod`](../go.mod); Docker builder `golang:1.26.1-alpine3.22`, runtime `alpine:3.22.2` in [`Dockerfile`](../Dockerfile).
- Framework: standard-library `net/http`, Uber Fx `go.uber.org/fx v1.24.0`, logging `go.uber.org/zap v1.26.0`.
- AWS SDK for Go v2: core `github.com/aws/aws-sdk-go-v2 v1.43.6`; config `github.com/aws/aws-sdk-go-v2/config v1.32.17`; credentials `github.com/aws/aws-sdk-go-v2/credentials v1.19.16`; DynamoDB mapping `github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue v1.20.39`; DynamoDB `github.com/aws/aws-sdk-go-v2/service/dynamodb v1.63.3`; Streams `github.com/aws/aws-sdk-go-v2/service/dynamodbstreams v1.36.6`; Smithy `github.com/aws/smithy-go v1.27.8`.
- Configuration/validation/identifiers: `github.com/caarlos0/env/v11 v11.4.1`, `github.com/go-playground/validator/v10 v10.30.2`, `github.com/google/uuid v1.6.0`.
- DynamoDB access styles: `high-level` and `low-level` are accepted selector values. Both currently use the same SDK v2 repository and typed key/attribute codecs, not separate clients or an enhanced-client API.
- API documentation: embedded OpenAPI `3.0.3` contract, API version `1.0.0`, and offline Swagger UI `swagger-ui-dist 5.34.0` assets with their Apache-2.0 license/notice. No server/model code generation is required.
- Testing: Go standard-library `testing`, `go test`, race detector, coverage tools, and build-tagged DynamoDB integration/smoke tests. Versions follow the Go toolchain; optional HTTP collections are [`instant-payments-verification.http`](../instant-payments-verification.http) and [`instant-payments-verification.postman_collection.json`](../instant-payments-verification.postman_collection.json).
- Datastores: remote Amazon DynamoDB or local ExtendDB dev, pinned in [`docker-compose.yml`](../docker-compose.yml) to `extenddb/extenddb-dev@sha256:20678dcdfedf2b9135b269cc58a19c1e2c72e22ac8774ad201f51b3d6f0064fe` with in-memory SQLite storage. The service is named `extenddb`, producing a Compose container named `<project>-extenddb-1`. Existing local script names are retained for workflow compatibility. ExtendDB is a separate DynamoDB-compatible implementation, not Amazon DynamoDB Local. It uses container port `18080`, published only on host loopback port `8000`, and seeds the dummy signing pair `AKIAlocal` / `local`.

## Quick start

Run all commands from the `go/` directory. This sample has no API authentication or authorization: use synthetic data, trusted local networks, and loopback bindings only.

### Build

```sh
scripts/build-app.sh
```

Runs unit tests and builds `bin/instantpayments`. Use `scripts/build-app.sh --skip-tests` to compile only. `make` also builds and tests; `make coverage` enforces the coverage threshold.

### Run (three ways)

#### 1. Host app + DynamoDB Local (Docker)

Use this mode for rapid Go development without an AWS account.

```sh
scripts/start-dynamodb-local.sh
HTTP_SERVER_ADDRESS=127.0.0.1:8080 scripts/run-app-local.sh
```

Stop the host app with Ctrl-C, then `scripts/stop-dynamodb-local.sh`. Stopping the in-memory database discards all its data. The start helper uses signed `ListTables` with dummy credentials: up to 60 attempts, each with a two-second request timeout and one-second delay between failures. An unsuccessful readiness check exits nonzero and prints container logs.

##### Command format & arguments

`scripts/start-dynamodb-local.sh` and `scripts/stop-dynamodb-local.sh` accept no arguments.

```sh
scripts/run-app-local.sh [--dynamodb-endpoint URL] [--dynamodb-region REGION] [--dynamodb-client-type high-level|low-level]
```

| Option | Default | Notes |
| --- | --- | --- |
| `--dynamodb-endpoint` | `http://localhost:8000` | Table API endpoint; helper overwrites `DYNAMODB_ENDPOINT` |
| `--dynamodb-region` | `eu-west-1` | Helper overwrites `DYNAMODB_REGION` |
| `--dynamodb-client-type` | `high-level` | `high-level` or `low-level`; invalid values fail before startup |
| `-h`, `--help` | Off | Print usage |
| `DYNAMODB_PROVISIONING_CREATERESOURCES` (environment) | `true` in helper | Set `false` to require a pre-provisioned table |

The host helper requires an already ready database; it does not start Docker or wait for database readiness.

#### 2. Full stack in Docker (app + DynamoDB Local)

Use this mode for a reproducible container demo without installing Go on the host.

```sh
scripts/run-app-docker.sh
scripts/run-app-docker.sh --stop
```

The `app` profile builds the multi-stage image and starts both services. The app connects to `http://extenddb:18080` on the Compose network; host ports are loopback-only `8000` and `8080`. The unprivileged app and both services use `no-new-privileges`. The app container performs the same bounded signed database readiness check before executing the binary. The helper returns after detached startup, not HTTP readiness; check `curl --fail http://localhost:8080/actuator/health` and `docker compose logs app`. Starting the local database alone does not activate the `app` profile. `--stop` tears down the stack and discards its in-memory data.

##### Command format & arguments

`scripts/run-app-docker.sh [--dynamodb-client-type high-level|low-level] [--stop]`

| Option | Default | Notes |
| --- | --- | --- |
| `--dynamodb-client-type` | `high-level` | Validated selector exported to Compose |
| `--stop` | Off | `docker compose --profile app down`; stops both services |
| `-h`, `--help` | Off | Print usage |
| `DYNAMODB_REGION` (environment) | `eu-west-1` | Compose region |
| `DYNAMODB_TABLE_INSTANTPAYMENTS_NAME` (environment) | `GoInstantPayments` | Compose table |
| `DYNAMODB_PROVISIONING_CREATERESOURCES` (environment) | `true` | Local bootstrap; `false` requires an existing table |

Other Compose policy variables retain the defaults in the configuration table below. HTTP worker and profile overrides require adding an explicit Compose environment mapping; host environment variables are not implicitly forwarded.

#### 3. Host app + AWS DynamoDB

Use this mode to exercise the sample against an infrastructure-managed AWS table. Use temporary credentials or an IAM role; never commit credentials. Provision the table, indexes, TTL, and stream first.

```sh
export AWS_PROFILE=instant-payments-demo
HTTP_SERVER_ADDRESS=127.0.0.1:8080 \
DYNAMODB_PROVISIONING_CREATERESOURCES=false \
scripts/run-app-local.sh \
  --dynamodb-endpoint https://dynamodb.eu-west-1.amazonaws.com \
  --dynamodb-region eu-west-1 \
  --dynamodb-client-type high-level
```

##### Command format & arguments

`DYNAMODB_PROVISIONING_CREATERESOURCES=false scripts/run-app-local.sh --dynamodb-endpoint https://dynamodb.REGION.amazonaws.com [--dynamodb-region REGION] [--dynamodb-client-type high-level|low-level]`

| Option | Default | Notes |
| --- | --- | --- |
| `--dynamodb-endpoint` | `http://localhost:8000` | Required override to target AWS; use the region's HTTPS endpoint |
| `--dynamodb-region` | `eu-west-1` | Must match endpoint and credentials' permitted region |
| `--dynamodb-client-type` | `high-level` | `high-level` or `low-level` |
| `-h`, `--help` | Off | Print usage |
| `DYNAMODB_PROVISIONING_CREATERESOURCES` (environment) | Helper defaults `true` | Explicitly use `false` outside local demos |
| `DYNAMODB_TABLE_INSTANTPAYMENTS_NAME` (environment) | `GoInstantPayments` | Override for the provisioned table |
| `AWS_PROFILE` / AWS SDK credential chain | No sample remote credentials | Prefer role credentials or an authenticated temporary-credential profile |

The helper does not wait for AWS readiness. Non-local endpoints use the SDK credential chain; Streams resolves its separate regional service endpoint. Only `localhost`, `127.0.0.1`, `dynamodb`, `dynamodb-local`, and `extenddb` share the table endpoint and dummy credentials with Streams.

#### Startup and provisioning safety

`dynamodb.provisioning.create-resources` maps to `DYNAMODB_PROVISIONING_CREATERESOURCES`. Direct binary startup defaults to `false`; both run helpers default it to `true` unless explicitly overridden. Enabled bootstrap creates the table if missing, enables TTL and `NEW_IMAGE` Streams, and inserts missing demo accounts without overwriting existing accounts. Disabled bootstrap only describes the table and fails clearly if it is absent. Avoid helper defaults against AWS: provisioning seeds synthetic accounts and may incur charges.

For external provisioning, mirror [`cmd/main/table.go`](../cmd/main/table.go): string `PK`/`SK`, both merchant GSIs, TTL attribute, and `NEW_IMAGE` stream. Runtime permissions include `DescribeTable`, `GetItem`, `PutItem`, `UpdateItem`, `DeleteItem`, `Query`, `BatchGetItem`, health-check `Scan`, plus Streams `ListStreams`, `DescribeStream`, `GetShardIterator`, and `GetRecords`. Transactions use underlying item IAM actions, not a separate `TransactWriteItems` IAM action. Bootstrap additionally needs `CreateTable`, `UpdateTable`, `DescribeTimeToLive`, and `UpdateTimeToLive`. Scope access to the configured table, indexes, and stream wherever supported.

### API documentation

In the default development profile:

- OpenAPI JSON: <http://localhost:8080/api-docs>
- Interactive Swagger UI: <http://localhost:8080/swagger-ui/index.html>
- Convenience redirect: <http://localhost:8080/swagger-ui.html>
- Health: <http://localhost:8080/actuator/health>

The contract is [`pkg/http/api/swagger.json`](../pkg/http/api/swagger.json), embedded with local UI assets for offline use. `APP_PROFILE=prod` always disables documentation, even with `DOCS_ENABLED=true`; `DOCS_ENABLED=false` also disables it in development/test. Health is not a documentation route.

## Configuration

| Environment variable | Default | Description |
| --- | --- | --- |
| `APP_PROFILE` | `dev` | `prod` always disables documentation; `test` selects smaller HTTP async pools |
| `DOCS_ENABLED` | `true` (`false` for `prod`) | Disable documentation in development/test; cannot enable it in production |
| `HTTP_SERVER_ADDRESS` | `:8080` | HTTP bind address |
| `HTTP_ASYNC_CORE_POOL_SIZE` | `10` (`4` for `test`) | HTTP async core workers, separate from business workers |
| `HTTP_ASYNC_MAX_POOL_SIZE` | `50` (`8` for `test`) | HTTP async maximum workers |
| `HTTP_ASYNC_QUEUE_CAPACITY` | `100` (`50` for `test`) | HTTP async queue capacity |
| `HTTP_ASYNC_TIMEOUT` | `10s` | Go duration for async request processing |
| `HTTP_SHUTDOWN_TIMEOUT` | `30s` | Go duration for graceful HTTP shutdown |
| `DYNAMODB_ENDPOINT` | Required | DynamoDB table API endpoint; shared with Streams only for local hostnames |
| `DYNAMODB_REGION` | `eu-west-1` | DynamoDB region |
| `DYNAMODB_CLIENTTYPE` | `high-level` | `high-level` or `low-level` access setting |
| `DYNAMODB_TABLE_INSTANTPAYMENTS_NAME` | `GoInstantPayments` | Single-table name |
| `DYNAMODB_PROVISIONING_CREATERESOURCES` | `false` | Create table, enable TTL/streams, and seed demo accounts |
| `DYNAMODB_TABLE_INSTANTPAYMENTS_IDEMPOTENCY_TTL` | `2592000` | Idempotency retention in seconds; valid range is 3600–31536000 |
| `DYNAMODB_TABLE_INSTANTPAYMENTS_RESERVATION_TIMEOUT` | `900` | Reservation timeout in seconds |
| `DYNAMODB_STREAMS_ITERATOR_TYPE` | `LATEST` (`TRIM_HORIZON` for `test`) | `LATEST` or `TRIM_HORIZON`; explicit setting overrides the profile |
| `DYNAMODB_STREAMS_ENABLED` | `true` | Enable the stream poller |
| `WORKER_POOL_SIZE` | `10` | Worker goroutines |
| `JOB_POOL_SIZE` | `100` | Bounded job queue capacity |

When provisioning is disabled, startup only describes the configured table and fails with an actionable error if it does not exist. Provisioning should be disabled for infrastructure-managed environments.

For AWS, Streams resolves its own regional service endpoint rather than using the table API endpoint. The local hostnames `localhost`, `127.0.0.1`, `dynamodb`, `dynamodb-local`, and `extenddb` use the configured endpoint for both APIs and dummy local credentials. A custom non-local table endpoint does not override the Streams endpoint.

Both SDK clients use a five-second overall call deadline, including retries, a 1500ms per-attempt timeout, and at most nine total attempts. Retry backoff uses full jitter with 25ms general and one-second throttling bases, capped at 20 seconds and bounded by the call deadline. Each client limits in-flight requests to 200 and acquisition to two seconds.

HTTP admission uses the async core/queue/burst limits independently of business-worker concurrency; saturation returns `503`. The async processing deadline also returns `503` and cancels the request context. Both use the standard JSON `error`/`message`/UTC `timestamp` envelope; middleware does not add custom response headers. SDK acquisition respects caller cancellation and the attempt deadline, so a request may time out before the two-second acquisition ceiling.

## Build and test verification

```sh
scripts/run-tests.sh --unit
scripts/run-tests.sh --integration
scripts/run-tests.sh --smoke
scripts/run-tests.sh --coverage
```

`scripts/run-tests.sh` selects all tiers by default using `go test -tags=integration,smoke ./...`. `--integration` and `--smoke` select genuine tagged suites. With no `AWS_ENDPOINT_URL_DYNAMODB`, the runner and direct tagged-test fixtures activate the module's DynamoDB Local Compose service and wait for readiness. An explicit loopback HTTP test endpoint reuses an already-running Local instance; the runner probes it and fails with guidance if unavailable rather than silently skipping. The fixture applies this endpoint to the application's table and Streams clients through `DYNAMODB_ENDPOINT`. Containers remain running for reuse. Before each integration test, the fixture deletes its unique UUID-named test table if present, waits for deletion, recreates the schema, and seeds and verifies all ten demo accounts. Cleanup deletes only that test-owned table; unrelated and application tables are never reset. Client configuration comes through the real `test` profile. Override the endpoint only for another disposable DynamoDB Local instance.

Unit tests use transport fixtures and do not need Docker or real AWS credentials. Integration tests exercise actual DynamoDB Local transactions and pagination. Smoke tests cover documentation access, stream-driven settlement, and expired-timer `REMOVE` release on disposable tables. DynamoDB Local does not run the managed AWS TTL scheduler: smoke tests explicitly delete an expired timer to exercise the stream/release path, not AWS TTL scheduling itself. Container-backed suites use dummy local credentials; never target an AWS production table. HTTP-only fixtures disable stream consumption; stream fixtures use `TRIM_HORIZON` to avoid `LATEST` timing races. The `test` profile uses HTTP async pools `4 / 8 / 50`, versus `10 / 50 / 100` normally.

Standalone `--coverage` uses pure fixture-based tests, instruments handwritten application code in `pkg/` and `cmd/main/`, prints function coverage, and requires at least 90% combined statement coverage. It does not require DynamoDB Local. Integration/smoke validation remains a separate prerequisite for a full operational check.

For focused real settlement and merchant pagination checks against a running local database:

```sh
scripts/start-dynamodb-local.sh
AWS_ENDPOINT_URL_DYNAMODB=http://localhost:8000 \
  go test -tags=integration ./pkg/service -run TestDynamoDB -v
```

These tests create and delete uniquely named tables. They check expired ACTIVE and already RELEASED holds, terminal-state replay, unchanged posted balances, absence of ledger debits, exact-decimal intent replay and conflict detection, hold/timer/ledger precision, duplicate release, fractional settlement ledger/account equality, and chronological pagination in both directions on both merchant GSIs.

For optional HTTP verification, start the app with seeded demo accounts, then run `make ijhttp` with the separately installed [JetBrains HTTP Client CLI](https://www.jetbrains.com/help/idea/http-client-cli.html), or import the Postman collection. Both create and process synthetic payments.

## Demo Flow

1. Create an outbound payment using `POST /api/v1/payments/outbound`; the idempotency key is part of the JSON body.
2. The `OUTBOUND_PAYMENT_CREATED` stream event processes the payment. The `POST /api/v1/payments/outbound/{paymentID}/process` endpoint runs the same processor for manual verification.
3. Read payment state and its event history with `GET /api/v1/payments/outbound/{paymentID}`.
4. Read current and available balances plus reservation history with `GET /api/v1/accounts/{accountID}`.
5. Use `GET /api/v1/merchants/{merchantID}/payments` and its `/state/{state}` variant to verify paginated views.

Both list routes default to 50 items when `limit` is omitted or non-positive, and cap positive limits at 100. Non-integer limits return `400 VALIDATION_ERROR`. Reservation amounts use the same currency units as payment amounts in both account and batch reservation responses.

Persisted pagination tokens must contain the complete GSI continuation key and match the merchant, state, route, page size, and direction. Tokens without a key, or in-memory offset tokens sent to a persisted backend, return `400 INVALID_PAGINATION_TOKEN`.

Payment heads use fixed-width UTC timestamps with nine fractional digits for `createdAtUtc`, so both merchant GSIs sort chronologically. When upgrading a populated table from variable-width timestamps, rewrite existing heads through the repository's `PutPayment` operation before relying on chronological ordering, and discard old pagination tokens. Stop application writes during this read/rewrite migration to avoid overwriting concurrent transitions. Preserve each head's full projection, including rejection `reasonCode`, so merchant lists remain consistent with event-based tracking.

Public/value timestamps use UTC precision groups of 0, 3, 6, or 9 fractional digits through `model.FormatTimestamp`. This does not change physical sort-key timestamps: payment-head `createdAtUtc` GSI values and newly constructed ledger sort keys intentionally retain fixed nine-digit precision for lexical ordering and compatibility. New lifecycle events write `occurredAt`; reads also accept the legacy `occurredAtUtc` attribute.

### Schema compatibility decisions

**Timestamp compatibility decision (2026-10-22):** following the project owner's direction to preserve correct ordering, retain fixed nine-digit UTC precision for payment-head `createdAtUtc` in both GSIs and newly constructed ledger sort keys. This is a scoped exception to specification Section 4.1's variable-precision rule for physical ordering fields, not a claim that the specification requires fixed width. Other value timestamps, including API responses, continue to use `model.FormatTimestamp` with the shortest precision group of 0, 3, 6, or 9 fractional digits that preserves the instant. For example, the whole second `2026-01-01T00:00:00Z` is stored in a physical timestamp key as `2026-01-01T00:00:00.000000000Z`. Decoded legacy ledger keys retain their original timestamp spelling while their instant is unchanged, preserving physical item identity. Apply this decision in subsequent implementation reviews. The specification and review skills remain unchanged.

Variable-width ISO-8601 strings do not preserve chronological byte order: `2026-01-01T00:00:00.001Z` sorts before `2026-01-01T00:00:00Z` even though it is later. Fixed precision avoids this whole-second boundary inversion in both merchant indexes and new ledger keys. Do not replace physical formatting with the value formatter without an agreed cross-SDK schema and migration plan. Regression tests assert literal encodings and chronological ordering across whole-second, millisecond, microsecond, and nanosecond boundaries, including paged reads in both directions through both GSIs.

The indexes use native DynamoDB multi-attribute keys, verified against DynamoDB Local by the integration suite. `GSI_MERCHANT_PAYMENTS` partitions by `merchantId` and sorts by `createdAtUtc`, then `paymentId`, providing deterministic ordering for equal timestamps. `GSI_MERCHANT_STATE_PAYMENTS` partitions by both `merchantId` and `aggregateState` and sorts by `createdAtUtc`. State queries constrain both partition attributes; neither index needs a synthetic `merchantState` attribute. Only payment heads carry all index key attributes.

**Existing-table upgrade:** the previous single-sort-key/synthetic-state indexes are incompatible. Startup verifies both native key schemas before seeding or serving requests and fails with migration guidance; it never deletes or rewrites a table. For a disposable demo, choose a new table name with provisioning enabled. For retained data, stop writers, back up the table, replace both GSIs with the schemas in [`cmd/main/table.go`](../cmd/main/table.go), and wait for both indexes to become `ACTIVE` before restarting. Existing heads already store `paymentId`, `merchantId`, and `aggregateState`; verify those attributes and fixed-width timestamps before rebuilding. Do not migrate during live writes. Old four-attribute pagination tokens are rejected: restart pagination after upgrade. An older Local version that rejects multi-key GSIs must be upgraded, not silently given a different schema.

**Account-overview access contract:** this implementation uses one `GetItem` for the account plus reservation-prefix `Query` pages. The specification's shared read-consistency contract explicitly identifies an eventual account `GetItem` for Story 5, and Story 5 scopes reservation reads to `RESERVATION#`. Both reads are eventual and do not promise a cross-item snapshot. A missing account returns not-found without querying reservations. This excludes timer/ledger history without a Scan or full-partition read. Accounts remain `PK=SK=ACCOUNT#{accountId}`; ledger entries remain in the account partition with `SK=LEDGER#{timestamp}#{entryId}`. Regression tests assert one account read, the prefix, and pagination. Section 2's single-Query summary is inconsistent with those details: `begins_with(SK, "RESERVATION#")` cannot return `SK=ACCOUNT#...`.

**Approved review exception (2026-10-22):** for this account-overview conflict only, the project owner authorized the Java implementation in [amazon-dynamodb/aws-dynamodb-examples#1](https://github.com/amazon-dynamodb/aws-dynamodb-examples/pull/1) as the source of truth, including subsequent reviews of this implementation. The inspected revision is `5f2ce2280013b2afde0ba89d8e8729dfec295a4e`. Its [enhanced-client account read](https://github.com/amazon-dynamodb/aws-dynamodb-examples/blob/5f2ce2280013b2afde0ba89d8e8729dfec295a4e/examples/sample-apps/instant-payments/java/src/main/java/software/amazon/awssdk/dynamodb/sampleapps/instantpayments/repository/HighLevelDynamoDbPaymentRepository.java#L208-L226) combines an eventual account `GetItem` with reservation-prefix `Query` pages, matching this Go access pattern. Assess that pattern as compliant under this explicit exception rather than failing it against Section 2's conflicting summary. This does not amend the specification or review skills, establish authority for other requirements, or automatically adopt future Java PR revisions.

Account reads remain eventually consistent in settlement and hold-release paths as well as account overview. Balance transactions retain account-version conditions and bounded conflict retries to handle stale reads. Payment partitions, idempotency bindings, and individual reservation reads retain their strongly consistent reads.

Key fields use the codecs in [`pkg/model/keys.go`](../pkg/model/keys.go): payment, account, idempotency, reservation, reservation timer, event sequence, and timestamp/ledger-entry keys. Pass logical values such as `model.PaymentKey("pay_123")` or `model.AccountKey("acc_usd_1")`, never already-prefixed storage values. Use `attributevalue.Marshal`/`MarshalMap` and `Unmarshal`/`UnmarshalMap` at DynamoDB boundaries so the codecs run; direct `AttributeValueMemberS` construction bypasses them. Invalid prefixes, empty suffixes, and malformed structured keys return errors. The fixed `#HEAD` and `IDEMPOTENCY` sort keys remain constants, and query prefix selectors are centralized alongside the codecs. The codecs preserve stored base-table key strings, HTTP IDs, and physical `eventKey` values; the separate native-index upgrade above changes continuation keys.

Decoded ledger keys retain their original timestamp spelling, including legacy variable precision or UTC offsets, so serializing them again targets the same DynamoDB item. Newly constructed keys, or decoded keys whose timestamp is changed, use fixed-width UTC timestamps with nine fractional digits.

Settlement retries reload payment/account state after transaction conflicts or stale optimistic-lock conditions, up to three attempts. An expired or released hold rejects the payment with `RESERVATION_EXPIRED` without posting a debit; the timer and audit reservation remain available for the release flow. Account reads preserve zero available balances and retrieve all reservation-history pages.

Account writes and seed creation use explicit `CurrentBalance` and `AvailableBalance` values, preserving zero and fractional balances without a legacy integer-balance fallback.

Numeric HTTP amounts retain their original decimal value before float64 conversion. Canonical hashing normalizes insignificant zeros and exponent spellings without merging distinct precise amounts. Payment heads, creation events, holds, timers, debits, tracking/list responses, reservation responses, and account balances preserve the exact decimal as a JSON/DynamoDB number. Programmatic Go callers can continue supplying `Amount`; when `AmountExact` is also supplied, the exact value is authoritative and both fields must describe the same amount.

Ledger `balanceAfter` uses the exact stored DynamoDB decimal balance and the same debit number as the account update, without a float64 subtraction. Optimistic account version checks keep this ledger snapshot consistent with the atomically updated account.

Persisted insufficient-funds decisions also compare the exact stored available balance with the debit number, so sub-float64 differences produce `INSUFFICIENT_FUNDS` rejection rather than repeated reservation conflicts.

Persistent payment requests replay into request-local aggregates and do not hold a service-wide mutex across DynamoDB I/O. Optimistic head/account conditions and bounded retries protect concurrent processing; only the in-memory demonstration backend needs its shared-state lock. HTTP and stream payment processing propagate caller cancellation through settlement reads, writes, and retry waits. Both SDK clients apply a five-second overall deadline per call, including SDK retries. Worker/stream shutdown closes admission first, drains accepted queued and running work within one ten-second grace period bounded by the lifecycle caller's deadline, then force-cancels unfinished work with an explicit timeout error.

A missing debtor during RECEIVED processing produces a durable `REJECTED` payment with reason `ACCOUNT_NOT_FOUND`, without reserving or debiting funds. Tracking the payment then reports the terminal rejection rather than a retryable missing-account failure.

The seed accounts are active: USD and EUR accounts `acc_*_1` through `acc_*_4` have balances of 10000, 5000, 500, and 100 respectively; account `acc_*_5` starts at zero.

## Operations and Troubleshooting

- Check `GET /actuator/health`; it executes a bounded DynamoDB request and returns `{"status":"healthy"}` on success or `{"status":"unhealthy"}` if the database is unavailable.
- Check application logs for the DynamoDB error code and stream shard ID. Stream records are not logged with their full images because those images can contain account and beneficiary data.
- The stream worker applies up to three attempts to a failed record and then logs a poison-record error. Expected `PAYMENT_NOT_FOUND` and `ACCOUNT_NOT_FOUND` outcomes log at DEBUG in the worker and during intermediate stream retries. Unexpected worker failures remain ERROR and unexpected stream retries remain WARN. Exhausted poison-record skips remain ERROR regardless of the original outcome. Inspect the payment and reservation state before manually retrying a failed flow.
- Stream discovery pages `DescribeStream` every one-second poll tick after an initial two-second delay. Successful checkpoints advance only after successful processing. A terminal poison-record skip advances the live shard iterator, not the successful checkpoint, so iterator renewal can retry skipped records after the last success. Both checkpoints and the 1024-entry retry LRU are process-local. Expired iterators resume after the successful checkpoint sequence; trimmed history falls back to the configured `LATEST`/`TRIM_HORIZON` policy. Recovery cannot restore records outside retention or guarantee automatic processing across restart; use manual `/process` for missed payment creation.
- Consumers filter `INSERT` payment-created events and `REMOVE` reservation timers in code. A timer removal can come from TTL expiry or normal settlement cleanup; already-consumed reservations are no-ops, not another debit.
- A payment head is stored at `PK=PAYMENT#{paymentId}, SK=#HEAD`; lifecycle events use ordered `EVENT#...` sort keys. Accounts use `PK=SK=ACCOUNT#{accountId}`.
- Idempotency records expire using DynamoDB TTL. TTL removal is asynchronous; the TTL value is not a precise deletion deadline.
- Enabled provisioning rejects an existing ENABLED/ENABLING TTL policy unless its attribute is exactly `ttl`; disable an incompatible policy and enable the required attribute before restarting. A missing TTL description also fails startup rather than reporting success.
- Durable idempotency records are authoritative: once TTL physically removes a binding, the same running app instance can accept that key again. Until then, retries replay the stored original response. Canonical hashes count Unicode characters before hashing the UTF-8 bytes.
- If table initialization reports a missing table while provisioning is disabled, provision the documented keys, GSIs, TTL attribute, and `NEW_IMAGE` stream using infrastructure-as-code, or enable provisioning for a local demo.

## Table Deletion

The normal build and run commands never delete tables. DynamoDB Local uses in-memory storage, so stopping its container discards local data.

Deleting the configured table is destructive. Run this helper only for a table you intend to remove:

```sh
scripts/delete-dynamodb-table.sh \
  --dynamodb-endpoint http://localhost:8000 \
  --dynamodb-region eu-west-1 \
  --table-name GoInstantPayments
```

The helper requires the AWS CLI and waits until the table is deleted.

For the local ExtendDB service, supply dummy signing credentials to the AWS CLI, for example `AWS_ACCESS_KEY_ID=AKIAlocal AWS_SECRET_ACCESS_KEY=local scripts/delete-dynamodb-table.sh`. Against AWS, use the SDK/CLI credential chain and verify endpoint, region, and table first: the helper deletes without a confirmation prompt. Normal start/build commands never delete tables.

## Deployment limitations

The API is an educational sample, not a production payment processor or banking-network integration. Before deployment add authentication, authorization, TLS, durable stream recovery, monitoring, appropriate money representations at API boundaries, and regulatory controls. Stream checkpoints are process-local; inspect logs and payment state and use the manual process endpoint for missed work after restart. TTL expiry is asynchronous and cannot guarantee exact hold-release timing. Stop Compose after demonstrations and explicitly delete unwanted AWS resources to avoid ongoing charges.

Follow the repository's [contribution guidelines](../../../../../CONTRIBUTING.md). The sample uses the repository's [Apache-2.0 license](../../../../../LICENSE.txt) and [notice](../../../../../NOTICE.txt).
