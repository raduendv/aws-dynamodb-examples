## Code Review

## **0 issues found.**

Review date: 2026-10-22  
Reviewed SDK: AWS SDK for Go v2, DynamoDB client v1.63.3  
Implementation: `/Users/radugribincea/projects/aws/sample-apps/aws-dynamodb-examples/examples/sample-apps/instant-payments/go`  
Specification: `/Users/radugribincea/projects/amazon-dynamodb/ddb-endava/DesignDocuments/Cross-SDKs/SampleApps/InstantPayments/[Issue523]SampleAppInstantPaymentsSpecification.md`  
Assigned scope: Complete specification review (Chapters 1 through 8)

## Status: **PASS**

| Priority | Issue count | Issues |
|---|---:|---|
| P0 (critical) | 0 | None |
| P1 (high) | 0 | None |
| P2 (medium) | 0 | None |
| P3 (low) | 0 | None |

## Issues

| Issue ID | Expected behavior | Current behavior | Impact | Recommended fix |
|---|---|---|---|---|

<details>
<summary>Traceability matrix for 172 assessed requirements, with 172 passing and 0 failing</summary>

## Traceability matrix

Owners below are the eight chapter scopes required by the unchanged review
orchestrator. `Source / repeats` identifies each requirement's first normative
section and the directly related repeated normative locations. Evidence labels
resolve in the evidence register below the matrix. The requirement descriptions
retain the previously established canonical IDs where fresh extraction confirmed
their distinct outcome.

