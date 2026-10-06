package sysinfo

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"
)

// The machine is read; a child of this process shows under it, with its
// name; killing it ends it and what it started.
func TestTreeAndKill(t *testing.T) {
	ctx := context.Background()
	s := New(t.TempDir())
	m := s.Machine(ctx)
	if m.MemTotal == 0 || m.DiskTotal == 0 || len(m.Cores) == 0 {
		t.Fatalf("machine = %+v", m)
	}
	cmd := exec.Command("/bin/sh", "-c", "sleep 30 & wait")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var list []Proc
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		self, l, err := s.Tree(ctx, int32(os.Getpid()))
		if err != nil || self.PID != int32(os.Getpid()) {
			t.Fatal(self, err)
		}
		list = l
		if slices.ContainsFunc(list, func(p Proc) bool { return p.Name == "sleep" && p.Depth == 1 }) || time.Now().After(deadline) {
			break
		}
	}
	i := slices.IndexFunc(list, func(p Proc) bool { return p.PID == int32(cmd.Process.Pid) })
	if i < 0 || list[i].Depth != 0 || !slices.ContainsFunc(list, func(p Proc) bool { return p.Name == "sleep" && p.Depth == 1 }) {
		t.Fatalf("tree = %+v", list)
	}
	if err := KillTree(ctx, int32(cmd.Process.Pid)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("not killed")
	}
	time.Sleep(100 * time.Millisecond)
	if _, l, _ := s.Tree(ctx, int32(os.Getpid())); slices.ContainsFunc(l, func(p Proc) bool { return p.Name == "sleep" }) {
		t.Fatal("its child lives on")
	}
}
