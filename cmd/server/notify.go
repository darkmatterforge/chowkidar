package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"text/template"
	"time"

	"chowkidar/internal/config"
	"chowkidar/internal/notify"
)

// notifProfileUsage tracks send counts for rate limiting, keyed by profile ID.
// dayKey/dayCount are mirrored to disk (see persistNotifUsage/loadNotifUsage)
// so the visible "N of cap used today" survives a restart instead of silently
// resetting to zero — which would otherwise let a profile send another full
// day's worth of notifications immediately after every restart, defeating the
// cap. burstKey/burstCount stay in-memory only: their window is minutes, so a
// restart losing them is inconsequential.
type notifProfileUsage struct {
	dayKey     string // "YYYY-MM-DD" in the display timezone — reset when the local day changes
	dayCount   int
	burstKey   string // truncated time bucket
	burstCount int
}

// notifData holds the values available inside per-profile message templates.
type notifData struct {
	ContainerName    string
	Action           string
	Reason           string
	Cycle            int
	MaxRetries       int
	Cooldown         int
	RuleName         string
	BaseURL          string
	ExternalHostname string
}

// renderNotifTemplate executes a Go text/template with d as the data context,
// falling back to the raw template string on any error.
func renderNotifTemplate(tmpl string, d notifData) string {
	t, err := template.New("").Parse(tmpl)
	if err != nil {
		return tmpl
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return tmpl
	}
	return buf.String()
}

func (a *app) sendNotification(event, body string) error {
	cfg := a.getConfig()
	prefix := "[chowkidar]"
	bodyWithHost := body
	if strings.TrimSpace(cfg.ExternalHostname) != "" {
		host := strings.TrimSpace(cfg.ExternalHostname)
		prefix = "[chowkidar@" + host + "]"
		bodyWithHost = "host=" + host + " | " + body
	}
	logInfof("notify: send event=%s", event)
	if err := a.notifier.Send(prefix+" "+event, bodyWithHost); err != nil {
		logWarnf("notify: send failed event=%s err=%v", event, err)
		return err
	}
	return nil
}

// buildNotifierServicesFromProfiles returns the service CSV for the global notifier.
// The default-flagged profile (if any) is used exclusively; otherwise all enabled profiles.
func buildNotifierServicesFromProfiles(profiles []config.NotificationProfile) string {
	for _, p := range profiles {
		if p.IsDefault && strings.TrimSpace(p.Service) != "" {
			return strings.TrimSpace(p.Service)
		}
	}
	services := make([]string, 0, len(profiles))
	for _, p := range profiles {
		if p.Enabled && strings.TrimSpace(p.Service) != "" {
			services = append(services, strings.TrimSpace(p.Service))
		}
	}
	return strings.Join(services, ",")
}

// cleanNotifError strips the verbose Apprise Python log wrapper from an error
// message so what's stored (and shown in the bell) is readable.
//
// Raw Apprise error format:
//
//	apprise send failed: exit status 1 (2026-06-02 12:43:43,738 - WARNING - Failed to send ntfy ...)
//
// We want:                  Failed to send ntfy ...
func cleanNotifError(raw string) string {
	s := raw
	// Strip leading "apprise send failed: exit status N (" prefix
	if idx := strings.Index(s, " ("); idx >= 0 {
		s = s[idx+2:]
	}
	// Remove trailing ")" from the combined-output wrapper
	s = strings.TrimSuffix(strings.TrimSpace(s), ")")

	// The Apprise output is a Python log: "YYYY-MM-DD HH:MM:SS,mmm - LEVEL - Message"
	// Strip the "YYYY-MM-DD HH:MM:SS,mmm - LEVEL - " prefix.
	// We keep everything after the third " - ".
	parts := strings.SplitN(s, " - ", 3)
	if len(parts) == 3 {
		s = strings.TrimSpace(parts[2])
	}

	// Strip verbose help/upgrade phrases and URLs that appear after the meaningful error.
	// e.g. ntfy: "quota reached; increase your limits with a paid plan, see https://ntfy.sh/app"
	verbosePhrases := []string{
		"; increase your", "; upgrade your", "; consider upgrading",
		"; see https", "; visit https", " see https", ", see https",
		" https://", " http://",
	}
	lower := strings.ToLower(s)
	for _, sep := range verbosePhrases {
		if idx := strings.Index(lower, sep); idx >= 0 {
			s = strings.TrimRight(s[:idx], " ;,")
			lower = strings.ToLower(s)
		}
	}

	s = strings.TrimSpace(s)
	if s == "" {
		return strings.TrimSpace(raw)
	}
	return s
}

func isRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, kw := range []string{"quota", "rate limit", "too many", "429", "limit reached", "throttl", "42908", "daily message", "exceeded"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}

// parseSuspendDuration parses strings like "6h", "30m", "3d", "2w" into a time.Duration.
func parseSuspendDuration(s string) (time.Duration, error) {
	if len(s) < 2 {
		return 0, fmt.Errorf("too short")
	}
	unit := s[len(s)-1]
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n < 1 {
		return 0, fmt.Errorf("invalid number")
	}
	switch unit {
	case 'm':
		return time.Duration(n) * time.Minute, nil
	case 'h':
		return time.Duration(n) * time.Hour, nil
	case 'd':
		return time.Duration(n) * 24 * time.Hour, nil
	case 'w':
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown unit %q — use m/h/d/w", unit)
	}
}

func nextMidnight(ianaZone string) time.Time {
	loc, err := time.LoadLocation(ianaZone)
	if err != nil || ianaZone == "" {
		loc = time.UTC
	}
	n := time.Now().In(loc)
	return time.Date(n.Year(), n.Month(), n.Day()+1, 0, 0, 0, 0, loc)
}

func endOfMonth(ianaZone string) time.Time {
	loc, err := time.LoadLocation(ianaZone)
	if err != nil || ianaZone == "" {
		loc = time.UTC
	}
	n := time.Now().In(loc)
	first := time.Date(n.Year(), n.Month()+1, 1, 0, 0, 0, 0, loc)
	return first
}

func (a *app) isProfileSuspended(p config.NotificationProfile) bool {
	return p.SuspendedUntil != nil && p.SuspendedUntil.After(time.Now().UTC())
}

// notifDayKey returns "YYYY-MM-DD" for the current moment in the resolved
// display timezone (falling back to UTC) — the same zone nextMidnight uses for
// auto-suspend, so the daily cap counter and "suspend until midnight" agree on
// when "today" rolls over, and the counter resets at the user's local midnight
// rather than UTC midnight.
func (a *app) notifDayKey() string {
	loc, err := time.LoadLocation(a.resolveTimezone())
	if err != nil {
		loc = time.UTC
	}
	return time.Now().In(loc).Format("2006-01-02")
}

// getOrCreateUsage returns the usage bucket for a profile, resetting stale counters.
func (a *app) getOrCreateUsage(profileID string, p config.NotificationProfile) *notifProfileUsage {
	a.notifUsageMu.Lock()
	defer a.notifUsageMu.Unlock()
	u, ok := a.notifUsage[profileID]
	if !ok {
		u = &notifProfileUsage{}
		a.notifUsage[profileID] = u
	}
	today := a.notifDayKey()
	if u.dayKey != today {
		u.dayKey = today
		u.dayCount = 0
	}
	if p.BurstWindowMinutes > 0 {
		bw := time.Duration(p.BurstWindowMinutes) * time.Minute
		bucketKey := time.Now().UTC().Truncate(bw).Format(time.RFC3339)
		if u.burstKey != bucketKey {
			u.burstKey = bucketKey
			u.burstCount = 0
		}
	}
	return u
}

