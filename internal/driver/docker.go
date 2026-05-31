package driver

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// dockerSpec describes a container to start for a database engine.
type dockerSpec struct {
	Image         string            // e.g. "postgres:16"
	Env           map[string]string // container env vars (passwords, etc.)
	ContainerPort int               // the port the DB listens on inside the container
	HostPort      int               // the host port to publish it on
	Name          string            // container name
}

// dockerAvailable reports whether the docker CLI is installed and the daemon is
// reachable.
func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "info").Run() == nil
}

// runDockerContainer starts a detached container per spec and returns its name.
// It does not wait for the database to become ready — callers should poll with
// waitForServer.
func runDockerContainer(ctx context.Context, spec dockerSpec) (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("docker is not installed or not on PATH")
	}

	args := []string{"run", "-d", "--name", spec.Name}
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, "-p", fmt.Sprintf("%d:%d", spec.HostPort, spec.ContainerPort))
	args = append(args, spec.Image)

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("docker run failed: %s", msg)
	}
	return spec.Name, nil
}

// removeDockerContainer force-removes a container (best-effort cleanup on
// failure).
func removeDockerContainer(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
}

// waitForServer polls Connect+Ping until the server accepts connections or the
// context is cancelled. Used after starting a Docker container, since the DB
// process takes a few seconds to come up.
func waitForServer(ctx context.Context, d Driver, info ConnInfo) error {
	ticker := time.NewTicker(1500 * time.Millisecond)
	defer ticker.Stop()

	var lastErr error
	for {
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return fmt.Errorf("server did not become ready: %w", lastErr)
			}
			return ctx.Err()
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			conn, err := d.Connect(pingCtx, info)
			if err == nil {
				err = conn.Ping(pingCtx)
				conn.Close()
			}
			cancel()
			if err == nil {
				return nil
			}
			lastErr = err
		}
	}
}

// freeHostPort returns a sensible host port: the requested one if provided,
// otherwise the engine default.
func freeHostPort(values map[string]string, def int) int {
	if p := strings.TrimSpace(values["port"]); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			return n
		}
	}
	return def
}
