package cache

import (
	"reflect"
	"testing"
)

func TestDetectModelFamily(t *testing.T) {
	tests := []struct {
		model string
		want  ModelFamily
	}{
		{model: "claude-sonnet-4-5", want: FamilyAnthropic},
		{model: " CLAUDE-3-HAIKU ", want: FamilyAnthropic},
		{model: "gpt-4o", want: FamilyOpenAI},
		{model: "chatgpt-4o-latest", want: FamilyOpenAI},
		{model: "o3-mini", want: FamilyOpenAI},
		{model: "o4-mini", want: FamilyOpenAI},
		{model: "gemini-2.5-pro", want: FamilyGoogle},
		{model: "qwen-max", want: FamilyUnknown},
		{model: "", want: FamilyUnknown},
	}
	for _, test := range tests {
		if got := DetectModelFamily(test.model); got != test.want {
			t.Fatalf("DetectModelFamily(%q) = %q, want %q", test.model, got, test.want)
		}
	}
}

func TestPlanBreakpointsAnthropicSpreadsOverStableRegion(t *testing.T) {
	messages := make([]Message, 12)
	for i := range messages {
		messages[i] = Message{Role: "user", Content: "m"}
	}
	planned := PlanBreakpoints(FamilyAnthropic, messages, 2)

	var positions []int
	for index, message := range planned {
		if message.CacheControl == nil {
			continue
		}
		if index >= 10 {
			t.Fatalf("breakpoint %d inside the volatile tail", index)
		}
		positions = append(positions, index)
		if message.CacheControl.Position != index {
			t.Fatalf("breakpoint position = %d, want %d", message.CacheControl.Position, index)
		}
		if message.CacheControl.Type != "ephemeral" {
			t.Fatalf("breakpoint type = %q, want ephemeral", message.CacheControl.Type)
		}
	}
	if len(positions) != MaxAnthropicBreakpoints {
		t.Fatalf("breakpoint count = %d, want %d", len(positions), MaxAnthropicBreakpoints)
	}
	for i := 1; i < len(positions); i++ {
		if positions[i] <= positions[i-1] {
			t.Fatalf("positions not strictly increasing: %v", positions)
		}
	}
	if positions[len(positions)-1] != 9 {
		t.Fatalf("last breakpoint = %d, want the last stable boundary 9", positions[len(positions)-1])
	}
	for index, message := range messages {
		if message.CacheControl != nil {
			t.Fatalf("input message %d was mutated", index)
		}
	}
}

func TestPlanBreakpointsAnthropicSmallConversation(t *testing.T) {
	messages := []Message{{Role: "user"}, {Role: "assistant"}, {Role: "user"}}
	planned := PlanBreakpoints(FamilyAnthropic, messages, 1)
	var positions []int
	for index, message := range planned {
		if message.CacheControl != nil {
			positions = append(positions, index)
		}
	}
	if !reflect.DeepEqual(positions, []int{1}) {
		t.Fatalf("positions = %v, want [1]", positions)
	}
}

func TestPlanBreakpointsImplicitFamiliesPlaceNothing(t *testing.T) {
	messages := []Message{{Role: "user"}, {Role: "assistant"}}
	for _, family := range []ModelFamily{FamilyOpenAI, FamilyGoogle, FamilyUnknown} {
		planned := PlanBreakpoints(family, messages, 1)
		for index, message := range planned {
			if message.CacheControl != nil {
				t.Fatalf("family %q placed a breakpoint at %d", family, index)
			}
		}
		if !reflect.DeepEqual(planned, messages) {
			t.Fatalf("family %q changed the messages", family)
		}
	}
}

func TestPlanBreakpointsEmptyMessages(t *testing.T) {
	planned := PlanBreakpoints(FamilyAnthropic, nil, 2)
	if len(planned) != 0 {
		t.Fatalf("planned = %v, want empty", planned)
	}
}
