//go:build linux && (amd64 || arm64)

package corpus

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/tokenmill/tokenmill/internal/replay"
)

var (
	bearerValuePattern = regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/=-]+`)
	privateKeyPattern  = regexp.MustCompile(`(?is)-----BEGIN (?:[A-Z0-9]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z0-9]+ )?PRIVATE KEY-----`)
	dataURLPattern     = regexp.MustCompile(`(?i)\bdata:[^,\s]+,[^\s"'<>]+`)
)

const maxSerializedJSONDepth = 16

// RedactRecord returns a validated deep JSON round trip with credential fields
// removed and credential-like values replaced. Raw opaque parts are traversed
// semantically, so unknown fields are retained unless they are credential
// bearing.
func RedactRecord(record replay.Record, options Options) (replay.Record, error) {
	normalized, err := options.normalized()
	if err != nil {
		return replay.Record{}, err
	}
	if err := record.Validate(); err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "record validation failed", err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "marshal record for redaction", err)
	}
	redactedJSON, err := redactJSONValue(encoded, nil)
	if err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "redact record JSON", err)
	}
	var redacted replay.Record
	if err := json.Unmarshal(redactedJSON, &redacted); err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "decode redacted record", err)
	}
	redacted.Redaction = "field-aware-v1"
	if err := redacted.Validate(); err != nil {
		return replay.Record{}, corpusError(CodeInputJSON, "redacted record validation failed", err)
	}
	if containsCredentialPattern(redacted) {
		return replay.Record{}, corpusError(CodeSecretInCorpus, "credential-like value remained after redaction", nil)
	}
	_ = normalized
	return redacted, nil
}

func redactJSONValue(raw []byte, path []string) ([]byte, error) {
	return redactJSONValueAtDepth(raw, path, 0)
}

func redactJSONValueAtDepth(raw []byte, path []string, serializedDepth int) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty JSON value")
	}
	switch trimmed[0] {
	case '{':
		object, err := decodeJSONObjectFields(trimmed)
		if err != nil {
			return nil, err
		}
		redacted := make([]jsonObjectField, 0, len(object))
		for _, field := range object {
			childPath := appendJSONPath(path, field.key)
			if isStructuralHashPath(childPath) {
				if validStructuralHash(field.value) {
					redacted = append(redacted, jsonObjectField{
						key:   field.key,
						value: append(json.RawMessage(nil), field.value...),
					})
				}
				continue
			}
			if credentialFieldName(path, field.key) {
				continue
			}
			value, err := redactJSONValueAtDepth(field.value, childPath, serializedDepth)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", field.key, err)
			}
			redacted = append(redacted, jsonObjectField{key: field.key, value: value})
		}
		return marshalJSONObjectFields(redacted)
	case '[':
		var array []json.RawMessage
		if err := json.Unmarshal(trimmed, &array); err != nil {
			return nil, err
		}
		for i, value := range array {
			redacted, err := redactJSONValueAtDepth(value, path, serializedDepth)
			if err != nil {
				return nil, fmt.Errorf("array item %d: %w", i, err)
			}
			array[i] = redacted
		}
		return json.Marshal(array)
	case '"':
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return nil, err
		}
		if !isStructuralHashPath(path) {
			value = redactSecretPatterns(value)
			var err error
			value, err = redactSerializedJSON(value, path, serializedDepth, redactJSONValueAtDepth, embeddedJSONContainsCredentialField)
			if err != nil {
				return nil, err
			}
		}
		return json.Marshal(value)
	default:
		if !json.Valid(trimmed) {
			return nil, fmt.Errorf("invalid JSON value")
		}
		return append([]byte(nil), trimmed...), nil
	}
}

type jsonValueRedactor func([]byte, []string, int) ([]byte, error)
type jsonCredentialDetector func([]byte, []string, int) bool

type jsonObjectField struct {
	key   string
	value json.RawMessage
}

