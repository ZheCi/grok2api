package audit

import "testing"

func TestClassifyCall(t *testing.T) {
	first := int64(200)
	healthyFirst := int64(200)
	tests := []struct {
		name       string
		record     Record
		failClosed bool
		wantLevel  CallLevel
		wantClass  string
	}{
		{
			name:      "healthy 2xx",
			record:    Record{StatusCode: 200, Streaming: true, OutputTokens: 100, FirstTokenMS: &healthyFirst, DurationMS: 2200},
			wantLevel: CallLevelHealthy,
		},
		{
			name:      "soft tps",
			record:    Record{StatusCode: 200, Streaming: true, OutputTokens: 600, FirstTokenMS: &first, DurationMS: 1400},
			wantLevel: CallLevelSoft,
			wantClass: DegradeClassSoft,
		},
		{
			name:      "hard tps",
			record:    Record{StatusCode: 200, Streaming: true, OutputTokens: 2000, FirstTokenMS: &first, DurationMS: 1200},
			wantLevel: CallLevelDegraded,
			wantClass: DegradeClassHard,
		},
		{
			name:       "burst when fail closed",
			record:     Record{StatusCode: 200, Streaming: true, OutputTokens: 200, FirstTokenMS: int64Pointer(50), DurationMS: 250},
			failClosed: true,
			wantLevel:  CallLevelDegraded,
			wantClass:  DegradeClassBurst,
		},
		{
			name:      "missing thinking 2xx",
			record:    Record{StatusCode: 200, Streaming: true, ErrorCode: ErrorQualityDegraded, OutputTokens: 48},
			wantLevel: CallLevelDegraded,
			wantClass: DegradeClassThinking,
		},
		{
			name:      "quality degraded 5xx",
			record:    Record{StatusCode: 503, Streaming: true, ErrorCode: ErrorQualityDegraded},
			wantLevel: CallLevelFailed,
			wantClass: DegradeClassThinking,
		},
		{
			name:      "client error",
			record:    Record{StatusCode: 429, ErrorCode: "upstream_quota_exhausted"},
			wantLevel: CallLevelDegraded,
		},
		{
			name:      "server error",
			record:    Record{StatusCode: 502, ErrorCode: "upstream_unavailable"},
			wantLevel: CallLevelFailed,
		},
		{
			name:      "2xx stream error",
			record:    Record{StatusCode: 200, Streaming: true, ErrorCode: "upstream_stream_interrupted", OutputTokens: 10, FirstTokenMS: &first, DurationMS: 2200},
			wantLevel: CallLevelDegraded,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ClassifyCall(test.record, DefaultDegradeSoftTPS, DefaultDegradeHardTPS, DefaultDegradeMinGenMS, test.failClosed)
			if got.Level != test.wantLevel {
				t.Fatalf("level = %s, want %s", got.Level, test.wantLevel)
			}
			if got.Class != test.wantClass {
				t.Fatalf("class = %q, want %q", got.Class, test.wantClass)
			}
		})
	}
}

func int64Pointer(value int64) *int64 { return &value }
