package replay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the canonical JSON schema for replay records.
const SchemaVersion = "tokenmill.session/v1"

// Role is the provider-neutral role of a replay message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleFunction  Role = "function"
)

// Source identifies the host and location from which a record was imported.
type Source struct {
	System          string `json:"system"`
	Version         string `json:"version,omitempty"`
	RelativeLocator string `json:"relative_locator,omitempty"`
	ContentSHA256   string `json:"content_sha256,omitempty"`
	extra           *rawFields
}

// MarshalJSON preserves unknown source metadata while keeping structured
// fields authoritative.
func (s Source) MarshalJSON() ([]byte, error) {
	type sourceJSON Source
	return marshalWithExtraFields(sourceJSON(s), s.extra,
		"system", "version", "relative_locator", "content_sha256")
}

// UnmarshalJSON retains source members not understood by this schema version.
func (s *Source) UnmarshalJSON(data []byte) error {
	object, err := decodeJSONObject(data)
	if err != nil {
		return err
	}
	if err := rejectCaseFoldedKnownFields(object,
		"system", "version", "relative_locator", "content_sha256"); err != nil {
		return err
	}
	var decoded Source
	if err := unmarshalExactField(object, "system", &decoded.System); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "version", &decoded.Version); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "relative_locator", &decoded.RelativeLocator); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "content_sha256", &decoded.ContentSHA256); err != nil {
		return err
	}
	decoded.extra = collectExtraFields(object,
		"system", "version", "relative_locator", "content_sha256")
	*s = decoded
	return nil
}