func decodeJSONObjectFields(data []byte) ([]jsonObjectField, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, fmt.Errorf("JSON value must be an object")
	}
	fields := make([]jsonObjectField, 0)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("JSON object key is not a string")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		fields = append(fields, jsonObjectField{key: key, value: value})
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if delimiter, ok := closing.(json.Delim); !ok || delimiter != '}' {
		return nil, fmt.Errorf("JSON object is not closed")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("JSON document contains trailing data")
		}
		return nil, err
	}
	return fields, nil
}

func marshalJSONObjectFields(fields []jsonObjectField) ([]byte, error) {
	sort.SliceStable(fields, func(left, right int) bool {
		return fields[left].key < fields[right].key
	})
	var output bytes.Buffer
	output.WriteByte('{')
	for index, field := range fields {
		if index > 0 {
			output.WriteByte(',')
		}
		key, err := json.Marshal(field.key)
		if err != nil {
			return nil, err
		}
		output.Write(key)
		output.WriteByte(':')
		value := bytes.TrimSpace(field.value)
		if len(value) == 0 || !json.Valid(value) {
			return nil, fmt.Errorf("JSON object field %q has an invalid value", field.key)
		}
		output.Write(value)
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}

func redactSerializedJSON(value string, path []string, serializedDepth int, redactor jsonValueRedactor, containsCredential jsonCredentialDetector) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') || !json.Valid([]byte(trimmed)) {
		return value, nil
	}
	if serializedDepth >= maxSerializedJSONDepth {
		return "", fmt.Errorf("serialized JSON nesting exceeds maximum depth %d", maxSerializedJSONDepth)
	}
	if !containsCredential([]byte(trimmed), path, serializedDepth+1) {
		return value, nil
	}
	redacted, err := redactor([]byte(trimmed), path, serializedDepth+1)
	if err != nil {
		return "", err
	}
	return string(redacted), nil
}

func embeddedJSONContainsCredentialField(raw []byte, path []string, serializedDepth int) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	switch trimmed[0] {
	case '{':
		object, err := decodeJSONObjectFields(trimmed)
		if err != nil {
			return true
		}
		for _, field := range object {
			childPath := appendJSONPath(path, field.key)
			if isStructuralHashPath(childPath) {
				continue
			}
			if credentialFieldName(path, field.key) || embeddedJSONContainsCredentialField(field.value, childPath, serializedDepth) {
				return true
			}
		}
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return false
		}
		for _, value := range values {
			if embeddedJSONContainsCredentialField(value, path, serializedDepth) {
				return true
			}
		}
	case '"':
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return false
		}
		nested := strings.TrimSpace(value)
		if len(nested) == 0 || (nested[0] != '{' && nested[0] != '[') || !json.Valid([]byte(nested)) {
			return false
		}
		if serializedDepth >= maxSerializedJSONDepth {
			return true
		}
		return embeddedJSONContainsCredentialField([]byte(nested), path, serializedDepth+1)
	}
	return false
}

func credentialFieldName(path []string, field string) bool {
	compact := compactFieldName(field)
	switch compact {
	case "auth", "authorization", "proxyauthorization", "wwwauthenticate",
		"cookie", "setcookie", "cookies",
		"apikey", "accesstoken", "refreshtoken", "idtoken",
		"authtoken", "bearertoken", "oauthtoken", "apitoken",
		"accesskeyid", "token", "password", "passwd",
		"passphrase", "secret", "clientsecret", "privatekey",
		"credential", "credentials", "sshkey", "sshprivatekey", "secretkey":
		return true
	}
	if compact == "key" || compact == "keys" {
		return credentialContext(path)
	}
	if compact == "value" && credentialContext(path) {
		return true
	}
	if providerCredentialField(compact) {
		return true
	}
	if strings.Contains(compact, "cookie") || (strings.HasPrefix(compact, "header") && compact != "header" && compact != "headers") {
		return true
	}
	if strings.Contains(compact, "key") && credentialKeyMaterialField(compact) {
		return true
	}
	return strings.Contains(compact, "authorization") ||
		strings.Contains(compact, "auth") ||
		strings.Contains(compact, "apikey") ||
		strings.Contains(compact, "accesstoken") ||
		strings.Contains(compact, "refreshtoken") ||
		strings.Contains(compact, "idtoken") ||
		strings.Contains(compact, "oauth") ||
		strings.Contains(compact, "token") ||
		strings.Contains(compact, "accesskey") ||
		strings.Contains(compact, "bearer") ||
		strings.Contains(compact, "privatekey") ||
		strings.Contains(compact, "secret") ||
		strings.Contains(compact, "credential") ||
		strings.Contains(compact, "password") ||
		strings.Contains(compact, "passwd") ||
		strings.Contains(compact, "hash") ||
		strings.Contains(compact, "sha256")
}

