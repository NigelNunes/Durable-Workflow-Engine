package chaos

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nigelnunes/durable-flow/internal/store"
	"github.com/nigelnunes/durable-flow/migrations"
	"github.com/nigelnunes/durable-flow/sdk/client"
)

const demoKey = "chaos-key"

var (
	dbURL     string
	root      string
	engineBin string
	workerBin string
	pool      *pgxpool.Pool
)

func TestMain(m *testing.M) {
	dbURL = os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	var err error
	root, err = moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	dir, err := os.MkdirTemp("", "durable-chaos")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	engineBin = filepath.Join(dir, "engine")
	workerBin = filepath.Join(dir, "chaos-worker")
	if err := build(engineBin, "./cmd/engine"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := build(workerBin, "./tests/chaos/worker"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx := context.Background()
	if _, err := store.Migrate(ctx, dbURL, migrations.FS); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	pool, err = pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	pool.Close()
	os.RemoveAll(dir)
	os.Exit(code)
}

func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

func build(out, pkg string) error {
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = root
	buf, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build %s: %w\n%s", pkg, err, buf)
	}
	return nil
}

func repeat(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	for i := 1; i <= 20; i++ {
		if !t.Run(fmt.Sprintf("%02d", i), fn) {
			t.Fatalf("iteration %d failed", i)
		}
	}
}

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
	t               *testing.T
	maxRedeliveries int
	port            int
	base            string
	http            *client.Client
	fake            *fakeService
	engine          *proc
	workers         []*proc
	names           int
}

func newCluster(t *testing.T, maxRedeliveries int) *cluster {
	t.Helper()
	if maxRedeliveries <= 0 {
		maxRedeliveries = 20
	}
	c := &cluster{t: t, maxRedeliveries: maxRedeliveries, fake: newFake()}
	t.Cleanup(c.stopAll)
	c.launchEngine()
	c.truncate()
	return c
}

func (c *cluster) launchEngine() {
	c.t.Helper()
	if c.port == 0 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			c.t.Fatal(err)
		}
		c.port = ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		c.base = fmt.Sprintf("http://127.0.0.1:%d", c.port)
		c.http = client.New(c.base, demoKey)
	}
	var last string
	for try := 0; try < 25; try++ {
		c.engine = c.command(engineBin, append(os.Environ(),
			"DATABASE_URL="+dbURL,
			"DEMO_KEY="+demoKey,
			"PORT="+strconv.Itoa(c.port),
			"LEASE_TTL=1s",
			"HEARTBEAT_INTERVAL=200ms",
			"REAPER_INTERVAL=100ms",
			"SCHEDULER_INTERVAL=50ms",
			"MAX_REDELIVERIES="+strconv.Itoa(c.maxRedeliveries),
		))
		if c.waitHealth(2 * time.Second) {
			return
		}
		last = c.engine.out.String()
		c.engine.kill()
		c.engine = nil
		time.Sleep(40 * time.Millisecond)
	}
	c.t.Fatalf("engine did not start: %s", last)
}

func (c *cluster) stopEngine() {
	if c.engine != nil {
		c.engine.kill()
		c.engine = nil
	}
}

func (c *cluster) command(bin string, env []string) *proc {
	c.t.Helper()
	var buf bytes.Buffer
	cmd := exec.Command(bin)
	cmd.Env = env
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	p := &proc{cmd: cmd, out: &buf, done: make(chan struct{})}
	if err := cmd.Start(); err != nil {
		c.t.Fatal(err)
	}
	go func() {
		_ = cmd.Wait()
		close(p.done)
	}()
	return p
}

