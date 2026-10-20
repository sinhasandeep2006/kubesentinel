package incident

import (
	"errors"
	"testing"
)

func TestCheckTransition(t *testing.T) {
	tests := []struct {
		from, to Status
		ok       bool
	}{
		{StatusDetected, StatusInvestigating, true},
		{StatusInvestigating, StatusDiagnosed, true},
		{StatusDiagnosed, StatusVerifying, true},
		{StatusVerifying, StatusResolved, true},
		{StatusVerifying, StatusDiagnosed, true},
		{StatusResolved, StatusReopened, true},
		{StatusReopened, StatusInvestigating, true},
		// Resolution only through verification.
		{StatusDiagnosed, StatusResolved, false},
		{StatusRemediating, StatusResolved, false},
		{StatusDetected, StatusResolved, false},
		{StatusDismissed, StatusInvestigating, false},
		{StatusResolved, StatusInvestigating, false},
	}
	for _, tc := range tests {
		err := CheckTransition(tc.from, tc.to)
		if tc.ok && err != nil {
			t.Errorf("%s → %s: unexpected error %v", tc.from, tc.to, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("%s → %s: got %v, want ErrInvalidTransition", tc.from, tc.to, err)
		}
	}
}

func TestEveryStatusHasTransitionEntry(t *testing.T) {
	for from, tos := range transitions {
		for _, to := range tos {
			if _, ok := transitions[to]; !ok {
				t.Errorf("%s → %s: target has no entry", from, to)
			}
		}
	}
}
