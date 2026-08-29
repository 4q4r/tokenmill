//go:build linux && (amd64 || arm64)

package corpus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tokenmill/tokenmill/internal/replay"
)

// CodexSource imports dated Codex rollout JSONL as archive data. Operational
// SQLite state, authentication, and other companion files are not transcript
// inputs and are never scanned.
type CodexSource struct {
	// Root is an explicit test or deployment root. When empty, Discover uses
	// Options.Root, CODEX_HOME, or ~/.codex in that order.
	Root string
}

// NewCodexSource creates a Codex importer with an optional explicit root.
func NewCodexSource(root ...string) *CodexSource {
	source := &CodexSource{}
	if len(root) > 0 {
		source.Root = root[0]
	}
	return source
}

// ID returns the stable source identifier.
func (s *CodexSource) ID() string {
	return "codex"
}

// Discover returns only sessions/YYYY/MM/DD/rollout-*.jsonl files. Any file
// below sessions that is not an approved rollout is rejected visibly.
func (s *CodexSource) Discover(ctx context.Context, options Options) ([]Artifact, error) {
	if ctx == nil {
		return nil, fmt.Errorf("codex discovery requires a context")
	}
	root, err := s.resolveRoot(options)
	if err != nil {
		return nil, err
	}
	return scanTranscriptTree(ctx, root, "sessions", codexAllowedDirectory, codexAllowedFile)
}

// Read parses one approved Codex rollout. Unknown envelope events and
// unsupported response items are represented as opaque replay parts.
func (s *CodexSource) Read(ctx context.Context, artifact Artifact, writer *Writer) error {
	state := codexImportState{usedTurnIDs: make(map[string]struct{})}
	return readSourceJSONL(ctx, artifact, writer, codexAllowedFile, func(line sourceLine) error {
		return codexReadLine(writer, artifact, &state, line)
	})
}

func codexReadLine(writer *Writer, artifact Artifact, state *codexImportState, line sourceLine) error {
	if !line.Complete {
		return quarantineSourceLine(writer, line, CodeInputJSON, "Codex rollout line is not newline terminated", corpusError(CodeInputJSON, "Codex rollout line is not newline terminated", nil))
	}
	envelope, err := decodeSourceObject(line.Payload)
	if err != nil {
		return quarantineSourceLine(writer, line, CodeInputJSON, "malformed Codex rollout record: "+err.Error(), corpusError(CodeInputJSON, "malformed Codex rollout record", err))
	}
	record, err := codexRecord(envelope, artifact, line, state)
	if err != nil {
		code := CodeOf(err)
		if code == "" {
			code = CodeInputJSON
		}
		return quarantineSourceLine(writer, line, code, err.Error(), err)
	}
	return writeImportedRecord(writer, line, record)
}

var (
	codexYearPattern    = regexp.MustCompile(`^[0-9]{4}$`)
	codexMonthPattern   = regexp.MustCompile(`^[0-9]{2}$`)
	codexDayPattern     = regexp.MustCompile(`^[0-9]{2}$`)
	codexRolloutPattern = regexp.MustCompile(`^rollout-[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}-[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}(?:_[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})?\.jsonl$`)
)

func codexAllowedDirectory(relative string) bool {
	parts := sourcePathParts(relative)
	if len(parts) < 2 || len(parts) > 4 || parts[0] != "sessions" {
		return false
	}
	switch len(parts) {
	case 2:
		return codexYearPattern.MatchString(parts[1])
	case 3:
		return codexYearPattern.MatchString(parts[1]) && validCalendarPart(parts[2], codexMonthPattern, 1, 12)
	case 4:
		return codexYearPattern.MatchString(parts[1]) && validCalendarPart(parts[2], codexMonthPattern, 1, 12) && validCalendarPart(parts[3], codexDayPattern, 1, 31)
	default:
		return false
	}
}

func codexAllowedFile(relative string) bool {
	parts := sourcePathParts(relative)
	if len(parts) != 5 || parts[0] != "sessions" {
		return false
	}
	if !codexAllowedDirectory(filepath.Join(parts[0], parts[1], parts[2], parts[3])) {
		return false
	}
	return codexRolloutPattern.MatchString(parts[4]) && !excludedSourceName(relative)
}

