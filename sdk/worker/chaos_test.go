package worker

import "testing"

func TestCrashExits137(t *testing.T) {
	w := New(Options{Name: "crash", EngineURL: "http://engine", DemoKey: "k"})
	var code int
	w.exit = func(c int) { code = c }
	w.applyChaos("crash")
	if code != 137 {
		t.Fatalf("exit code %d", code)
	}
}

func TestStallZeroPausesHeartbeats(t *testing.T) {
	w := New(Options{Name: "stall", EngineURL: "http://engine", DemoKey: "k"})
	if w.heartbeatsPaused() {
		t.Fatal("paused before stall")
	}
	w.applyChaos("stall:0")
	if !w.heartbeatsPaused() {
		t.Fatal("stall did not pause heartbeats")
	}
}