| Requirement ID | Owner | Source / repeats | Status | Description | Fresh implementation evidence | Issues |
|---|---|---|---|---|---|---|
| 1.1-a | Overview and scope | §1.1. Repeats §§2-5 and §8. | PASS | Demonstrate atomic writes, conditional transitions, Streams, merchant indexes, TTL, optimistic concurrency, and one composite-key table. | E01 | |
| 1.1-b | Overview and scope | §1.1. Repeats §§3, 5.2, 6.1, 7.2, and §8. | PASS | Expose the idiomatic DynamoDB client styles that the SDK ships through startup configuration without changing HTTP behavior or transaction shapes. | E01. The SDK-dependent interpretation is recorded in Caveats. | |
| 1.2-a | Overview and scope | §1.2. Repeats §8 Stories 2 and 3. | PASS | Exclude settled reversal and caller-requested rejection after reservation while retaining the stated simplified scope. | E01, E06 | |
| 1.2-b | Overview and scope | §1.2. Repeats §§3 and 5.2.1. | PASS | Allow unrelated HTTP requests to progress while other requests await DynamoDB. | E01, E12 | |
| 2-a | Business capabilities | §2 Story 0. Repeats §8 Story 0 and §5.2.2. | PASS | Expose enabled OpenAPI and Swagger UI with no domain DynamoDB operations. | E02, E20 | |
| 2-b | Business capabilities | §2 Story 1. Repeats §§4.2, 5.1, and §8 Story 1. | PASS | Accept intent atomically, replay matching retries, reject conflicts, and retain deduplication for configured TTL. | E03, E11, E12 | |
| 2-c | Business capabilities | §2 Story 2. Repeats §§4.2, 5.4, and §8 Story 2. | PASS | Validate, reserve, complete, or reject safely under duplicate delivery and concurrency. | E04, E06, E11 | |
| 2-d | Business capabilities | §2 Story 3. Repeats §§4.2, 5.4, and §8 Story 3. | PASS | Release abandoned active holds without changing payment history. | E05, E06, E11 | |
| 2-e | Business capabilities | §2 Story 4. Repeats §§4.2, 5.1, and §8 Story 4. | PASS | Return payment state and ordered history through one logical read. | E06, E11, E12 | |
| 2-f | Business capabilities | §2 Story 5. Repeats §§4.2 and §8 Story 5. | PASS | Return account information and durable reservations without scans under the authorized Java-reference interpretation. | E07, E11 | |
| 2-g | Business capabilities | §2 Story 6. Repeats §§4.2, 5.1, and §8 Story 6. | PASS | Retrieve bounded selected reservations and distinguish found from missing identifiers. | E08, E11, E12 | |
| 2-h | Business capabilities | §2 Story 7. Repeats §§4.3, 5.1, and §8 Story 7. | PASS | List chronological merchant pages without base-table scans. | E09, E11, E12 | |
| 2-i | Business capabilities | §2 Story 8. Repeats §§4.3, 5.1, and §8 Story 8. | PASS | List state-scoped merchant pages without base-table scans. | E09, E11, E12 | |
| 3-a | Architecture | §3. Repeats §§1.2, 5.4, and §8 Stories 2-3. | PASS | Share one duplicate-safe processor between manual and stream paths. | E04, E06, E10 | |
| 3-b | Architecture | §3. Repeats §§2, 4.3, and §8 Stories 4-8. | PASS | Shape query responsibilities around payment, account, and merchant keys. | E07, E08, E09, E10 | |
| 3-c | Architecture | §3. Repeats §§1.1, 5.4, and §8 Stories 2-3. | PASS | Process only creation-event INSERTs and reservation-timer REMOVEs. | E06, E10 | |
| 3-d | Architecture | §3. Repeats §1.1 and §§5.2, 6.1, 7.2. | PASS | Keep business rules behind one persistence boundary and independent of the SDK access implementation. | E01, E10. The SDK-dependent interpretation is recorded in Caveats. | |
| 4.1-a | DynamoDB model | §4.1. Repeats §§5.3 and 8. | PASS | Use one string-key PAY_PER_REQUEST table with NEW_IMAGE, correct TTL attributes, language-qualified name, entityType, and index attributes. | E11, E13 | |
| 4.1-b | DynamoDB model | §4.1. Repeats §§4.2 and 8. | PASS | Use UTC grouped value timestamps and the retained fixed-width physical-ordering representation. | E11 | |
| 4.1-c | DynamoDB model | §4.1. Repeats §4.2 and §8. | PASS | Encode physical keys with reserved prefixes rather than storing raw user identifiers as complete keys. | E11 | |
| 4.2-a | DynamoDB model | §4.2 PaymentStreamHead. Repeats §8 Stories 1, 2, 4, 7, and 8. | PASS | Store the specified head keys, discriminator, sequence/state, timestamps, identifiers, amount/currency, and nullable reason. | E11 | |
| 4.2-b | DynamoDB model | §4.2 PaymentEvent. Repeats §8 Stories 1, 2, and 4. | PASS | Store event keys, 19-digit sequence, lifecycle metadata, and complete create-only intent. | E11 | |
| 4.2-c | DynamoDB model | §4.2 IdempotencyRecord. Repeats §§5.1-5.3 and §8 Story 1. | PASS | Store idempotency keys, discriminator, hash, response snapshot, creation time, and epoch TTL. | E03, E11, E13 | |
| 4.2-d | DynamoDB model | §4.2 Account. Repeats §§5.3 and 8 Stories 2, 3, and 5. | PASS | Store account keys, identity/status, numeric balances, currency, and version. | E11 | |
| 4.2-e | DynamoDB model | §4.2 Reservation. Repeats §8 Stories 2, 3, 5, and 6. | PASS | Store durable reservation keys, identifiers, amount/status/time, and expiresAt without TTL. | E11 | |
| 4.2-f | DynamoDB model | §4.2 Temporary reservation timer. Repeats §§5.4 and 8 Stories 2-3. | PASS | Store timer keys, identifiers, amount, ACTIVE status, time, and equal ttl/expiresAt. | E06, E11 | |
| 4.2-g | DynamoDB model | §4.2 LedgerEntry. Repeats §8 Story 2. | PASS | Store ledger keys, identifiers, DEBIT metadata, amount/balanceAfter, and posting time. | E11 | |
| 4.3-a | DynamoDB model | §4.3 GSI_MERCHANT_PAYMENTS. Repeats §8 Story 7. | PASS | Use merchantId HASH, createdAtUtc/paymentId RANGE, and ALL projection. | E11 | |
| 4.3-b | DynamoDB model | §4.3 GSI_MERCHANT_STATE_PAYMENTS. Repeats §8 Story 8. | PASS | Use merchantId/aggregateState HASH, createdAtUtc RANGE, and the specified seven-attribute INCLUDE projection. | E11 | |
| 5.1-a | Cross-cutting contracts | §5.1 Success. Repeats §8. | PASS | Return 200 for reads, replay, processing, batch reads, and merchant pages. | E12 | |
| 5.1-b | Cross-cutting contracts | §5.1 Success. Repeats §8 Story 1. | PASS | Return 201 for the first successful payment acceptance. | E12 | |
| 5.1-c | Cross-cutting contracts | §5.1 Error envelope. Repeats §8. | PASS | Return client-safe JSON error, message, and UTC timestamp outside host-stack exceptions. | E12 | |
| 5.1-d | Cross-cutting contracts | §5.1 Domain errors. Repeats §8 Stories 1, 2, 6, 7, and 8. | PASS | Return 400 VALIDATION_ERROR for invalid request syntax. | E12 | |
| 5.1-e | Cross-cutting contracts | §5.1 Domain errors. Repeats §8 Story 8. | PASS | Return INVALID_PAYMENT_STATE for well-formed unsupported states. | E12 | |
| 5.1-f | Cross-cutting contracts | §5.1 Domain errors and token scoping. Repeats §8 Stories 7-8. | PASS | Return INVALID_PAGINATION_TOKEN for invalid or wrongly scoped continuation tokens before querying. | E09, E12 | |
| 5.1-g | Cross-cutting contracts | §5.1 Domain errors. Repeats §8 Story 6. | PASS | Return INVALID_BATCH_GET_RESERVATIONS_REQUEST for valid-syntax batch domain failures. | E08, E12 | |
| 5.1-h | Cross-cutting contracts | §5.1 Domain errors. Repeats §8 Stories 2 and 4. | PASS | Return PAYMENT_NOT_FOUND for missing payment heads on tracking or processing. | E04, E06, E12 | |
| 5.1-i | Cross-cutting contracts | §5.1 Domain errors. Repeats §8 Story 5. | PASS | Return ACCOUNT_NOT_FOUND for a missing account overview row. | E07, E12 | |
| 5.1-j | Cross-cutting contracts | §5.1 Domain errors. Repeats §8 Story 1. | PASS | Return 409 IDEMPOTENCY_CONFLICT for a changed payload under a reused key. | E03, E12 | |
| 5.1-k | Cross-cutting contracts | §5.1 Domain errors. Repeats §8. | PASS | Return safe 500 INTERNAL_ERROR for unexpected application faults. | E12 | |
| 5.1-l | Cross-cutting contracts | §5.1 Dependency errors. Repeats §8. | PASS | Return 503 THROUGHPUT_EXCEEDED with Retry-After: 1. | E12 | |
| 5.1-m | Cross-cutting contracts | §5.1 Dependency errors. Repeats §8. | PASS | Return 503 REQUEST_LIMIT_EXCEEDED with Retry-After: 1. | E12 | |
| 5.1-n | Cross-cutting contracts | §5.1 Dependency errors. Repeats §8. | PASS | Return 503 TABLE_NOT_FOUND without Retry-After. | E12 | |
| 5.1-o | Cross-cutting contracts | §5.1 Dependency errors. Repeats §8. | PASS | Return 503 DYNAMODB_INTERNAL_ERROR with Retry-After: 1. | E12 | |
| 5.1-p | Cross-cutting contracts | §5.1 Async error classification. Repeats §8. | PASS | Unwrap asynchronous and SDK causes before dependency-fault classification. | E12 | |
| 5.1-q | Cross-cutting contracts | §5.1 Header contract. Repeats §8 body routes. | PASS | Use standard JSON headers and body-field idempotency on body routes. | E12 | |
| 5.1-r | Cross-cutting contracts | §5.1 Header contract. Repeats §8. | PASS | Add no application response headers beyond conditional Retry-After. | E12 | |
| 5.1-s | Cross-cutting contracts | §5.1 Favicon exception. Repeats §7 and §8 Story 0. | PASS | Return empty 204 for favicon without content type. | E12, E20 | |
| 5.1-t | Cross-cutting contracts | §5.1 Pagination token scoping. Repeats §8 Stories 7-8. | PASS | Encode and validate complete GSI continuation keys and merchant scope. | E09, E12 | |
| 5.2-a | Cross-cutting contracts | §5.2 DynamoDB connection. Repeats §§6.1, 6.2, 7.2, and 8. | PASS | Support specified endpoint, region, access-style values, and defaults. | E13 | |
| 5.2-b | Cross-cutting contracts | §5.2 DynamoDB provisioning. Repeats §§6.1, 6.2, 7.2. | PASS | Default resource provisioning to false. | E13 | |
| 5.2-c | Cross-cutting contracts | §5.2 Table policy. Repeats §§5.3 and 7.2. | PASS | Default idempotency retention to 2592000 seconds within inclusive 3600-31536000 bounds. | E13 | |
| 5.2-d | Cross-cutting contracts | §5.2 Table policy. Repeats §8 Story 2. | PASS | Default reservation timeout to 900 positive seconds. | E13 | |
| 5.2-e | Cross-cutting contracts | §5.2 Stream processing. Repeats §§1.2, 5.4, 6.3, and 7. | PASS | Default uncheckpointed iterators to LATEST and support TRIM_HORIZON. | E13, E14 | |
| 5.2-f | Cross-cutting contracts | §5.2 Stream processing. Repeats §§5.4 and 6.3. | PASS | Enable Streams by default and omit poller construction when disabled. | E13, E14 | |
| 5.2.1-a | Cross-cutting contracts | §5.2.1. Repeats §§6.2, 7.2, and 8. | PASS | Default the HTTP port to 8080. | E12, E13, E17 | |
| 5.2.1-b | Cross-cutting contracts | §5.2.1. Repeats §§6.2 and 7.2. | PASS | Gracefully drain HTTP requests within 30 seconds. | E12, E13 | |
| 5.2.1-c | Cross-cutting contracts | §5.2.1. Repeats §8 Story 2 and §7.2. | PASS | Set the async timeout to 10000ms above the five-second SDK call timeout. | E12, E13 | |
| 5.2.1-d | Cross-cutting contracts | §5.2.1. Repeats §6.3 and §7.2. | PASS | Use admission limits 10/50/100 normally and 4/8/50 under test. | E12, E13, E18 | |
| 5.2.2-a | Cross-cutting contracts | §5.2.2. Repeats §§7.5 and 8 Story 0. | PASS | Configure application/json as default produced and consumed API media. | E20 | |
| 5.2.2-b | Cross-cutting contracts | §5.2.2. Repeats §§7.5 and 8 Story 0. | PASS | Sort Swagger UI operations by HTTP method. | E20 | |
| 5.2.2-c | Cross-cutting contracts | §5.2.2. Repeats §§7.5 and 8 Story 0. | PASS | Provide fixed OpenAPI info, contact, and server metadata. | E20 | |
| 5.2.3-a | Cross-cutting contracts | §5.2.3. Repeats §§6.2 and 7.2. | PASS | Accept the five canonical DynamoDB environment-variable names. | E13, E17 | |
| 5.2.4-a | Cross-cutting contracts | §5.2.4. Repeats §§6.2 and 7.2. | PASS | Select local fake credentials for four named hosts and the default chain elsewhere. | E13 | |
| 5.3-a | Cross-cutting contracts | §5.3 Startup validation. Repeats §7.2. | PASS | Fail startup on missing or blank required connectivity values. | E13 | |
| 5.3-b | Cross-cutting contracts | §5.3 Provisioning. Repeats §§4.1, 6.2, and 7.2. | PASS | Create the schema and accept concurrent existing-table creation when provisioning is enabled. | E13 | |
| 5.3-c | Cross-cutting contracts | §5.3 TTL. Repeats §§4.1 and 7.2. | PASS | Enable TTL on ttl and accept already-enabled state only for that attribute. | E13 | |
| 5.3-d | Cross-cutting contracts | §5.3 Seed data. Repeats §§4.2 and 7.2. | PASS | Conditionally seed ten ACTIVE version-one accounts without overwriting balances. | E13 | |
| 5.3-e | Cross-cutting contracts | §5.3 Managed mode. Repeats §7.2. | PASS | Verify managed-table existence with actionable failure and no create, TTL, or seed writes. | E13 | |
| 5.4-a | Cross-cutting contracts | §5.4 Filter/dual-source. Repeats §8 Stories 2-3. | PASS | Route expiry and completion timer removals to one release path that skips consumed holds. | E06, E14 | |
| 5.4-b | Cross-cutting contracts | §5.4 Poll cadence. Repeats §7.2. | PASS | Poll after about 2s, then every 1000ms with limit 100 and at most 512 rounds per shard/tick. | E14 | |
| 5.4-c | Cross-cutting contracts | §5.4 Shard discovery. Repeats §1.2 and §7.2. | PASS | Discover at most 100 shards per page and prune stale checkpoints. | E14 | |
| 5.4-d | Cross-cutting contracts | §5.4 Iterator renewal. Repeats §1.2 and §7.2. | PASS | Reopen expired iterators after the last successful sequence. | E14 | |
| 5.4-e | Cross-cutting contracts | §5.4 Trimmed data. Repeats §§1.2, 6.3, and 7.1. | PASS | Fall back to configured LATEST or TRIM_HORIZON after trimmed data. | E14 | |
| 5.4-f | Cross-cutting contracts | §5.4 Shard ordering. Repeats §8 Story 2. | PASS | Preserve per-shard sequence without assuming global order. | E14 | |
| 5.4-g | Cross-cutting contracts | §5.4 Poison-pill handling. Repeats §1.2. | PASS | Retry poison records three times with namespaced, access-ordered 1024-entry LRU bookkeeping. | E14 | |
| 5.4-h | Cross-cutting contracts | §5.4 Sequence advancement. Repeats §1.2. | PASS | Advance successful checkpoints only after record processing succeeds. | E14 | |
| 5.4-i | Cross-cutting contracts | §5.4 Lifecycle. Repeats §5.2.1. | PASS | Give admitted stream work up to ten seconds before forced cancellation. | E14 | |
| 5.4-j | Cross-cutting contracts | §5.4 Named constants. Repeats §7.3. | PASS | Define tuning values as named constants with rationale. | E14 | |
| 5.5-a | Cross-cutting contracts | §5.5 PII rule. Repeats §1.2. | PASS | Keep regulated payment PII out of INFO and higher logs. | E15 | |
| 5.5-b | Cross-cutting contracts | §5.5 Expected outcomes. Repeats §§2, 8, and §1.2. | PASS | Keep expected missing, terminal, expiry, and initialization outcomes below WARN while permitting conflict WARN. | E15 | |
| 6.1-a | Build and operations | §6.1 Script conventions. Repeats §§7.2 and 7.6. | PASS | Use Bash strict mode and project-relative paths except CLI-only helper. | E16 | |
| 6.1-b | Build and operations | §6.1 build-app.sh. Repeats §7.2. | PASS | Build the application and run unit tests unless --skip-tests is supplied. | E16, E18 | |
| 6.1-c | Build and operations | §6.1 run-app-local.sh. Repeats §7.2. | PASS | Support local endpoint, region, and client-type options with specified defaults. | E16 | |
| 6.1-d | Build and operations | §6.1 run-app-docker.sh. Repeats §§6.2.2 and 7.2. | PASS | Build/start Compose, support stop, and validate client selection. | E16, E17 | |
| 6.1-e | Build and operations | §6.1 run-tests.sh. Repeats §6.3 and §7.2. | PASS | Support unit, integration, and smoke selectors with all tiers by default. | E16, E18 | |
| 6.1-f | Build and operations | §6.1 start-dynamodb-local.sh. Repeats §§6.2.2 and 7.2. | PASS | Start the module Compose service and wait for DynamoDB Local readiness. | E16, E17 | |
| 6.1-g | Build and operations | §6.1 stop-dynamodb-local.sh. Repeats §§6.2.2 and 7.2. | PASS | Stop the module Compose service and wait for completion. | E16, E17 | |
| 6.1-h | Build and operations | §6.1 delete-dynamodb-table.sh. Repeats §7.2. | PASS | Support required endpoint, region, and table options and wait for deletion. | E16 | |
| 6.1-i | Build and operations | §6.1 Script inventory. Repeats §7.6. | PASS | Provide only the seven permitted operational scripts. | E16 | |
| 6.2.1-a | Build and operations | §6.2.1. Repeats §§7.2 and 7.7. | PASS | Use patch-pinned multistage Docker with one artifact, slim non-root runtime, port, and entry point. | E17 | |
| 6.2.2-a | Build and operations | §6.2.2. Repeats §§5.2.3, 7.2, and 7.7. | PASS | Configure required Compose services, image pinning, storage, ports, profile/dependency, variables, and security. | E17 | |
| 6.3-a | Build and operations | §6.3 unit tier. Repeats §§7.2 and 7.4. | PASS | Keep unit tests Docker-free with pure logic and mocks. | E18 | |
| 6.3-b | Build and operations | §6.3 integration tier. Repeats §§7.2 and 7.4. | PASS | Use Local/test configuration and delete, recreate, and reseed each isolated test table. | E18 | |
| 6.3-c | Build and operations | §6.3 smoke tier. Repeats §§7.2 and 7.4. | PASS | Cover docs, real stream settlement, and expired-timer release while disclosing Local TTL limitations. | E18 | |
| 6.3-d | Build and operations | §6.3 HTTP-only controls. Repeats §5.2. | PASS | Disable Streams per HTTP-only fixture without disabling asynchronous test suites. | E18 | |
| 6.3-e | Build and operations | §6.3 iterator controls. Repeats §§5.2 and 5.4. | PASS | Select TRIM_HORIZON for stream tests. | E18 | |
| 7.1-a | Documentation | §7.1. Repeats §§7.1.1-7.1.2. | PASS | Separate workload and implementation README scopes without duplicating long prose. | E19 | |
| 7.1.1-a | Documentation | §7.1.1 structure | PASS | Order workload README headings as Summary, Why DynamoDB?, and Endpoints under the title. | E19 | |
| 7.1.1-b | Documentation | §7.1.1 Summary. Repeats §§1.1-1.2. | PASS | Explain scope, model, lifecycle, memory-only checkpoints, restart risk, and recovery. | E19 | |
| 7.1.1-c | Documentation | §7.1.1 Why DynamoDB? Repeats §§1-5. | PASS | Explain capabilities, stream filtering, and discard costs. | E19 | |
| 7.1.1-d | Documentation | §7.1.1 Endpoints | PASS | List all seven endpoint subsections in required order. | E19 | |
| 7.1.1-e | Documentation | §7.1.1 endpoint content | PASS | Describe each endpoint purpose, capability, and key behavior. | E19 | |
| 7.1.1-f | Documentation | §7.1.1 content limits | PASS | Keep workload README within its specified content contract. | E19 | |
| 7.1.2-a | Documentation | §7.1.2 structure | PASS | Use required Go README title and heading order. | E19 | |
| 7.1.2-b | Documentation | §7.1.2 opening. Repeats §7.1. | PASS | Identify Go, framework, and SDK and link to workload overview. | E19 | |
| 7.1.2-c | Documentation | §7.1.2 features table | PASS | Use Feature, Where it's used, and Code pointers columns. | E19 | |
| 7.1.2-d | Documentation | §7.1.2 features table | PASS | Link feature descriptions to relevant local implementation locations. | E19 | |
| 7.1.2-e | Documentation | §7.1.2 Runbook | PASS | Provide a short pointer to the Go runbook. | E19 | |
| 7.1.2-f | Documentation | §7.1.2 content limits | PASS | Keep Go README within its specified content contract. | E19 | |
| 7.2-a | Documentation | §7.2 structure | PASS | Use the required Runbook title and initial sections. | E19 | |
| 7.2-b | Documentation | §7.2 Prerequisites | PASS | Use exactly Need and When columns in prerequisites table. | E19 | |
| 7.2-c | Documentation | §7.2 Prerequisites. Repeats §7.2 Tech stack. | PASS | Identify runtime/tools and require AWS credentials only for remote AWS. | E19 | |
| 7.2-d | Documentation | §7.2 Tech stack. Repeats §7.8. | PASS | Identify declared versions, framework, access styles, docs, testing, and datastore options. | E19 | |
| 7.2-e | Documentation | §7.2 Quick start Build | PASS | Provide the primary build command. | E19 | |
| 7.2-f | Documentation | §7.2 Run modes | PASS | Present host/Local, full Docker, and host/AWS modes in order. | E19 | |
| 7.2-g | Documentation | §7.2 API documentation | PASS | Provide local OpenAPI and interactive documentation URLs. | E19 | |
| 7.2-h | Documentation | §7.2 Run modes. Repeats §§6.1-6.2. | PASS | Explain use cases and ready-to-run commands, syntax, options, defaults, and notes. | E19 | |
| 7.2-i | Documentation | §7.2 bootstrap | PASS | Explain provisioning modes, table creation, Streams, TTL, and demo seeds. | E19 | |
| 7.2-j | Documentation | §7.2 implementation accuracy. Repeats §7.8. | PASS | Match runbook commands, configuration, behavior, and documented compatibility choices to implementation. | E19 | |
| 7.3-a | Documentation | §7.3 source docs | PASS | Document Go declarations' responsibilities and relevant inputs, outputs, failures, effects, and boundaries. | E01, E19 | |
| 7.3-b | Documentation | §7.3 inline comments | PASS | Explain non-obvious persistence, concurrency, retry, and recovery choices. | E01, E14, E19 | |
| 7.3-c | Documentation | §7.3 configuration docs | PASS | Explain configuration/startup defaults, validation, selection, and runtime effects. | E13, E19 | |
| 7.4-a | Documentation | §7.4 suites | PASS | Briefly describe test suite coverage. | E18 | |
| 7.4-b | Documentation | §7.4 test names | PASS | Name test operation, condition, and expected outcome. | E18 | |
| 7.4-c | Documentation | §7.4 helpers | PASS | Document reusable fixture mechanics and non-obvious test behavior. | E18 | |
| 7.5-a | Documentation | §7.5 API artifact. Repeats §5.2.2. | PASS | Keep OpenAPI in a standard local artifact. | E20 | |
| 7.5-b | Documentation | §7.5 operations | PASS | Document accurate operation summaries, descriptions, and response statuses. | E20 | |
| 7.5-c | Documentation | §7.5 route contract. Repeats §5.1 and §8. | PASS | Document methods, paths, parameters, bodies, validation, and headers. | E20 | |
| 7.5-d | Documentation | §7.5 responses. Repeats §5.1. | PASS | Document response meanings, media, schemas, and middleware 503 errors. | E20 | |
| 7.6-a | Documentation | §7.6 script headers. Repeats §6.1. | PASS | Document usage, defaults, prerequisites, failures, destructive effects, and readiness. | E16, E19 | |
| 7.6-b | Documentation | §7.6 focused scripts. Repeats §6.1. | PASS | Give the seven supported scripts distinct task-focused names and headers. | E16 | |
| 7.6-c | Documentation | §7.6 workflow consistency. Repeats §§6.1-6.2. | PASS | Keep Runbook workflows consistent with scripts, Dockerfile, and Compose. | E16, E17, E19 | |
| 7.7-a | Documentation | §7.7 Dockerfile. Repeats §6.2.1. | PASS | Explain stages, pinning, and non-root runtime decisions. | E17 | |
| 7.7-b | Documentation | §7.7 Compose. Repeats §§5.2.3 and 6.2.2. | PASS | Make Compose service and operational/security settings explicit. | E17 | |
| 7.8-a | Documentation | §7.8 consistency. Repeats §§6-7. | PASS | Resolve documentation references and claims without stale or conflicting guidance under the recorded interpretations. | E19 | |
| 7.8-b | Documentation | §7.8 version test. Repeats §7.2. | PASS | Test Runbook dependency versions against manifests/build files. | E18, E19 | |
| 8-a | User stories | §8 Story 0. Repeats §§5.2.2 and 7.5. | PASS | Redirect Swagger entry to interactive UI and serve docs with 200 when enabled. | E02, E20 | |
| 8-b | User stories | §8 Story 0. Repeats §5.1. | PASS | Keep documentation access free of domain DynamoDB reads, writes, and mutations. | E02, E20 | |
| 8-c | User stories | §8 Story 1. Repeats §5.1. | PASS | Accept specified idempotency, merchant, debtor, creditor, currency strings, and numeric amount. | E03, E12 | |
| 8-d | User stories | §8 Story 1. Repeats §4.2. | PASS | Return paymentId, state, correlationId, and UTC createdAtUtc on create. | E03, E12 | |
| 8-e | User stories | §8 Story 1. Repeats §4.2. | PASS | Order create writes as head, event, and idempotency record at indices 0/1/2. | E03, E11 | |
| 8-f | User stories | §8 Story 1. Repeats §§4.2 and 5.1. | PASS | Guard create only with idempotency, avoid payment writes on duplicates, and replay with strong read. | E03, E11 | |
| 8-g | User stories | §8 Story 1. Repeats §§4.2 and 5.1. | PASS | Hash normalized exact amount and seven ordered length-prefixed UTF-8 fields to lowercase SHA-256. | E03, E11 | |
| 8-h | User stories | §8 Story 1. Repeats §§4.2 and 5.1. | PASS | Replay the original stored response and timestamp on matching retries. | E03, E11 | |
| 8-i | User stories | §8 Story 2. Repeats §§3, 4.2, and 5.4. | PASS | Share processing, replay/validate/resume reserved payments, and skip terminal states. | E04, E06, E11 | |
| 8-j | User stories | §8 Story 2. Repeats §§4.2 and 5.1. | PASS | Persist REJECTED/ACCOUNT_NOT_FOUND when debtor account is missing before reservation. | E04, E11 | |
| 8-k | User stories | §8 Story 2. Repeats §5.1. | PASS | Accept a bodyless manual process request. | E04, E12 | |
| 8-l | User stories | §8 Story 2. Repeats §5.1. | PASS | Return paymentId, state, and nullable reasonCode from processing. | E04, E12 | |
| 8-m | User stories | §8 Story 2 path validation. Repeats Stories 4-8. | PASS | Validate ASCII path identifiers and 64-character maximum before repository access. | E12 | |
| 8-n | User stories | §8 Story 2 reserve. Repeats §§4.2 and 5.4. | PASS | Order five reserve writes with balance, version, and head guards. | E04, E11 | |
| 8-o | User stories | §8 Story 2 complete. Repeats §§4.2 and 5.4. | PASS | Order six completion writes with account, reservation, timer, ledger, head, and event guards. | E04, E11 | |
| 8-p | User stories | §8 Story 2 reject/expiry. Repeats Story 3. | PASS | Reject without money rows and prevent debit after reservation expiry. | E04, E05, E11 | |
| 8-q | User stories | §8 Story 2 account guards. Repeats Story 3. | PASS | Guard and increment versions for money writes and apply specified balance movements. | E04, E05, E11 | |
| 8-r | User stories | §8 Story 2 retry strategy. Repeats Story 3 and Story 6. | PASS | Use bounded conflict, read-skew, release, and batch-retry schedules. | E04, E05, E08 | |
| 8-s | User stories | §8 Story 2 SDK policy. Repeats §§5.2.1 and 7.2. | PASS | Use nine SDK attempts, specified jitter, 1500ms attempts, five-second calls, 200 connections, and two-second acquisition. | E13 | |
| 8-t | User stories | §8 Story 3. Repeats §§4.2 and 5.4. | PASS | Release ACTIVE holds without history changes and skip missing or terminal holds. | E05, E06, E11 | |
| 8-u | User stories | §8 Story 3 release transaction. Repeats §5.4. | PASS | Order reservation/account release and distinguish terminal skips from bounded concurrency retries. | E05, E11 | |
| 8-v | User stories | §8 Story 4. Repeats §§4.2 and 5.1. | PASS | Return required payment identity/state/time/intent, amount/version, nullable reason, and ordered event metadata. | E06, E11, E12 | |
| 8-w | User stories | §8 Story 4 read consistency. Repeats Stories 1, 2, 5-8. | PASS | Use strong reads for idempotency, payment, and reservation and eventual reads for account/GSI. | E06, E07, E09 | |
| 8-x | User stories | §8 Story 4 replay invariant. Repeats Story 2. | PASS | Require replay count and state to match head after bounded re-reads or fail. | E04, E06 | |
| 8-y | User stories | §8 Story 5. Repeats §§4.2 and 5.1. | PASS | Return account identity/status/currency, balances, and required reservation fields. | E07, E11, E12 | |
| 8-z | User stories | §8 Story 5. Repeats §§4.2 and 5.1. | PASS | Preserve exact stored account balance decimals without recalculation. | E07, E11 | |
| 8-aa | User stories | §8 Story 6. Repeats §5.1. | PASS | Accept reservationIds as a string array in the request body. | E08, E12 | |
| 8-ab | User stories | §8 Story 6. Repeats §5.1. | PASS | Accept 1-100 nonblank IDs, preserve deduplication order, allow all misses, and define empty-distinct errors. | E08, E12 | |
| 8-ac | User stories | §8 Story 6. Repeats §§4.2 and 5.1. | PASS | Return required reservation objects and missingReservationIds. | E08, E11, E12 | |
| 8-ad | User stories | §8 Story 6. Repeats Story 2 retry strategy. | PASS | Read requested keys only and resolve UnprocessedKeys with bounded backoff before response assembly. | E08, E11 | |
| 8-ae | User stories | §8 Story 7. Repeats Story 8. | PASS | Default merchant pages to 50 newest-first, support reverse order/empty pages, and conditionally continue. | E09, E12 | |
| 8-af | User stories | §8 Story 7. Repeats §5.1. | PASS | Provide a bodyless merchant GET with path and typed pagination queries. | E09, E12 | |
| 8-ag | User stories | §8 Story 7. Repeats §§4.2-4.3. | PASS | Return complete projected fields with state/version mapping and optional nonterminal token. | E09, E11 | |
| 8-ah | User stories | §8 Story 8. Repeats §§4.3 and 5.1. | PASS | Match state case-insensitively, allow empty pages, and reject unknown states. | E09, E12 | |
| 8-ai | User stories | §8 Story 8. Repeats §5.1. | PASS | Provide a bodyless state-list GET with merchant/state paths and typed pagination queries. | E09, E12 | |

