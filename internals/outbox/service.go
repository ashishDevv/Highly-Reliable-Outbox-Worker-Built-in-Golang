package outbox

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/ashishDevv/outbox-worker/pkg/apperror"
	"github.com/ashishDevv/outbox-worker/pkg/rabbitmq"
	"github.com/google/uuid"
)

type Service struct {
	repo         *Repository
	rmqPublisher *rabbitmq.Publisher
	uowFactory   *UnitOfWorkFactory
	
}

func NewService(repo *Repository, rmqPublisher *rabbitmq.Publisher, uowFactory *UnitOfWorkFactory) *Service {
	return &Service{
		repo:         repo,
		rmqPublisher: rmqPublisher,
		uowFactory:   uowFactory,
	}
}

func (s *Service) ProcessOutboxEvents(ctx context.Context) error {
	err := s.process(ctx)
	if err == nil {
		return nil
	}

	var appErr *apperror.Error
	if errors.As(err, &appErr) {
		return appErr
	}

	return &apperror.Error{
		Kind: apperror.Internal,
		Op:   "service.outbox.process",
		Err:  err,
	}
}


func (s *Service) process(ctx context.Context) error {
	events, err := s.acquireEvents(ctx, 50)
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}

	if err := s.publishEvents(ctx, events); err != nil {
		return err
	}

	return s.finalizeEvents(ctx, events)
}


func (s *Service) acquireEvents(ctx context.Context, limit int) ([]Event, error) {
	const op = "service.outbox.acquire_events"

	uow, err := s.uowFactory.NewUnitOfWork(ctx)
	if err != nil {
		return nil, err
	}
	defer uow.Rollback()

	events, err := uow.Repo().GetPendingEvents(ctx, limit)
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, nil
	}

	ids := make([]uuid.UUID, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID)
	}

	if err := uow.Repo().LockEvents(ctx, "worker-1", ids); err != nil {
		return nil, err
	}

	if err := uow.Commit(); err != nil {
		return nil, err
	}

	return events, nil
}

func (s *Service) publishEvents(ctx context.Context, events []Event) error {
	const op = "service.outbox.publish"

	data := make([][]byte, 0, len(events))
	for _, e := range events {
		payload, err := json.Marshal(e)
		if err != nil {
			return &apperror.Error{
				Kind:    apperror.Internal,
				Op:      op,
				Message: "failed to marshal event",
				Err:     err,
			}
		}
		data = append(data, payload)
	}

	if err := s.rmqPublisher.PublishBatch(ctx, data); err != nil {
		return &apperror.Error{
			Kind:    apperror.Dependency,
			Op:      op,
			Message: "failed to publish events on RabbiMQ",
			Err:     err,
		}
	}

	return nil
}

func (s *Service) finalizeEvents(ctx context.Context, events []Event) error {
	const op = "service.outbox.finalize"

	uow, err := s.uowFactory.NewUnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer uow.Rollback()

	ids := make([]uuid.UUID, 0, len(events))
	for _, e := range events {
		ids = append(ids, e.ID)
	}

	if err := uow.Repo().MarkProcessed(ctx, ids); err != nil {
		return err
	}

	if err := uow.Commit(); err != nil {
		return err
	}

	return nil
}