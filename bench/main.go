// Command bench measures Durable Flow throughput, latency, dispatch, recovery,
// chaos correctness, and DB footprint. Numbers are printed as markdown.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/nigelnunes/durable-flow/examples/orders"
	"github.com/nigelnunes/durable-flow/sdk/client"
)

func main() {
	dbURL := flag.String("database-url", os.Getenv("DATABASE_URL"), "Postgres URL")
	mode := flag.String("mode", "all", "all|throughput|latency|dispatch|recovery|chaos|footprint|machine")
	workers := flag.Int("workers", 3, "worker count for single-run modes")
	count := flag.Int("count", 50, "workflows per run")
	runs := flag.Int("runs", 3, "repetitions for median/spread")
	leaseTTL := flag.String("lease-ttl", "1s", "LEASE_TTL for the engine")
	flag.Parse()
	if *dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL or -database-url is required")
		os.Exit(2)
	}

	switch *mode {
	case "machine":
		printMachine()
	case "all":
		printMachine()
		fmt.Println()
		runThroughput(*dbURL, *runs, *leaseTTL)
		fmt.Println()
		runLatencyDispatch(*dbURL, *workers, *count, *runs, *leaseTTL, false)
		fmt.Println()
		runLatencyDispatch(*dbURL, *workers, *count, *runs, *leaseTTL, true)
		fmt.Println()
		runRecovery(*dbURL, *leaseTTL)
		fmt.Println()
		runChaosCorrectness(*dbURL, *leaseTTL)
		fmt.Println()
		runFootprint(*dbURL, *leaseTTL)
	case "throughput":
		runThroughput(*dbURL, *runs, *leaseTTL)
	case "latency", "dispatch":
		runLatencyDispatch(*dbURL, *workers, *count, *runs, *leaseTTL, false)
	case "recovery":
		runRecovery(*dbURL, *leaseTTL)
	case "chaos":
		runChaosCorrectness(*dbURL, *leaseTTL)
	case "footprint":
		runFootprint(*dbURL, *leaseTTL)
	default:
		fmt.Fprintf(os.Stderr, "unknown mode %q\n", *mode)
		os.Exit(2)
	}
}