// Message is one ordered message in a replay record.
type Message struct {
	Role       Role   `json:"role"`
	ID         string `json:"id,omitempty"`
	Name       string `json:"name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Parts      []Part `json:"parts"`
	extra      *rawFields
}

// MarshalJSON preserves unknown message metadata while keeping structured
// fields authoritative.
func (m Message) MarshalJSON() ([]byte, error) {
	type messageJSON Message
	return marshalWithExtraFields(messageJSON(m), m.extra,
		"role", "id", "name", "tool_call_id", "parts")
}

// UnmarshalJSON retains message members not understood by this schema version.
func (m *Message) UnmarshalJSON(data []byte) error {
	object, err := decodeJSONObject(data)
	if err != nil {
		return err
	}
	if err := rejectCaseFoldedKnownFields(object,
		"role", "id", "name", "tool_call_id", "parts"); err != nil {
		return err
	}
	var decoded Message
	if err := unmarshalExactField(object, "role", &decoded.Role); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "id", &decoded.ID); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "name", &decoded.Name); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "tool_call_id", &decoded.ToolCallID); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "parts", &decoded.Parts); err != nil {
		return err
	}
	decoded.extra = collectExtraFields(object,
		"role", "id", "name", "tool_call_id", "parts")
	*m = decoded
	return nil
}

// Part is one ordered message part. Text is the only part type that the
// replay engine may transform. Opaque parts retain their source JSON in Raw
// so an importer cannot silently discard fields it does not understand.
type Part struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// Raw contains the semantic source object for an opaque part. It is
	// intentionally excluded from ordinary object encoding because MarshalJSON
	// merges structured text fields into it. JSON whitespace and object key
	// order may be normalized when it is marshaled again.
	Raw json.RawMessage `json:"-"`

	// textSet distinguishes a decoded string text field from the zero value on
	// an opaque non-text part. It prevents a later structured mutation from
	// being mistaken for an absent field.
	textSet bool
}

// MarshalJSON preserves opaque part fields semantically. For text parts,
// Type and Text are structured authorities and all other Raw object fields
// are retained. For non-text parts, Raw remains opaque and must agree with
// the structured Type and Text values; contradictory data is rejected.
func (p Part) MarshalJSON() ([]byte, error) {
	if len(p.Raw) > 0 {
		if err := p.validate("part"); err != nil {
			return nil, err
		}
		object, err := rawObject(p.Raw)
		if err != nil {
			return nil, err
		}
		if p.Type == "text" {
			typeJSON, err := json.Marshal(p.Type)
			if err != nil {
				return nil, fmt.Errorf("marshal text part type: %w", err)
			}
			textJSON, err := json.Marshal(p.Text)
			if err != nil {
				return nil, fmt.Errorf("marshal text part text: %w", err)
			}
			object["type"] = typeJSON
			object["text"] = textJSON
		}
		return json.Marshal(object)
	}

	if p.Type == "text" {
		return json.Marshal(struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: p.Type, Text: p.Text})
	}

	return json.Marshal(struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}{Type: p.Type, Text: p.Text})
}

// UnmarshalJSON decodes type before attempting to read a string text field.
// The complete object is retained as semantic Raw JSON so unknown fields and
// non-string text values on opaque non-text parts survive the round trip.
func (p *Part) UnmarshalJSON(data []byte) error {
	object, err := rawObject(data)
	if err != nil {
		return err
	}
	if err := rejectCaseFoldedKnownFields(object, "type", "text"); err != nil {
		return err
	}

	var decoded Part

	if rawType, ok := object["type"]; ok {
		if err := unmarshalExactRaw(rawType, "type", &decoded.Type); err != nil {
			return err
		}
	}
	if rawText, ok := object["text"]; ok {
		if decoded.Type == "text" {
			if err := unmarshalExactRaw(rawText, "text", &decoded.Text); err != nil {
				return err
			}
			decoded.textSet = true
		} else {
			var text string
			if err := json.Unmarshal(rawText, &text); err == nil {
				decoded.Text = text
				decoded.textSet = true
			}
		}
	}
	decoded.Raw = append(json.RawMessage(nil), data...)
	*p = decoded
	return nil
}

// Record is one provider-neutral replayable turn.
type Record struct {
	Schema    string    `json:"schema"`
	RecordID  string    `json:"record_id"`
	Source    Source    `json:"source"`
	SessionID string    `json:"session_id"`
	TurnID    string    `json:"turn_id,omitempty"`
	Sequence  int       `json:"sequence"`
	Timestamp time.Time `json:"timestamp,omitempty"`
	Model     string    `json:"model,omitempty"`
	Messages  []Message `json:"messages"`
	Replay    string    `json:"replay,omitempty"`
	Redaction string    `json:"redaction,omitempty"`
	extra     *rawFields
}

// MarshalJSON preserves unknown record metadata while keeping structured
// fields authoritative.
func (r Record) MarshalJSON() ([]byte, error) {
	type recordJSON Record
	return marshalWithExtraFields(recordJSON(r), r.extra,
		"schema", "record_id", "source", "session_id", "turn_id", "sequence",
		"timestamp", "model", "messages", "replay", "redaction")
}

// UnmarshalJSON retains record members not understood by this schema version.
func (r *Record) UnmarshalJSON(data []byte) error {
	object, err := decodeJSONObject(data)
	if err != nil {
		return err
	}
	if err := rejectCaseFoldedKnownFields(object,
		"schema", "record_id", "source", "session_id", "turn_id", "sequence",
		"timestamp", "model", "messages", "replay", "redaction"); err != nil {
		return err
	}
	var decoded Record
	if err := unmarshalExactField(object, "schema", &decoded.Schema); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "record_id", &decoded.RecordID); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "source", &decoded.Source); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "session_id", &decoded.SessionID); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "turn_id", &decoded.TurnID); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "sequence", &decoded.Sequence); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "timestamp", &decoded.Timestamp); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "model", &decoded.Model); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "messages", &decoded.Messages); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "replay", &decoded.Replay); err != nil {
		return err
	}
	if err := unmarshalExactField(object, "redaction", &decoded.Redaction); err != nil {
		return err
	}
	decoded.extra = collectExtraFields(object,
		"schema", "record_id", "source", "session_id", "turn_id", "sequence",
		"timestamp", "model", "messages", "replay", "redaction")
	*r = decoded
	return nil
}

// NewRecord returns a deep copy of record, including every message, part,
// slice, and opaque raw JSON byte sequence.
func NewRecord(record Record) Record {
	return record.Clone()
}

// Clone returns a deep copy of the record.
func (r Record) Clone() Record {
	r.extra = cloneRawFields(r.extra)
	r.Source.extra = cloneRawFields(r.Source.extra)
	r.Messages = cloneMessages(r.Messages)
	return r
}

// Validate checks the invariants required by the replay interchange format.
func (r Record) Validate() error {
	if r.Schema != SchemaVersion {
		return validationError("schema", "must be "+SchemaVersion)
	}
	if strings.TrimSpace(r.RecordID) == "" {
		return validationError("record_id", "must not be empty")
	}
	if strings.TrimSpace(r.Source.System) == "" {
		return validationError("source.system", "must not be empty")
	}
	if !isNamespacedID(r.RecordID, r.Source.System) {
		return validationError("record_id", "must be namespaced with source.system")
	}
	if strings.TrimSpace(r.SessionID) == "" {
		return validationError("session_id", "must not be empty")
	}
	if !isNamespacedID(r.SessionID, r.Source.System) {
		return validationError("session_id", "must be namespaced with source.system")
	}
	if r.Sequence < 0 {
		return validationError("sequence", "must be nonnegative")
	}
	if len(r.Messages) == 0 {
		return validationError("messages", "must contain at least one message")
	}

	for messageIndex, message := range r.Messages {
		if !validRole(message.Role) {
			return validationError(fmt.Sprintf("messages[%d].role", messageIndex), "is not a valid message role")
		}
		if len(message.Parts) == 0 {
			return validationError(fmt.Sprintf("messages[%d].parts", messageIndex), "must contain at least one part")
		}
		for partIndex, part := range message.Parts {
			if err := part.validate(fmt.Sprintf("messages[%d].parts[%d]", messageIndex, partIndex)); err != nil {
				return err
			}
		}
	}

	return nil
}

func (p Part) validate(path string) error {
	if strings.TrimSpace(p.Type) == "" {
		return validationError(path+".type", "must not be empty")
	}
	if len(p.Raw) == 0 && p.Type == "text" {
		return nil
	}
	if len(p.Raw) == 0 {
		return validationError(path+".raw", "is required for an opaque non-text part")
	}
	object, err := rawObject(p.Raw)
	if err != nil {
		return validationError(path+".raw", err.Error())
	}
	if err := rejectCaseFoldedKnownFields(object, "type", "text"); err != nil {
		return validationError(path+".raw", err.Error())
	}
	rawType, ok := object["type"]
	if !ok {
		return validationError(path+".raw.type", "must be present")
	}
	var rawTypeString string
	if err := unmarshalExactRaw(rawType, path+".raw.type", &rawTypeString); err != nil {
		return validationError(path+".raw.type", err.Error())
	}
	if rawTypeString != p.Type {
		return validationError(path+".raw.type", "must match part.type")
	}
	if p.Type == "text" {
		if rawText, ok := object["text"]; ok {
			var rawTextString string
			if err := unmarshalExactRaw(rawText, path+".raw.text", &rawTextString); err != nil {
				return validationError(path+".raw.text", err.Error())
			}
		}
		return nil
	}
	if rawText, ok := object["text"]; ok {
		var rawTextString string
		if err := json.Unmarshal(rawText, &rawTextString); err != nil {
			if p.textSet || p.Text != "" {
				return validationError(path+".text", "contradicts non-string raw text")
			}
			return nil
		}
		if (p.textSet || p.Text != "") && p.Text != rawTextString {
			return validationError(path+".text", "contradicts raw text")
		}
	} else if p.textSet || p.Text != "" {
		return validationError(path+".text", "is not present in raw part")
	}
	return nil
}

func rawObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	object, err := decodeJSONObject(raw)
	if err != nil {
		return nil, fmt.Errorf("raw part must be a JSON object: %w", err)
	}
	return object, nil
}

type rawFields map[string]json.RawMessage

func decodeJSONObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	object, err := decodeJSONObjectValue(decoder)
	if err != nil {
		return nil, err
	}
	if err := ensureJSONDocumentEnd(decoder); err != nil {
		return nil, err
	}
	return object, nil
}

func decodeJSONObjectValue(decoder *json.Decoder) (map[string]json.RawMessage, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, fmt.Errorf("must be a JSON object")
	}

	object := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("JSON object member name must be a string")
		}
		if _, exists := object[key]; exists {
			return nil, fmt.Errorf("duplicate JSON object member %q", key)
		}

		value, err := decodeRawValue(decoder)
		if err != nil {
			return nil, fmt.Errorf("decode JSON member %q: %w", key, err)
		}
		object[key] = value
	}

	end, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := end.(json.Delim); !ok || delim != '}' {
		return nil, fmt.Errorf("JSON object is not terminated")
	}
	return object, nil
}

func decodeRawValue(decoder *json.Decoder) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if err := validateJSONDocument(raw); err != nil {
		return nil, err
	}
	return append(json.RawMessage(nil), raw...), nil
}

func validateJSONDocument(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := validateJSONValue(decoder); err != nil {
		return err
	}
	return ensureJSONDocumentEnd(decoder)
}

func validateJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object member name must be a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON object member %q", key)
			}
			seen[key] = struct{}{}
			if err := validateJSONValue(decoder); err != nil {
				return fmt.Errorf("decode JSON member %q: %w", key, err)
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if endDelim, ok := end.(json.Delim); !ok || endDelim != '}' {
			return fmt.Errorf("JSON object is not terminated")
		}
	case '[':
		for decoder.More() {
			if err := validateJSONValue(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if endDelim, ok := end.(json.Delim); !ok || endDelim != ']' {
			return fmt.Errorf("JSON array is not terminated")
		}
	case '}', ']':
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func ensureJSONDocumentEnd(decoder *json.Decoder) error {
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return fmt.Errorf("JSON document contains trailing data")
	}
	return err
}

func unmarshalExactField(object map[string]json.RawMessage, field string, target any) error {
	raw, ok := object[field]
	if !ok {
		return nil
	}
	return unmarshalExactRaw(raw, field, target)
}

func unmarshalExactRaw(raw json.RawMessage, field string, target any) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("decode JSON member %q: null is not allowed", field)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("decode JSON member %q: %w", field, err)
	}
	return nil
}

func rejectCaseFoldedKnownFields(object map[string]json.RawMessage, known ...string) error {
	knownSet := make(map[string]struct{}, len(known))
	for _, field := range known {
		knownSet[field] = struct{}{}
	}
	for _, field := range sortedRawFieldNames(object) {
		if _, exact := knownSet[field]; exact {
			continue
		}
		for _, structuredField := range known {
			if strings.EqualFold(field, structuredField) {
				return fmt.Errorf("JSON member %q conflicts with structured field %q", field, structuredField)
			}
		}
	}
	return nil
}

func collectExtraFields(object map[string]json.RawMessage, known ...string) *rawFields {
	knownSet := make(map[string]struct{}, len(known))
	for _, field := range known {
		knownSet[field] = struct{}{}
	}

	var extra rawFields
	for field, value := range object {
		if _, ok := knownSet[field]; ok {
			continue
		}
		if extra == nil {
			extra = make(rawFields)
		}
		extra[field] = append(json.RawMessage(nil), value...)
	}
	if len(extra) == 0 {
		return nil
	}
	return &extra
}

func marshalWithExtraFields(value any, extra *rawFields, known ...string) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if extra == nil || len(*extra) == 0 {
		return encoded, nil
	}

	object, err := decodeJSONObject(encoded)
	if err != nil {
		return nil, err
	}
	knownSet := make(map[string]struct{}, len(known))
	for _, field := range known {
		knownSet[field] = struct{}{}
	}
	for _, field := range sortedRawFieldNames(map[string]json.RawMessage(*extra)) {
		raw := (*extra)[field]
		if _, ok := knownSet[field]; ok {
			return nil, fmt.Errorf("extra field %q conflicts with structured field", field)
		}
		for _, structuredField := range known {
			if strings.EqualFold(field, structuredField) {
				return nil, fmt.Errorf("extra field %q conflicts with structured field %q", field, structuredField)
			}
		}
		if err := validateJSONDocument(raw); err != nil {
			return nil, fmt.Errorf("extra field %q: %w", field, err)
		}
		object[field] = append(json.RawMessage(nil), raw...)
	}
	return json.Marshal(object)
}

func sortedRawFieldNames(object map[string]json.RawMessage) []string {
	fields := make([]string, 0, len(object))
	for field := range object {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func cloneRawFields(fields *rawFields) *rawFields {
	if fields == nil {
		return nil
	}
	cloned := make(rawFields, len(*fields))
	for field, value := range *fields {
		cloned[field] = append(json.RawMessage(nil), value...)
	}
	return &cloned
}

func isNamespacedID(value, namespace string) bool {
	prefix := namespace + ":"
	return strings.HasPrefix(value, prefix) && len(value) > len(prefix)
}

func validRole(role Role) bool {
	switch role {
	case RoleSystem, RoleDeveloper, RoleUser, RoleAssistant, RoleTool, RoleFunction:
		return true
	default:
		return false
	}
}

func cloneMessages(messages []Message) []Message {
	if messages == nil {
		return nil
	}
	cloned := make([]Message, len(messages))
	for i, message := range messages {
		cloned[i] = message
		cloned[i].extra = cloneRawFields(message.extra)
		if message.Parts == nil {
			continue
		}
		cloned[i].Parts = make([]Part, len(message.Parts))
		for j, part := range message.Parts {
			cloned[i].Parts[j] = part
			if part.Raw != nil {
				cloned[i].Parts[j].Raw = append(json.RawMessage(nil), part.Raw...)
			}
		}
	}
	return cloned
}

// Policy controls which replay transformations are allowed.
type Policy struct {
	MinSavingsPercent int             `json:"min_savings_percent"`
	MinSavingsTokens  int             `json:"min_savings_tokens"`
	AllowedCodecs     map[string]bool `json:"allowed_codecs,omitempty"`
	SafeTextOnly      bool            `json:"safe_text_only"`
}

// NewPolicy returns a deep copy of policy, including its codec allow-list.
func NewPolicy(policy Policy) Policy {
	return policy.Clone()
}

// Clone returns a deep copy of the policy.
func (p Policy) Clone() Policy {
	if p.AllowedCodecs != nil {
		allowedCodecs := p.AllowedCodecs
		p.AllowedCodecs = make(map[string]bool, len(allowedCodecs))
		for codec, allowed := range allowedCodecs {
			p.AllowedCodecs[codec] = allowed
		}
	}
	return p
}

// ResultStatus is the outcome of replaying one record.
type ResultStatus string

const (
	StatusTransformed ResultStatus = "transformed"
	StatusUnchanged   ResultStatus = "unchanged"
	StatusIgnored     ResultStatus = "ignored"
	StatusFailed      ResultStatus = "failed"
)

// Failure is a stable, reportable replay failure.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// RecordResult contains the outcome and metrics for one replay record.
type RecordResult struct {
	Status            ResultStatus     `json:"status"`
	SourceID          string           `json:"source_id,omitempty"`
	RecordID          string           `json:"record_id"`
	Provider          string           `json:"provider,omitempty"`
	Model             string           `json:"model,omitempty"`
	TargetCount       int              `json:"target_count,omitempty"`
	OriginalTokens    int              `json:"original_tokens,omitempty"`
	TransformedTokens int              `json:"transformed_tokens,omitempty"`
	SavedTokens       int              `json:"saved_tokens,omitempty"`
	SavingsBasis      string           `json:"savings_basis,omitempty"`
	PhaseTimings      map[string]int64 `json:"phase_timings,omitempty"`
	OutputSHA256      string           `json:"output_sha256,omitempty"`
	Failures          []Failure        `json:"failures,omitempty"`
}

// NewRecordResult returns a deep copy of result.
func NewRecordResult(result RecordResult) RecordResult {
	if result.PhaseTimings != nil {
		result.PhaseTimings = cloneInt64Map(result.PhaseTimings)
	}
	if result.Failures != nil {
		result.Failures = append([]Failure(nil), result.Failures...)
	}
	return result
}

// RunSummary aggregates a replay run without retaining prompt text.
type RunSummary struct {
	TotalRecords           int            `json:"total_records"`
	TransformedRecords     int            `json:"transformed_records"`
	UnchangedRecords       int            `json:"unchanged_records"`
	IgnoredRecords         int            `json:"ignored_records"`
	FailedRecords          int            `json:"failed_records"`
	TotalOriginalTokens    int            `json:"total_original_tokens"`
	TotalTransformedTokens int            `json:"total_transformed_tokens"`
	TotalSavedTokens       int            `json:"total_saved_tokens"`
	SavingsPercent         *float64       `json:"savings_percent"`
	ElapsedMillis          int64          `json:"elapsed_ms"`
	BySource               map[string]int `json:"by_source,omitempty"`
	ByProvider             map[string]int `json:"by_provider,omitempty"`
	ByModel                map[string]int `json:"by_model,omitempty"`
	Failures               map[string]int `json:"failures,omitempty"`
}

// NewRunSummary returns a deep copy of summary, including all aggregate maps.
func NewRunSummary(summary RunSummary) RunSummary {
	return summary.Clone()
}

// Clone returns a deep copy of the summary, including its aggregate maps and
// optional savings percentage value.
func (s RunSummary) Clone() RunSummary {
	if s.SavingsPercent != nil {
		savingsPercent := *s.SavingsPercent
		s.SavingsPercent = &savingsPercent
	}
	s.BySource = cloneIntMap(s.BySource)
	s.ByProvider = cloneIntMap(s.ByProvider)
	s.ByModel = cloneIntMap(s.ByModel)
	s.Failures = cloneIntMap(s.Failures)
	return s
}

func cloneIntMap(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	cloned := make(map[string]int, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneInt64Map(values map[string]int64) map[string]int64 {
	if values == nil {
		return nil
	}
	cloned := make(map[string]int64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// ValidationError reports one invalid record field.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("replay validation failed for %s: %s", e.Field, e.Reason)
}

func validationError(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}
