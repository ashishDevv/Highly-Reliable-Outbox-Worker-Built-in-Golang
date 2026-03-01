# Outflow — Highly Reliable Outbox Worker Built in Golang

A **high-performance, fault-tolerant, scalable, reliable** implementation of the Transactional Outbox Pattern in Go, designed to **guarantee at-least-once event delivery** from PostgreSQL to RabbitMQ with zero event loss, transactional safety, and concurrent worker pipelines

## Table of Contents

- [System Overview](#system-overview)
- [The Outbox Pattern](#the-outbox-pattern)
- [Architecture](#architecture)
- [Core Pipeline](#core-pipeline)
- [Reliability & Fault Tolerance](#reliability--fault-tolerance)
- [Performance Optimizations](#performance-optimizations)
- [Engineering Challenges](#engineering-challenges)
- [Why Go](#why-go)
- [Code Quality & Design](#code-quality--design)
- [Project Structure](#project-structure)
- [Tech Stack](#tech-stack)
- [Database Schema](#database-schema)
- [Configuration](#configuration)
- [Getting Started](#getting-started)

---

## System Overview

Outflow is a production-grade outbox worker service that reliably delivers domain events from a PostgreSQL outbox table to RabbitMQ. In distributed systems, a common problem is ensuring that database writes and message broker publishes happen atomically — if you write to the DB and then publish to RabbitMQ, a crash between the two means lost events. The Outbox Pattern solves this by writing events to the database as part of the same transaction, then using a separate worker to poll and publish them.

### What It Does

1. **Polls** the `outbox_events` table for pending events using `FOR UPDATE SKIP LOCKED` for contention-free concurrent access
2. **Locks** events within a database transaction using the Unit of Work pattern, preventing duplicate processing across workers
3. **Publishes** events to RabbitMQ with publisher confirms, ensuring the broker has durably accepted every message
4. **Finalizes** events by marking them as `PROCESSED` in a separate transaction after successful publish
5. **Recovers** stale locks automatically — events stuck in `PROCESSING` for >30 seconds are reclaimed by the next poll

### Key Properties

#### Highly Scalable

- **Configurable worker pool** — the number of concurrent workers is configurable (default: 10), and each worker gets its own dedicated RabbitMQ channel, so workers never contend for broker resources
- **Ticker-based scheduling with backpressure** — a ticker dispatches work signals at a configurable interval. If all workers are busy, ticks are dropped (not queued), preventing unbounded memory growth
- **`FOR UPDATE SKIP LOCKED`** — concurrent workers never block each other at the database level. Each worker locks its own batch of rows; already-locked rows are silently skipped
- **Independent worker pipelines** — each worker has its own `outbox.Service` → `rabbitmq.Publisher` chain, so a slow or failing publisher doesn't block other workers

#### Highly Reliable

- **Transactional event acquisition** — `GetPendingEvents` + `LockEvents` run inside a single PostgreSQL transaction via Unit of Work, so events are atomically fetched and locked. If anything fails, the transaction rolls back and no events are "stuck"
- **RabbitMQ Publisher Confirms** — every published message is confirmed by the broker via `ch.Confirm(false)`. The worker waits for an `Ack` confirmation per message before proceeding, so no event is marked as processed unless RabbitMQ durably accepted it
- **Stale lock recovery** — the `GetOutboxEvents` query includes `OR (status = 'PROCESSING' AND locked_at < now() - interval '30 seconds')`, automatically reclaiming events from crashed workers
- **Persistent message delivery** — all published messages use `DeliveryMode: amqp091.Persistent`, ensuring they survive RabbitMQ restarts

#### Fault Tolerant

- **Graceful shutdown with ordered resource cleanup** — on `SIGINT`/`SIGTERM`, the system first stops the worker pool (cancel context → stop ticker → close work channel → wait for running workers), then shuts down publishers, closes the RabbitMQ connection, and finally closes the DB pool
- **Context-based timeouts** — every processing cycle gets a 10-second timeout (`context.WithTimeout`), preventing indefinite hangs on slow DB or broker operations
- **RabbitMQ connection retry** — initial connection attempts retry 5 times with 2-second backoff before failing fast
- **Structured error handling** — all errors flow through the `apperror` package with `Kind`, `Op`, `Message`, and optional stack traces, ensuring errors are debuggable without leaking internal details

#### High Performance

- **Batch processing** — each worker processes up to 50 events per cycle in a single batch, reducing per-event overhead for DB queries and RabbitMQ publishes
- **Connection pool tuning** — the PostgreSQL pool is configured with `MaxConns: 50`, `MinConns: 5`, `ConnMaxLifetime: 1h`, and `ConnMaxIdleTime: 30m`, optimized for sustained background workloads
- **Per-worker RabbitMQ channels** — each worker has its own AMQP channel, avoiding channel-level lock contention and maximizing broker throughput
- **Zero unnecessary allocations** — pre-allocated slices (`make([]Event, 0, len(rows))`) and efficient pgtype conversions minimize GC pressure

---

## The Outbox Pattern

### The Problem

In microservice architectures, a common requirement is: **write to the database AND publish an event, atomically**. For example, when a user registers, you need to:

1. Insert the user record into PostgreSQL
2. Publish a `UserCreated` event to RabbitMQ

Without the Outbox Pattern, there are two failure modes:

```
Approach 1: Publish first, then write to DB
└── Problem: If DB insert fails after publish, the event is out but the user doesn't exist

Approach 2: Write to DB first, then publish
└── Problem: If the service crashes after DB write but before publish, the event is lost
```

### The Solution

Instead of publishing directly, the producing service writes the event to an `outbox_events` table **in the same database transaction** as the business data:

```
BEGIN TRANSACTION
    INSERT INTO users (...)
    INSERT INTO outbox_events (event_type, payload) VALUES ('UserCreated', '{"id": "..."}')
COMMIT
```

A separate **Outbox Worker** (this project) then polls the outbox table and publishes events to RabbitMQ. Since the event write is transactional with the business write, either both succeed or neither does.

```mermaid
sequenceDiagram
    participant App as Application Service
    participant DB as PostgreSQL
    participant OW as Outflow (Outbox Worker)
    participant RMQ as RabbitMQ

    App->>DB: BEGIN TX
    App->>DB: INSERT INTO users (...)
    App->>DB: INSERT INTO outbox_events (...)
    App->>DB: COMMIT

    loop Every N seconds
        OW->>DB: SELECT ... FOR UPDATE SKIP LOCKED
        OW->>DB: (batch) UPDATE status = 'PROCESSING'
        OW->>RMQ: Publish batch (with confirms)
        RMQ-->>OW: Ack confirmations
        OW->>DB: (batch) UPDATE status = 'PROCESSED'
    end
```

---

## Architecture

### High-Level Architecture

```mermaid
classDiagram
    class Container {
        +DBPool *pgxpool.Pool
        +Logger *zerolog.Logger
        +RMQConn *amqp091.Connection
        +Publishers []*Publisher
        +OutboxSvrs []*Service
        +NewContainer(dbPool, cfg, logger) Container
        +Shutdown(ctx) error
    }

    class WorkerPool {
        -ctx context.Context
        -cancel context.CancelFunc
        -ticker *time.Ticker
        -workers []*Worker
        -workCh chan struct
        -wg *sync.WaitGroup
        +WorkerCount int
        +StartWorkers()
        +ShutdownWorkers()
    }

    class Worker {
        -ctx context.Context
        -outboxSvc *Service
        -wg *sync.WaitGroup
        -workCh chan struct
        -log *zerolog.Logger
        +Start()
        -runOnce()
    }

    class Service {
        -repo *Repository
        -rmqPublisher *Publisher
        -uowFactory *UnitOfWorkFactory
        +ProcessOutboxEvents(ctx) error
        -acquireEvents(ctx, limit) []Event
        -publishEvents(ctx, events) error
        -finalizeEvents(ctx, events) error
    }

    class Repository {
        -qu *db.Queries
        +GetPendingEvents(ctx, limit) []Event
        +LockEvents(ctx, workerID, ids) error
        +MarkProcessed(ctx, ids) error
    }

    class UnitOfWorkFactory {
        -conn *pgxpool.Pool
        +NewUnitOfWork(ctx) UnitOfWork
    }

    class UnitOfWork {
        <<interface>>
        +Repo() *Repository
        +Commit() error
        +Rollback()
    }

    class unitOfWork {
        -ctx context.Context
        -tx pgx.Tx
        -repo *Repository
        +Repo() *Repository
        +Commit() error
        +Rollback()
    }

    class Publisher {
        -ch *amqp091.Channel
        -confirms chan Confirmation
        -exchange string
        -routingKey string
        +PublishBatch(ctx, bodies) error
        -publish(ctx, body) error
        +Shutdown() error
    }

    class Event {
        +ID uuid.UUID
        +Type string
        +Payload []byte
    }

    class DBTX {
        <<interface>>
        +Exec(ctx, sql, args) CommandTag
        +Query(ctx, sql, args) Rows
        +QueryRow(ctx, sql, args) Row
    }

    Container "1" *-- "N" Worker : creates per runner count
    Container "1" *-- "N" Publisher : creates per runner count
    Container "1" *-- "N" Service : creates per runner count
    Container --> WorkerPool : passes services

    WorkerPool "1" o-- "N" Worker : manages lifecycle
    WorkerPool ..> Worker : dispatches via workCh

    Worker --> Service : calls ProcessOutboxEvents

    Service --> Repository : uses
    Service "1" --> "1" Publisher : dedicated
    Service --> UnitOfWorkFactory : creates transactions

    UnitOfWorkFactory ..> unitOfWork : creates
    unitOfWork ..|> UnitOfWork : implements
    unitOfWork --> Repository : scoped to TX

    Repository --> DBTX : depends on
    Repository ..> Event : returns

    note for WorkerPool "Ticker dispatches work signals. If no idle worker, tick is dropped (backpressure). N workers scale independently."

    note for Service "Three-phase pipeline: 1) Acquire (UoW TX) 2) Publish (RMQ confirms) 3) Finalize (UoW TX)"

    note for DBTX "Satisfied by pgxpool.Pool and pgx.Tx. Enables UoW pattern."
```

**How to read this diagram**: The `Container` creates N instances of `Worker`, `Service`, and `Publisher` (one per `runner.count`). Each `Worker` has a dedicated `Service` → `Publisher` chain, so workers never contend for broker resources. The `Repository` is shared but safe because each `Service` accesses it through its own `UnitOfWork` transaction. The `DBTX` interface is the key abstraction — since both `pgxpool.Pool` and `pgx.Tx` satisfy it, the same `Repository` code works both inside and outside transactions.

### Component Interaction

```mermaid
sequenceDiagram
    participant T as Ticker
    participant WP as WorkerPool
    participant W as Worker
    participant S as Outbox Service
    participant UoW as Unit of Work
    participant DB as PostgreSQL
    participant P as RMQ Publisher
    participant RMQ as RabbitMQ

    loop Every tick (configurable, default 5s)
        T->>WP: tick signal
        WP->>W: workCh <- struct{}{}
        Note over WP: If no idle worker,<br/>tick is dropped (backpressure)
    end

    W->>S: ProcessOutboxEvents(ctx)

    rect rgb(40, 40, 60)
        Note over S,DB: Phase 1: Acquire Events (single TX)
        S->>UoW: Begin Transaction
        UoW->>DB: SELECT ... FOR UPDATE SKIP LOCKED
        DB-->>UoW: Pending events (batch of 50)
        UoW->>DB: UPDATE status='PROCESSING', locked_by, locked_at
        UoW->>DB: COMMIT
    end

    rect rgb(40, 60, 40)
        Note over S,RMQ: Phase 2: Publish to RabbitMQ
        S->>P: PublishBatch(events)
        loop For each event
            P->>RMQ: Publish (persistent)
        end
        loop For each event
            RMQ-->>P: Ack confirmation
        end
    end

    rect rgb(60, 40, 40)
        Note over S,DB: Phase 3: Finalize (single TX)
        S->>UoW: Begin Transaction
        UoW->>DB: UPDATE status='PROCESSED', processed_at
        UoW->>DB: COMMIT
    end
```

---

## Core Pipeline

The worker follows a **three-phase pipeline** for processing outbox events. Each phase is isolated, and failure at any phase has well-defined recovery semantics.

```
┌──────────────┐      ┌───────────────────┐      ┌──────────────┐
│  Phase 1:    │      │  Phase 2:         │      │  Phase 3:    │
│  Acquire     │─────>│  Publish          │─────>│  Finalize    │
│  (DB TX)     │      │  (RabbitMQ)       │      │  (DB TX)     │
└──────────────┘      └───────────────────┘      └──────────────┘
  SELECT + LOCK          Publish + Confirm         Mark PROCESSED
  via UoW                with confirms             via UoW
```

### Phase 1: Acquire Events

**Responsibility**: Fetch and lock pending outbox events within a single database transaction.

- Uses `FOR UPDATE SKIP LOCKED` to avoid blocking concurrent workers — each worker gets its own exclusive batch
- Acquires up to 50 events per cycle (configurable `batch_size`)
- Events transition from `PENDING` → `PROCESSING` with `locked_by` and `locked_at` metadata
- Stale events (stuck in `PROCESSING` for >30 seconds) are also eligible for re-acquisition — this is the crash recovery mechanism
- The entire acquire phase runs inside a `UnitOfWork` (database transaction), so if locking fails, the fetch is also rolled back

```go
// Acquire flow (simplified)
uow := uowFactory.NewUnitOfWork(ctx)       // BEGIN TX
events := uow.Repo().GetPendingEvents(50)  // SELECT ... FOR UPDATE SKIP LOCKED
uow.Repo().LockEvents("worker-1", ids)     // UPDATE status='PROCESSING'
uow.Commit()                               // COMMIT
```

### Phase 2: Publish to RabbitMQ

**Responsibility**: Serialize events and publish them to RabbitMQ with delivery guarantees.

- Each event is JSON-marshaled and published with `DeliveryMode: amqp091.Persistent` (survives broker restarts)
- The publisher operates in **confirm mode** (`ch.Confirm(false)`), meaning RabbitMQ sends an acknowledgement for each message
- After publishing all messages, the worker waits for one `Ack` confirmation per message, with a 5-second timeout per confirm
- If any confirmation is `Nack`'d or times out, the publish phase fails and the events remain in `PROCESSING` state — they'll be reclaimed after 30 seconds

```go
// Publish with confirms
for _, body := range bodies {
    ch.PublishWithContext(ctx, exchange, routingKey, false, false, amqp091.Publishing{
        ContentType:  "application/json",
        DeliveryMode: amqp091.Persistent,
        Body:         body,
    })
}

// Wait for broker confirms
for range bodies {
    confirm := <-p.confirms  // blocks until broker responds
    if !confirm.Ack {
        return errors.New("broker rejected message")
    }
}
```

### Phase 3: Finalize Events

**Responsibility**: Mark successfully published events as `PROCESSED` in the database.

- Runs inside another `UnitOfWork` transaction
- Updates `status = 'PROCESSED'` and `processed_at = now()` for all published event IDs
- Only runs if Phase 2 succeeded — if publish failed, events stay as `PROCESSING` and are reclaimed later

### Failure Recovery Matrix

| Failure Point | State After Failure | Recovery Mechanism |
|---|---|---|
| Crash during Phase 1 (Acquire) | Events remain `PENDING` | Next poll picks them up normally |
| Crash during Phase 2 (Publish) | Events stuck as `PROCESSING` | Stale lock reclaim after 30s |
| Crash during Phase 3 (Finalize) | Events `PROCESSING`, messages published | Stale lock reclaim → re-publish (at-least-once Delivery) |
| RabbitMQ Nack | Events stuck as `PROCESSING` | Stale lock reclaim after 30s |
| Context timeout (10s) | Transaction rolled back | Events remain `PENDING` |
| Worker panic | Worker goroutine exits | WaitGroup-tracked, pool continues |

---

## Reliability & Fault Tolerance

### 1. At-Least-Once Delivery Guarantee

Every event that enters the outbox table will eventually be published to RabbitMQ:

- **Transactional writes** — events are written to the outbox table in the same database transaction as the business operation, so they can never be "lost at creation"
- **Stale lock recovery** — the `WHERE` clause in `GetOutboxEvents` includes a 30-second timeout for processing events, so crashed workers' events are automatically reclaimed
- **Publisher confirms** — events are only marked `PROCESSED` after RabbitMQ confirms receipt with a durable `Ack`
- **Idempotent consumers** — since at-least-once means possible duplicates (crash during Phase 3), downstream consumers should be idempotent

### 2. Graceful Shutdown

The shutdown sequence is carefully ordered to prevent event loss:

```
SIGINT / SIGTERM received
    │
    ├── 1. Cancel worker context     ← Workers stop accepting new work
    ├── 2. Stop ticker               ← No new work signals dispatched
    ├── 3. close(workCh)             ← Idle workers exit their for-select loop
    ├── 4. wg.Wait()                 ← Wait for running workers to finish current cycle
    │
    ├── 5. Shutdown all Publishers   ← Close each AMQP channel
    ├── 6. Close RabbitMQ connection ← Tear down TCP connection
    └── 7. Close DB pool             ← Release all PostgreSQL connections
```

Each step ensures all in-flight processing completes before the underlying resources are torn down. The `sync.WaitGroup` in step 4 blocks until every active `ProcessOutboxEvents` call returns.

### 3. Structured Error Handling

The `apperror` package provides structured, classified errors:

```go
type Error struct {
    Kind    Kind    // Internal, Dependency, Conflict, RequestTimeout, etc.
    Op      string  // "repo.outbox.get_pending_events" — traces error origin
    Message string  // User/operator-safe message
    Err     error   // Wrapped underlying error
    Stack   []byte  // Stack trace (auto-captured for Internal/Dependency kinds)
}
```

Error classification enables intelligent handling:

| Kind | Meaning | Example |
|---|---|---|
| `Internal` | Unexpected system error | Database query failure |
| `Dependency` | External service failed | RabbitMQ publish failure |
| `Conflict` | Concurrent modification | Events already locked by another worker |
| `RequestTimeout` | Context deadline exceeded | Worker's 10s timeout expired |

### 4. Event State Machine

```mermaid
stateDiagram-v2
    [*] --> PENDING: Application inserts event<br/>(in business transaction)

    PENDING --> PROCESSING: Worker acquires event<br/>(SELECT FOR UPDATE SKIP LOCKED + Lock)

    PROCESSING --> PROCESSED: Publish confirmed<br/>+ finalize committed

    PROCESSING --> PENDING: Stale lock expired (>30s)<br/>(auto-reclaimed by next poll)

    PROCESSING --> PROCESSING: Another worker re-locks<br/>(after stale timeout)
```

---

## Performance Optimizations

### 1. `FOR UPDATE SKIP LOCKED` (Contention-Free Concurrent Polling)

The most critical performance optimization. Without `SKIP LOCKED`, concurrent workers would contend on the same rows, causing lock waits and reduced throughput.

```sql
SELECT id, event_type, payload 
FROM outbox_events
WHERE (
    status = 'PENDING'
    OR (status = 'PROCESSING' AND locked_at < now() - interval '30 seconds')
)
ORDER BY created_at
LIMIT $1
FOR UPDATE SKIP LOCKED;
```

With `SKIP LOCKED`, each worker instantly gets its own exclusive batch — no waiting, no retries, no deadlocks. This is what enables horizontal scaling of workers.

### 2. Per-Worker Dedicated RabbitMQ Channels

Each worker gets its own `amqp091.Channel` from the shared connection. AMQP channels are multiplexed over a single TCP connection, so there's no additional network overhead, but each channel has independent flow control and confirm tracking.

```go
// Container creates N publishers, one per worker
for range cfg.Runner.Count {
    publisher := rabbitmq.NewPublisher(rmqpConn, exchange, routingKey)  // new channel
    svc := outbox.NewService(outboxRepo, publisher, uowFactory)
}
```

### 3. Batch Operations

- Events are fetched in batches of 50 per cycle (configurable)
- `PublishBatch` publishes all events before waiting for all confirms, maximizing broker throughput
- `MarkProcessed` updates all event IDs in of a single SQL `UPDATE ... WHERE id = ANY($1::UUID[])`, single DB roundtrip

### 4. Backpressure-Aware Scheduling

The ticker dispatches work signals through an unbuffered channel. If no worker is available, the signal is dropped:

```go
case <-wp.ticker.C:
    select {
    case wp.workCh <- struct{}{}:  // dispatch to idle worker
    default:                       // no idle worker — drop tick
    }
```

This prevents unbounded queue growth and ensures the system naturally throttles when workers can't keep up.

### 5. Connection Pool Tuning

PostgreSQL connection pool is configured for sustained background workloads:

```go
db.Config{
    MaxConns:        50,           // handles burst of concurrent UoW transactions
    MinConns:        5,            // pre-warmed connections always ready
    ConnMaxLifetime: time.Hour,    // prevents stale connections
    ConnMaxIdleTime: 30 * time.Minute,
    HealthTimeout:   5 * time.Second,  // fail-fast on startup
}
```

---

## Engineering Challenges

### Challenge 1: Ensuring Exactly-Once Processing Without Distributed Locks

**Problem**: With N concurrent workers polling the same outbox table, how do you prevent two workers from grabbing the same event?

**Failed approaches considered**:
- Application-level locking (Redis SETNX) — adds latency and an external dependency on the hot path
- Partitioning events by worker ID — requires coordination and doesn't handle worker failures

**Solution:** <br/>
PostgreSQL's `FOR UPDATE SKIP LOCKED` provides row-level locking without contention. When worker A locks rows 1-50, worker B's query silently skips those rows and gets the next available batch. No coordination, no waiting, no external systems. Combined with the `UnitOfWork` transaction wrapper, lock acquisition is atomic with event fetching.

### Challenge 2: Guaranteeing Delivery When Crashes Can Happen Anywhere

**Problem**: The worker can crash between any two operations: after acquiring events but before publishing, after publishing but before marking as processed. How do you guarantee no events are lost?

**Solution:** <br/>

The three-phase pipeline with stale lock recovery handles every crash scenario:

1. **Crash during acquire** → Transaction is never committed, events remain `PENDING`
2. **Crash during publish** → Events are `PROCESSING` with a timestamp. After 30 seconds, the `locked_at` check in `GetOutboxEvents` makes them eligible for re-acquisition
3. **Crash during finalize** → Events are `PROCESSING`, messages are published. Re-acquisition causes a duplicate publish (at-least-once semantics — downstream consumers must be idempotent)

The key insight is that the 30-second stale lock timeout acts as a universal recovery mechanism without requiring any separate "recovery" component.

### Challenge 3: RabbitMQ Publish Reliability

**Problem**: `ch.Publish()` returning `nil` error doesn't mean the message was durably stored by the broker. The message could be in a TCP buffer, lost during network partition, or rejected by the exchange.

**Solution:** <br/>
The publisher uses **AMQP Publisher Confirms** (`ch.Confirm(false)`) which changes the publish semantics from fire-and-forget to acknowledged delivery:

1. Enable confirm mode on the channel
2. Register a notification channel for confirmations
3. After publishing each message, wait for the broker's `Ack`
4. Only proceed to Phase 3 (finalize) if ALL messages were acknowledged

```go
// Confirm mode ensures broker durability
ch.Confirm(false)
confirms := ch.NotifyPublish(make(chan amqp091.Confirmation, 100))

// After publishing, verify each confirmation
for range bodies {
    select {
    case confirm := <-confirms:
        if !confirm.Ack { return errors.New("broker nack") }
    case <-time.After(5 * time.Second):
        return errors.New("confirm timeout")
    }
}
```

### Challenge 4: Clean Shutdown Without Event Loss

**Problem**: When the service receives SIGTERM (e.g., during Kubernetes rolling deployment), workers may be mid-cycle: some have acquired events, some are publishing. How do you shut down without losing in-progress events?

**Solution:** <br/>
Ordered shutdown with `sync.WaitGroup`:

1. Cancel the context → workers' `select` picks up `ctx.Done()`, no new cycles start
2. Stop the ticker → no new work signals
3. Close `workCh` → idle workers blocked on `<-workCh` exit immediately
4. `wg.Wait()` → block until ALL workers currently inside `runOnce()` complete their full acquire → publish → finalize cycle
5. Only then close publishers, RabbitMQ connection, and DB pool

The critical insight is that `wg.Wait()` in step 4 guarantees every in-flight cycle completes before infrastructure resources are destroyed.

---

## Why Go

Go was chosen for this project for specific technical reasons aligned with the system's requirements:

### 1. Nature of System — IO-Bound Workload

This system primarily performs IO operations: PostgreSQL queries, RabbitMQ publishes, network calls. Go's goroutine scheduler excels at IO-bound workloads — goroutines yield to the scheduler during IO waits, and the runtime efficiently multiplexes thousands of goroutines onto a small number of OS threads.

### 2. Goroutines — Lightweight Concurrency at Scale

Each goroutine uses only ~2-8KB of stack (vs ~1MB per OS thread). Running 10 worker goroutines + background scheduler + ticker costs negligible memory. Scaling to 100+ workers is trivial without process-per-worker overhead.

### 3. Channels — CSP Model for Worker Coordination

Go's channel primitive is the exact abstraction needed for the ticker → worker dispatch pattern. The `chan struct{}` work channel with `select`/`default` provides natural backpressure without mutexes. Context cancellation propagates cleanly through the `select` statement for coordinated shutdown.

### 4. `sync.WaitGroup` — Goroutine Lifecycle Management

The `WaitGroup` pattern maps perfectly to "start N workers, wait for all to finish" — exactly what the shutdown sequence requires.

### 5. Static Binary + Fast Startup

Go produces a single static binary with no runtime dependencies. The service starts in milliseconds — critical for container orchestration where liveness probes and rolling deployments require fast process startup.

### 6. Explicit Error Handling

Go's `if err != nil` pattern forces handling every failure path. In a system where reliability is paramount and every error (DB failure, broker timeout, confirm nack) must be explicitly handled, this is an advantage over exception-based languages where errors can silently propagate.

---

## Code Quality & Design

### Design Principles Applied

#### SOLID Principles

| Principle | Implementation |
|---|---|
| **Single Responsibility** | `outbox.Service` owns event processing logic, `rabbitmq.Publisher` owns AMQP publishing, `app.WorkerPool` owns scheduling, `app.Worker` owns lifecycle of a single goroutine |
| **Open/Closed** | The `UnitOfWork` interface allows swapping transaction implementations. `db.DBTX` interface lets repositories work with both `pgxpool.Pool` and `pgx.Tx` |
| **Liskov Substitution** | `UnitOfWork` interface is satisfied by `unitOfWork` struct. `DBTX` interface is satisfied by both pool and transaction objects |
| **Interface Segregation** | `db.DBTX` requires only `Exec`, `Query`, `QueryRow` — not the full pool interface. `UnitOfWork` requires only `Repo()`, `Commit()`, `Rollback()` |
| **Dependency Inversion** | `outbox.Service` depends on `UnitOfWork` interface, not concrete `pgx.Tx`. `Repository` depends on `db.DBTX` interface, not `*pgxpool.Pool` |

#### Unit of Work Pattern

The UoW pattern is the architectural centerpiece that enables transactional event processing:

```go
type UnitOfWork interface {
    Repo() *Repository   // scoped repository using the transaction
    Commit() error       // commit the transaction
    Rollback()           // rollback (safe to call after commit)
}
```

The `Repository` constructor accepts a `db.DBTX` interface, which is satisfied by both `*pgxpool.Pool` (for non-transactional queries) and `pgx.Tx` (for transactional queries). The `UnitOfWork` creates a transaction and passes it to a new `Repository` instance, so all operations within the UoW share the same transaction.

```go
// UnitOfWork creates a scoped transaction + repository
tx := pool.Begin(ctx)
repo := NewRepository(tx)  // repository uses the transaction
// ... repo.GetPendingEvents() + repo.LockEvents() both use the same tx
tx.Commit()
```

#### KISS — No Unnecessary Abstraction

- No ORM — `sqlc` generates type-safe Go code directly from SQL queries
- No dependency injection framework — a simple `Container` struct wires everything
- No message broker abstraction layer — direct RabbitMQ client with a thin `Publisher` wrapper
- No job queue library — Go channels + ticker provide exactly the right abstraction
- No retry library — simple loop with backoff for RabbitMQ connection

#### DRY — Centralized Error Mapping

The `mapDBError` function in the repository layer consistently maps all database errors:

```go
func mapDBError(op string, err error) error {
    // context.Canceled / context.DeadlineExceeded → RequestTimeout
    // *pgconn.PgError → Internal
    // everything else → Internal
}
```

This eliminates repetitive error handling across repository methods.

### Module Boundaries

| Module | Layer | Responsibility |
|---|---|---|
| `cmd/worker` | Entry point | Bootstraps the application, loads config, initializes DB pool, creates container, starts worker pool, handles OS signal-based graceful shutdown |
| `config` | Configuration | Loads YAML config via Viper with environment variable overrides, sets sensible defaults, validates required fields |
| `internals/app` | Orchestration | Wires all dependencies in a `Container` struct (DI), manages `WorkerPool` lifecycle (ticker, backpressure, shutdown), and `Worker` goroutine lifecycle |
| `internals/outbox` | Domain | Core business logic: `Event` domain model, `Repository` (DB queries), `Service` (three-phase pipeline), `UnitOfWork` (transactional boundaries) |
| `pkg/apperror` | Shared — Errors | Structured `Error` type with `Kind`, `Op`, `Message`, wrapped `Err`, and stack traces. Maps error kinds to HTTP status codes |
| `pkg/db` | Shared — Database | Connection pool initialization with health check, sqlc-generated type-safe query functions for outbox operations |
| `pkg/rabbitmq` | Shared — Messaging | RabbitMQ connection with retry, topology setup (exchange/queue/binding), and `Publisher` with confirm mode |
| `pkg/logger` | Shared — Logging | Zerolog initialization with environment-aware formatting (JSON for production, console for development) |
| `pkg/utils` | Shared — Utilities | JSON response helpers, type converters, response message constants |

---

## Project Structure

```
outbox-worker/
├── cmd/worker/
│   └── main.go                          # Entry point: config → DB → container → worker pool → shutdown
├── config/
│   ├── viper.go                         # Viper config loading with YAML + env overrides + validation
│   ├── model.go                         # Config structs: Config, RunnerConfig, RabbitMQConfig
│   └── env.yaml                         # Default configuration (gitignored)
├── internals/
│   ├── app/
│   │   ├── container.go                 # DI container: creates N publishers + N services, ordered shutdown
│   │   ├── worker_pool.go               # Ticker-based scheduler, backpressure, coordinated shutdown
│   │   └── worker.go                    # Single worker goroutine: ctx/workCh select loop, runOnce()
│   └── outbox/
│       ├── domain.go                    # Event struct: ID (UUID), Type, Payload
│       ├── repository.go               # GetPendingEvents, LockEvents, MarkProcessed + mapDBError
│       ├── service.go                   # Three-phase pipeline: acquireEvents → publishEvents → finalizeEvents
│       └── uow.go                      # UnitOfWork interface + factory (transaction management)
├── pkg/
│   ├── apperror/
│   │   ├── apperror.go                  # Error struct with Kind, Op, Message, Err, Stack
│   │   ├── kind.go                      # Error kinds: Internal, Dependency, Conflict, RequestTimeout, etc.
│   │   ├── codes.go                     # App-specific error codes (frontend-facing)
│   │   └── http.go                      # Kind → HTTP status code mapping
│   ├── db/
│   │   ├── dbConn.go                    # pgx pool initialization with health check (fail-fast)
│   │   ├── db.go                        # sqlc: DBTX interface, Queries struct, WithTx helper
│   │   ├── models.go                    # sqlc generated: OutboxEvent, AuthIdentity models
│   │   └── outbox_events.sql.go         # sqlc generated: GetOutboxEvents, LockOutboxEvents, MarkProcessed
│   ├── rabbitmq/
│   │   ├── rabbitmq.go                  # Connection with 5x retry, topology setup (exchange/queue/bindings)
│   │   └── publisher.go                 # Publisher: dedicated channel, confirm mode, PublishBatch
│   ├── logger/
│   │   └── logger.go                    # Zerolog: JSON (prod) / console (dev), service context, caller info
│   └── utils/
│       ├── response.go                  # WriteJSON, WriteError, FromAppError helpers
│       ├── constants.go                 # Response message constants
│       └── converters.go               # UUID type conversion utilities
├── migration/
│   └── 20251213..._create_outbox_events_table.sql  # Goose migration: outbox_events + index
├── sqlc/
│   ├── sqlc.yaml                        # sqlc config: PostgreSQL + pgx/v5
│   └── queries/
│       └── outbox_events.sql            # SQL query definitions for sqlc code generation
├── go.mod                               # Go 1.24, pgx/v5, amqp091-go, zerolog, viper
└── .env.example                         # Environment variable template
```

---

## Tech Stack

| Layer | Technology | Rationale |
|---|---|---|
| **Language** | Go 1.24 | Goroutines, channels, static binary, explicit error handling |
| **Database** | PostgreSQL + pgx/v5 | `FOR UPDATE SKIP LOCKED`, connection pooling, transaction support |
| **SQL Generation** | sqlc | Type-safe queries from SQL, no ORM overhead |
| **Message Broker** | RabbitMQ + amqp091-go | Publisher confirms, persistent delivery, exchange/queue topology |
| **Config** | Viper | YAML + environment variable overrides + defaults |
| **Logging** | zerolog | Zero-allocation structured JSON logging |
| **Migration** | Goose | SQL-based migrations, up/down support |

---

## Database Schema

```mermaid
erDiagram
    outbox_events {
        uuid id PK "gen_random_uuid()"
        text event_type "NOT NULL"
        jsonb payload "NOT NULL"
        text status "DEFAULT 'PENDING'"
        text locked_by "nullable"
        timestamptz locked_at "nullable"
        timestamptz created_at "DEFAULT now()"
        timestamptz processed_at "nullable"
    }
```

### Event Lifecycle

| Status | Meaning | Transition |
|---|---|---|
| `PENDING` | Event created, waiting for processing | → `PROCESSING` (worker acquires it) |
| `PROCESSING` | Worker has locked the event | → `PROCESSED` (publish confirmed) or → `PENDING` (stale lock, >30s) |
| `PROCESSED` | Successfully published to RabbitMQ | Terminal state |

### Index

```sql
CREATE INDEX idx_outbox_pending ON outbox_events (status, locked_at);
```

This composite index accelerates the `WHERE status = 'PENDING' OR (status = 'PROCESSING' AND locked_at < ...)` query used by every worker on every poll cycle.

---

## Configuration

All system parameters are configurable via `config/env.yaml` with environment variable overrides (via Viper's `AutomaticEnv`):

```yaml
# ─── General ───────────────────────────────────────────
db_url: "postgresql://user:pass@host:5432/dbname?sslmode=require"
port: 8080
env: "production"                       # production | development
service_name: "cosmic-outbox-worker"    # identifier for structured logs

# ─── Worker Pool ──────────────────────────────────────
runner:
  count: 10                             # Number of concurrent worker goroutines
  batch_size: 10                        # Max events fetched per worker per cycle
  interval_time_sec: 5                  # Ticker interval (seconds between work signals)

# ─── RabbitMQ ─────────────────────────────────────────
rabbitmq:
  broker_link: "amqp://guest:guest@localhost:5672/"
  exchange_name: "x.direct.users"       # Exchange to publish events to
  exchange_type: "direct"               # Exchange type (direct, topic, fanout, headers)
  queue_name: "q.create.users"          # Queue bound to the exchange
  routing_key: "rk.create.user"         # Routing key for message routing
```

### Environment Variable Overrides

Viper replaces dots with underscores for env var names:
- `runner.count` → `RUNNER_COUNT`
- `rabbitmq.broker_link` → `RABBITMQ_BROKER_LINK`
- `db_url` → `DB_URL`

---

## Getting Started

### Prerequisites

- Go 1.24+
- PostgreSQL 15+
- RabbitMQ 3.12+
- Goose (for database migrations)

### Run Locally

```bash
# 1. Clone
git clone https://github.com/ashishDevv/outbox-worker.git && cd outbox-worker

# 2. Configure
cp config/env.yaml.example config/env.yaml  # Edit with your DB/RabbitMQ URLs

# 3. Run migrations
goose -dir migration postgres "your_connection_string" up

# 4. Run
go run cmd/worker/main.go
```

### Verify RabbitMQ Topology

After starting, the worker automatically creates the exchange, queue, and binding defined in your config. You can verify via the RabbitMQ management UI at `http://localhost:15672`.

### How to Produce Events

Your application service should insert events into the `outbox_events` table **in the same transaction** as your business operation:

```sql
BEGIN;
    -- Your business operation
    INSERT INTO users (id, name, email) VALUES (...);

    -- Write the event to the outbox (same transaction!)
    INSERT INTO outbox_events (event_type, payload)
    VALUES ('UserCreated', '{"user_id": "...", "email": "..."}');
COMMIT;
```

The Outflow worker will automatically pick up and publish these events to RabbitMQ.

---

*Built with Go, designed for reliability, engineered for at-least-once delivery.*
