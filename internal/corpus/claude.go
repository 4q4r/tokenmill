//go:build linux && (amd64 || arm64)

package corpus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tokenmill/tokenmill/internal/replay"
)

// ClaudeSource imports Claude Code project transcripts as archive data. The
// source has no writable replay path: it only reads the approved projects tree.
type ClaudeSource struct {
	// Root is an explicit test or deployment root. When empty, Discover uses
	// Options.Root, CLAUDE_CONFIG_DIR, or ~/.claude in that order.
	Root string
}

// NewClaudeSource creates a Claude importer. An optional root is an explicit
// test-root injection and is never inferred from transcript content.
func NewClaudeSource(root ...string) *ClaudeSource {
	source := &ClaudeSource{}
	if len(root) > 0 {
		source.Root = root[0]
	}
	return source
}

// ID returns the stable source identifier.
func (s *ClaudeSource) ID() string {
	return "claude"
}

// Discover returns only approved Claude transcript files below
// ~/.claude/projects (or the explicitly injected equivalent). Main transcripts
// live directly below a project; subagent transcripts live below a session's
// subagents namespace. Root history and unrelated state files are intentionally
// outside the scanned tree.
func (s *ClaudeSource) Discover(ctx context.Context, options Options) ([]Artifact, error) {
	if ctx == nil {
		return nil, fmt.Errorf("claude discovery requires a context")
	}
	root, err := s.resolveRoot(options)
	if err != nil {
		return nil, err
	}
	return scanTranscriptTreeWithPolicy(ctx, root, "projects", claudeAllowedDirectory, claudeAllowedFile, claudeIgnoredDirectory, claudeIgnoredFile)
}

// Read parses one approved Claude JSONL transcript. Every malformed or
// incomplete line is quarantined; no source line is silently skipped.
func (s *ClaudeSource) Read(ctx context.Context, artifact Artifact, writer *Writer) error {
	return readSourceJSONL(ctx, artifact, writer, claudeAllowedFile, func(line sourceLine) error {
		if !line.Complete {
			return quarantineSourceLine(writer, line, CodeInputJSON, "Claude transcript line is not newline terminated", corpusError(CodeInputJSON, "Claude transcript line is not newline terminated", nil))
		}
		object, err := decodeSourceObject(line.Payload)
		if err != nil {
			return quarantineSourceLine(writer, line, CodeInputJSON, "malformed Claude transcript record: "+err.Error(), corpusError(CodeInputJSON, "malformed Claude transcript record", err))
		}
		record, err := claudeRecord(object, artifact, line)
		if err != nil {
			code := CodeOf(err)
			if code == "" {
				code = CodeInputJSON
			}
			return quarantineSourceLine(writer, line, code, err.Error(), err)
		}
		return writeImportedRecord(writer, line, record)
	})
}

var (
	claudeSessionIDPattern    = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	claudeSessionFilePattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\.jsonl$`)
	claudeAgentFilePattern    = regexp.MustCompile(`^agent-(?:[0-9a-fA-F]{7,32}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)
	claudeMetadataFilePattern = regexp.MustCompile(`^agent-(?:[0-9a-fA-F]{7,32}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.meta\.json$`)
	claudeWorkflowIDPattern   = regexp.MustCompile(`^wf_[A-Za-z0-9-]{1,128}$`)
)

func claudeAllowedDirectory(relative string) bool {
	parts := sourcePathParts(relative)
	if len(parts) < 2 || len(parts) > 6 || parts[0] != "projects" {
		return false
	}
	if !safeClaudeDirectoryComponent(parts[1]) {
		return false
	}
	switch len(parts) {
	case 2:
		return true
	case 3:
		return claudeSessionIDPattern.MatchString(parts[2])
	case 4:
		return parts[3] == "subagents" && claudeSessionIDPattern.MatchString(parts[2])
	case 5:
		return parts[3] == "subagents" && parts[4] == "workflows" && claudeSessionIDPattern.MatchString(parts[2])
	case 6:
		return parts[3] == "subagents" && parts[4] == "workflows" && claudeSessionIDPattern.MatchString(parts[2]) && claudeWorkflowIDPattern.MatchString(parts[5])
	default:
		return false
	}
}

func claudeAllowedFile(relative string) bool {
	parts := sourcePathParts(relative)
	if len(parts) < 3 || len(parts) > 7 || parts[0] != "projects" || !safeClaudeDirectoryComponent(parts[1]) {
		return false
	}
	if len(parts) == 3 {
		return claudeSessionFilePattern.MatchString(parts[2]) && !excludedSourceName(parts[2])
	}
	if !claudeSessionIDPattern.MatchString(parts[2]) {
		return false
	}
	if len(parts) == 5 && parts[3] == "subagents" {
		return claudeAgentFilePattern.MatchString(parts[4]) && !excludedSourceName(parts[4])
	}
	if len(parts) == 7 && parts[3] == "subagents" && parts[4] == "workflows" && claudeWorkflowIDPattern.MatchString(parts[5]) {
		return claudeAgentFilePattern.MatchString(parts[6]) && !excludedSourceName(parts[6])
	}
	return false
}

