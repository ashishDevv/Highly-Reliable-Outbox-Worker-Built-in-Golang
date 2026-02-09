package main

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"github.com/ashishDevv/outbox-worker/config"
	"github.com/ashishDevv/outbox-worker/internals/app"
	"github.com/ashishDevv/outbox-worker/pkg/db"
	"github.com/ashishDevv/outbox-worker/pkg/logger"
)

func main() {
	// Load envs
	cfg := config.LoadConfig("config/env.yaml")

	// Get Context with signals attached -> when ever a signal occurs , then `Done` channel of ctx will get closed
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Inititiate Base/global logger
	log := logger.Init(cfg)
	log.Info().Msg("logger initialized")

	// Initialize DB Pool
	dbPool, err := db.Init(ctx, db.Config{
        URL:             cfg.DBURL,
        MaxConns:        50,
        MinConns:        5,
        ConnMaxLifetime: time.Hour,
        ConnMaxIdleTime: 30 * time.Minute,
        HealthTimeout:   5 * time.Second,
    }, log)
	if err != nil {
		log.Fatal().Msgf("failed to initialize db pool : %v", err)
	}
	log.Info().Msg("database pool initialized")
	defer dbPool.Close()

	// Inject Dependencies
	container, err := app.NewContainer(dbPool, cfg, log)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize dependencies")
	}
	log.Info().Msg("dependencies initialized")
	
	// Initilize WorkerPool
	wp := app.NewWorkerPool(ctx, container, cfg)
	log.Info().Msgf("worker pool created of size : %v", wp.WorkerCount)
	
	// Start workers
	wp.StartWorkers()
	log.Info().Msg("Workers started")
	
	<-ctx.Done() 
	log.Info().Msg("shutdown signal received")
	
	// 1. Stop WorkerPool -> it will stop all workers
	wp.ShutdownWorkers()
	
	// 2. Shutdown other dependecies
	shutdownCtx, cancle := context.WithTimeout(context.Background(), 30*time.Second) // this is new context, acts as buffer time to close all resources
	defer cancle()

	if err := container.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("dependecies shutdown failed")
	}

	// Shutdown done
	log.Info().Msg("graceful shutdown complete") 
}
