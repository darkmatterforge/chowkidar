package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

const defaultAppIconURL = "https://raw.githubusercontent.com/darkmatterforge/chowkidar/main/unraid/icon.svg"

// Notifier sends notifications through the Apprise CLI tool.
// When Enabled is false all Send calls are silent no-ops.
type Notifier struct {
	Services []string
	Enabled  bool
	IconURL  string
}

// New builds a Notifier from a comma-separated list of Apprise service URLs.
// If servicesCSV is empty or whitespace-only, Enabled is false.
// New builds a Notifier using the default icon (env or built-in).
func New(servicesCSV string) *Notifier {
	return NewWithIcon(servicesCSV, "")
}

// NewWithIcon builds a Notifier and sets the icon URL to use for provider
// payloads that support custom avatars/icons. If iconURL is empty, the
// built-in GitHub raw URL is used as a sensible default.
func NewWithIcon(servicesCSV, iconURL string) *Notifier {
	parts := strings.Split(servicesCSV, ",")
	services := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			services = append(services, trimmed)
		}
	}
	if strings.TrimSpace(iconURL) == "" {
		iconURL = defaultAppIconURL
	}
	return &Notifier{Services: services, Enabled: len(services) > 0, IconURL: iconURL}
}

// Send dispatches a notification with the given title and body to all configured
// Apprise services. Returns an error if apprise exits non-zero.
func (n *Notifier) Send(title, body string) error {
	if !n.Enabled {
		return nil
	}

	log.Printf("[notify] send title=%q services=%v", title, n.Services)

	// Handle Discord webhooks directly so we can set the avatar (app icon).
	remaining := make([]string, 0, len(n.Services))
	var discordErrs []string
	for _, svc := range n.Services {
		s := strings.TrimSpace(svc)
		if s == "" {
			continue
		}
		// Slack incoming webhook (https://hooks.slack.com/services/...) — allow icon_url query param or default to app icon
		if strings.Contains(s, "hooks.slack.com") {
			wh := s
			var iconURL string
			if u, err := url.Parse(s); err == nil {
				q := u.Query()
				iconURL = q.Get("icon_url")
				wh = u.String()
			}
			if iconURL == "" {
				iconURL = n.IconURL
				if iconURL == "" {
					iconURL = defaultAppIconURL
				}
			}
			payload := map[string]any{
				"text":     body,
				"username": title,
				"icon_url": iconURL,
			}
			b, _ := json.Marshal(payload)
			req, _ := http.NewRequest("POST", wh, bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			cli := &http.Client{Timeout: 10 * time.Second}
			resp, err := cli.Do(req)
			if err != nil {
				discordErrs = append(discordErrs, fmt.Sprintf("slack send failed: %v", err))
				continue
			}
			resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				discordErrs = append(discordErrs, fmt.Sprintf("slack send failed: status=%d", resp.StatusCode))
			}
			continue
		}

		// discord://ID/TOKEN
		if strings.HasPrefix(s, "discord://") || strings.HasPrefix(s, "https://discord.com/api/webhooks/") {
			// Normalize to full webhook URL
			wh := s
			if strings.HasPrefix(s, "discord://") {
				parts := strings.Split(strings.TrimPrefix(s, "discord://"), "/")
				if len(parts) >= 2 {
					wh = fmt.Sprintf("https://discord.com/api/webhooks/%s/%s", parts[0], parts[1])
				} else {
					discordErrs = append(discordErrs, fmt.Sprintf("invalid discord service: %s", s))
					continue
				}
			}

			// Build payload with avatar pointing to project icon on GitHub (public fallback)
			avatar := n.IconURL
			if strings.TrimSpace(avatar) == "" {
				avatar = defaultAppIconURL
			}
			payload := map[string]any{
				"content":    body,
				"username":   title,
				"avatar_url": avatar,
			}
			b, _ := json.Marshal(payload)
			req, _ := http.NewRequest("POST", wh, bytes.NewReader(b))
			req.Header.Set("Content-Type", "application/json")
			cli := &http.Client{Timeout: 10 * time.Second}
			resp, err := cli.Do(req)
			if err != nil {
				discordErrs = append(discordErrs, fmt.Sprintf("discord send failed: %v", err))
				continue
			}
			resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				discordErrs = append(discordErrs, fmt.Sprintf("discord send failed: status=%d", resp.StatusCode))
			}
			continue
		}
		remaining = append(remaining, s)
	}

	// If there are remaining services, call apprise CLI for them
	if len(remaining) > 0 {
		args := []string{"-vv", "-t", title, "-b", body}
		args = append(args, remaining...)

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "apprise", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("[notify] send failed: %v — output: %s", err, strings.TrimSpace(string(out)))
			// include any discord errors too
			allErr := strings.TrimSpace(string(out))
			if len(discordErrs) > 0 {
				allErr = allErr + "; " + strings.Join(discordErrs, "; ")
			}
			return fmt.Errorf("apprise send failed: %w (%s)", err, allErr)
		}
		log.Printf("[notify] send ok output=%s", strings.TrimSpace(string(out)))
	} else if len(discordErrs) > 0 {
		// No apprise services called, but there were discord errors
		return errors.New(strings.Join(discordErrs, "; "))
	}

	return nil
}