func claudeIgnoredFile(relative string) bool {
	parts := sourcePathParts(relative)
	if len(parts) == 3 && parts[0] == "projects" && safeClaudeDirectoryComponent(parts[1]) {
		return parts[2] == "sessions-index.json"
	}
	if len(parts) == 5 && parts[0] == "projects" && parts[3] == "subagents" {
		return safeClaudeDirectoryComponent(parts[1]) && claudeSessionIDPattern.MatchString(parts[2]) && claudeMetadataFilePattern.MatchString(parts[4])
	}
	if len(parts) == 7 && parts[0] == "projects" && parts[3] == "subagents" && parts[4] == "workflows" {
		return safeClaudeDirectoryComponent(parts[1]) && claudeSessionIDPattern.MatchString(parts[2]) && claudeWorkflowIDPattern.MatchString(parts[5]) && claudeMetadataFilePattern.MatchString(parts[6])
	}
	return false
}

func claudeIgnoredDirectory(relative string) bool {
	parts := sourcePathParts(relative)
	if len(parts) == 3 && parts[0] == "projects" && safeClaudeDirectoryComponent(parts[1]) {
		return parts[2] == "memory"
	}
	return len(parts) == 4 && parts[0] == "projects" && safeClaudeDirectoryComponent(parts[1]) && claudeSessionIDPattern.MatchString(parts[2]) && parts[3] == "tool-results"
}

func (s *ClaudeSource) resolveRoot(options Options) (string, error) {
	if s != nil && strings.TrimSpace(s.Root) != "" {
		return s.Root, nil
	}
	if strings.TrimSpace(options.Root) != "" {
		return options.Root, nil
	}
	if configured := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); configured != "" {
		return configured, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve Claude home: %w", err)
	}
	return filepath.Join(home, ".claude"), nil
}

func claudeRecord(object map[string]json.RawMessage, artifact Artifact, line sourceLine) (replay.Record, error) {
	eventType := sourceString(object, "type")
	sessionID := sourceString(object, "sessionId", "session_id")
	messageObject := sourceObject(object, "message")
	if sessionID == "" && messageObject != nil {
		sessionID = sourceString(messageObject, "sessionId", "session_id")
	}
	if sessionID == "" {
		sessionID = artifact.RelativePath
	}
	agentID := sourceString(object, "agentId", "agent_id", "subagentId", "subagent_id")
	if agentID == "" {
		agentID = claudeAgentIDFromPath(artifact.RelativePath)
	}
	if agentID != "" {
		sessionID += ":subagent:" + agentID
	}
	turnID := sourceString(object, "uuid", "id", "messageId", "message_id")
	if turnID == "" {
		turnID = fmt.Sprintf("line-%d", line.Line)
	}
	timestamp := sourceTimestamp(object, "timestamp", "created_at", "createdAt", "time")
	model := sourceString(object, "model", "modelId", "model_id")
	if messageObject != nil && model == "" {
		model = sourceString(messageObject, "model", "modelId", "model_id")
	}

	message, err := claudeMessage(eventType, object, messageObject, turnID)
	if err != nil {
		return replay.Record{}, err
	}
	record := newImportedRecord("claude", artifact, sessionID, turnID, line.Line-1, timestamp, model, message)
	if record.RecordID == "" {
		return replay.Record{}, corpusError(CodeInputJSON, "Claude record ID is empty", nil)
	}
	return record, nil
}

func claudeAgentIDFromPath(relative string) string {
	parts := sourcePathParts(relative)
	var filename string
	switch {
	case len(parts) == 5 && parts[3] == "subagents" && claudeAgentFilePattern.MatchString(parts[4]):
		filename = parts[4]
	case len(parts) == 7 && parts[3] == "subagents" && parts[4] == "workflows" && claudeWorkflowIDPattern.MatchString(parts[5]) && claudeAgentFilePattern.MatchString(parts[6]):
		filename = parts[6]
	default:
		return ""
	}
	return strings.TrimSuffix(filename, ".jsonl")
}

