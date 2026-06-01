package dockerhealth

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(_ context.Context) error { return f.err }

func TestSocketDiagnosticsWritableAndReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix sockets are not supported on windows")
	}
	if os.Getuid() == 0 {
		// Root bypasses file permission checks; the writable/read-only distinction
		// is meaningless when running as root (e.g. GitHub Actions runners).
		t.Skip("running as root — file permission enforcement is not meaningful")
	}

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("chowkidar-%d.sock", time.Now().UnixNano()))
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Listen unix socket error = %v", err)
	}
	defer func() { _ = listener.Close() }()

	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatalf("chmod writable error = %v", err)
	}
	writable := SocketDiagnostics(context.Background(), fakePinger{}, socketPath, 1)
	if !writable.SocketPresent || !writable.SocketWritable || !writable.DockerReachable {
		t.Fatalf("expected writable reachable socket, got %#v", writable)
	}

	if err := os.Chmod(socketPath, 0o400); err != nil {
		t.Fatalf("chmod read-only error = %v", err)
	}
	readonly := SocketDiagnostics(context.Background(), fakePinger{}, socketPath, 1)
	if !readonly.SocketPresent || readonly.SocketWritable {
		t.Fatalf("expected read-only socket, got %#v", readonly)
	}
	_ = os.Remove(socketPath)
}

func TestSocketDiagnosticsDockerHostModes(t *testing.T) {
	orig := os.Getenv("DOCKER_HOST")
	t.Cleanup(func() {
		t.Setenv("DOCKER_HOST", orig)
	})

	t.Setenv("DOCKER_HOST", "tcp://docker-socket-proxy:2375")
	d := SocketDiagnostics(context.Background(), fakePinger{}, "/var/run/docker.sock", 1)
	if d.Mode != "socket-proxy" || !d.DockerReachable {
		t.Fatalf("expected socket-proxy reachable diagnostics, got %#v", d)
	}

	t.Setenv("DOCKER_HOST", "tcp://docker:2375")
	d = SocketDiagnostics(context.Background(), fakePinger{}, "/var/run/docker.sock", 1)
	if d.Mode != "docker_host" || !d.DockerReachable {
		t.Fatalf("expected docker_host reachable diagnostics, got %#v", d)
	}
}