// persistNotifUsage snapshots the in-memory daily-usage counters and writes
// them to disk so a restart doesn't silently zero out "N of cap used today" —
// the hard enforcement (SuspendedUntil) was already persisted, but the visible
// count drifting back to 0 on every restart was confusing and effectively let
// a profile send another full day's worth of notifications right after a
// restart, defeating the cap. Burst counters are intentionally left in-memory
// only (see the call site).
func (a *app) persistNotifUsage() {
	a.notifUsageMu.Lock()
	snapshot := make(map[string]notifUsageRecord, len(a.notifUsage))
	for id, u := range a.notifUsage {
		if u.dayCount > 0 {
			snapshot[id] = notifUsageRecord{DayKey: u.dayKey, DayCount: u.dayCount}
		}
	}
	a.notifUsageMu.Unlock()
	if err := saveNotifUsage(a.cfg.ConfigDir, snapshot); err != nil {
		logWarnf("notif: failed to persist usage counters: %v", err)
	}
}

// suspendProfile writes SuspendedUntil + optional error to the profile and persists.
func (a *app) suspendProfile(profileID string, until time.Time, reason string) {
	a.mu.Lock()
	profiles := make([]config.NotificationProfile, len(a.notifications))
	copy(profiles, a.notifications)
	for i, p := range profiles {
		if p.ID != profileID {
			continue
		}
		t := until
		profiles[i].SuspendedUntil = &t
		if reason != "" {
			profiles[i].LastRateLimitError = reason
			now := time.Now().UTC()
			profiles[i].LastRateLimitAt = &now
		}
		break
	}
	a.notifications = profiles
	configDir := a.cfg.ConfigDir
	a.mu.Unlock()
	if err := config.SaveNotificationProfiles(configDir, profiles); err != nil {
		logWarnf("notif: failed to persist suspension for profile=%s: %v", profileID, err)
	}
}

// resumeProfile clears the suspension and persists.
func (a *app) resumeProfile(profileID string) {
	a.mu.Lock()
	profiles := make([]config.NotificationProfile, len(a.notifications))
	copy(profiles, a.notifications)
	for i, p := range profiles {
		if p.ID == profileID {
			profiles[i].SuspendedUntil = nil
			break
		}
	}
	a.notifications = profiles
	configDir := a.cfg.ConfigDir
	a.mu.Unlock()
	if err := config.SaveNotificationProfiles(configDir, profiles); err != nil {
		logWarnf("notif: failed to persist resume for profile=%s: %v", profileID, err)
	}
}