func claudeMessage(eventType string, event map[string]json.RawMessage, messageObject map[string]json.RawMessage, fallbackID string) (replay.Message, error) {
	known := eventType == "system" || eventType == "user" || eventType == "assistant" || eventType == "tool" || eventType == "message"
	if !known || messageObject == nil {
		partType := eventType
		if partType == "" {
			partType = "claude_unknown"
		}
		return replay.Message{
			Role:  replay.RoleSystem,
			ID:    fallbackID,
			Parts: []replay.Part{{Type: partType, Raw: rawObjectBytes(event)}},
		}, nil
	}

	role, ok := importedRole(sourceString(messageObject, "role"))
	if !ok {
		role, ok = importedRole(eventType)
	}
	if !ok {
		return replay.Message{}, corpusError(CodeInputJSON, "Claude message has an unsupported role", nil)
	}
	content, exists := messageObject["content"]
	if !exists {
		wrapped := map[string]json.RawMessage{
			"type":    json.RawMessage(`"claude_message"`),
			"message": rawObjectBytes(messageObject),
		}
		return replay.Message{Role: role, ID: fallbackID, Parts: []replay.Part{{Type: "claude_message", Raw: rawObjectBytes(wrapped)}}}, nil
	}

	parts, toolCallID, toolName, hasToolResult, err := claudeContentParts(content)
	if err != nil {
		return replay.Message{}, err
	}
	if hasToolResult && role == replay.RoleUser {
		role = replay.RoleTool
	}
	return replay.Message{Role: role, ID: fallbackID, Name: toolName, ToolCallID: toolCallID, Parts: parts}, nil
}

func claudeContentParts(raw json.RawMessage) ([]replay.Part, string, string, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return nil, "", "", false, corpusError(CodeInputJSON, "decode Claude string content", err)
		}
		return []replay.Part{{Type: "text", Text: text, Raw: json.RawMessage(`{"type":"text","text":""}`)}}, "", "", false, nil
	}
	if !strings.HasPrefix(trimmed, "[") {
		return []replay.Part{{Type: "claude_content", Raw: rawObjectBytes(map[string]json.RawMessage{
			"type":    json.RawMessage(`"claude_content"`),
			"content": append(json.RawMessage(nil), raw...),
		})}}, "", "", false, nil
	}
	var blocks []json.RawMessage
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, "", "", false, corpusError(CodeInputJSON, "decode Claude content blocks", err)
	}
	parts := make([]replay.Part, 0, len(blocks))
	var callID, toolName string
	hasToolResult := false
	for _, block := range blocks {
		object, err := decodeSourceObject(block)
		if err != nil {
			return nil, "", "", false, corpusError(CodeInputJSON, "decode Claude content block", err)
		}
		blockType := sourceString(object, "type")
		if blockType == "" {
			blockType = "claude_block"
			object["type"] = json.RawMessage(`"claude_block"`)
			block = rawObjectBytes(object)
		}
		switch blockType {
		case "text":
			text, ok := sourceStringWithPresence(object, "text")
			if !ok {
				return nil, "", "", false, corpusError(CodeInputJSON, "Claude text block has no text", nil)
			}
			parts = append(parts, replay.Part{Type: "text", Text: text, Raw: append(json.RawMessage(nil), block...)})
		case "tool_use", "function_call":
			parts = append(parts, replay.Part{Type: blockType, Raw: append(json.RawMessage(nil), block...)})
			if callID == "" {
				callID = sourceString(object, "id", "call_id", "callId")
			}
			if toolName == "" {
				toolName = sourceString(object, "name", "tool")
			}
		default:
			if blockType == "tool_result" || blockType == "function_result" {
				hasToolResult = true
				if callID == "" {
					callID = sourceString(object, "tool_use_id", "toolUseId", "call_id", "callId")
				}
			}
			parts = append(parts, replay.Part{Type: blockType, Raw: append(json.RawMessage(nil), block...)})
		}
	}
	if len(parts) == 0 {
		return nil, "", "", false, corpusError(CodeInputJSON, "Claude content has no blocks", nil)
	}
	return parts, callID, toolName, hasToolResult, nil
}

func sourceStringWithPresence(object map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := object[key]
	if !ok {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}

func rawObjectBytes(object map[string]json.RawMessage) json.RawMessage {
	encoded, err := json.Marshal(object)
	if err != nil {
		return json.RawMessage(`{"type":"opaque"}`)
	}
	return encoded
}

func sourcePathParts(relative string) []string {
	clean := filepath.ToSlash(filepath.Clean(relative))
	return strings.Split(clean, "/")
}

func safeTranscriptComponent(component string) bool {
	return safeClaudeDirectoryComponent(component) && !excludedSourceComponent(component)
}

func safeClaudeDirectoryComponent(component string) bool {
	if component == "" || component == "." || component == ".." {
		return false
	}
	for _, character := range component {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' || character == '.' {
			continue
		}
		return false
	}
	return true
}

var _ Source = (*ClaudeSource)(nil)
