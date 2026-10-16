package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nigelnunes/durable-flow/sdk/client"
)

const demoKey = "bench-key"

type proc struct {
	cmd  *exec.Cmd
	out  *bytes.Buffer
	done chan struct{}
}

func (p *proc) kill() {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
}

type cluster struct {
	dbURL       string
	pool        *pgxpool.Pool
	root        string
	binDir      string
	engineBin   string
	workerBin   string
	servicesBin string
	port        int
	svcPort     int
	base        string
	svcBase     string
	http        *client.Client
	engine      *proc
	services    *proc
	workers     []*proc
	metricsPath string
	leaseTTL    string
	orders      bool
}

func newCluster(dbURL string, orders bool, leaseTTL string) (*cluster, error) {
	if leaseTTL == "" {
		leaseTTL = "1s"
	}
	root, err := moduleRoot()
	if err != nil {
		return nil, err
	}
	binDir, err := os.MkdirTemp("", "durable-bench-*")
	if err != nil {
		return nil, err
	}
	c := &cluster{
		dbURL:    dbURL,
		root:     root,
		binDir:   binDir,
		orders:   orders,
		leaseTTL: leaseTTL,
	}
	c.engineBin = filepath.Join(binDir, "engine")
	c.workerBin = filepath.Join(binDir, "bench-worker")
	c.servicesBin = filepath.Join(binDir, "demo-services")
	c.metricsPath = filepath.Join(binDir, "dispatch.metrics")

	if err := build(c.engineBin, "./cmd/engine", root); err != nil {
		_ = os.RemoveAll(binDir)
		return nil, err
	}
	if err := build(c.workerBin, "./bench/worker", root); err != nil {
		_ = os.RemoveAll(binDir)
		return nil, err
	}
	if orders {
		if err := build(c.servicesBin, "./cmd/demo-services", root); err != nil {
			_ = os.RemoveAll(binDir)
			return nil, err
		}
	}

	if err := runMigrate(root, dbURL); err != nil {
		_ = os.RemoveAll(binDir)
		return nil, err
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		_ = os.RemoveAll(binDir)
		return nil, err
	}
	c.pool = pool
	if err := c.launchEngine(); err != nil {
		c.close()
		return nil, err
	}
	if orders {
		if err := c.launchServices(); err != nil {
			c.close()
			return nil, err
		}
	}
	if err := c.truncate(); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func (c *cluster) close() {
	for _, w := range c.workers {
		w.kill()
	}
	c.workers = nil
	if c.engine != nil {
		c.engine.kill()
		c.engine = nil
	}
	if c.services != nil {
		c.services.kill()
		c.services = nil
	}
	if c.pool != nil {
		c.pool.Close()
	}
	_ = os.RemoveAll(c.binDir)
}

func (c *cluster) launchEngine() error {
	if c.port == 0 {
		port, err := freePort()
		if err != nil {
			return err
		}
		c.port = port
		c.base = fmt.Sprintf("http://127.0.0.1:%d", c.port)
		c.http = client.New(c.base, demoKey)
	}
	var last string
	for try := 0; try < 25; try++ {
		c.engine = c.command(c.engineBin, append(os.Environ(),
			"DATABASE_URL="+c.dbURL,
			"DEMO_KEY="+demoKey,
			"PORT="+strconv.Itoa(c.port),
			"LEASE_TTL="+c.leaseTTL,
			"HEARTBEAT_INTERVAL=200ms",
			"REAPER_INTERVAL=100ms",
			"SCHEDULER_INTERVAL=50ms",
		))
		if c.waitURL(c.base+"/healthz", 2*time.Second) {
			return nil
		}
		last = c.engine.out.String()
		c.engine.kill()
		c.engine = nil
		time.Sleep(40 * time.Millisecond)
	}
	return fmt.Errorf("engine did not start: %s", last)
}

func (c *cluster) launchServices() error {
	port, err := freePort()
	if err != nil {
		return err
	}
	c.svcPort = port
	c.svcBase = fmt.Sprintf("http://127.0.0.1:%d", c.svcPort)
	c.services = c.command(c.servicesBin, append(os.Environ(),
		"DATABASE_URL="+c.dbURL,
		"PORT="+strconv.Itoa(c.svcPort),
		"LATENCY=0",
	))
	if !c.waitURL(c.svcBase+"/healthz", 3*time.Second) {
		last := c.services.out.String()
		c.services.kill()
		c.services = nil
		return fmt.Errorf("demo-services did not start: %s", last)
	}
	return nil
}

func (c *cluster) command(bin string, env []string) *proc {
	var buf bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Env = env
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	p := &proc{cmd: cmd, out: &buf, done: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p
}

func (c *cluster) waitURL(url string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	httpClient := &http.Client{Timeout: 200 * time.Millisecond}
	for time.Now().Before(deadline) {
		res, err := httpClient.Get(url)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (c *cluster) startWorkers(n int) error {
	return c.startWorkersWith(n, nil)
}

func (c *cluster) startWorkersWith(n int, extra []string) error {
	_ = os.Remove(c.metricsPath)
	for i := 0; i < n; i++ {
		env := append(os.Environ(),
			"ENGINE_URL="+c.base,
			"DEMO_KEY="+demoKey,
			"DATABASE_URL="+c.dbURL,
			"BENCH_METRICS="+c.metricsPath,
			"WORKER_NAME="+fmt.Sprintf("bench-%d", i+1),
		)
		env = append(env, extra...)
		if c.orders {
			env = append(env, "BENCH_ORDERS=1", "SERVICES_URL="+c.svcBase)
		}
		c.workers = append(c.workers, c.command(c.workerBin, env))
	}
	return c.waitWorkers(n, 5*time.Second)
}

func (c *cluster) stopWorkers() {
	for _, w := range c.workers {
		w.kill()
	}
	c.workers = nil
}

func (c *cluster) killOneAndReplace(extra ...string) {
	if len(c.workers) == 0 {
		return
	}
	idx := len(c.workers) - 1
	c.workers[idx].kill()
	c.workers = c.workers[:idx]
	env := append(os.Environ(),
		"ENGINE_URL="+c.base,
		"DEMO_KEY="+demoKey,
		"DATABASE_URL="+c.dbURL,
		"BENCH_METRICS="+c.metricsPath,
		"WORKER_NAME="+fmt.Sprintf("bench-r-%d", time.Now().UnixNano()),
	)
	env = append(env, extra...)
	if c.orders {
		env = append(env, "BENCH_ORDERS=1", "SERVICES_URL="+c.svcBase)
	}
	c.workers = append(c.workers, c.command(c.workerBin, env))
}

func (c *cluster) waitWorkers(n int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var count int
		err := c.pool.QueryRow(context.Background(), `SELECT count(*) FROM workers`).Scan(&count)
		if err == nil && count >= n {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %d workers", n)
}

func (c *cluster) truncate() error {
	_, err := c.pool.Exec(context.Background(), `TRUNCATE workflows, workers RESTART IDENTITY CASCADE`)
	return err
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}

func moduleRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", wd)
		}
		dir = parent
	}
}

func build(out, pkg, root string) error {
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = root
	cmd.Env = os.Environ()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build %s: %w\n%s", pkg, err, buf.String())
	}
	return nil
}

func runMigrate(root, dbURL string) error {
	cmd := exec.Command("go", "run", "./cmd/migrate")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "DATABASE_URL="+dbURL)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("migrate: %w\n%s", err, buf.String())
	}
	return nil
}
