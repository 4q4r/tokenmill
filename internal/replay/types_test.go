package replay

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRecordValidatesAndRoundTripsWithOpaquePart(t *testing.T) {
	record := validRecord()
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"schema":"tokenmill.session/v1"`)) {
		t.Fatalf("JSON does not contain canonical schema: %s", encoded)
	}

	var decoded Record
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded Validate: %v", err)
	}
	if len(decoded.Messages[1].Parts[0].Raw) == 0 {
		t.Fatal("opaque part raw JSON was not preserved")
	}
	assertJSONEqual(t, decoded.Messages[1].Parts[0].Raw, record.Messages[1].Parts[0].Raw)
}

func TestTextPartPreservesUnknownFieldsAndUsesStructuredText(t *testing.T) {
	input := json.RawMessage(`{"type":"text","text":"before","annotations":{"priority":1},"vendor":{"flag":true}}`)
	var part Part
	if err := json.Unmarshal(input, &part); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	part.Text = "after"

	encoded, err := json.Marshal(part)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := json.RawMessage(`{"type":"text","text":"after","annotations":{"priority":1},"vendor":{"flag":true}}`)
	assertJSONEqual(t, encoded, want)
}

func TestRecordPreservesUnknownFieldsAtEveryObjectLevel(t *testing.T) {
	input := []byte(`{
		"schema":"tokenmill.session/v1",
		"record_id":"opencode:session-1:turn-4",
		"source":{
			"system":"opencode",
			"version":"1.18.21",
			"source_vendor":{"region":"test"}
		},
		"session_id":"opencode:session-1",
		"sequence":4,
		"timestamp":"2026-08-27T12:00:00Z",
		"messages":[{
			"role":"user",
			"parts":[{"type":"text","text":"hello"}],
			"message_vendor":["future",true]
		}],
		"record_vendor":{"trace":{"sampled":true}}
	}`)

	var record Record
	if err := json.Unmarshal(input, &record); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := record.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	reencoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	assertJSONEqual(t, reencoded, input)
	secondEncoding, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("second Marshal: %v", err)
	}
	if !bytes.Equal(reencoded, secondEncoding) {
		t.Fatalf("unknown-field merge is not deterministic:\n%s\n%s", reencoded, secondEncoding)
	}

	record.RecordID = "opencode:session-1:turn-5"
	record.Source.System = "opencode-v2"
	record.Messages[0].Role = RoleAssistant
	reencoded, err = json.Marshal(record)
	if err != nil {
		t.Fatalf("Marshal after structured changes: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(reencoded, &got); err != nil {
		t.Fatalf("decode reencoded record: %v", err)
	}
	if got["record_id"] != "opencode:session-1:turn-5" {
		t.Fatalf("record_id was not authoritative: %#v", got["record_id"])
	}
	source, ok := got["source"].(map[string]any)
	if !ok || source["system"] != "opencode-v2" {
		t.Fatalf("source.system was not authoritative: %#v", got["source"])
	}
	messages, ok := got["messages"].([]any)
	if !ok || messages[0].(map[string]any)["role"] != string(RoleAssistant) {
		t.Fatalf("message.role was not authoritative: %#v", got["messages"])
	}
	if _, ok := got["record_vendor"]; !ok {
		t.Fatal("record unknown field was lost")
	}
	if _, ok := source["source_vendor"]; !ok {
		t.Fatal("source unknown field was lost")
	}
	if _, ok := messages[0].(map[string]any)["message_vendor"]; !ok {
		t.Fatal("message unknown field was lost")
	}
}

func TestRecordCloneCopiesUnknownFields(t *testing.T) {
	input := []byte(`{
		"schema":"tokenmill.session/v1",
		"record_id":"opencode:session-1:turn-4",
		"source":{"system":"opencode","source_vendor":{"version":1}},
		"session_id":"opencode:session-1",
		"sequence":4,
		"messages":[{"role":"user","parts":[{"type":"text","text":"hello"}],"message_vendor":{"version":1}}],
		"record_vendor":{"version":1}
	}`)

	var original Record
	if err := json.Unmarshal(input, &original); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	clone := original.Clone()

	(*original.extra)["record_vendor"] = json.RawMessage(`{"version":2}`)
	(*original.Source.extra)["source_vendor"] = json.RawMessage(`{"version":2}`)
	(*original.Messages[0].extra)["message_vendor"] = json.RawMessage(`{"version":2}`)

	encoded, err := json.Marshal(clone)
	if err != nil {
		t.Fatalf("Marshal clone: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode clone: %v", err)
	}
	if got["record_vendor"].(map[string]any)["version"] != float64(1) {
		t.Fatalf("record unknown field aliased into clone: %#v", got["record_vendor"])
	}
	source := got["source"].(map[string]any)
	if source["source_vendor"].(map[string]any)["version"] != float64(1) {
		t.Fatalf("source unknown field aliased into clone: %#v", source["source_vendor"])
	}
	messages := got["messages"].([]any)
	message := messages[0].(map[string]any)
	if message["message_vendor"].(map[string]any)["version"] != float64(1) {
		t.Fatalf("message unknown field aliased into clone: %#v", message["message_vendor"])
	}
}

func TestStructuredFieldsRejectConflictingExtraFields(t *testing.T) {
	tests := []struct {
		name  string
		value any
	}{
		{
			name: "record",
			value: func() Record {
				record := validRecord()
				record.extra = &rawFields{"schema": json.RawMessage(`"other-schema"`)}
				return record
			}(),
		},
		{
			name: "source",
			value: Source{
				System: "opencode",
				extra:  &rawFields{"system": json.RawMessage(`"other-system"`)},
			},
		},
		{
			name: "message",
			value: Message{
				Role:  RoleUser,
				Parts: []Part{{Type: "text", Text: "hello"}},
				extra: &rawFields{"role": json.RawMessage(`"assistant"`)},
			},
		},
		{
			name: "case-folded record",
			value: func() Record {
				record := validRecord()
				record.extra = &rawFields{"Schema": json.RawMessage(`"other-schema"`)}
				return record
			}(),
		},
		{
			name: "case-folded source",
			value: Source{
				System: "opencode",
				extra:  &rawFields{"System": json.RawMessage(`"other-system"`)},
			},
		},
		{
			name: "case-folded message",
			value: Message{
				Role:  RoleUser,
				Parts: []Part{{Type: "text", Text: "hello"}},
				extra: &rawFields{"Role": json.RawMessage(`"assistant"`)},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := json.Marshal(test.value); err == nil {
				t.Fatal("Marshal accepted extra data that conflicts with structured fields")
			}
		})
	}
}

func TestJSONDecodingRejectsDuplicateMembersRecursively(t *testing.T) {
	tests := []struct {
		name string
		data string
		load func([]byte) error
	}{
		{
			name: "record top level",
			data: `{"schema":"tokenmill.session/v1","schema":"tokenmill.session/v1"}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "source member",
			data: `{"system":"opencode","system":"opencode"}`,
			load: func(data []byte) error {
				var source Source
				return json.Unmarshal(data, &source)
			},
		},
		{
			name: "message member",
			data: `{"role":"user","role":"assistant","parts":[{"type":"text","text":"hello"}]}`,
			load: func(data []byte) error {
				var message Message
				return json.Unmarshal(data, &message)
			},
		},
		{
			name: "part member",
			data: `{"type":"text","type":"text","text":"hello"}`,
			load: func(data []byte) error {
				var part Part
				return json.Unmarshal(data, &part)
			},
		},
		{
			name: "record source nested member",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode","version":"one","version":"two"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":"user","parts":[{"type":"text","text":"hello"}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "record message nested member",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":"user","role":"assistant","parts":[{"type":"text","text":"hello"}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "record part nested member",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":"user","parts":[{"type":"text","type":"text","text":"hello"}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "unknown nested member",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":"user","parts":[{"type":"text","text":"hello"}]}],"vendor":{"value":1,"value":2}}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "trailing data",
			data: `{"system":"opencode"} {"system":"opencode"}`,
			load: func(data []byte) error {
				var source Source
				return json.Unmarshal(data, &source)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.load([]byte(test.data)); err == nil {
				t.Fatal("duplicate JSON member was accepted")
			}
		})
	}
}

