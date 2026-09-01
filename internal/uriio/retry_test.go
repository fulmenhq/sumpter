package uriio

import (
	"context"
	"errors"
	"testing"

	gonimbusprovider "github.com/3leaps/gonimbus/pkg/provider"
)

func TestRetryTransientFailFastOnPermanent(t *testing.T) {
	s := &Session{}
	calls := 0
	err := s.retryTransient(context.Background(), func() error {
		calls++
		return errors.New("access denied")
	})
	if err == nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestRetryTransientRetriesThrottledThenSucceeds(t *testing.T) {
	s := &Session{budget: NewStagingBudget(StagingBudgetConfig{MaxBytes: 10, MaxFiles: 1, ObjectMax: 10})}
	calls := 0
	err := s.retryTransient(context.Background(), func() error {
		calls++
		if calls < 3 {
			return gonimbusprovider.ErrThrottled
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
	st := s.budget.Stats()
	if st.RetriesThrottle != 2 {
		t.Fatalf("retries=%d", st.RetriesThrottle)
	}
}
