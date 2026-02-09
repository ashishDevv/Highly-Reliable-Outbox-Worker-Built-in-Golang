package outbox

import (
	"context"
	"errors"

	"github.com/ashishDevv/outbox-worker/pkg/apperror"
	"github.com/ashishDevv/outbox-worker/pkg/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct {
	qu *db.Queries
}

func NewRepository(dbExecutor db.DBTX) *Repository {
	return &Repository{
		qu: db.New(dbExecutor),
	}
}

func (r *Repository) GetPendingEvents(ctx context.Context, limit int) ([]Event, error) {
	const op = "repo.outbox.get_pending_events"

	rows, err := r.qu.GetOutboxEvents(ctx, int32(limit))
	if err != nil {
		return nil, mapDBError(op, err)
	}

	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, Event{
			ID:      row.ID.Bytes,
			Type:    row.EventType,
			Payload: row.Payload,
		})
	}

	return events, nil
}


func (r *Repository) LockEvents(ctx context.Context, workerID string, ids []uuid.UUID) error {
	const op = "repo.outbox.lock_events"

	if len(ids) == 0 {
		return nil
	}
	
	pgIDs := make([]pgtype.UUID, 0, len(ids))

	for _, id := range ids {
		pgIDs = append(pgIDs, pgtype.UUID{
			Bytes: id,
			Valid: true,
		})
	}

	rows, err := r.qu.LockOutboxEvents(ctx, db.LockOutboxEventsParams{
		LockedBy: pgtype.Text{
			String: workerID,
			Valid:  true,
			},
		Column2: pgIDs,
	})
	
	if err != nil {
		return mapDBError(op, err)
	}
	if int(rows) != len(pgIDs) {
		return &apperror.Error{
			Kind:    apperror.Conflict,
			Op:      op,
			Message: "some events were already locked",
		}
	}

	return nil
}


func (r *Repository) MarkProcessed(ctx context.Context, ids []uuid.UUID) error {
	const op string = "repo.events.mark_processed"
	
	if len(ids) == 0 {
		return nil
	}
	
	pgIDs := make([]pgtype.UUID, 0, len(ids))

	for _, id := range ids {
		pgIDs = append(pgIDs, pgtype.UUID{
			Bytes: id,
			Valid: true,
		})
	}

	rows, err := r.qu.MarkOutboxEventProcessed(ctx, pgIDs)
	
	if err != nil {
		return mapDBError(op, err)
	}
	if int(rows) != len(pgIDs) {
		return &apperror.Error{
			Kind:    apperror.Conflict,
			Op:      op,
			Message: "some events were already locked",
		}
	}

	return nil
}


func mapDBError(op string, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &apperror.Error{
			Kind:    apperror.RequestTimeout,
			Op:      op,
			Message: "request cancelled or timed out",
			Err:     err,
		}
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return &apperror.Error{
			Kind:    apperror.Internal,
			Op:      op,
			Message: "database error",
			Err:     err,
		}
	}

	return &apperror.Error{
		Kind:    apperror.Internal,
		Op:      op,
		Message: "unexpected repository error",
		Err:     err,
	}
}

