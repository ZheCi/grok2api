package audit

const QualityProbeRequestPrefix = "quality_"

type CallLevel int

const (
	CallLevelFailed   CallLevel = 0
	CallLevelDegraded CallLevel = 1
	CallLevelSoft     CallLevel = 2
	CallLevelHealthy  CallLevel = 3
)

func (level CallLevel) String() string {
	switch level {
	case CallLevelFailed:
		return "failed"
	case CallLevelDegraded:
		return "degraded"
	case CallLevelSoft:
		return "soft"
	default:
		return "healthy"
	}
}

type CallClassification struct {
	Level CallLevel
	Class string
	TPS   float64
}

// ClassifyCall maps one audit row onto the four-level sparkline used by the
// request-audit table and the quality-guard degrade list. Speed classes reuse
// ClassifyOutputSpeed so the two surfaces agree on "soft" vs "hard".
func ClassifyCall(record Record, softTPS, hardTPS float64, minGenMS int64, failClosed bool) CallClassification {
	firstTokenMS := int64(0)
	if record.FirstTokenMS != nil {
		firstTokenMS = *record.FirstTokenMS
	}
	class, tps, _ := ClassifyOutputSpeed(record.OutputTokens, record.ReasoningTokens, firstTokenMS, record.DurationMS, softTPS, hardTPS, minGenMS, failClosed)
	if record.ErrorCode == ErrorQualityDegraded {
		if record.StatusCode >= 200 && record.StatusCode < 300 {
			return CallClassification{Level: CallLevelDegraded, Class: DegradeClassThinking, TPS: tps}
		}
		return CallClassification{Level: CallLevelFailed, Class: DegradeClassThinking, TPS: tps}
	}
	if record.StatusCode >= 500 {
		return CallClassification{Level: CallLevelFailed, Class: class, TPS: tps}
	}
	if record.StatusCode >= 400 {
		return CallClassification{Level: CallLevelDegraded, Class: class, TPS: tps}
	}
	if record.ErrorCode != "" {
		return CallClassification{Level: CallLevelDegraded, Class: class, TPS: tps}
	}
	switch class {
	case DegradeClassHard, DegradeClassBurst:
		return CallClassification{Level: CallLevelDegraded, Class: class, TPS: tps}
	case DegradeClassSoft:
		return CallClassification{Level: CallLevelSoft, Class: class, TPS: tps}
	default:
		return CallClassification{Level: CallLevelHealthy, Class: class, TPS: tps}
	}
}