### Evidence register

The evidence below was freshly inspected in this review. Line ranges identify
the implementation source or test that controls each referenced behavior.

| Evidence | Implementation evidence |
|---|---|
| E01 | `pkg/env/env.go:49-120`, `pkg/aws/config.go:27-94`, `pkg/aws/module.go:9-19`, `pkg/aws/dynamodb.go:11-21`, `pkg/service/payment.go:463-774`, `pkg/aws/repository.go:108-143,507-755`, `cmd/main/table.go:9-36`, `pkg/http/server.go:39-115`. |
| E02 | `pkg/http/api/apidocs.go:20-63`, `pkg/http/api/swagger_ui.go:17-61`, `pkg/http/api/module.go:16-50`, `cmd/main/documentation_test.go:1-99`, `cmd/main/smoke_test.go`. |
| E03 | `pkg/service/payment.go:197-275,463-578`, `pkg/aws/repository.go:108-143,473-505`, `pkg/model/idempotency_record.go`, `pkg/util/sha256_hash.go`, `pkg/dto/outbound_payment.go:12-77`, `pkg/service/payment_test.go`, `pkg/service/payment_persistence_test.go`. |
| E04 | `pkg/service/payment.go:596-774`, `pkg/aws/repository.go:507-755`, `pkg/http/api/v1/operations/process.go:15-41`, `pkg/workerpool/job/payment_job.go`, `pkg/service/payment_persistence_test.go`, `pkg/service/payment_integration_test.go`. |
| E05 | `pkg/service/payment.go:899-935`, `pkg/aws/repository.go:756-847`, `pkg/workerpool/job/account_job.go`, `pkg/workerpool/job/indetify_task.go`, `pkg/service/payment_integration_test.go`. |
| E06 | `pkg/aws/repository.go:412-472`, `pkg/service/payment.go:389-419,775-801`, `pkg/http/api/v1/payments/query.go:23-53`, `pkg/aws/stream_processor.go:399-450`, `pkg/service/payment_recovery_test.go`. |
| E07 | `pkg/aws/repository.go:222-250,275-320`, `pkg/service/payment.go:802-841`, `pkg/http/api/v1/accounts/query.go:16-44`, `pkg/service/payment_persistence_test.go:169-227`. The account operation interpretation is recorded in Caveats. |
| E08 | `pkg/aws/repository.go:321-367`, `pkg/service/payment.go:842-898`, `pkg/http/api/v1/accounts/query.go:45-79`, `pkg/service/payment_recovery_test.go:537-622`, `pkg/aws/repository_branches_test.go:175-225`. |
| E09 | `pkg/aws/repository.go:150-207`, `pkg/service/payment.go:936-1120`, `pkg/http/api/v1/payments/query.go:55-149`, `pkg/service/payment_multikey_integration_test.go`, `pkg/service/payment_recovery_test.go:345-421`. |
| E10 | `pkg/http/api/v1/module.go:1-24`, `pkg/service/module.go`, `pkg/aws/module.go:9-19`, `pkg/workerpool/module.go`, `pkg/aws/stream_processor.go:484-567`, `pkg/workerpool/job/indetify_task.go`. |
| E11 | `pkg/model/keys.go:1-238`, `pkg/model/timestamp.go:1-20`, `pkg/model/payment.go`, `pkg/model/payment_event.go`, `pkg/model/account.go`, `pkg/model/idempotency_record.go`, `pkg/model/settlement.go`, `pkg/model/indexes.go`, `cmd/main/table.go:9-36`, `pkg/aws/encoding.go:15-55`, `pkg/aws/repository.go:108-143,412-847`. |
| E12 | `pkg/http/api/v1/internal/request/request.go`, `pkg/http/api/v1/internal/response/response.go`, `pkg/http/api/v1/payments/outbound.go:22-65`, `pkg/http/api/v1/payments/outbound_by_id.go:17-33`, `pkg/http/api/v1/operations/process.go:15-41`, `pkg/http/api/v1/accounts/query.go:16-83`, `pkg/http/api/v1/payments/query.go:23-149`, `pkg/http/server.go:115-190`, and the adjacent handler tests. |
| E13 | `pkg/env/env.go:10-120`, `pkg/aws/config.go:27-94`, `cmd/main/main.go:29-318`, `cmd/main/table.go:9-36`, `pkg/aws/dynamodb.go:11-21`, `pkg/env/validation_test.go`, `pkg/aws/config_validation_test.go`, `pkg/aws/config_timeout_test.go`. |
| E14 | `pkg/aws/stream_processor.go:46-68,125-185,203-380,399-475,484-567`, `pkg/workerpool/job/indetify_task.go`, `pkg/aws/stream_processor_test.go`, `pkg/workerpool/job/identify_job_test.go`. |
| E15 | `pkg/log/log.go`, `pkg/service/payment.go`, `pkg/aws/repository.go`, `pkg/aws/stream_processor.go`, `pkg/workerpool/job/account_job.go`, `pkg/workerpool/job/payment_job.go`, `pkg/workerpool/worker_logging_test.go`, `pkg/log/log_test.go`. |
| E16 | `scripts/build-app.sh`, `scripts/run-app-local.sh`, `scripts/run-app-docker.sh`, `scripts/run-tests.sh`, `scripts/start-dynamodb-local.sh`, `scripts/stop-dynamodb-local.sh`, `scripts/delete-dynamodb-table.sh`, `cmd/main/scripts_test.go`. |
| E17 | `Dockerfile:1-20`, `docker-compose.yml:1-44`, `docs/Runbook.md:42-152`, `cmd/main/smoke_test.go`. |
| E18 | `cmd/main/*_test.go`, `internal/testutil/dynamodb.go`, `pkg/**/*_test.go`, `scripts/run-tests.sh`, `scripts/build-app.sh`, `docs/Runbook.md:180-246`. Fresh executions are recorded under Fresh validation. |
| E19 | `../README.md:1-38`, `README.md:1-18`, `docs/Runbook.md:1-286`, Go doc comments across `pkg/` and `cmd/main/`, script headers, `cmd/main/documentation_test.go:1-99`. |
| E20 | `pkg/http/api/swagger.json:1-1022`, `pkg/http/api/apidocs.go:20-63`, `pkg/http/api/swagger_ui.go:17-61`, `pkg/http/api/handlers_test.go:71-182`, `pkg/http/api/swagger_ui_test.go`. |

