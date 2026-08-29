//go:build linux

package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/replay"
)

func TestRedactRecordRemovesCredentialFieldsAndPatternsButPreservesStructure(t *testing.T) {
	input := []byte(`{
		"schema":"tokenmill.session/v1",
		"record_id":"fixture:session-1:turn-1",
		"source":{
			"system":"fixture",
			"content_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			"authorization":"Bearer source-secret",
			"source_vendor":{"region":"test"}
		},
		"session_id":"fixture:session-1",
		"sequence":1,
		"messages":[{
			"role":"user",
			"parts":[{
				"type":"text",
				"text":"Bearer prompt-secret data:text/plain;base64,c2VjcmV0 -----BEGIN PRIVATE KEY-----\\nprivate-secret\\n-----END PRIVATE KEY-----",
				"api_key":"field-secret",
				"message_vendor":{"safe":true}
			}],
			"headers":{"Authorization":"Bearer nested-secret","x-safe":"keep"}
		}],
		"record_vendor":{"safe":"keep"}
	}`)
	var record replay.Record
	if err := json.Unmarshal(input, &record); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	redacted, err := RedactRecord(record, Options{})
	if err != nil {
		t.Fatalf("RedactRecord: %v", err)
	}
	if err := redacted.Validate(); err != nil {
		t.Fatalf("redacted Validate: %v", err)
	}

	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("Marshal redacted: %v", err)
	}
	output := string(encoded)
	for _, secret := range []string{
		"source-secret",
		"prompt-secret",
		"field-secret",
		"nested-secret",
		"private-secret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("redacted output contains secret %q: %s", secret, output)
		}
	}
	for _, structural := range []string{
		`"record_id":"fixture:session-1:turn-1"`,
		`"session_id":"fixture:session-1"`,
		`"content_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`,
		`"source_vendor":{"region":"test"}`,
		`"message_vendor":{"safe":true}`,
		`"record_vendor":{"safe":"keep"}`,
		`"x-safe":"keep"`,
	} {
		if !strings.Contains(output, structural) {
			t.Fatalf("redaction lost structural metadata %s: %s", structural, output)
		}
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("pattern redaction marker missing: %s", output)
	}
}

func TestRedactRecordRejectsPrivateModeWithoutExplicitAllowAndLocalOutput(t *testing.T) {
	record := corpusRecord(0)
	if _, err := RedactRecord(record, Options{Privacy: PrivacyPrivate}); err == nil {
		t.Fatal("RedactRecord accepted private mode without AllowPrivate")
	} else if got := CodeOf(err); got != CodeSecretInCorpus {
		t.Fatalf("error code = %q, want %q", got, CodeSecretInCorpus)
	}

	if _, err := RedactRecord(record, Options{
		Privacy:      PrivacyPrivate,
		AllowPrivate: true,
	}); err == nil {
		t.Fatal("RedactRecord accepted private mode without a local output path")
	} else if got := CodeOf(err); got != CodeSecretInCorpus {
		t.Fatalf("missing-output error code = %q, want %q", got, CodeSecretInCorpus)
	}
}

