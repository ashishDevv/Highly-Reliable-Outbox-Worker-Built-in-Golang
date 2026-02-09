package app

import (
	"context"
	"errors"

	"github.com/ashishDevv/outbox-worker/config"
	"github.com/ashishDevv/outbox-worker/internals/outbox"
	"github.com/ashishDevv/outbox-worker/pkg/rabbitmq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rabbitmq/amqp091-go"
	"github.com/rs/zerolog"
)

type Container struct {
	DBPool    *pgxpool.Pool
	Logger    *zerolog.Logger
	RMQConn   *amqp091.Connection
	Publishers []*rabbitmq.Publisher
	OutboxSvrs []*outbox.Service
}

func NewContainer(dbPool *pgxpool.Pool, cfg *config.Config, logger *zerolog.Logger) (*Container, error) {
	
	outboxRepo := outbox.NewRepository(dbPool)
	uowFactory := outbox.NewUnitOfWorkFactory(dbPool)
	
	rmqpConn, err := setupRabbitMQ(cfg.RabbitMQ)
	if err != nil {
		return nil, err
	}
	
	var publishers []*rabbitmq.Publisher
	var services []*outbox.Service
	
	for range cfg.Runner.Count {
		publisher, err := rabbitmq.NewPublisher(rmqpConn, cfg.RabbitMQ.ExchangeName, cfg.RabbitMQ.RoutingKey)
		if err != nil {
			return nil, err
		}
		svc := outbox.NewService(outboxRepo, publisher, uowFactory)
		
		publishers = append(publishers, publisher)
		services = append(services, svc)
	}
	
	if len(services) != cfg.Runner.Count {
		return nil, errors.New("services/workers mismatch")
	}

	return &Container{
		DBPool:    dbPool,
		Logger:    logger,
		RMQConn: rmqpConn,
		Publishers: publishers,
		OutboxSvrs: services,
	}, nil
}

func (c *Container) Shutdown(ctx context.Context) error {
	// 1. Stop ALL RabbitMQ publishers
	for _, publisher := range c.Publishers {
		_ = publisher.Shutdown()
	}

	// 2. Close RabbitMQ connection
	if c.RMQConn != nil {
		c.RMQConn.Close()
	}

	// 3. Close DB pool
	if c.DBPool != nil {
		c.DBPool.Close()
	}
	return nil
}

func setupRabbitMQ(cfg *config.RabbitMQConfig) (*amqp091.Connection, error) {
	rmqpConn, err := rabbitmq.NewConnection(cfg)
	if err != nil {
		return nil, err
	}
	if err := rabbitmq.SetupTopology(rmqpConn, cfg); err != nil {
		return nil, err
	}
	return rmqpConn, nil
}