</details>

## Fresh review reconciliation

All eight chapter scopes were applied directly as permitted by the orchestrator.
The unchanged orchestrator, all eight chapter `SKILL.md` files, and all seven
referenced shared resources were read. The full current specification and
relevant implementation source, configuration, tests, scripts, interface
definitions, and documentation were inspected. No secret environment file was
read. The fresh extraction retained 172 canonical requirements because review
of the full source and repeated story criteria did not establish a justified
additional row or an unsupported canonical row. A provisional finding against
1.1-b and 3-d was withdrawn after rechecking the SDK-dependent qualification in
specification §1.1 against the two-client wording in §3. The unchanged Go
implementation uses one native SDK client with mapping helpers. The absence of
two separately wired Go implementations does not establish an omitted
SDK-shipped client. Earlier PASS outcomes were therefore not overturned by an
unsupported requirement for additional implementation paths. The exact wording,
evidence, and interpretation are recorded in the final caveats.

## Fresh validation

The parent session freshly ran these checks against the current implementation.
All exited successfully:

```bash
AWS_ENDPOINT_URL_DYNAMODB=http://localhost:8000 \
  go test -race -count=1 -tags=integration,smoke ./...
go vet ./...
scripts/run-tests.sh --coverage
bash -n scripts/*.sh
git diff --check
git diff --cached --check
```

