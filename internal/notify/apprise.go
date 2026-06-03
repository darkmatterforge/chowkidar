package notify

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// Notifier sends notifications through the Apprise CLI tool.
// When Enabled is false all Send calls are silent no-ops.
type Notifier struct {
	Services []string
	Enabled  bool
}

// New builds a Notifier from a comma-separated list of Apprise service URLs.
// If servicesCSV is empty or whitespace-only, Enabled is false.
func New(servicesCSV string) *Notifier {
	parts := strings.Split(servicesCSV, ",")
	services := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			services = append(services, trimmed)
		}
	}
	return &Notifier{Services: services, Enabled: len(services) > 0}
}

// Send dispatches a notification with the given title and body to all configured
// Apprise services. Returns an error if apprise exits non-zero.
func (n *Notifier) Send(title, body string) error {
	if !n.Enabled {
		return nil
	}

	log.Printf("[notify] send title=%q services=%v", title, n.Services)

	args := []string{"-vv", "-t", title, "-b", body}
	args = append(args, n.Services...)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "apprise", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[notify] send failed: %v — output: %s", err, strings.TrimSpace(string(out)))
		return fmt.Errorf("apprise send failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	log.Printf("[notify] send ok output=%s", strings.TrimSpace(string(out)))
	return nil
}