func printMachine() {
	fmt.Println("## Machine")
	fmt.Printf("- Go: %s\n", runtime.Version())
	fmt.Printf("- OS/Arch: %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("- CPUs: %d\n", runtime.NumCPU())
	if out, err := exec.Command("uname", "-a").Output(); err == nil {
		fmt.Printf("- uname: %s\n", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		var n int64
		if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); err == nil {
			fmt.Printf("- RAM: %.1f GiB\n", float64(n)/(1<<30))
		}
	}
	fmt.Println("- Postgres: local Compose on host port 5433 (defaults)")
	fmt.Println("- Engine LEASE_TTL: see each experiment (bench default 1s)")
}

func runThroughput(dbURL string, runs int, leaseTTL string) {
	fmt.Println("## Throughput")
	fmt.Println("Completed workflows per second. Median of", runs, "runs. LEASE_TTL="+leaseTTL)
	fmt.Println()
	fmt.Println("| Scenario | Workers | Workflows | Median wf/s | Spread (min–max) |")
	fmt.Println("|----------|---------|-----------|-------------|------------------|")
	for _, useOrders := range []bool{false, true} {
		name := "bench_noop"
		nWF := 80
		if useOrders {
			name = "order_fulfilment"
			nWF = 40
		}
		for _, w := range []int{1, 3, 6} {
			rates := make([]float64, 0, runs)
			for i := 0; i < runs; i++ {
				rate, err := oneThroughput(dbURL, w, nWF, useOrders, leaseTTL)
				if err != nil {
					fmt.Fprintf(os.Stderr, "throughput %s workers=%d run=%d: %v\n", name, w, i+1, err)
					continue
				}
				rates = append(rates, rate)
			}
			lo, hi := spread(rates)
			fmt.Printf("| %s | %d | %d | %.2f | %.2f–%.2f |\n", name, w, nWF, medianOf(rates), lo, hi)
		}
	}
}

func oneThroughput(dbURL string, workers, count int, useOrders bool, leaseTTL string) (float64, error) {
	c, err := newCluster(dbURL, useOrders, leaseTTL)
	if err != nil {
		return 0, err
	}
	defer c.close()
	if err := c.startWorkers(workers); err != nil {
		return 0, err
	}
	start := time.Now()
	ids, err := startMany(c, count, useOrders)
	if err != nil {
		return 0, err
	}
	if err := waitAll(c.http, ids, 3*time.Minute); err != nil {
		return 0, err
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	return float64(count) / elapsed, nil
}

func runLatencyDispatch(dbURL string, workers, count, runs int, leaseTTL string, useOrders bool) {
	name := "bench_noop"
	if useOrders {
		name = "order_fulfilment"
		if count > 40 {
			count = 40
		}
	}
	fmt.Printf("## Latency and dispatch (%s, %d workers)\n", name, workers)
	fmt.Println("End-to-end client latency and task pending→leased dispatch. LEASE_TTL="+leaseTTL)
	fmt.Println()

	var e2eP50, e2eP95, e2eP99, dispP50, dispP99 []float64
	for i := 0; i < runs; i++ {
		lat, disp, err := oneLatency(dbURL, workers, count, useOrders, leaseTTL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "latency run %d: %v\n", i+1, err)
			continue
		}
		e2eP50 = append(e2eP50, lat.percentile(50))
		e2eP95 = append(e2eP95, lat.percentile(95))
		e2eP99 = append(e2eP99, lat.percentile(99))
		dispP50 = append(dispP50, disp.percentile(50))
		dispP99 = append(dispP99, disp.percentile(99))
	}
	fmt.Println("| Metric | Median across runs | Spread |")
	fmt.Println("|--------|--------------------|--------|")
	printMetricRow("e2e p50 (ms)", e2eP50)
	printMetricRow("e2e p95 (ms)", e2eP95)
	printMetricRow("e2e p99 (ms)", e2eP99)
	printMetricRow("dispatch p50 (ms)", dispP50)
	printMetricRow("dispatch p99 (ms)", dispP99)
}

func printMetricRow(label string, vals []float64) {
	lo, hi := spread(vals)
	fmt.Printf("| %s | %s | %s–%s |\n", label, fmtMS(medianOf(vals)), fmtMS(lo), fmtMS(hi))
}

func oneLatency(dbURL string, workers, count int, useOrders bool, leaseTTL string) (sampleSet, sampleSet, error) {
	c, err := newCluster(dbURL, useOrders, leaseTTL)
	if err != nil {
		return sampleSet{}, sampleSet{}, err
	}
	defer c.close()
	if err := c.startWorkers(workers); err != nil {
		return sampleSet{}, sampleSet{}, err
	}

	type timed struct {
		id    string
		start time.Time
	}
	started := make([]timed, 0, count)
	for i := 0; i < count; i++ {
		t0 := time.Now()
		id, err := startOne(c, i, useOrders)
		if err != nil {
			return sampleSet{}, sampleSet{}, err
		}
		started = append(started, timed{id: id, start: t0})
	}
	ids := make([]string, len(started))
	for i, s := range started {
		ids[i] = s.id
	}
	if err := waitAll(c.http, ids, 3*time.Minute); err != nil {
		return sampleSet{}, sampleSet{}, err
	}
	var lat sampleSet
	for _, s := range started {
		events, err := c.http.GetHistory(context.Background(), s.id)
		if err != nil || len(events) == 0 {
			return sampleSet{}, sampleSet{}, fmt.Errorf("history %s: %v", s.id, err)
		}
		end := events[len(events)-1].CreatedAt
		lat.add(end.Sub(s.start))
	}
	disp, err := loadDispatchFile(c.metricsPath)
	if err != nil {
		return sampleSet{}, sampleSet{}, err
	}
	return lat, disp, nil
}

func runRecovery(dbURL, leaseTTL string) {
	fmt.Println("## Recovery time")
	fmt.Println("Forced worker SIGKILL while workflows are in flight. LEASE_TTL="+leaseTTL)
	fmt.Println("Measure: lease-expiry event → next ActivityCompleted or WorkflowCompleted.")
	fmt.Println()

	c, err := newCluster(dbURL, false, leaseTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recovery: %v\n", err)
		return
	}
	defer c.close()
	slow := []string{"BENCH_SLOW_ACTIVITY_MS=1500"}
	if err := c.startWorkersWith(3, slow); err != nil {
		fmt.Fprintf(os.Stderr, "recovery workers: %v\n", err)
		return
	}

	const workflows = 120
	const kills = 100
	ids, err := startMany(c, workflows, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "recovery start: %v\n", err)
		return
	}

	done := make(chan struct{})
	go func() {
		_ = waitAll(c.http, ids, 8*time.Minute)
		close(done)
	}()

killLoop:
	for i := 0; i < kills; i++ {
		select {
		case <-done:
			break killLoop
		default:
		}
		c.killOneAndReplace(slow...)
		time.Sleep(500 * time.Millisecond)
	}
	<-done

	samples, err := c.recoverySamples()
	if err != nil {
		fmt.Fprintf(os.Stderr, "recovery query: %v\n", err)
		return
	}
	fmt.Printf("- Samples: %d\n", samples.len())
	fmt.Printf("- Min: %s ms\n", fmtMS(samples.min()))
	fmt.Printf("- Median: %s ms\n", fmtMS(samples.median()))
	fmt.Printf("- p95: %s ms\n", fmtMS(samples.percentile(95)))
	fmt.Printf("- Max: %s ms\n", fmtMS(samples.max()))
}