Coverage was **92.74%**, above the **90%** minimum. This report records the
parent-run results and does not duplicate those executions. It changes no
implementation, specification, or external review skill.

The parent session compared its before/after fingerprints and verified that
the external specification, all protected external skill files, tracked
implementation files excluding this report, and the staged diff were unchanged.
Only the requested report was edited.

## Caveats

### Subsequent local database replacement

On 2026-10-23 the Compose database image was replaced with digest-pinned
ExtendDB dev. This is a user-directed change from the specification's explicit
Amazon DynamoDB Local image requirement. The historical PASS table above is
not a fresh compliance assessment of that replacement.

The complete unit, integration and smoke suites passed against an isolated
ExtendDB instance using `AWS_ENDPOINT_URL_DYNAMODB`. This verifies the exercised
application behavior, not complete equivalence with Amazon DynamoDB or
DynamoDB Local. Existing TTL scheduling caveats describe the earlier Local
validation and do not establish ExtendDB scheduler behavior.

### SDK-dependent access-style interpretation

Specification §1.1, line 68, states:

> A runnable exposes the interchangeable DynamoDB access styles that its SDK offers (for example a lower-level operation assembly, a document-level model, and a higher-level mapping to application records), each selected at startup, without changing HTTP behaviour or transactional shape. The exact set of styles depends on the SDK, so an implementation exposes one selectable access style for every idiomatic DynamoDB client that SDK ships. This is an operator concern, not part of the API contract.

