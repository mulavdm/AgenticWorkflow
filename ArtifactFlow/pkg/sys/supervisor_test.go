package sys

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	logger "github.com/user/CentralLogger"
)

func TestSupervisorStatusAndStopForStartedSidecar(t *testing.T) {
	if os.Getenv("ARTIFACTFLOW_HELPER_SLEEP") == "1" {
		time.Sleep(30 * time.Second)
		return
	}

	tempDir := t.TempDir()
	logger.SetLogDir(tempDir)
	supervisor := NewSupervisor()
	defer supervisor.Close()

	cmd := helperCommand("ARTIFACTFLOW_HELPER_SLEEP", "1")
	if err := supervisor.StartSidecar(context.Background(), SidecarConfig{
		Name:    "test-sidecar",
		BinPath: cmd.Path,
		Args:    cmd.Args[1:],
	}); err != nil {
		t.Fatalf("StartSidecar failed: %v", err)
	}

	status, ok := supervisor.Status("test-sidecar")
	if !ok {
		t.Fatal("expected sidecar status to exist")
	}
	if !status.Running || status.PID == 0 || status.LogPath == "" {
		t.Fatalf("unexpected sidecar status: %#v", status)
	}

	if err := supervisor.StopSidecar("test-sidecar"); err != nil {
		t.Fatalf("StopSidecar failed: %v", err)
	}
	if _, ok := supervisor.Status("test-sidecar"); ok {
		t.Fatal("expected stopped sidecar status to be removed")
	}
}

func TestSupervisorCleansUpAfterHealthFailure(t *testing.T) {
	if os.Getenv("ARTIFACTFLOW_HELPER_SLEEP") == "1" {
		time.Sleep(30 * time.Second)
		return
	}

	tempDir := t.TempDir()
	logger.SetLogDir(tempDir)
	supervisor := NewSupervisor()
	defer supervisor.Close()

	port := freeTestPort(t)
	cmd := helperCommand("ARTIFACTFLOW_HELPER_SLEEP", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := supervisor.StartSidecar(ctx, SidecarConfig{
		Name:      "test-health-failure",
		BinPath:   cmd.Path,
		Args:      cmd.Args[1:],
		HealthURL: "http://127.0.0.1:" + strconv.Itoa(port) + "/health",
	})
	if err == nil {
		t.Fatal("expected health check failure")
	}
	if _, ok := supervisor.Status("test-health-failure"); ok {
		t.Fatal("expected failed sidecar to be removed from supervisor state")
	}
}

func helperCommand(envName, envValue string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=TestSupervisor")
	cmd.Env = append(os.Environ(), envName+"="+envValue)
	return cmd
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
