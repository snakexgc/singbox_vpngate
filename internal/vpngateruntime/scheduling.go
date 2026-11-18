package vpngateruntime

import (
	"time"

	"guiforcores/internal/vpngate"
)

// Attempts are durable before a worker starts, so a failed request or an app
// restart cannot bypass the configured interval. Success starts a fresh interval
// after completion; manual operations use the same clock as automatic updates.
type updateSchedule struct {
	RefreshAttemptAt time.Time `json:"refreshAttemptAt,omitempty"`
	RefreshSuccessAt time.Time `json:"refreshSuccessAt,omitempty"`
	RulesAttemptAt   time.Time `json:"rulesAttemptAt,omitempty"`
	RulesSuccessAt   time.Time `json:"rulesSuccessAt,omitempty"`
}

func (s *updateSchedule) recordAttempt(kind string, at time.Time) bool {
	switch kind {
	case "refresh":
		s.RefreshAttemptAt = at.UTC()
	case "rules":
		s.RulesAttemptAt = at.UTC()
	default:
		return false
	}
	return true
}

func (s *updateSchedule) recordSuccess(kind string, at time.Time) {
	switch kind {
	case "refresh":
		s.RefreshSuccessAt = at.UTC()
	case "rules":
		s.RulesSuccessAt = at.UTC()
	}
}

// Older state files have no scheduling clocks. Reuse their actual feed/rule
// update times, including freshly seeded local rules, instead of treating every
// launch as overdue. These fallbacks stay stable across ordinary state reads.
func (m *Manager) initializeUpdateSchedule() {
	if m.scheduleState.RefreshSuccessAt.IsZero() {
		for _, n := range m.pool.Snapshot() {
			seen, err := time.Parse(time.RFC3339, n.LastSeen)
			if err == nil && seen.After(m.scheduleState.RefreshSuccessAt) {
				m.scheduleState.RefreshSuccessAt = seen
			}
		}
	}
	if m.scheduleState.RulesSuccessAt.IsZero() && m.rules.CN.Ready && m.rules.GFW.Ready {
		cn, cnErr := time.Parse(time.RFC3339, m.rules.CN.UpdatedAt)
		gfw, gfwErr := time.Parse(time.RFC3339, m.rules.GFW.UpdatedAt)
		if cnErr == nil && gfwErr == nil {
			m.scheduleState.RulesSuccessAt = cn
			if gfw.Before(cn) {
				m.scheduleState.RulesSuccessAt = gfw
			}
		}
	}
}

func updateIsDue(at, attempt, success time.Time, interval time.Duration) bool {
	if attempt.After(success) {
		success = attempt
	}
	return interval > 0 && (success.IsZero() || !at.Before(success.Add(interval)))
}

// The scheduler and deterministic restart tests use the same decision boundary.
// Pool capacity limits merging; it does not suppress periodic metadata refresh.
func (m *Manager) scheduledUpdatesDue(at time.Time) (vpngate.Settings, bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.settings
	refresh := updateIsDue(at, m.scheduleState.RefreshAttemptAt, m.scheduleState.RefreshSuccessAt, time.Duration(s.RefreshMinutes)*time.Minute)
	rules := s.RulesAutoUpdate && updateIsDue(at, m.scheduleState.RulesAttemptAt, m.scheduleState.RulesSuccessAt, time.Duration(s.RulesUpdateHours)*time.Hour)
	return s, refresh, rules
}