Specification §3, line 122, states:

> **Repository boundary.** All **DynamoDB** access sits behind one persistence boundary. Business rules stay independent of the two interchangeable client implementations: `high-level` enhanced mapping and `low-level` request assembly.

Section 3 explicitly describes two implementations, but it does not name two
Go SDK clients. Section 1.1 expressly qualifies the required set by the clients
the selected SDK ships. Reading the two together does not require inventing a
Go enhanced client, duplicating repository code, or introducing a branch merely
because two configuration values exist.

The fresh evidence is `go.mod:6-11`, which declares the Go v2 DynamoDB client
and attributevalue mapping package, `pkg/aws/dynamodb.go:11-13`, which constructs
the native client, `pkg/aws/module.go:9-17`, which wires one repository, and
`pkg/aws/config.go:65-66`, which validates both selector values.
`docs/Runbook.md:20` explicitly discloses that both values use the same repository
and typed codecs rather than separate clients or an enhanced-client API.
Mapping helpers are not a second SDK client.

The review did not establish a missing idiomatic Go SDK client. The provisional
failure inferred that distinct branches were required from the selector labels
and the unqualified architecture wording. That inference exceeded §1.1's
SDK-dependent scope, so it was withdrawn rather than treated as a new defect
in unchanged source. PASS does not claim that the two selector values execute
different code paths.