func (c *cluster) recoverySamples() (sampleSet, error) {
	rows, err := c.pool.Query(context.Background(), `
		SELECT EXTRACT(EPOCH FROM (done.created_at - exp.created_at)) * 1000 AS ms
		FROM workflow_events exp
		JOIN LATERAL (
			SELECT created_at FROM workflow_events done
			WHERE done.workflow_id = exp.workflow_id
			  AND done.seq > exp.seq
			  AND done.type IN ('ActivityCompleted', 'WorkflowCompleted')
			ORDER BY done.seq
			LIMIT 1
		) done ON true
		WHERE exp.type IN ('ActivityLeaseExpired', 'WorkflowTaskLeaseExpired')
	`)
	if err != nil {
		return sampleSet{}, err
	}
	defer rows.Close()
	var out sampleSet
	for rows.Next() {
		var ms float64
		if err := rows.Scan(&ms); err != nil {
			return sampleSet{}, err
		}
		out.addMS(ms)
	}
	return out, rows.Err()
}

func runChaosCorrectness(dbURL, leaseTTL string) {
	fmt.Println("## Correctness under chaos")
	fmt.Println("200 order workflows, 3 workers, kill+replace one every 10s. LATENCY=0. LEASE_TTL="+leaseTTL)
	fmt.Println()

	c, err := newCluster(dbURL, true, leaseTTL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chaos: %v\n", err)
		return
	}
	defer c.close()
	if err := c.startWorkers(3); err != nil {
		fmt.Fprintf(os.Stderr, "chaos workers: %v\n", err)
		return
	}

	const n = 200
	ids, err := startMany(c, n, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "chaos start: %v\n", err)
		return
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				c.killOneAndReplace()
			}
		}
	}()

	err = waitAll(c.http, ids, 10*time.Minute)
	close(stop)
	wg.Wait()
	if err != nil {
		fmt.Fprintf(os.Stderr, "chaos wait: %v\n", err)
		return
	}

	completed := 0
	failed := 0
	for _, id := range ids {
		wf, err := c.http.GetWorkflow(context.Background(), id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "get %s: %v\n", id, err)
			continue
		}
		switch wf.Status {
		case "completed":
			completed++
		case "failed":
			failed++
		}
	}
	dup, err := c.duplicateCharges()
	if err != nil {
		fmt.Fprintf(os.Stderr, "stats: %v\n", err)
		return
	}
	fmt.Printf("- Workflows completed: %d / %d\n", completed, n)
	fmt.Printf("- Workflows failed: %d\n", failed)
	fmt.Printf("- Lost workflows: %d\n", n-completed-failed)
	fmt.Printf("- Duplicate charges: %d\n", dup)
	fmt.Println("- Chaos suite F1–F12: see docs/guarantees.md (Phase 7, 20/20 each)")
}

