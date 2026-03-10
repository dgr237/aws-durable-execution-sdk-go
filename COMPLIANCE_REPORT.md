# AWS Durable Execution SDK for Go — Compliance Report

**Spec Version:** 1.2 | **Date:** 2026-03-10 | **Conformance Level:** Level 3 (Complete)

---

## Summary Table

| Section | Title | MUST | SHOULD | Notes |
|---|---|---|---|---|
| §2 | Core Concepts & Determinism | ✅ | ⚠️ Partial | Helpers + docs present; no linting tools |
| §3 | Execution Lifecycle | ✅ | ✅ | Pagination, size handling, all 3 status codes |
| §4 | Durable Operations | ✅ | ✅ | All 5 primitives + higher-level constructs |
| §5 | Checkpointing | ✅ | ✅ | Tokens, batching, 3 strategies |
| §6 | Concurrency & Parallelism | ✅ | ✅ | Map, Parallel, completion policies |
| §7 | Advanced Operations | ✅ | ✅ | WaitForCondition, callbacks, all 4 combinators |
| §8 | Error Handling | ✅ | ✅ | Full taxonomy, runtime classification, recovery |
| §9 | Serialization | ✅ | ✅ | JSON default + custom Serdes interface |
| §10 | Logging & Observability | ✅ | ✅ | Structured logging, replay-aware suppression |
| §11 | Type Safety | ✅ | — | Full generics across all operations |
| §12 | Handler Registration | ✅ | — | `WithDurableExecution` wrapper |
| §13 | Performance | — | ⚠️ Partial | No memory/cold-start guidance |
| §14 | Limits & Constraints | ✅ | ✅ | All limits defined in `limits.go` |
| §15 | API Client | ✅ | ✅ | Pagination, validation, default client |
| §16 | Execution Semantics | ✅ | ✅ | Exactly-once, replay, checkpoint strategies |

---

## Section-by-Section Detail

### §2 — Core Concepts & Determinism

**MUST: Compliant**

- Checkpoint-and-replay model implemented. Replay detection in `context/factory.go:62-67`; operations skip re-execution when stored result found (`operations/step.go:81-87`).
- Determinism documentation in `determinism.go:1-66` — rules for user code, forbidden patterns (`time.Now()`, `rand`, map iteration), and `CurrentTime()` replay-safe helper.

**SHOULD: Partial**

- ❌ No linting rules or static analysis tools to detect non-deterministic patterns.
- ❌ No test utilities for validating deterministic replay behaviour.

---

### §3 — Execution Lifecycle

**MUST: Compliant**

- Invocation input processing: `context/factory.go:26-89` — extracts ARN and token, loads all operations, handles `NextMarker` pagination loop.
- All three output formats: `durable.go:227-230` (SUCCEEDED), `durable.go:241` (FAILED), `durable.go:262-264` (PENDING).
- Response size enforcement: `durable.go:206-225` — results exceeding `lambdaResponseSizeLimit` (6 MB − 50 bytes, line 40) are checkpointed via EXECUTION SUCCEED and returned with an empty Result.
- Operation consistency checks: `context/step_id.go` — `ValidateReplayConsistency()` called in all nine operation entry points.

**SHOULD: Compliant**

- Size-limit failures returned as FAILED status, not propagated as Lambda errors (`durable.go:211-214`).

---

### §4 — Durable Operations

**MUST: Compliant** — all five primitives implemented:

| Primitive | File | Key Lines |
|---|---|---|
| STEP | `operations/step.go` | Entry: 56; Replay: 81-87; Execute: 130-149; Retry: 200-259 |
| WAIT | `operations/wait.go` | Entry: 41; Checkpoint: 80-91; Replay: 59-60; Type: `OperationTypeWait` |
| CALLBACK | `operations/wait_for_callback.go` | Entry: 54; Submitter: 114; Checkpoint: 118-127 |
| CHAINED_INVOKE | `operations/invoke.go` | Entry: 55; Serialise: 173-182; Checkpoint: 147-167 |
| CONTEXT | `operations/map.go`, `parallel.go`, `run_in_child_context.go` | ParentId association throughout |

EXECUTION operation: identified in `factory.go:52-59`, input extracted `durable.go:167-170`, oversized result checkpointed `durable.go:208-215`.

**SHOULD: Compliant** — all higher-level constructs present: `Map`, `Parallel`, `WaitForCondition`, `WaitForCallback`, `RunInChildContext`. All four promise combinators in `operations/combinators.go`: `All`, `AllSettled`, `Any`, `Race`.

