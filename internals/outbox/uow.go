package outbox

import (
	"context"

	"github.com/ashishDevv/outbox-worker/pkg/apperror"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)


type UnitOfWork interface {
	Repo() *Repository
	Commit() error
	Rollback()
}

type unitOfWork struct {
	ctx context.Context
	tx  pgx.Tx
	repo *Repository
}

func NewUnitOfWork(ctx context.Context, conn *pgxpool.Pool) (*unitOfWork, error) {
	const op = "uow.begin"
	
	tx, err := conn.Begin(ctx)
	if err != nil {
		return nil, &apperror.Error{
			Kind:    apperror.Internal,
			Op:      op,
			Message: "failed to begin transaction",
			Err:     err,
		}
	}
	return &unitOfWork{
		ctx:  ctx,
		tx:   tx,
		repo: NewRepository(tx),
	}, nil
}

func (u *unitOfWork) Repo() *Repository {
	return u.repo
}

func (u *unitOfWork) Commit() error {
	const op = "uow.commit"
	
	if err := u.tx.Commit(u.ctx); err != nil {
		return &apperror.Error{
			Kind:    apperror.Internal,
			Op:      op,
			Message: "failed to commit transaction",
			Err:     err,
		}
	}
	return nil
}

func (u *unitOfWork) Rollback() {
	_ = u.tx.Rollback(u.ctx) // intentionally ignored
}

////////////////////////////////////////////////////////////////////

type UnitOfWorkFactory struct {
	conn *pgxpool.Pool
}

func NewUnitOfWorkFactory(conn *pgxpool.Pool) *UnitOfWorkFactory {
	return &UnitOfWorkFactory{
		conn: conn,
	}
}

func (f *UnitOfWorkFactory) NewUnitOfWork(ctx context.Context) (UnitOfWork, error){
	return NewUnitOfWork(ctx, f.conn)
}