### Account overview interpretation

The account overview is a logical read path, not a guarantee of one DynamoDB
request. Go performs an eventually consistent account `GetItem` followed by
reservation-prefix `Query` pages, without a scan. The user explicitly authorized
[amazon-dynamodb/aws-dynamodb-examples#1](https://github.com/amazon-dynamodb/aws-dynamodb-examples/pull/1)
at revision
[`5f2ce2280013b2afde0ba89d8e8729dfec295a4e`](https://github.com/amazon-dynamodb/aws-dynamodb-examples/commit/5f2ce2280013b2afde0ba89d8e8729dfec295a4e)
as the source of truth for this account case. Its enhanced Java account read
[`queryAccountPartition`](https://github.com/amazon-dynamodb/aws-dynamodb-examples/blob/5f2ce2280013b2afde0ba89d8e8729dfec295a4e/examples/sample-apps/instant-payments/java/src/main/java/software/amazon/awssdk/dynamodb/sampleapps/instantpayments/repository/HighLevelDynamoDbPaymentRepository.java#L208-L226)
combines eventual account `GetItem` with reservation-prefix queries, matching
the Go implementation. This authorized interpretation is not a new failure or
an unapproved exception to the review rules.

### Physical timestamp compatibility decision

The user directed retaining fixed-nine-digit UTC precision for physical payment
head ordering fields and newly constructed ledger keys. API/value timestamps
continue to use 0, 3, 6, or 9 fractional digits. This preserves lexical
chronology across precision boundaries. It is a compatibility decision, not a
claim of byte-for-byte identity with the specification's variable-fraction
wording or with Java physical key formatting. Existing-data compatibility and
migration considerations remain in `docs/Runbook.md`. No change to the
specification or implementation was made for this decision.

### Runtime and environment boundaries

The fresh race integration/smoke suites, vet, coverage gate, shell syntax, and
tracked diff checks passed as listed above. Managed AWS TTL scheduling and
remote AWS deployment were not exercised. DynamoDB Local does not run the
managed TTL scheduler. The smoke path explicitly deletes an expired timer and
verifies the real `REMOVE`-driven release. These are documented validation
limits, not unverified claims of AWS behavior.

Supplemental Docker verification completed earlier on 2026-10-22, not as part
of this fresh review. It used image
`sha256:3c8af61fa769f486e9da2723b21d7da7a88f1d3fcd0592935b62c08a91628de9`,
an isolated Compose project, a private in-memory DynamoDB Local, no published
database port, and an ephemeral loopback application port. Both selector values
passed health, OpenAPI/UI, non-root UID 100, no-new-privileges, create 201,
real stream settlement with three events, and idempotent replay 200, using
TRIM_HORIZON. Those results prove the same runtime path under two accepted
configuration values. They do not establish distinct access-style
implementations.

An initial low-level Docker probe using the default LATEST iterator did not
settle an immediately created payment within 45 seconds. Health readiness does
not guarantee a stream iterator has already been acquired. The deterministic
Docker checks used TRIM_HORIZON as specified for stream tests. The documented
LATEST startup gap and in-memory checkpoint limitation remain.