---

### §5 — Checkpointing

**MUST: Compliant**

- Checkpoint API usage with ARN + token rotation: `checkpoint/manager.go:270-318`.
- Atomic batch: `manager.go:106-147` (`CheckpointBatch`), used in Map for parent+children atomicity (`map.go:214-218`).
- Operation update fields (Id, Action, Type): enforced across all operation checkpoint calls.
- Batch ordering rules (EXECUTION last, children after parent START): implemented by construction in all operations.

**SHOULD: Compliant**

- Three checkpoint strategies in `types/types.go:9-27`; wired through `Config` → `runHandler` → `Manager`. Optimistic mode skips blocking in `manager.go:175-178` and `manager.go:137-139`.

---

### §6 — Concurrency & Parallelism

**MUST: Compliant**

- `Map[TIn, TOut]`: `operations/map.go:59-94` — items slice, map function, concurrency control, returns `BatchResult[TOut]`.
- `Parallel[TOut]`: `operations/parallel.go:53-87` — branch functions, concurrency control, returns `BatchResult[TOut]`.
- `BatchResult` with items, success/failure count, completion reason: `types/types.go` (`BatchResult`, `BatchResultItem`, helper methods).

**SHOULD: Compliant**

- All three completion policies (`MinSuccessful`, `ToleratedFailureCount`, `ToleratedFailurePercentage`) in `operations/completion.go:62-82`.
- Convenient result access methods (`Values()`, `GetErrors()`, `HasErrors()`, `ThrowIfErrors()`).

---

### §7 — Advanced Operations

**MUST: Compliant**

- `WaitForCondition`: `operations/wait_for_condition.go` — reducer pattern using STEP RETRY with state payload; wait strategy controls delay.
- `CreateCallback`: `operations/create_callback.go` — returns buffered channel + callbackID; replay pre-fills channel.
- `WaitForCallback`: `operations/wait_for_callback.go` — submitter receives callbackID; suspends until external completion.

**SHOULD: Compliant**

- Promise combinators in `operations/combinators.go`: `All` (line 13), `AllSettled` (line 35), `Any` (line 47), `Race` (line 73) — all implemented as STEP operations for durability.

---

### §8 — Error Handling

**MUST: Compliant**

- Runtime error classification: `errors/errors.go:301-311` — `IsRuntimeError()` with `Runtime.`, `Sandbox.`, `Extension.` prefixes; `Sandbox.Timedout` correctly excluded.
- Full error taxonomy: `StepError`, `CallbackError`, `InvokeError`, `ParallelError`, `WaitConditionError`, `ChildContextError`, `NonDeterministicError`, `UnrecoverableError`, `SerdesError`, `SerdesFailedError`, `TerminatedError`, `AggregateError`.
- Retryable vs terminal distinction: `IsUnrecoverableInvocationError()` (`errors.go:210-218`) used in `durable.go:192,238` to decide throw vs FAILED status.
- Error objects with `ErrorType` and `ErrorMessage`: `types/types.go` `ErrorObject` struct; `StackTrace` field present.

**SHOULD: Compliant**

- Retry strategy on step failure with RETRY action and `NextAttemptDelaySeconds`: `operations/step.go:207-239`.
- Context-level failure checkpointed: `operations/parallel.go:320-336`, `operations/map.go:226-247`.

---

### §9 — Serialization

**MUST: Compliant**

- JSON serialization default: `utils/serdes.go` — `JSONSerdes` as `DefaultSerdes`, `SafeSerialize` / `SafeDeserialize` used uniformly.
- Serialisation errors terminal: `SerdesFailedError` triggers `Terminate(TerminationReasonSerdesFailed)` across all operations.
- Deserialisation preserves target type via Go generic unmarshalling.

**SHOULD: Compliant**

- Custom `Serdes` interface: `types/types.go`; per-operation override via `WithStepSerdes`, `WithMapSerdes`, `WithInvokeSerdes`, `WithParallelSerdes`, etc. (`operations/options.go`).

---

### §10 — Logging & Observability

**MUST: Compliant**

- Structured logger backed by `slog` with JSON output: `utils/logger.go:19`.
- Context fields (executionArn, requestId): logger created with both in `durable.go:148`.
- Log levels: Info, Warn, Error, Debug.

**SHOULD: Compliant**