func (a *app) sendJobNotifications(event string, data notifData, profileIDs []string) error {
	if len(profileIDs) == 0 {
		return nil
	}
	profiles := a.getNotificationProfiles()
	byID := make(map[string]config.NotificationProfile, len(profiles))
	for _, p := range profiles {
		byID[p.ID] = p
	}
	cfg := a.getConfig()
	host := strings.TrimSpace(cfg.ExternalHostname)
	baseURL := strings.TrimSpace(cfg.PrimaryBaseURL)
	// Inject cfg-level values so templates can reference them even when
	// the call-site didn't populate them.
	if data.ExternalHostname == "" {
		data.ExternalHostname = host
	}
	if data.BaseURL == "" {
		data.BaseURL = baseURL
	}
	prefix := "[chowkidar]"
	if host != "" {
		prefix = "[chowkidar@" + host + "]"
	}
	var lastErr error
	for _, id := range profileIDs {
		p, ok := byID[id]
		if !ok || !p.Enabled || strings.TrimSpace(p.Service) == "" {
			continue
		}

		// ── Suspension check ──────────────────────────────────────────────
		if a.isProfileSuspended(p) {
			logInfof("notif: skip suspended profile=%s until=%s", p.Name, p.SuspendedUntil.Format(time.RFC3339))
			continue
		}

		// ── Rate limit checks (in-memory counters) ────────────────────────
		if p.DailyLimit > 0 || p.BurstLimit > 0 {
			u := a.getOrCreateUsage(id, p)
			a.notifUsageMu.Lock()
			hitBurst := p.BurstLimit > 0 && u.burstCount >= p.BurstLimit
			hitDaily := p.DailyLimit > 0 && u.dayCount >= p.DailyLimit
			a.notifUsageMu.Unlock()
			if hitBurst || hitDaily {
				reason := "daily limit reached"
				if hitBurst {
					reason = "burst limit reached"
				}
				logWarnf("notif: limit hit profile=%s reason=%s", p.Name, reason)
				action := p.OnLimitAction
				if action == "" {
					action = "auto-suspend"
				}
				switch action {
				case "auto-suspend":
					a.suspendProfile(id, nextMidnight(a.resolveTimezone()), reason)
				case "drop":
					// silently drop
				}
				lastErr = fmt.Errorf("notification limit hit: %s", reason)
				continue
			}
		}

		titleTmpl := p.AppliedTitleTemplate(event)
		var title string
		if titleTmpl != "" {
			title = prefix + " " + renderNotifTemplate(titleTmpl, data)
		} else {
			title = prefix + " " + event
		}
		body := renderNotifTemplate(p.AppliedTemplate(event), data)
		if host != "" {
			body = "host=" + host + " | " + body
		}
		n := notify.New(p.Service)
		if err := n.Send(title, body); err != nil {
			logWarnf("job notification failed profile=%s err=%v", p.Name, err)
			lastErr = err

			// ── Auto-suspend on quota / rate-limit error ───────────────
			if isRateLimitError(err) {
				shouldAutoSuspend := p.AutoSuspendOnError || p.OnLimitAction == "auto-suspend" || p.OnLimitAction == ""
				if shouldAutoSuspend && !a.isProfileSuspended(p) {
					tz := a.resolveTimezone()
					until := nextMidnight(tz)
					logInfof("notif: auto-suspending profile=%s until=%s tz=%s reason=rate-limit", p.Name, until.Format(time.RFC3339), tz)
					a.suspendProfile(id, until, cleanNotifError(err.Error()))
				}
			}
		} else {
			// Increment usage counters on successful send
			if p.DailyLimit > 0 || p.BurstLimit > 0 {
				u := a.getOrCreateUsage(id, p)
				a.notifUsageMu.Lock()
				u.dayCount++
				u.burstCount++
				a.notifUsageMu.Unlock()
				// Persist the daily count so it survives a restart — only the
				// burst counter is restart-volatile (its window is minutes, so
				// losing it on a rare restart is inconsequential).
				a.persistNotifUsage()
			}
		}
	}
	return lastErr
}

// sendDockerHostNotif sends a notification when a docker host goes offline or
// recovers. Uses the host's DownTemplate if set, otherwise a default message.
// sendDockerHostNotif sends the per-host offline or recovery notification to
// every configured notification profile. It sends title + body directly via
// Apprise so the user's custom message template is always delivered as-is,
// regardless of which agent (Slack, Discord, Telegram, etc.) is configured.
func (a *app) sendDockerHostNotif(host config.DockerHostProfile, down bool) {
	if len(host.Notifications) == 0 {
		return
	}
	var tmpl string
	if down {
		tmpl = strings.TrimSpace(host.DownTemplate)
		if tmpl == "" {
			tmpl = fmt.Sprintf("Docker host %q is offline", host.Name)
		}
	} else {
		tmpl = strings.TrimSpace(host.RecoveryTemplate)
		if tmpl == "" {
			tmpl = fmt.Sprintf("Docker host %q is back online", host.Name)
		}
	}
	body := strings.ReplaceAll(tmpl, "{{.HostName}}", host.Name)

	cfg := a.getConfig()
	prefix := "[chowkidar]"
	if h := strings.TrimSpace(cfg.ExternalHostname); h != "" {
		prefix = "[chowkidar@" + h + "]"
	}
	title := prefix + " Docker host offline"
	if !down {
		title = prefix + " Docker host recovered"
	}

	profiles := a.getNotificationProfiles()
	byID := make(map[string]config.NotificationProfile, len(profiles))
	for _, p := range profiles {
		byID[p.ID] = p
	}
	for _, id := range host.Notifications {
		p, ok := byID[id]
		if !ok || !p.Enabled || strings.TrimSpace(p.Service) == "" {
			continue
		}
		if err := notify.New(p.Service).Send(title, body); err != nil {
			logWarnf("notif: docker host notification failed host=%s profile=%s err=%v", host.Name, p.Name, err)
		}
	}
}
