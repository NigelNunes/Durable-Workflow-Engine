package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/nigelnunes/durable-flow/examples/orders"
	"github.com/nigelnunes/durable-flow/sdk/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("demo worker stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	engineURL := os.Getenv("ENGINE_URL")
	demoKey := os.Getenv("DEMO_KEY")
	if engineURL == "" || demoKey == "" || os.Getenv("SERVICES_URL") == "" {
		return fmt.Errorf("ENGINE_URL, DEMO_KEY, and SERVICES_URL are required")
	}
	name := os.Getenv("WORKER_NAME")
	if name == "" {
		name = "demo-worker"
	}
	w := worker.New(worker.Options{
		Name:      name,
		EngineURL: engineURL,
		DemoKey:   demoKey,
	})
	worker.RegisterWorkflow(w, "order_fulfilment", orders.Workflow)
	worker.RegisterActivity(w, "ChargeCard", orders.ChargeCard)
	worker.RegisterActivity(w, "ReserveStock", orders.ReserveStock)
	worker.RegisterActivity(w, "SendReceipt", orders.SendReceipt)
	return w.Run(context.Background())
}