func (c *cluster) waitHealth(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 200 * time.Millisecond}
	for time.Now().Before(deadline) {
		res, err := client.Get(c.base + "/healthz")
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

func (c *cluster) startWorker(scenario, role, version, name string) *proc {
	c.t.Helper()
	if name == "" {
		c.names++
		name = fmt.Sprintf("%s-%s-%d", scenario, role, c.names)
	}
	p := c.command(workerBin, append(os.Environ(),
		"ENGINE_URL="+c.base,
		"DEMO_KEY="+demoKey,
		"FAKE_URL="+c.fake.url(),
		"SCENARIO="+scenario,
		"ROLE="+role,
		"WORKFLOW_VERSION="+version,
		"WORKER_NAME="+name,
	))
	c.workers = append(c.workers, p)
	return p
}

func (c *cluster) waitWorkers(n int) {
	c.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var got int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM workers`).Scan(&got); err != nil {
			c.t.Fatal(err)
		}
		if got >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.t.Fatalf("registered workers never reached %d", n)
}

func (c *cluster) waitExit(p *proc, timeout time.Duration) {
	c.t.Helper()
	select {
	case <-p.done:
	case <-time.After(timeout):
		c.t.Fatalf("process still running\n%s", p.out.String())
	}
	kept := c.workers[:0]
	for _, w := range c.workers {
		if w != p {
			kept = append(kept, w)
		}
	}
	c.workers = kept
}

func (c *cluster) killRandomAndReplace(scenario string) {
	c.t.Helper()
	if len(c.workers) == 0 {
		c.t.Fatal("no workers left")
	}
	i := rand.Intn(len(c.workers))
	victim := c.workers[i]
	victim.kill()
	c.workers = append(c.workers[:i], c.workers[i+1:]...)
	c.startWorker(scenario, "other", "1", "")
}

func (c *cluster) start(workflowType string, input any) string {
	c.t.Helper()
	id, err := c.http.StartWorkflow(context.Background(), workflowType, input, "")
	if err != nil {
		c.t.Fatal(err)
	}
	return id
}

func (c *cluster) waitStatus(id, want string, timeout time.Duration) client.Workflow {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	var wf client.Workflow
	for time.Now().Before(deadline) {
		got, err := c.http.GetWorkflow(context.Background(), id)
		if err == nil {
			wf = got
			if wf.Status == want {
				return wf
			}
		} else {
			last = err
		}
		time.Sleep(30 * time.Millisecond)
	}
	errText := ""
	if wf.Error != nil {
		errText = *wf.Error
	}
	c.t.Fatalf("workflow %s status %q error %s (last err %v), want %s\nhistory %v", id, wf.Status, errText, last, want, eventTypes(c.events(id)))
	return client.Workflow{}
}

func (c *cluster) events(id string) []client.Event {
	c.t.Helper()
	events, err := c.http.GetHistory(context.Background(), id)
	if err != nil {
		c.t.Fatal(err)
	}
	return events
}

func (c *cluster) waitEventCount(id, eventType string, n int, timeout time.Duration) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)
	var last []client.Event
	for time.Now().Before(deadline) {
		last = c.events(id)
		if countType(last, eventType) >= n {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	c.t.Fatalf("wanted >= %d %s, history %v", n, eventType, eventTypes(last))
}

func (c *cluster) waitNoLeased() {
	c.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var n int
	for {
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM tasks WHERE status = 'leased'`).Scan(&n); err != nil {
			c.t.Fatal(err)
		}
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("%d tasks still leased", n)
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func (c *cluster) rewindActivityLeases() {
	c.t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE tasks
		SET lease_expires_at = now() - interval '30 seconds'
		WHERE status = 'leased' AND kind = 'activity'
	`); err != nil {
		c.t.Fatal(err)
	}
}

func (c *cluster) countStatus(status string) int {
	c.t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM workflows WHERE status = $1`, status).Scan(&n); err != nil {
		c.t.Fatal(err)
	}
	return n
}