func validCalendarPart(value string, pattern *regexp.Regexp, minimum, maximum int) bool {
	if !pattern.MatchString(value) {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number >= minimum && number <= maximum
}

func (s *CodexSource) resolveRoot(options Options) (string, error) {
	if s != nil && strings.TrimSpace(s.Root) != "" {
		return s.Root, nil
	}
	if strings.TrimSpace(options.Root) != "" {
		return options.Root, nil
	}
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve Codex home: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

type codexImportState struct {
	sessionID          string
	sessionIDCanonical bool
	model              string
	version            string
	usedTurnIDs        map[string]struct{}
}

func codexRecord(envelope map[string]json.RawMessage, artifact Artifact, line sourceLine, state *codexImportState) (replay.Record, error) {
	if state == nil {
		return replay.Record{}, fmt.Errorf("codex import state is required")
	}
	eventType := sourceString(envelope, "type")
	payload, hasPayload := envelope["payload"]
	payloadObject := map[string]json.RawMessage(nil)
	if hasPayload {
		payloadObject = sourceObject(map[string]json.RawMessage{"payload": payload}, "payload")
	}
	if payloadObject != nil {
		if model := sourceString(payloadObject, "model", "model_id", "modelId"); model != "" {
			state.model = model
		}
		if version := sourceString(payloadObject, "version"); version != "" {
			state.version = version
		}
	}
	if !state.sessionIDCanonical {
		explicitSessionID := sourceString(payloadObject, "session_id", "sessionId", "thread_id", "threadId", "conversation_id", "conversationId")
		if explicitSessionID == "" && eventType == "session_meta" {
			explicitSessionID = sourceString(payloadObject, "id")
		}
		if explicitSessionID == "" {
			explicitSessionID = sourceString(envelope, "session_id", "sessionId", "thread_id", "threadId")
		}
		if explicitSessionID != "" {
			state.sessionID = explicitSessionID
			state.sessionIDCanonical = true
		}
	}
	if state.sessionID == "" {
		state.sessionID = artifact.RelativePath
	}
	timestamp := sourceTimestamp(envelope, "timestamp", "created_at", "createdAt")
	turnID := ""
	var message replay.Message
	var err error

	switch eventType {
	case "session_meta":
		turnID = sourceString(payloadObject, "id", "session_id", "sessionId")
		message = opaqueCodexMessage(eventType, envelope, fmt.Sprintf("line-%d", line.Line))
	case "turn_context":
		turnID = sourceString(payloadObject, "turn_id", "turnId", "id")
		message = opaqueCodexMessage(eventType, envelope, fmt.Sprintf("line-%d", line.Line))
	case "event_msg":
		turnID = sourceString(payloadObject, "turn_id", "turnId", "id")
		message, err = codexEventMessage(payloadObject, envelope, fmt.Sprintf("line-%d", line.Line))
		if err != nil {
			return replay.Record{}, err
		}
	case "response_item":
		turnID = sourceString(payloadObject, "turn_id", "turnId", "id", "message_id", "messageId")
		message, err = codexResponseMessage(payloadObject, envelope, fmt.Sprintf("line-%d", line.Line))
		if err != nil {
			return replay.Record{}, err
		}
	default:
		turnID = sourceString(envelope, "id")
		message = opaqueCodexMessage(eventType, envelope, fmt.Sprintf("line-%d", line.Line))
	}
	if turnID == "" {
		turnID = fmt.Sprintf("line-%d", line.Line)
	}
	// A rollout can contain several records for one turn. Including the source
	// line in the fallback keeps record IDs unique without changing the source
	// call_id used for correlation.
	if sourceString(payloadObject, "turn_id", "turnId") != "" && eventType != "turn_context" {
		turnID += fmt.Sprintf(":line-%d", line.Line)
	}
	if state.usedTurnIDs == nil {
		state.usedTurnIDs = make(map[string]struct{})
	}
	baseTurnID := turnID
	if _, exists := state.usedTurnIDs[turnID]; exists {
		for suffix := 1; ; suffix++ {
			candidate := fmt.Sprintf("%s:line-%d", baseTurnID, line.Line)
			if suffix > 1 {
				candidate = fmt.Sprintf("%s:line-%d-%d", baseTurnID, line.Line, suffix)
			}
			if _, candidateExists := state.usedTurnIDs[candidate]; !candidateExists {
				turnID = candidate
				break
			}
		}
	}
	state.usedTurnIDs[turnID] = struct{}{}
	record := newImportedRecord("codex", artifact, state.sessionID, turnID, line.Line-1, timestamp, state.model, message)
	record.Source.Version = state.version
	return record, nil
}

func opaqueCodexMessage(eventType string, envelope map[string]json.RawMessage, fallbackID string) replay.Message {
	partType := eventType
	if partType == "" {
		partType = "codex_unknown"
	}
	raw := rawObjectBytes(envelope)
	if sourceString(envelope, "type") == "" {
		raw = rawObjectBytes(map[string]json.RawMessage{
			"type":     json.RawMessage(`"codex_unknown"`),
			"envelope": raw,
		})
	}
	return replay.Message{Role: replay.RoleSystem, ID: fallbackID, Parts: []replay.Part{{Type: partType, Raw: raw}}}
}

func codexEventMessage(payload, envelope map[string]json.RawMessage, fallbackID string) (replay.Message, error) {
	eventType := sourceString(payload, "type")
	switch eventType {
	case "user_message":
		content, ok := payload["message"]
		if !ok {
			content = payload["content"]
		}
		parts, err := codexContentParts(content)
		if err != nil {
			return replay.Message{}, err
		}
		return replay.Message{Role: replay.RoleUser, ID: fallbackID, Parts: parts}, nil
	case "agent_message", "assistant_message":
		parts, err := codexContentParts(payload["message"])
		if err != nil {
			return replay.Message{}, err
		}
		return replay.Message{Role: replay.RoleAssistant, ID: fallbackID, Parts: parts}, nil
	default:
		return opaqueCodexMessage("event_msg", envelope, fallbackID), nil
	}
}

func codexResponseMessage(payload, envelope map[string]json.RawMessage, fallbackID string) (replay.Message, error) {
	itemType := sourceString(payload, "type")
	switch itemType {
	case "message":
		role, ok := importedRole(sourceString(payload, "role"))
		if !ok {
			return replay.Message{}, corpusError(CodeInputJSON, "Codex response message has an unsupported role", nil)
		}
		parts, err := codexContentParts(payload["content"])
		if err != nil {
			return replay.Message{}, err
		}
		return replay.Message{Role: role, ID: fallbackID, Parts: parts}, nil
	case "function_call", "tool_call":
		part, err := codexFunctionCallPart(payload)
		if err != nil {
			return replay.Message{}, err
		}
		return replay.Message{Role: replay.RoleAssistant, ID: fallbackID, Name: sourceString(payload, "name", "tool"), ToolCallID: sourceString(payload, "call_id", "callId"), Parts: []replay.Part{part}}, nil
	case "function_call_output", "tool_result", "tool_call_result":
		return replay.Message{Role: replay.RoleTool, ID: fallbackID, ToolCallID: sourceString(payload, "call_id", "callId", "tool_call_id", "toolCallId"), Parts: []replay.Part{{Type: itemType, Raw: rawObjectBytes(payload)}}}, nil
	default:
		if itemType == "" {
			return opaqueCodexMessage("response_item", envelope, fallbackID), nil
		}
		return replay.Message{Role: replay.RoleSystem, ID: fallbackID, Parts: []replay.Part{{Type: itemType, Raw: rawObjectBytes(payload)}}}, nil
	}
}

func codexFunctionCallPart(payload map[string]json.RawMessage) (replay.Part, error) {
	copyOfPayload := cloneRawObject(payload)
	if rawArguments, ok := copyOfPayload["arguments"]; ok {
		var encoded string
		if json.Unmarshal(rawArguments, &encoded) == nil {
			parsed, err := decodeSourceObject([]byte(encoded))
			if err != nil {
				return replay.Part{}, corpusError(CodeInputJSON, "decode Codex function arguments", err)
			}
			copyOfPayload["arguments"] = rawObjectBytes(parsed)
		}
	}
	partType := sourceString(copyOfPayload, "type")
	if partType == "" {
		partType = "function_call"
		copyOfPayload["type"] = json.RawMessage(`"function_call"`)
	}
	return replay.Part{Type: partType, Raw: rawObjectBytes(copyOfPayload)}, nil
}

func codexContentParts(raw json.RawMessage) ([]replay.Part, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, corpusError(CodeInputJSON, "Codex message has no content", nil)
	}
	trimmed := bytes.TrimSpace(raw)
	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, corpusError(CodeInputJSON, "decode Codex text content", err)
		}
		return []replay.Part{{Type: "text", Text: text, Raw: rawObjectBytes(map[string]json.RawMessage{
			"type": json.RawMessage(`"text"`),
			"text": raw,
		})}}, nil
	}
	if trimmed[0] != '[' {
		return []replay.Part{{Type: "codex_content", Raw: rawObjectBytes(map[string]json.RawMessage{
			"type":    json.RawMessage(`"codex_content"`),
			"content": append(json.RawMessage(nil), raw...),
		})}}, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(trimmed, &blocks); err != nil {
		return nil, corpusError(CodeInputJSON, "decode Codex content blocks", err)
	}
	parts := make([]replay.Part, 0, len(blocks))
	for _, block := range blocks {
		object, err := decodeSourceObject(block)
		if err != nil {
			return nil, corpusError(CodeInputJSON, "decode Codex content block", err)
		}
		blockType := sourceString(object, "type")
		text, hasText := sourceStringWithPresence(object, "text")
		if hasText && (blockType == "text" || blockType == "input_text" || blockType == "output_text") {
			if blockType != "text" {
				object["source_type"] = json.RawMessage(strconv.Quote(blockType))
				object["type"] = json.RawMessage(`"text"`)
			}
			parts = append(parts, replay.Part{Type: "text", Text: text, Raw: rawObjectBytes(object)})
			continue
		}
		if blockType == "" {
			blockType = "codex_block"
			object["type"] = json.RawMessage(`"codex_block"`)
		}
		parts = append(parts, replay.Part{Type: blockType, Raw: rawObjectBytes(object)})
	}
	if len(parts) == 0 {
		return nil, corpusError(CodeInputJSON, "Codex content has no blocks", nil)
	}
	return parts, nil
}

func cloneRawObject(object map[string]json.RawMessage) map[string]json.RawMessage {
	clone := make(map[string]json.RawMessage, len(object))
	for key, value := range object {
		clone[key] = append(json.RawMessage(nil), value...)
	}
	return clone
}

var _ Source = (*CodexSource)(nil)
