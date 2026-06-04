package parallel

import (
	"context"
	"testing"
	"time"
)

func TestResultAggregator_OrderedOutput(t *testing.T) {
	agg := NewResultAggregator(3)

	inputChan := make(chan WorkResult, 3)

	agg.Collect(inputChan, []int{}, 3)

	inputChan <- WorkResult{RecordNum: 2, Data: map[string]interface{}{"id": 3}, Error: nil}
	inputChan <- WorkResult{RecordNum: 1, Data: map[string]interface{}{"id": 1}, Error: nil}
	inputChan <- WorkResult{RecordNum: 3, Data: map[string]interface{}{"id": 2}, Error: nil}
	close(inputChan)

	got := collectOutputRecordNums(agg)
	agg.Wait()

	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("output record nums = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("output record nums = %v, want %v", got, want)
		}
	}
}

func TestResultAggregator_SkippedRecords(t *testing.T) {
	agg := NewResultAggregator(3)

	inputChan := make(chan WorkResult, 2)

	agg.Collect(inputChan, []int{2}, 3)

	inputChan <- WorkResult{RecordNum: 3, Data: map[string]interface{}{"id": 3}, Error: nil}
	inputChan <- WorkResult{RecordNum: 1, Data: map[string]interface{}{"id": 1}, Error: nil}
	close(inputChan)

	got := collectOutputRecordNums(agg)
	agg.Wait()

	want := []int{1, 3}
	if len(got) != len(want) {
		t.Fatalf("output record nums = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("output record nums = %v, want %v", got, want)
		}
	}
}

func TestResultAggregator_ReleasesWindowSlotsInOutputOrder(t *testing.T) {
	releases := 0
	agg := NewResultAggregatorWithRelease(3, func() {
		releases++
	})

	inputChan := make(chan WorkResult, 3)
	agg.Collect(inputChan, []int{}, 3)

	inputChan <- WorkResult{RecordNum: 2, Data: map[string]interface{}{"id": 2}}
	inputChan <- WorkResult{RecordNum: 1, Data: map[string]interface{}{"id": 1}}
	inputChan <- WorkResult{RecordNum: 3, Data: map[string]interface{}{"id": 3}}
	close(inputChan)

	got := collectOutputRecordNums(agg)
	agg.Wait()

	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("output record nums = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("output record nums = %v, want %v", got, want)
		}
	}
	if releases != 3 {
		t.Fatalf("release count = %d, want 3", releases)
	}
}

func TestWorkScheduler_WindowSlotsBackpressure(t *testing.T) {
	ws := &WorkScheduler{
		ctx:         context.Background(),
		windowSlots: make(chan struct{}, 1),
	}

	if !ws.acquireWindowSlot() {
		t.Fatal("first acquire failed")
	}

	acquired := make(chan struct{})
	go func() {
		if ws.acquireWindowSlot() {
			close(acquired)
		}
	}()

	select {
	case <-acquired:
		t.Fatal("second acquire succeeded before slot release")
	case <-time.After(20 * time.Millisecond):
	}

	ws.releaseWindowSlot()

	select {
	case <-acquired:
	case <-time.After(time.Second):
		t.Fatal("second acquire did not proceed after slot release")
	}
}

func collectOutputRecordNums(agg *ResultAggregator) []int {
	var got []int
	for result := range agg.GetOutputChannel() {
		got = append(got, result.RecordNum)
	}
	return got
}
