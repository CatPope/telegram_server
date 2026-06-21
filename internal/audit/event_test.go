package audit

import (
	"encoding/json"
	"testing"
)

func TestMarshalDetailsEmptyDefault(t *testing.T) {
	e := Event{}
	b, err := e.marshalDetails()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "{}" {
		t.Fatalf("expected empty object, got %s", b)
	}
}

func TestMarshalDetailsRoundtrip(t *testing.T) {
	e := Event{Details: map[string]any{"reason": "denied", "count": 3}}
	b, err := e.marshalDetails()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["reason"] != "denied" {
		t.Fatalf("missing reason: %v", out)
	}
}

func TestStageConstantsCoverV6(t *testing.T) {
	want := []Stage{
		StageReceived, StageValidated, StageDispatched, StageDelivered,
		StageDenied, StageRetried, StageDeferred,
		StageIntrusionKick, StageIntrusionUnmitigated,
		StageBotNotAdmin, StageTelegramAuthFailed,
	}
	seen := map[Stage]bool{}
	for _, s := range want {
		if seen[s] {
			t.Fatalf("duplicate stage constant: %s", s)
		}
		seen[s] = true
	}
}