- Replay-aware logging: `context/durable_context.go:178-184` — returns `NopLogger` during replay when `modeAware=true` (default).
- User-configurable: `ConfigureLogger()` `durable_context.go:209-217`.

---

### §11 — Type Safety & Language Integration

**MUST: Compliant**

- Generics throughout: `Step[TOut]`, `Invoke[TIn,TOut]`, `Map[TIn,TOut]`, `Parallel[TOut]`, `WaitForCallback[T]`, `WaitForCondition[TState]`, `CreateCallback[TResult]`, `RunInChildContext[T]`.
- `HandlerFunc[TEvent, TResult]` type at `durable.go:62`.
- Idiomatic `(value, error)` return pattern across all operations.

---

### §12 — Handler Registration

**MUST: Compliant**

- `WithDurableExecution[TEvent, TResult]()` at `durable.go:86` — validates input, initialises state, runs handler, returns AWS Lambda-compatible `LambdaHandler`.
- `Config` struct at `durable.go:43-52` — custom client and checkpoint strategy.

---

### §13 — Performance Considerations

**SHOULD: Partial**

- ❌ No explicit memory management guidance (releasing completed operation data, chunking large state).
- ❌ No cold-start optimisation guidance (deferred initialisation, client reuse).
- ✅ Client created lazily when not provided (`durable.go:100-107`).

---

### §14 — Limitations & Constraints

**MUST: Compliant**

- All limits in `types/limits.go`: `MaxExecutionDuration` (1 year), `MaxResponsePayloadBytes` (6 MB), `MinWaitSeconds` (1), `MaxWaitDuration`, `MaxCallbackTimeoutSeconds`, `DefaultMaxRetryAttempts` (3).
- Link to AWS service limits docs in file header.
- Response size enforced at runtime (`durable.go:206-225`).
- Determinism requirements documented (`determinism.go:15-26`).

**SHOULD: Compliant**

- Batch limits enforced: `manager.go:25-27` (`maxPayloadSize = 750 KB`, `maxItemsInBatch = 250`).

---

### §15 — API Client Requirements

**MUST: Compliant**

- `Client` interface: `checkpoint/execution_context.go:14-17` — `Checkpoint()` and `GetExecutionState()`.
- Pagination: `context/factory.go:32-48`.
- Input validation: `execution_context.go:58-74` — ARN and token presence.
- Default Lambda client: `client/client.go`, created when `cfg.Client == nil` (`durable.go:100-107`).

**SHOULD: Compliant**

- `InvalidParameterValueException` handling: `checkpoint/manager.go:322-329` — classified as unrecoverable invocation error, causes Lambda retry.

---

### §16 — Execution Semantics

**MUST: Compliant**

- At-least-once delivery: operations may re-execute before checkpointing; checkpointed results returned consistently on replay.
- Exactly-once checkpoint semantics via single-use tokens: enforced by `manager.go:300-312`.
- Atomic batch semantics: entire batch succeeds or fails (`manager.go:284-296`).

**SHOULD: Compliant**

- Checkpoint strategy trade-offs documented and configurable: `types/types.go:9-27`, `durable.go:48-51`.

---

## Gaps and Recommendations

### HIGH — Unresolved MUST Violations

None.

### MEDIUM — SHOULD Gaps with Material Impact

| # | Section | Gap | Recommendation |
|---|---|---|---|
| 1 | §2.5 | No linting rules or static analysis to detect `time.Now()`, `rand`, map iteration outside operations | Add a "Common Pitfalls" section to README with concrete before/after code examples |
| 2 | §2.5 | No test utilities for replay simulation | Provide a `testing` subpackage with a mock executor that can simulate replay sequences |

### LOW — SHOULD Gaps with Minor Impact

| # | Section | Gap | Recommendation |
|---|---|---|---|
| 3 | §13.1 | No memory management guidance for large histories | Document recommendation to keep per-step payloads small; reference pagination |
| 4 | §13.2 | No cold-start guidance | Document client reuse pattern in README |

---

## Conformance Verdict

**LEVEL 3 — COMPLETE CONFORMANCE**

All MUST requirements across §2–§16 are fully satisfied. All SHOULD requirements are met except two optional tooling items (linting rules and test utilities for determinism validation) — both are SHOULD, not MUST, and do not affect runtime correctness. The remaining two LOW gaps are documentation-only. The SDK is production-ready and fully compliant with AWS Lambda Durable Execution SDK Specification v1.2.