func credentialKeyMaterialField(compact string) bool {
	if strings.HasPrefix(compact, "keymaterial") || strings.HasPrefix(compact, "keyvault") || strings.HasPrefix(compact, "keybackup") {
		return true
	}
	if strings.HasSuffix(compact, "key") || strings.HasSuffix(compact, "keys") {
		return true
	}
	for _, prefix := range []string{
		"access", "api", "aws", "azure", "encryption", "gcp", "google",
		"master", "openai", "private", "provider", "secret", "session", "signing", "ssh",
	} {
		if strings.HasPrefix(compact, prefix) {
			return true
		}
	}
	return false
}

func compactFieldName(field string) string {
	var compact strings.Builder
	compact.Grow(len(field))
	for _, character := range strings.ToLower(strings.TrimSpace(field)) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			compact.WriteRune(character)
		}
	}
	return compact.String()
}

func credentialContext(path []string) bool {
	for _, component := range path {
		compact := compactFieldName(component)
		switch compact {
		case "api", "apis", "auth", "authentication", "authorization",
			"credential", "credentials", "header", "headers", "oauth",
			"provider", "providers", "secret", "secrets", "token", "tokens":
			return true
		}
		if strings.Contains(compact, "provider") {
			return true
		}
		if isCredentialProvider(compact) {
			return true
		}
	}
	return false
}

func providerCredentialField(field string) bool {
	for _, provider := range credentialProviders {
		if !strings.HasPrefix(field, provider) {
			continue
		}
		suffix := strings.TrimPrefix(field, provider)
		switch suffix {
		case "key", "keys", "apikey", "apikeys", "token", "tokens", "secret", "secrets",
			"credential", "credentials", "password", "passwd", "auth", "authorization",
			"accesskey", "accesskeys", "privatekey", "privatekeys":
			return true
		}
	}
	return false
}

func isCredentialProvider(field string) bool {
	for _, provider := range credentialProviders {
		if field == provider {
			return true
		}
	}
	return false
}

var credentialProviders = []string{
	"amazon", "anthropic", "aws", "azure", "bedrock", "claude", "cline", "codex", "cohere",
	"copilot", "cursor", "deepseek", "discord", "factory", "fireworks", "gemini",
	"gcp", "github", "gitlab", "google", "groq", "hermes", "huggingface", "kimi", "mistral",
	"ollama", "openai", "openclaw", "openrouter", "opencode", "perplexity", "pi",
	"slack", "stripe", "together", "twilio", "vertex", "windsurf", "xai",
}

func appendJSONPath(path []string, field string) []string {
	result := make([]string, len(path)+1)
	copy(result, path)
	result[len(path)] = field
	return result
}

func isStructuralHashPath(path []string) bool {
	return len(path) == 2 && path[0] == "source" && path[1] == "content_sha256"
}

func validStructuralHash(raw []byte) bool {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func redactSecretPatterns(value string) string {
	value = privateKeyPattern.ReplaceAllString(value, "[REDACTED]")
	value = bearerValuePattern.ReplaceAllString(value, "[REDACTED]")
	return dataURLPattern.ReplaceAllString(value, "[REDACTED]")
}

func containsCredentialPattern(record replay.Record) bool {
	encoded, err := json.Marshal(record)
	if err != nil {
		return true
	}
	return privateKeyPattern.Match(encoded) || bearerValuePattern.Match(encoded) || dataURLPattern.Match(encoded)
}
