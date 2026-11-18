package vpngateruntime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"guiforcores/internal/vpngate"
)

func TestScheduledAttemptIsDurableBeforeWorkerStarts(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	_, j, err := m.beginJob("refresh")
	if err != nil {
		t.Fatal(err)
	}
	defer m.finishJob(j, errors.New("network unavailable"))
	raw, err := os.ReadFile(filepath.Join(m.dir, "state.json"))
	if err != nil {
		t.Fatalf("accepted scheduled work has no durable attempt: %v", err)
	}
	var saved struct {
		Schedule struct {
			RefreshAttemptAt time.Time `json:"refreshAttemptAt"`
		} `json:"schedule"`
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Schedule.RefreshAttemptAt.IsZero() {
		t.Fatal("accepted refresh did not save its attempt before starting network work")
	}
}

func TestFailedScheduledAttemptsKeepTheirIntervalAfterRestart(t *testing.T) {
	for _, kind := range []string{"refresh", "rules"} {
		t.Run(kind, func(t *testing.T) {
			m, err := New(t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, job, err := m.beginJob(kind)
			if err != nil {
				m.Close()
				t.Fatal(err)
			}
			attempt, interval := m.scheduleState.RefreshAttemptAt, 30*time.Minute
			if kind == "rules" {
				attempt, interval = m.scheduleState.RulesAttemptAt, 12*time.Hour
			}
			m.finishJob(job, errors.New("upstream unavailable"))
			m.Close()
			restarted, err := New(m.dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			for _, test := range []struct {
				after time.Duration
				want  bool
			}{{interval - time.Nanosecond, false}, {interval, true}} {
				_, refresh, rules := restarted.scheduledUpdatesDue(attempt.Add(test.after))
				got := refresh
				if kind == "rules" {
					got = rules
				}
				if got != test.want {
					t.Fatalf("%s due %s after failed attempt/restart = %v, want %v", kind, test.after, got, test.want)
				}
			}
		})
	}
}

func TestManualUpdateCompletionResetsAutomaticInterval(t *testing.T) {
	for _, kind := range []string{"refresh", "rules"} {
		t.Run(kind, func(t *testing.T) {
			m, err := New(t.TempDir(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			_, job, err := m.beginJob(kind)
			if err != nil {
				t.Fatal(err)
			}
			// Model a long operation without sleeping: the earlier attempt must
			// not make the scheduler fire immediately after a successful finish.
			m.scheduleState.RefreshAttemptAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			m.scheduleState.RulesAttemptAt = m.scheduleState.RefreshAttemptAt
			m.finishJob(job, nil)
			success, interval := m.scheduleState.RefreshSuccessAt, 30*time.Minute
			if kind == "rules" {
				success, interval = m.scheduleState.RulesSuccessAt, 12*time.Hour
			}
			restarted, err := New(m.dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			for _, test := range []struct {
				after time.Duration
				want  bool
			}{{interval - time.Nanosecond, false}, {interval, true}} {
				_, refresh, rules := restarted.scheduledUpdatesDue(success.Add(test.after))
				got := refresh
				if kind == "rules" {
					got = rules
				}
				if got != test.want {
					t.Fatalf("%s due %s after manual completion/restart = %v, want %v", kind, test.after, got, test.want)
				}
			}
		})
	}
}

func TestNewSeededRulesWaitTwelveHoursWithoutResettingOnStateRead(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	baseline := m.scheduleState.RulesSuccessAt
	if baseline.IsZero() {
		t.Fatal("freshly seeded rules have no scheduling baseline")
	}
	for i := 0; i < 3; i++ {
		_ = m.State()
	}
	if !m.scheduleState.RulesSuccessAt.Equal(baseline) {
		t.Fatal("reading application state extended the rules interval")
	}
	_, _, before := m.scheduledUpdatesDue(baseline.Add(12*time.Hour - time.Nanosecond))
	_, _, at := m.scheduledUpdatesDue(baseline.Add(12 * time.Hour))
	if before || !at {
		t.Fatalf("seeded rules due before/at twelve hours = %v/%v", before, at)
	}
}

func TestLegacyUpdateTimesSurviveRestartWithFullNodePool(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Close()
	baseline := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	s := vpngate.DefaultSettings()
	s.PoolCapacity = 1
	legacy := struct {
		Settings vpngate.Settings `json:"settings"`
		Nodes    []vpngate.Node   `json:"nodes"`
	}{s, []vpngate.Node{{ID: "existing", IP: "8.8.8.8", Port: 443, Status: "available", LastSeen: baseline.Format(time.RFC3339)}}}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "state.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cn", "gfw"} {
		if err = os.Chtimes(filepath.Join(dir, "rules", name+".srs"), baseline, baseline); err != nil {
			t.Fatal(err)
		}
	}
	restarted, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	_, before, _ := restarted.scheduledUpdatesDue(baseline.Add(30*time.Minute - time.Nanosecond))
	_, at, _ := restarted.scheduledUpdatesDue(baseline.Add(30 * time.Minute))
	_, _, rulesBefore := restarted.scheduledUpdatesDue(baseline.Add(12*time.Hour - time.Nanosecond))
	_, _, rulesAt := restarted.scheduledUpdatesDue(baseline.Add(12 * time.Hour))
	if before || !at || rulesBefore || !rulesAt {
		t.Fatalf("legacy clocks lost or full pool suppressed refresh: feed=%v/%v rules=%v/%v", before, at, rulesBefore, rulesAt)
	}
}

func TestPersistedCustomUpdateIntervalsRemainEffective(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	baseline := time.Date(2026, 11, 18, 1, 0, 0, 0, time.UTC)
	m.settings.RefreshMinutes, m.settings.RulesUpdateHours = 75, 24
	m.scheduleState = updateSchedule{RefreshSuccessAt: baseline, RulesSuccessAt: baseline}
	if err = m.persistLocked(); err != nil {
		m.Close()
		t.Fatal(err)
	}
	m.Close()
	restarted, err := New(m.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	_, feedBefore, _ := restarted.scheduledUpdatesDue(baseline.Add(75*time.Minute - time.Nanosecond))
	_, feedAt, _ := restarted.scheduledUpdatesDue(baseline.Add(75 * time.Minute))
	_, _, rulesBefore := restarted.scheduledUpdatesDue(baseline.Add(24*time.Hour - time.Nanosecond))
	_, _, rulesAt := restarted.scheduledUpdatesDue(baseline.Add(24 * time.Hour))
	if feedBefore || !feedAt || rulesBefore || !rulesAt {
		t.Fatalf("custom intervals lost on reload: feed=%v/%v rules=%v/%v", feedBefore, feedAt, rulesBefore, rulesAt)
	}
	restarted.settings.RulesAutoUpdate = false
	_, _, disabled := restarted.scheduledUpdatesDue(baseline.Add(48 * time.Hour))
	if disabled {
		t.Fatal("disabled automatic rule updates were scheduled")
	}
}

func TestRejectedScheduleReservationDoesNotLeakOrAdvanceClock(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	before := m.scheduleState
	// A directory at the atomic-write destination rejects persistence on all
	// supported systems, without relying on platform-specific permission flags.
	if err = os.Mkdir(filepath.Join(m.dir, "state.json"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, job, err := m.beginJob("rules")
	if err == nil || ctx != nil || job != nil {
		t.Fatal("unpersisted scheduled reservation was accepted")
	}
	if m.scheduleState != before || len(m.jobs) != 0 || len(m.cancels) != 0 || len(m.jobDone) != 0 {
		t.Fatalf("failed reservation leaked bookkeeping or advanced interval: schedule=%+v jobs=%d cancels=%d done=%d", m.scheduleState, len(m.jobs), len(m.cancels), len(m.jobDone))
	}
}