func TestExtraFieldMarshalRejectsDuplicateNestedMembers(t *testing.T) {
	record := validRecord()
	record.extra = &rawFields{
		"record_vendor": json.RawMessage(`{"metadata":{"version":1,"version":2}}`),
	}
	if _, err := json.Marshal(record); err == nil {
		t.Fatal("Marshal accepted duplicate nested members in extra fields")
	}
}

func TestJSONDecodingRejectsCaseFoldedKnownFields(t *testing.T) {
	tests := []struct {
		name string
		data string
		load func([]byte) error
	}{
		{
			name: "record exact and folded schema",
			data: `{"schema":"tokenmill.session/v1","Schema":"other","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":"user","parts":[{"type":"text","text":"hello"}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "record folded-only schema",
			data: `{"Schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":"user","parts":[{"type":"text","text":"hello"}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "source system",
			data: `{"system":"opencode","System":"other"}`,
			load: func(data []byte) error {
				var source Source
				return json.Unmarshal(data, &source)
			},
		},
		{
			name: "message role",
			data: `{"role":"user","Role":"assistant","parts":[{"type":"text","text":"hello"}]}`,
			load: func(data []byte) error {
				var message Message
				return json.Unmarshal(data, &message)
			},
		},
		{
			name: "part type",
			data: `{"type":"text","Type":"attachment","text":"hello"}`,
			load: func(data []byte) error {
				var part Part
				return json.Unmarshal(data, &part)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.load([]byte(test.data)); err == nil {
				t.Fatal("case-folded known field was accepted")
			}
		})
	}
}

func TestJSONDecodingRejectsNullAndInvalidTypedFields(t *testing.T) {
	tests := []struct {
		name string
		data string
		load func([]byte) error
	}{
		{
			name: "record sequence null",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":null,"messages":[{"role":"user","parts":[{"type":"text","text":"hello"}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "source system null",
			data: `{"system":null}`,
			load: func(data []byte) error {
				var source Source
				return json.Unmarshal(data, &source)
			},
		},
		{
			name: "message role null",
			data: `{"role":null,"parts":[{"type":"text","text":"hello"}]}`,
			load: func(data []byte) error {
				var message Message
				return json.Unmarshal(data, &message)
			},
		},
		{
			name: "message parts null",
			data: `{"role":"user","parts":null}`,
			load: func(data []byte) error {
				var message Message
				return json.Unmarshal(data, &message)
			},
		},
		{
			name: "direct text part text null",
			data: `{"type":"text","text":null}`,
			load: func(data []byte) error {
				var part Part
				return json.Unmarshal(data, &part)
			},
		},
		{
			name: "nested text part text null",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":"user","parts":[{"type":"text","text":null}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "nested message role null",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":null,"parts":[{"type":"text","text":"hello"}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "record sequence invalid type",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":"one","messages":[{"role":"user","parts":[{"type":"text","text":"hello"}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "nested message role invalid type",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":7,"parts":[{"type":"text","text":"hello"}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
		{
			name: "nested text part invalid type",
			data: `{"schema":"tokenmill.session/v1","record_id":"opencode:session-1:turn-1","source":{"system":"opencode"},"session_id":"opencode:session-1","sequence":1,"messages":[{"role":"user","parts":[{"type":"text","text":[]}]}]}`,
			load: func(data []byte) error {
				var record Record
				return json.Unmarshal(data, &record)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.load([]byte(test.data)); err == nil {
				t.Fatal("JSON null or invalid typed field was accepted")
			}
		})
	}
}

func TestOpaquePartPreservesNonStringText(t *testing.T) {
	raw := json.RawMessage(`{"type":"attachment","text":{"format":"markdown"},"vendor":{"id":7}}`)
	var part Part
	if err := json.Unmarshal(raw, &part); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if part.Type != "attachment" {
		t.Fatalf("type = %q, want attachment", part.Type)
	}
	if err := part.validate("part"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	encoded, err := json.Marshal(part)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	assertJSONEqual(t, encoded, raw)
}

func TestOpaquePartPreservesNullText(t *testing.T) {
	raw := json.RawMessage(`{"type":"attachment","text":null,"vendor":{"id":7}}`)
	var part Part
	if err := json.Unmarshal(raw, &part); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if err := part.validate("part"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	encoded, err := json.Marshal(part)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	assertJSONEqual(t, encoded, raw)
}

func TestPartRejectsNullInRawStructuredFields(t *testing.T) {
	tests := []struct {
		name string
		part Part
	}{
		{
			name: "raw type",
			part: Part{
				Type: "text",
				Text: "hello",
				Raw:  json.RawMessage(`{"type":null,"text":"hello"}`),
			},
		},
		{
			name: "raw text",
			part: Part{
				Type: "text",
				Text: "hello",
				Raw:  json.RawMessage(`{"type":"text","text":null}`),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var validationErr *ValidationError
			if err := test.part.validate("part"); !errors.As(err, &validationErr) {
				t.Fatalf("validate error = %v, want *ValidationError", err)
			}
			if !strings.Contains(validationErr.Reason, "null is not allowed") {
				t.Fatalf("validate reason = %q, want explicit null rejection", validationErr.Reason)
			}
			if _, err := json.Marshal(test.part); err == nil {
				t.Fatal("Marshal accepted null in raw structured field")
			}
		})
	}
}

func TestPartRejectsRawStructuredContradiction(t *testing.T) {
	tests := []struct {
		name string
		part Part
	}{
		{
			name: "type mismatch",
			part: Part{
				Type: "text",
				Text: "new",
				Raw:  json.RawMessage(`{"type":"attachment","text":"old"}`),
			},
		},
		{
			name: "non-string text with structured text",
			part: Part{
				Type: "attachment",
				Text: "new",
				Raw:  json.RawMessage(`{"type":"attachment","text":{"format":"markdown"}}`),
			},
		},
		{
			name: "case-folded raw field",
			part: Part{
				Type: "text",
				Text: "hello",
				Raw:  json.RawMessage(`{"type":"text","Type":"attachment","text":"hello"}`),
			},
		},
		{
			name: "duplicate nested raw field",
			part: Part{
				Type: "attachment",
				Raw:  json.RawMessage(`{"type":"attachment","vendor":{"version":1,"version":2}}`),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var validationErr *ValidationError
			if err := test.part.validate("part"); !errors.As(err, &validationErr) {
				t.Fatalf("validate error = %v, want *ValidationError", err)
			}
			if _, err := json.Marshal(test.part); err == nil {
				t.Fatal("Marshal accepted contradictory Raw and structured fields")
			}
		})
	}
}

func TestRecordValidationRejectsMalformedRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Record)
	}{
		{name: "schema", mutate: func(record *Record) { record.Schema = "" }},
		{name: "record id", mutate: func(record *Record) { record.RecordID = "" }},
		{name: "source system", mutate: func(record *Record) { record.Source.System = "" }},
		{name: "session id", mutate: func(record *Record) { record.SessionID = "" }},
		{name: "namespace", mutate: func(record *Record) { record.SessionID = "other:session-1" }},
		{name: "negative sequence", mutate: func(record *Record) { record.Sequence = -1 }},
		{name: "invalid role", mutate: func(record *Record) { record.Messages[0].Role = "moderator" }},
		{name: "empty parts", mutate: func(record *Record) { record.Messages[0].Parts = nil }},
		{name: "empty part type", mutate: func(record *Record) {
			record.Messages[0].Parts[0].Type = ""
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := validRecord()
			test.mutate(&record)
			var validationErr *ValidationError
			if err := record.Validate(); !errors.As(err, &validationErr) {
				t.Fatalf("Validate error = %v, want *ValidationError", err)
			}
		})
	}
}

func TestNewRecordCopiesInputSlicesAndRawJSON(t *testing.T) {
	original := validRecord()
	copyOfRecord := NewRecord(original)

	original.Messages[0].Parts[0].Text = "mutated text"
	original.Messages[1].Parts[0].Raw[0] = '{'
	original.Messages = append(original.Messages, Message{Role: RoleUser, Parts: []Part{{Type: "text", Text: "new"}}})
	original.Messages[0].Parts = append(original.Messages[0].Parts, Part{Type: "text", Text: "new"})

	if copyOfRecord.Messages[0].Parts[0].Text != "system" {
		t.Fatalf("stored text changed through input alias: %#v", copyOfRecord.Messages[0].Parts[0])
	}
	if !bytes.Equal(copyOfRecord.Messages[1].Parts[0].Raw, []byte(`{"type":"attachment","uri":"file:///tmp/a"}`)) {
		t.Fatalf("stored raw JSON changed through input alias: %s", copyOfRecord.Messages[1].Parts[0].Raw)
	}
	if len(copyOfRecord.Messages) != 2 || len(copyOfRecord.Messages[0].Parts) != 1 {
		t.Fatalf("stored slices changed through input alias: %#v", copyOfRecord)
	}
}

func TestNewPolicyCopiesAllowedCodecs(t *testing.T) {
	allowed := map[string]bool{"dedup": true}
	policy := NewPolicy(Policy{AllowedCodecs: allowed})
	allowed["ansi"] = true
	if len(policy.AllowedCodecs) != 1 || !policy.AllowedCodecs["dedup"] {
		t.Fatalf("policy map changed through input alias: %#v", policy.AllowedCodecs)
	}
}

func TestNewRunSummaryCopiesSavingsPercent(t *testing.T) {
	savings := 12.5
	summary := NewRunSummary(RunSummary{SavingsPercent: &savings})
	savings = 99
	if summary.SavingsPercent == nil || *summary.SavingsPercent != 12.5 {
		t.Fatalf("summary pointer changed through input alias: %v", summary.SavingsPercent)
	}
	*summary.SavingsPercent = 1
	if savings != 99 {
		t.Fatalf("input pointer changed through summary alias: %v", savings)
	}
}

func TestRecordMarshalIsDeterministic(t *testing.T) {
	record := validRecord()
	first, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("first Marshal: %v", err)
	}
	second, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("second Marshal: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("repeated Marshal differs:\n%s\n%s", first, second)
	}
}

func validRecord() Record {
	return Record{
		Schema:    SchemaVersion,
		RecordID:  "opencode:session-1:turn-4",
		Source:    Source{System: "opencode", Version: "1.18.21", RelativeLocator: "session/session-1/message/msg-4", ContentSHA256: "64-hex"},
		SessionID: "opencode:session-1",
		TurnID:    "msg-4",
		Sequence:  4,
		Timestamp: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC),
		Model:     "exact-model-id",
		Messages: []Message{
			{Role: RoleSystem, Parts: []Part{{Type: "text", Text: "system"}}},
			{Role: RoleUser, Parts: []Part{{Type: "attachment", Raw: json.RawMessage(`{"type":"attachment","uri":"file:///tmp/a"}`)}}},
		},
		Replay:    "archive",
		Redaction: "field-aware-v1",
	}
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode got JSON: %v", err)
	}
	var wantValue any
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode want JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON values differ: got %s want %s", got, want)
	}
}