func TestRedactRecordAllowsExplicitPrivateModeForLocalOutput(t *testing.T) {
	record := corpusRecord(0)
	redacted, err := RedactRecord(record, Options{
		OutputPath:   t.TempDir() + "/private.jsonl",
		Privacy:      PrivacyPrivate,
		AllowPrivate: true,
	})
	if err != nil {
		t.Fatalf("RedactRecord: %v", err)
	}
	if err := redacted.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestRedactRecordRemovesCamelCaseCredentialFields(t *testing.T) {
	input := []byte(`{
		"schema":"tokenmill.session/v1",
		"record_id":"fixture:session-1:turn-1",
		"source":{"system":"fixture"},
		"session_id":"fixture:session-1",
		"sequence":1,
		"messages":[{"role":"user","parts":[{"type":"text","text":"safe"}]}],
		"record_vendor":{
			"privateKey":"camel-private-secret",
			"accessToken":"camel-access-secret",
			"clientSecret":"camel-client-secret",
			"safe":"keep"
		}
	}`)
	var record replay.Record
	if err := json.Unmarshal(input, &record); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	redacted, err := RedactRecord(record, Options{})
	if err != nil {
		t.Fatalf("RedactRecord: %v", err)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	output := string(encoded)
	for _, secret := range []string{"camel-private-secret", "camel-access-secret", "camel-client-secret"} {
		if strings.Contains(output, secret) {
			t.Fatalf("camelCase credential value remained in output: %q in %s", secret, output)
		}
	}
	if !strings.Contains(output, `"safe":"keep"`) {
		t.Fatalf("safe opaque metadata was removed: %s", output)
	}
}

func TestRedactRecordClassifiesCredentialNamesBeforeHashPreservation(t *testing.T) {
	input := []byte(`{
		"schema":"tokenmill.session/v1",
		"record_id":"fixture:session-1:turn-1",
		"source":{"system":"fixture","content_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"},
		"session_id":"fixture:session-1",
		"sequence":1,
		"messages":[{"role":"user","parts":[{"type":"text","text":"safe"}]}],
		"record_vendor":{
			"access_token_hash":"access-hash-secret",
			"api_key_hash":"api-hash-secret",
			"password_hash":"password-hash-secret",
			"openai_api_key":"openai-secret",
			"x-api-key":"header-secret",
			"aws_secret_access_key":"aws-secret",
			"ssh_private_key":"ssh-secret",
			"oauth_token":"oauth-secret",
			"api_token":"api-token-secret",
			"access_key_id":"access-key-secret",
			"auth":"auth-secret",
			"api/key":"slash-secret",
			"openai_key":"provider-key-secret",
			"openai.key":"nested-provider-secret",
			"api":{"key":"nested-api-secret"},
			"openai":{"key":"nested-openai-secret"},
			"metadata":{"key":"safe-key-metadata"},
			"vendor_hash":"arbitrary-hash-secret",
			"content-sha256":"arbitrary-structural-secret",
			"content_sha256":"another-structural-secret",
			"safe":"keep"
		}
	}`)
	var record replay.Record
	if err := json.Unmarshal(input, &record); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	redacted, err := RedactRecord(record, Options{})
	if err != nil {
		t.Fatalf("RedactRecord: %v", err)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	output := string(encoded)
	for _, secret := range []string{
		"access-hash-secret",
		"api-hash-secret",
		"password-hash-secret",
		"openai-secret",
		"header-secret",
		"aws-secret",
		"ssh-secret",
		"oauth-secret",
		"api-token-secret",
		"access-key-secret",
		"auth-secret",
		"slash-secret",
		"provider-key-secret",
		"nested-provider-secret",
		"nested-api-secret",
		"nested-openai-secret",
		"arbitrary-hash-secret",
		"arbitrary-structural-secret",
		"another-structural-secret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("credential bypass value remained in output: %q in %s", secret, output)
		}
	}
	if !strings.Contains(output, `"content_sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`) {
		t.Fatalf("explicit content hash was not preserved: %s", output)
	}
	if !strings.Contains(output, `"safe":"keep"`) {
		t.Fatalf("safe metadata was removed: %s", output)
	}
	if !strings.Contains(output, `"safe-key-metadata"`) {
		t.Fatalf("arbitrary opaque metadata was removed: %s", output)
	}
}

func TestRedactRecordRemovesPunctuationAndFormatSeparatedCredentialFields(t *testing.T) {
	input := []byte(`{
		"schema":"tokenmill.session/v1",
		"record_id":"fixture:session-1:turn-1",
		"source":{"system":"fixture"},
		"session_id":"fixture:session-1",
		"sequence":1,
		"messages":[{"role":"user","parts":[{"type":"text","text":"safe"}]}],
		"record_vendor":{
			"api:key":"colon-secret",
			"api\u200bkey":"format-secret",
			"x-api:key":"header-colon-secret",
			"lowercase_pem":"-----begin private key-----\nlowercase-secret\n-----end private key-----",
			"key":"safe-key-metadata",
			"safe":"keep"
		}
	}`)
	var record replay.Record
	if err := json.Unmarshal(input, &record); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	redacted, err := RedactRecord(record, Options{})
	if err != nil {
		t.Fatalf("RedactRecord: %v", err)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	output := string(encoded)
	for _, secret := range []string{
		"colon-secret",
		"format-secret",
		"header-colon-secret",
		"lowercase-secret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("separator-separated credential value remained in output: %q in %s", secret, output)
		}
	}
	for _, preserved := range []string{`"safe":"keep"`, `"safe-key-metadata"`} {
		if !strings.Contains(output, preserved) {
			t.Fatalf("safe opaque metadata was removed: %s", output)
		}
	}
}

func TestRedactRecordDropsInvalidStructuralHashValues(t *testing.T) {
	input := []byte(`{
		"schema":"tokenmill.session/v1",
		"record_id":"fixture:session-1:turn-1",
		"source":{"system":"fixture","content_sha256":"opaque-source-secret"},
		"session_id":"fixture:session-1",
		"sequence":1,
		"messages":[{"role":"user","parts":[{"type":"text","text":"safe"}]}],
		"record_vendor":{
			"content_sha256":"opaque-vendor-secret",
			"content-sha256":"opaque-variant-secret",
			"safe":"keep"
		}
	}`)
	var record replay.Record
	if err := json.Unmarshal(input, &record); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	redacted, err := RedactRecord(record, Options{})
	if err != nil {
		t.Fatalf("RedactRecord: %v", err)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	output := string(encoded)
	for _, secret := range []string{
		"opaque-source-secret",
		"opaque-vendor-secret",
		"opaque-variant-secret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("invalid structural hash value remained in output: %q in %s", secret, output)
		}
	}
	if !strings.Contains(output, `"safe":"keep"`) {
		t.Fatalf("safe metadata was removed: %s", output)
	}
}

func TestRedactRecordRemovesProviderKeyCookieAndHeaderCredentialFields(t *testing.T) {
	input := []byte(`{
		"schema":"tokenmill.session/v1",
		"record_id":"fixture:session-1:turn-provider-fields",
		"source":{"system":"fixture"},
		"session_id":"fixture:session-1",
		"sequence":1,
		"messages":[{"role":"user","parts":[{"type":"text","text":"safe"}]}],
		"record_vendor":{
			"aws_key":"aws-secret",
			"awsKey":"aws-camel-secret",
			"gcp_key":"gcp-secret",
			"provider_key":"provider-secret",
			"providerKey":"provider-camel-secret",
			"cookie_value":"cookie-secret",
			"cookieHeader":"cookie-header-secret",
			"header_key":"header-secret",
			"openai_key_id":"openai-secret",
			"encryption_key":"encryption-secret",
			"signing_key":"signing-secret",
			"master_key":"master-secret",
			"session_key":"session-secret",
			"provider":{"value":"provider-value-secret"},
			"headers":{"value":"header-value-secret"},
			"metadata":{"key":"safe-key-metadata"},
			"safe":"keep"
		}
	}`)
	var record replay.Record
	if err := json.Unmarshal(input, &record); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	redacted, err := RedactRecord(record, Options{})
	if err != nil {
		t.Fatalf("RedactRecord: %v", err)
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	output := string(encoded)
	for _, secret := range []string{
		"aws-secret", "aws-camel-secret", "gcp-secret", "provider-secret",
		"provider-camel-secret", "cookie-secret", "cookie-header-secret",
		"header-secret", "openai-secret", "encryption-secret", "signing-secret",
		"master-secret", "session-secret", "provider-value-secret",
		"header-value-secret",
	} {
		if strings.Contains(output, secret) {
			t.Fatalf("credential value remained in output: %q in %s", secret, output)
		}
	}
	for _, preserved := range []string{`"safe":"keep"`, `"safe-key-metadata"`} {
		if !strings.Contains(output, preserved) {
			t.Fatalf("safe opaque metadata was removed: %s", output)
		}
	}
}

func TestWriterWriteRemovesKeyMaterialAndProviderConfigCredentials(t *testing.T) {
	record := corpusRecord(0)
	record.Messages = []replay.Message{{
		Role: replay.RoleTool,
		Parts: []replay.Part{{
			Type: "tool_result",
			Raw:  []byte(`{"type":"tool_result","record_vendor":{"key_material":"key-material-secret","keyvault":"key-vault-secret","keybackup":"key-backup-secret","provider_config":{"key":"provider-config-secret"},"metadata":{"key":"safe-key-metadata"}}}`),
		}},
	}}
	output := filepath.Join(t.TempDir(), "redacted.jsonl")
	writer, err := NewWriter(Options{OutputPath: output})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Write(record); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	encoded, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, secret := range []string{"key-material-secret", "key-vault-secret", "key-backup-secret", "provider-config-secret"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("credential value remained in Writer output: %q in %s", secret, encoded)
		}
	}
	if !strings.Contains(string(encoded), "safe-key-metadata") {
		t.Fatalf("safe metadata was removed: %s", encoded)
	}
}

func TestRedactRecordRemovesCredentialsInsideSerializedJSONStrings(t *testing.T) {
	record := recordWithSerializedCredential(t)
	redacted, err := RedactRecord(record, Options{})
	if err != nil {
		t.Fatalf("RedactRecord: %v", err)
	}
	output := mustJSON(t, redacted)
	if strings.Contains(output, "nested-api-secret") || strings.Contains(output, `api_key`) {
		t.Fatalf("serialized credential remained in output: %s", output)
	}
	if !strings.Contains(output, "nested-safe") {
		t.Fatalf("safe serialized metadata was removed: %s", output)
	}
}

func TestRedactImportedRecordRemovesCredentialsInsideSerializedJSONStrings(t *testing.T) {
	record := recordWithSerializedCredential(t)
	redacted, err := redactImportedRecord(record)
	if err != nil {
		t.Fatalf("redactImportedRecord: %v", err)
	}
	output := mustJSON(t, redacted)
	if strings.Contains(output, "nested-api-secret") || strings.Contains(output, `api_key`) {
		t.Fatalf("serialized credential remained in imported output: %s", output)
	}
	if !strings.Contains(output, "nested-safe") {
		t.Fatalf("safe serialized metadata was removed from imported output: %s", output)
	}
}

func TestRedactRecordRemovesCredentialsHiddenByDuplicateSerializedJSONKeys(t *testing.T) {
	record := recordWithDuplicateSerializedCredential(t)
	redacted, err := RedactRecord(record, Options{})
	if err != nil {
		t.Fatalf("RedactRecord: %v", err)
	}
	output := mustJSON(t, redacted)
	if strings.Contains(output, "duplicate-api-secret") {
		t.Fatalf("duplicate-key serialized credential remained in output: %s", output)
	}
}

func TestRedactImportedRecordRemovesCredentialsHiddenByDuplicateSerializedJSONKeys(t *testing.T) {
	record := recordWithDuplicateSerializedCredential(t)
	redacted, err := redactImportedRecord(record)
	if err != nil {
		t.Fatalf("redactImportedRecord: %v", err)
	}
	output := mustJSON(t, redacted)
	if strings.Contains(output, "duplicate-api-secret") {
		t.Fatalf("duplicate-key serialized credential remained in imported output: %s", output)
	}
}

func recordWithDuplicateSerializedCredential(t *testing.T) replay.Record {
	t.Helper()
	record := corpusRecord(0)
	record.Messages = []replay.Message{{
		Role: replay.RoleTool,
		Parts: []replay.Part{{
			Type: "tool_result",
			Raw:  []byte(`{"type":"tool_result","output":"{\"safe\":\"{\\\"api_key\\\":\\\"duplicate-api-secret\\\"}\",\"safe\":\"not-json\"}"}`),
		}},
	}}
	return record
}

func recordWithSerializedCredential(t *testing.T) replay.Record {
	t.Helper()
	record := corpusRecord(0)
	record.Messages = []replay.Message{{
		Role: replay.RoleTool,
		Parts: []replay.Part{{
			Type: "tool_result",
			Raw:  []byte(`{"type":"tool_result","output":"{\"nested\":\"{\\\"api_key\\\":\\\"nested-api-secret\\\",\\\"safe\\\":\\\"nested-safe\\\"}\"}"}`),
		}},
	}}
	return record
}