func (c *cluster) duplicateCharges() (int, error) {
	res, err := http.Get(c.svcBase + "/stats")
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	var stats struct {
		DuplicateCharges int `json:"duplicate_charges"`
	}
	if err := json.Unmarshal(body, &stats); err != nil {
		return 0, err
	}
	return stats.DuplicateCharges, nil
}

func runFootprint(dbURL, leaseTTL string) {
	fmt.Println("## Database footprint")
	fmt.Println("Rows and approximate bytes for one completed workflow. LEASE_TTL="+leaseTTL)
	fmt.Println()

	for _, useOrders := range []bool{false, true} {
		name := "bench_noop"
		if useOrders {
			name = "order_fulfilment"
		}
		c, err := newCluster(dbURL, useOrders, leaseTTL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "footprint: %v\n", err)
			continue
		}
		if err := c.startWorkers(1); err != nil {
			c.close()
			fmt.Fprintf(os.Stderr, "footprint workers: %v\n", err)
			continue
		}
		id, err := startOne(c, 0, useOrders)
		if err != nil {
			c.close()
			fmt.Fprintf(os.Stderr, "footprint start: %v\n", err)
			continue
		}
		if err := waitAll(c.http, []string{id}, time.Minute); err != nil {
			c.close()
			fmt.Fprintf(os.Stderr, "footprint wait: %v\n", err)
			continue
		}
		rows, bytes, err := c.footprint(id)
		c.close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "footprint query: %v\n", err)
			continue
		}
		fmt.Printf("### %s (`%s`)\n", name, id)
		fmt.Printf("- Rows: %d (workflows + events + tasks)\n", rows)
		fmt.Printf("- Approx bytes: %d\n\n", bytes)
	}
}

func (c *cluster) footprint(id string) (rows int, bytes int, err error) {
	err = c.pool.QueryRow(context.Background(), `
		SELECT
			1 + (SELECT count(*) FROM workflow_events e WHERE e.workflow_id = $1)
			  + (SELECT count(*) FROM tasks t WHERE t.workflow_id = $1),
			COALESCE((SELECT pg_column_size(w.*) FROM workflows w WHERE w.id = $1), 0)
			+ COALESCE((SELECT sum(pg_column_size(e.*)) FROM workflow_events e WHERE e.workflow_id = $1), 0)
			+ COALESCE((SELECT sum(pg_column_size(t.*)) FROM tasks t WHERE t.workflow_id = $1), 0)
	`, id).Scan(&rows, &bytes)
	return rows, bytes, err
}

func startMany(c *cluster, count int, useOrders bool) ([]string, error) {
	ids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		id, err := startOne(c, i, useOrders)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func startOne(c *cluster, i int, useOrders bool) (string, error) {
	ctx := context.Background()
	if !useOrders {
		return c.http.StartWorkflow(ctx, "bench_noop", struct{}{}, "")
	}
	id := fmt.Sprintf("bench-ord-%d-%d", time.Now().UnixNano(), i)
	in := orders.Input{
		OrderID:     id,
		AmountCents: 100,
		SKU:         "widget",
		Qty:         1,
		Email:       "bench@example.com",
		PaceSeconds: 0,
	}
	return c.http.StartWorkflow(ctx, "order_fulfilment", in, id)
}

func waitAll(c *client.Client, ids []string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	pending := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		pending[id] = struct{}{}
	}
	for len(pending) > 0 {
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for %d workflows", len(pending))
		}
		for id := range pending {
			wf, err := c.GetWorkflow(context.Background(), id)
			if err != nil {
				return err
			}
			if wf.Status == "completed" || wf.Status == "failed" {
				delete(pending, id)
			}
		}
		if len(pending) > 0 {
			time.Sleep(25 * time.Millisecond)
		}
	}
	return nil
}