func (c *cluster) openActivityTasks() int {
	c.t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM tasks WHERE kind = 'activity' AND status <> 'done'
	`).Scan(&n); err != nil {
		c.t.Fatal(err)
	}
	return n
}

func (c *cluster) truncate() {
	c.t.Helper()
	if _, err := pool.Exec(context.Background(), `TRUNCATE workflows, workers RESTART IDENTITY CASCADE`); err != nil {
		c.t.Fatal(err)
	}
}

func (c *cluster) stopAll() {
	var logs []string
	failed := c.t.Failed()
	for _, w := range c.workers {
		w.kill()
		if failed {
			logs = append(logs, w.out.String())
		}
	}
	c.workers = nil
	if c.engine != nil {
		out := c.engine.out.String()
		c.engine.kill()
		c.engine = nil
		if failed {
			logs = append(logs, "engine:\n"+out)
		}
	}
	if c.fake != nil {
		c.fake.close()
		c.fake = nil
	}
	if failed {
		for _, s := range logs {
			if s != "" {
				c.t.Log(s)
			}
		}
	}
}

func restartPostgres(t *testing.T) {
	t.Helper()
	cmd := exec.Command("docker", "compose", "restart", "postgres")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("restart postgres: %v\n%s", err, out)
	}
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		last = pool.Ping(ctx)
		cancel()
		if last == nil {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("postgres did not come back: %v", last)
}

func countType(events []client.Event, eventType string) int {
	n := 0
	for _, ev := range events {
		if ev.Type == eventType {
			n++
		}
	}
	return n
}

func eventTypes(events []client.Event) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Type
	}
	return out
}

func eventNames(events []client.Event, name string) int {
	n := 0
	for _, ev := range events {
		var body struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(ev.Data, &body) == nil && body.Name == name {
			n++
		}
	}
	return n
}

func resultString(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	return s
}

type fakeService struct {
	srv     *httptest.Server
	mu      sync.Mutex
	charges map[string]string
	blocked int
	failing bool
	ready   chan string
}

func newFake() *fakeService {
	f := &fakeService{
		charges: map[string]string{},
		ready:   make(chan string, 64),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /charge", f.charge)
	mux.HandleFunc("POST /ready", f.markReady)
	mux.HandleFunc("GET /stats", f.stats)
	mux.HandleFunc("GET /failing", f.getFailing)
	mux.HandleFunc("POST /failing", f.postFailing)
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeService) url() string { return f.srv.URL }

func (f *fakeService) close() {
	f.srv.Close()
}

func (f *fakeService) reset() {
	f.mu.Lock()
	f.charges = map[string]string{}
	f.blocked = 0
	f.failing = false
	f.mu.Unlock()
	for {
		select {
		case <-f.ready:
		default:
			return
		}
	}
}

func (f *fakeService) setFailing(v bool) {
	f.mu.Lock()
	f.failing = v
	f.mu.Unlock()
}

func (f *fakeService) snapshot() (charges, blocked int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.charges), f.blocked
}

func (f *fakeService) waitReady(t *testing.T, timeout time.Duration) string {
	t.Helper()
	select {
	case phase := <-f.ready:
		return phase
	case <-time.After(timeout):
		t.Fatal("ready signal not received")
	}
	return ""
}

func (f *fakeService) charge(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" {
		http.Error(w, "missing idempotency key", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing {
		http.Error(w, "payments down", http.StatusServiceUnavailable)
		return
	}
	if id, ok := f.charges[key]; ok {
		f.blocked++
		writeJSON(w, map[string]any{"charge_id": id, "deduplicated": true})
		return
	}
	id := fmt.Sprintf("ch-%d", len(f.charges)+1)
	f.charges[key] = id
	writeJSON(w, map[string]string{"charge_id": id})
}

func (f *fakeService) markReady(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Phase string `json:"phase"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	_ = json.Unmarshal(raw, &body)
	select {
	case f.ready <- body.Phase:
	default:
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeService) stats(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	writeJSON(w, map[string]int{
		"charges":                    len(f.charges),
		"duplicate_charges":          0,
		"duplicate_requests_blocked": f.blocked,
	})
}

func (f *fakeService) getFailing(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	enabled := f.failing
	f.mu.Unlock()
	writeJSON(w, map[string]bool{"enabled": enabled})
}

func (f *fakeService) postFailing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	f.setFailing(body.Enabled)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
