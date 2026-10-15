package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/nigelnunes/durable-flow/examples/onboarding"
	"github.com/nigelnunes/durable-flow/examples/orders"
	"github.com/nigelnunes/durable-flow/examples/reports"
	"github.com/nigelnunes/durable-flow/examples/transfer"
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
	worker.RegisterWorkflow(w, "user_onboarding", onboarding.Workflow)
	worker.RegisterActivity(w, "CreateAccount", onboarding.CreateAccount)
	worker.RegisterActivity(w, "SendEmail", onboarding.SendEmail)
	worker.RegisterWorkflow(w, "money_transfer", transfer.Workflow)
	worker.RegisterActivity(w, "Debit", transfer.Debit)
	worker.RegisterActivity(w, "Credit", transfer.Credit)
	worker.RegisterActivity(w, "Refund", transfer.Refund)
	worker.RegisterWorkflow(w, "report_pipeline", reports.Workflow)
	worker.RegisterActivity(w, "FetchData", reports.FetchData)
	worker.RegisterActivity(w, "Process", reports.Process)
	worker.RegisterActivity(w, "Upload", reports.Upload)
	worker.RegisterActivity(w, "Notify", reports.Notify)
	return w.Run(context.Background())
}
