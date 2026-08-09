package quality

import "time"

type Gate struct {
	policy        Policy
	state         GateState
	quarantined   bool
	lastWindowEnd time.Time
	seenRefs      map[EvidenceRef]struct{}
}

func NewGate(policy Policy) (*Gate, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &Gate{policy: policy, state: GateState{Source: policy.Source, Status: StatusInsufficient, RecoveryRequired: policy.RecoveryHealthyWindows}, seenRefs: make(map[EvidenceRef]struct{})}, nil
}

func (g *Gate) Advance(report Report) GateState {
	if report.Source != g.policy.Source {
		g.quarantined = true
		g.state.Status, g.state.HealthyWindowStreak, g.state.Reasons = StatusQuarantined, 0, []string{"invalid_report_source"}
		return cloneGateState(g.state)
	}
	if !report.Window.End.IsZero() {
		if !g.lastWindowEnd.IsZero() && report.Window.End.Equal(g.lastWindowEnd) {
			return cloneGateState(g.state)
		}
		if !g.lastWindowEnd.IsZero() && report.Window.End.Before(g.lastWindowEnd) {
			g.quarantined = true
			g.state.Status, g.state.HealthyWindowStreak, g.state.Reasons = StatusQuarantined, 0, []string{"non_monotonic_window"}
			return cloneGateState(g.state)
		}
		hasNew := false
		currentRefs := make(map[EvidenceRef]struct{}, len(report.EvidenceRefs))
		for _, ref := range report.EvidenceRefs {
			if _, ok := g.seenRefs[ref]; !ok {
				hasNew = true
			}
			currentRefs[ref] = struct{}{}
		}
		g.lastWindowEnd = report.Window.End
		g.seenRefs = currentRefs
		if report.Status == StatusHealthy && !hasNew {
			g.state.Reasons = []string{"no_new_evidence"}
			return cloneGateState(g.state)
		}
	}
	g.state.Reasons = append([]string(nil), report.Reasons...)
	switch report.Status {
	case StatusQuarantined:
		g.quarantined = true
		g.state.Status, g.state.HealthyWindowStreak = StatusQuarantined, 0
	case StatusHealthy:
		if g.quarantined {
			g.state.HealthyWindowStreak++
			if g.state.HealthyWindowStreak >= g.policy.RecoveryHealthyWindows {
				g.state.Status, g.state.HealthyWindowStreak, g.quarantined = StatusHealthy, g.policy.RecoveryHealthyWindows, false
			} else {
				g.state.Status = StatusRecovering
			}
		} else {
			g.state.Status, g.state.HealthyWindowStreak = StatusHealthy, 0
		}
	case StatusDegraded:
		g.state.HealthyWindowStreak = 0
		if g.quarantined {
			g.state.Status = StatusQuarantined
		} else {
			g.state.Status = StatusDegraded
		}
	case StatusInsufficient:
		if g.state.Status == StatusRecovering {
			g.state.Status, g.state.HealthyWindowStreak, g.quarantined = StatusQuarantined, 0, true
		} else if !g.quarantined {
			g.state.Status, g.state.HealthyWindowStreak = StatusInsufficient, 0
		}
	default:
		g.state.Status, g.state.HealthyWindowStreak, g.quarantined = StatusQuarantined, 0, true
		g.state.Reasons = []string{"invalid_report_status"}
	}
	return cloneGateState(g.state)
}

func (g *Gate) State() GateState { return cloneGateState(g.state) }
func cloneGateState(state GateState) GateState {
	state.Reasons = append([]string(nil), state.Reasons...)
	return state
}
