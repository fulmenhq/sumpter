package parallel

import (
	"testing"
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

func collectOutputRecordNums(agg *ResultAggregator) []int {
	var got []int
	for result := range agg.GetOutputChannel() {
		got = append(got, result.RecordNum)
	}
	return got
}
