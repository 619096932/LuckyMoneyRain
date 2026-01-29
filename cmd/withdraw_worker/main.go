package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"hongbao/internal/config"
	"hongbao/internal/db"
	"hongbao/internal/handlers"
)

func main() {
	cfg := config.Load()
	mysql, err := db.NewMySQL(cfg.MySQLDSN)
	if err != nil {
		log.Fatalf("mysql init error: %v", err)
	}
	defer mysql.Close()

	srv := handlers.NewServer(cfg, mysql, nil)
	worker := handlers.NewWithdrawWorker(srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("withdraw worker started")
	worker.Run(ctx)
	log.Printf("withdraw worker stopped")
}
