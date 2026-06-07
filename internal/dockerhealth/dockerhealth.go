package dockerhealth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	nethttp "net/http"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

type Client struct {
	cli *client.Client
}

type Diagnostics struct {
	DockerReachable bool   `json:"dockerReachable"`
	SocketPresent   bool   `json:"socketPresent"`
	SocketWritable  bool   `json:"socketWritable"`
	Mode            string `json:"mode"`
	Details         string `json:"details"`
}

type Pinger interface {
	Ping(ctx context.Context) error
}

func NewClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

// NewClientForHost creates a Docker client for a specific host profile.
// hostType is "socket" or "http"/"https", endpoint is the path or URL.
func NewClientForHost(hostType, endpoint string, tlsCfg *TLSConfig) (*Client, error) {
	var dockerHost string
	if hostType == "socket" {
		dockerHost = "unix://" + endpoint
	} else {
		dockerHost = endpoint
	}
	opts := []client.Opt{
		client.WithHost(dockerHost),
		client.WithAPIVersionNegotiation(),
	}
	if tlsCfg != nil && (tlsCfg.CACert != "" || tlsCfg.Cert != "" || tlsCfg.Key != "" || tlsCfg.SkipVerify) {
		tc, err := buildTLSClientConfig(tlsCfg)
		if err != nil {
			return nil, fmt.Errorf("TLS config: %w", err)
		}
		transport := &nethttp.Transport{TLSClientConfig: tc}
		opts = append(opts, client.WithHTTPClient(&nethttp.Client{Transport: transport}))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

func (c *Client) Close() error {
	return c.cli.Close()
}

func (c *Client) UnhealthyContainers(ctx context.Context) ([]containertypes.Summary, error) {
	args := filters.NewArgs()
	args.Add("health", "unhealthy")
	args.Add("status", "running")

	return c.cli.ContainerList(ctx, containertypes.ListOptions{Filters: args})
}

func (c *Client) ExitedContainers(ctx context.Context) ([]containertypes.Summary, error) {
	args := filters.NewArgs()
	args.Add("status", "exited")

	return c.cli.ContainerList(ctx, containertypes.ListOptions{All: true, Filters: args})
}

func (c *Client) RestartingContainers(ctx context.Context) ([]containertypes.Summary, error) {
	args := filters.NewArgs()
	args.Add("status", "restarting")

	return c.cli.ContainerList(ctx, containertypes.ListOptions{Filters: args})
}

func (c *Client) StartingContainers(ctx context.Context) ([]containertypes.Summary, error) {
	args := filters.NewArgs()
	args.Add("health", "starting")
	args.Add("status", "running")
	return c.cli.ContainerList(ctx, containertypes.ListOptions{Filters: args})
}

func (c *Client) RunningContainers(ctx context.Context) ([]containertypes.Summary, error) {
	args := filters.NewArgs()
	args.Add("status", "running")
	return c.cli.ContainerList(ctx, containertypes.ListOptions{Filters: args})
}

func (c *Client) AllContainers(ctx context.Context) ([]containertypes.Summary, error) {
	return c.cli.ContainerList(ctx, containertypes.ListOptions{All: true})
}

func (c *Client) RestartContainer(ctx context.Context, id string, timeoutSeconds int) error {
	t := timeoutSeconds
	return c.cli.ContainerRestart(ctx, id, containertypes.StopOptions{Timeout: &t})
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	return c.cli.ContainerStart(ctx, id, containertypes.StartOptions{})
}

func (c *Client) StopContainer(ctx context.Context, id string, timeoutSeconds int) error {
	t := timeoutSeconds
	return c.cli.ContainerStop(ctx, id, containertypes.StopOptions{Timeout: &t})
}

func (c *Client) InspectContainer(ctx context.Context, id string) (containertypes.InspectResponse, error) {
	return c.cli.ContainerInspect(ctx, id)
}

func ContainerHasEnvVar(inspected containertypes.InspectResponse, needle string) bool {
	if needle == "" {
		return true
	}
	for _, env := range inspected.Config.Env {
		if strings.Contains(env, needle) {
			return true
		}
	}
	return false
}

func ContainerName(inspected containertypes.InspectResponse) string {
	return strings.TrimPrefix(inspected.Name, "/")
}

func ContainerIsExited(inspected containertypes.InspectResponse) bool {
	return strings.EqualFold(inspected.State.Status, "exited")
}

func MatchContainerName(summary containertypes.Summary, names []string) bool {
	if len(names) == 0 {
		return true
	}
	for _, n := range summary.Names {
		trimmed := strings.TrimPrefix(n, "/")
		if slices.Contains(names, trimmed) {
			return true
		}
	}
	return false
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

func SocketDiagnostics(ctx context.Context, c Pinger, socketPath string, pingTimeoutSeconds int) Diagnostics {
	d := Diagnostics{
		DockerReachable: false,
		SocketPresent:   false,
		SocketWritable:  false,
		Mode:            "socket",
		Details:         "",
	}

	dockerHost := os.Getenv("DOCKER_HOST")
	if dockerHost != "" {
		d.Mode = "docker_host"
		if strings.Contains(strings.ToLower(dockerHost), "docker-socket-proxy") {
			d.Mode = "socket-proxy"
		}
		d.SocketPresent = true
		d.SocketWritable = true
		d.Details = fmt.Sprintf("Using DOCKER_HOST=%s", dockerHost)
		if err := c.Ping(ctx); err != nil {
			d.Details = fmt.Sprintf("Cannot reach Docker daemon via DOCKER_HOST: %v", err)
			return d
		}
		d.DockerReachable = true
		return d
	}

	st, err := os.Stat(socketPath)
	if err != nil {
		d.Details = fmt.Sprintf("Docker socket not found at %s: %v", socketPath, err)
		return d
	}
	d.SocketPresent = st.Mode()&os.ModeSocket != 0
	if !d.SocketPresent {
		d.Details = fmt.Sprintf("Path exists but is not a socket: %s", socketPath)
		return d
	}

	if err := syscall.Access(socketPath, 2); err != nil {
		d.Details = fmt.Sprintf("Docker socket is not writable at %s: %v", socketPath, err)
	} else {
		d.SocketWritable = true
	}

	if pingTimeoutSeconds < 1 {
		pingTimeoutSeconds = 5
	}
	pingCtx, cancel := context.WithTimeout(ctx, time.Duration(pingTimeoutSeconds)*time.Second)
	defer cancel()
	if err := c.Ping(pingCtx); err != nil {
		d.Details = strings.TrimSpace(d.Details + "; cannot reach Docker daemon: " + err.Error())
		return d
	}

	d.DockerReachable = true
	if d.Details == "" {
		d.Details = "Docker socket is reachable"
	}
	return d
}

func IsNotFound(err error) bool {
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "not found") || errors.Is(err, syscall.ENOENT))
}

// TLSConfig holds optional TLS parameters for connecting to a remote Docker daemon.
type TLSConfig struct {
	CACert     string
	Cert       string
	Key        string
	SkipVerify bool
}

func buildTLSClientConfig(cfg *TLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.SkipVerify {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // user-opted-in via TLS config
	}
	if cfg.CACert != "" {
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(cfg.CACert)) {
			return nil, fmt.Errorf("parse CA certificate PEM: no valid certificates found")
		}
		tlsCfg.RootCAs = pool
	}
	if cfg.Cert != "" && cfg.Key != "" {
		pair, err := tls.X509KeyPair([]byte(cfg.Cert), []byte(cfg.Key))
		if err != nil {
			return nil, fmt.Errorf("parse client certificate/key PEM: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{pair}
	}
	return tlsCfg, nil
}

// PingHost creates a temporary Docker client for the given host and pings it.
// hostType is "socket", "http", or "https". endpoint is the path or URL.
// tlsCfg is optional; when non-nil it configures TLS for HTTPS connections.
// Returns (reachable, infoString, error).
func PingHost(ctx context.Context, hostType, endpoint string, timeoutSeconds int, tlsCfg *TLSConfig) (bool, string, error) {
	var dockerHost string
	if hostType == "socket" {
		dockerHost = "unix://" + endpoint
	} else {
		dockerHost = endpoint
	}

	opts := []client.Opt{
		client.WithHost(dockerHost),
		client.WithAPIVersionNegotiation(),
	}

	if tlsCfg != nil && (tlsCfg.CACert != "" || tlsCfg.Cert != "" || tlsCfg.Key != "" || tlsCfg.SkipVerify) {
		tc, err := buildTLSClientConfig(tlsCfg)
		if err != nil {
			return false, "", fmt.Errorf("TLS config: %w", err)
		}
		transport := &nethttp.Transport{TLSClientConfig: tc}
		opts = append(opts, client.WithHTTPClient(&nethttp.Client{Transport: transport}))
	}

	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return false, "", err
	}
	defer func() { _ = cli.Close() }()

	timeout := timeoutSeconds
	if timeout < 1 {
		timeout = 5
	}
	pingCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	ping, err := cli.Ping(pingCtx)
	if err != nil {
		return false, "", err
	}
	info := fmt.Sprintf("API v%s · %s", ping.APIVersion, ping.OSType)
	return true, info, nil
}
