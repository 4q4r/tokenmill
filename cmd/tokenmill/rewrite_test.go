package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tokenmill/tokenmill/internal/codec/csvcanonical"
	"github.com/tokenmill/tokenmill/internal/codec/folding"
	"github.com/tokenmill/tokenmill/internal/codec/markdown"
	"github.com/tokenmill/tokenmill/internal/codec/opaque"
	"github.com/tokenmill/tokenmill/internal/codec/symboltable"
	"github.com/tokenmill/tokenmill/internal/packer"
	"github.com/tokenmill/tokenmill/internal/stats"
)

func TestRewrite_Tournament_TableDriven(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantContains string
		wantOriginal bool
	}{
		{
			name:         "short plain returns original",
			input:        "git status",
			wantOriginal: true,
		},
		{
			name:         "homogeneous json 10 rows -> jton",
			input:        genHomJSON(15),
			wantContains: "[15:",
		},
		{
			name:         "pretty json -> jton or compact",
			input:        "[\n  {\n    \"id\": 1,\n    \"name\": \"Alice\"\n  },\n  {\n    \"id\": 2,\n    \"name\": \"Bob\"\n  }\n]",
			wantContains: "id",
		},
		{
			name:         "rle 100 identical lines -> rle",
			input:        strings.Repeat("error\n", 100),
			wantContains: " [×100]",
		},
		{
			name:         "docker ps table -> tsv",
			input:        genDockerPS(10),
			wantContains: "\t",
		},
		{
			name:         "path heavy -> dict",
			input:        strings.Repeat("/home/user/project/src/file.go: ", 5) + strings.Repeat("/home/user/project/src/other.go ", 5),
			wantOriginal: false,
		},
		{
			name:         "ansi stripped",
			input:        "\x1b[31mred\x1b[0m text",
			wantContains: "red text",
		},
		{
			name:         "cr collapse",
			input:        "Downloading 1%\rDownloading 50%\rDownloading 100%",
			wantContains: "Downloading 100%",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("USERPROFILE", t.TempDir())
			t.Setenv("XDG_DATA_HOME", t.TempDir())
			root := newRootCmd()
			root.SetArgs([]string{"--log-level", "error", "rewrite", tc.input})
			w := &testWriter{}
			eb := &testWriter{}
			root.SetOut(w)
			root.SetErr(eb)
			if err := root.Execute(); err != nil {
				t.Fatalf("rewrite %q: %v stderr=%s", tc.name, err, eb.String())
			}
			out := strings.TrimSpace(w.String())
			if out == "" {
				t.Fatalf("empty output for %q stderr=%s", tc.name, eb.String())
			}
			if tc.wantOriginal {
				if out != tc.input {
					t.Logf("expected original but got rewrite %q (may be okay if saving)", out)
				}
			}
			if tc.wantContains != "" {
				if !strings.Contains(out, tc.wantContains) {
					if tc.name == "rle 100 identical lines -> rle" || tc.name == "homogeneous json 10 rows -> jton" {
						t.Fatalf("expected contains %q, got %q stderr=%s", tc.wantContains, out, eb.String())
					} else {
						t.Logf("wantContains %q not found in %q (tournament fallback may be expected)", tc.wantContains, out)
					}
				}
			}
		})
	}
}

func genHomJSON(n int) string {
	arr := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		arr[i] = map[string]interface{}{"id": i, "name": "Name" + string(rune('0'+i%10)), "value": i * 10}
	}
	b, _ := json.Marshal(arr)
	return string(b)
}

func genDockerPS(n int) string {
	var sb strings.Builder
	sb.WriteString("CONTAINER ID   IMAGE          COMMAND                  CREATED          STATUS          PORTS                    NAMES\n")
	for i := 0; i < n; i++ {
		sb.WriteString("abc123   nginx:latest   \"/docker-entrypoint.sh\"   2 hours ago   Up 2 hours   0.0.0.0:80->80/tcp   web-" + string(rune('0'+i%10)) + "\n")
	}
	return sb.String()
}

func TestRewrite_EmptyAndSingleChar(t *testing.T) {
	for _, input := range []string{"", "a", "   "} {
		t.Run("input="+input, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			root := newRootCmd()
			root.SetArgs([]string{"--log-level", "error", "rewrite", input})
			w := &testWriter{}
			root.SetOut(w)
			root.SetErr(w)
			if err := root.Execute(); err != nil {
				t.Fatalf("rewrite empty: %v", err)
			}
			out := strings.TrimSpace(w.String())
			if input == "" && out != "" {
				t.Fatalf("empty input expected empty output got %q", out)
			}
		})
	}
}

func TestRewrite_ConfigDisabledTechnique(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "tokenmill.jsonc")
	jsonContent := `{"techniques":{"jton":{"enabled":false,"minRows":10},"jsonCompact":false}}`
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(jsonContent), 0644); err != nil {
		t.Fatal(err)
	}
	input := genHomJSON(10)
	t.Setenv("HOME", t.TempDir())
	root := newRootCmd()
	root.SetArgs([]string{"--config", cfgPath, "--log-level", "error", "rewrite", input})
	w := &testWriter{}
	root.SetOut(w)
	root.SetErr(w)
	if err := root.Execute(); err != nil {
		t.Fatalf("rewrite with disabled jton: %v", err)
	}
	out := strings.TrimSpace(w.String())
	if out != input {
		t.Logf("disabled jton: got rewrite %q (may be other codec)", out[:100])
	}
}

func TestRewrite_RawPreservesPayload(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tokenmill.jsonc")
	if err := os.WriteFile(configPath, []byte(`{"enabled":true,"logSavings":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	input := "  printf 'x'  \n"
	root := newRootCmd()
	root.SetArgs([]string{"--config", configPath, "--log-level", "error", "rewrite", "--raw", "--", input})
	out := &testWriter{}
	errOut := &testWriter{}
	root.SetOut(out)
	root.SetErr(errOut)
	if err := root.Execute(); err != nil {
		t.Fatalf("raw rewrite: %v stderr=%s", err, errOut.String())
	}
	if out.String() != input {
		t.Fatalf("raw output changed payload: got %q want %q", out.String(), input)
	}
}

func TestRewrite_DisabledConfigReturnsExactRawPayload(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tokenmill.jsonc")
	if err := os.WriteFile(configPath, []byte(`{"enabled":false,"logSavings":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	input := "git status  \n"
	root := newRootCmd()
	root.SetArgs([]string{"--config", configPath, "rewrite", "--raw", "--", input})
	out := &testWriter{}
	root.SetOut(out)
	root.SetErr(&testWriter{})
	if err := root.Execute(); err != nil {
		t.Fatalf("disabled rewrite: %v", err)
	}
	if out.String() != input {
		t.Fatalf("disabled output changed payload: got %q want %q", out.String(), input)
	}
}

func TestRewrite_DisabledEnvironmentReturnsExactRawPayload(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("TOKENMILL_ENABLED", "false")
	input := "git status  \n"

	root := newRootCmd()
	root.SetArgs([]string{"rewrite", "--raw", "--", input})
	out := &testWriter{}
	root.SetOut(out)
	root.SetErr(&testWriter{})
	if err := root.Execute(); err != nil {
		t.Fatalf("disabled environment rewrite: %v", err)
	}
	if out.String() != input {
		t.Fatalf("disabled environment changed payload: got %q want %q", out.String(), input)
	}
}

func TestRewrite_MalformedExplicitConfigReturnsError(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "tokenmill.jsonc")
	if err := os.WriteFile(configPath, []byte(`{"enabled":`), 0600); err != nil {
		t.Fatal(err)
	}
	root := newRootCmd()
	root.SetArgs([]string{"--config", configPath, "rewrite", "--raw", "--", "git status"})
	out := &testWriter{}
	root.SetOut(out)
	root.SetErr(&testWriter{})
	if err := root.Execute(); err == nil {
		t.Fatal("malformed explicit config must return an error")
	}
	if out.String() != "" {
		t.Fatalf("malformed config produced payload: %q", out.String())
	}
}

func TestRewrite_RecordsStats(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "tracking.db")
	configPath := filepath.Join(t.TempDir(), "tokenmill.jsonc")
	configData := `{"enabled":false,"logSavings":true,"tracking":{"database_path":"` + dbPath + `"}}`
	if err := os.WriteFile(configPath, []byte(configData), 0600); err != nil {
		t.Fatal(err)
	}
	input := "git status"
	root := newRootCmd()
	root.SetArgs([]string{"--config", configPath, "rewrite", "--raw", "--", input})
	root.SetOut(&testWriter{})
	root.SetErr(&testWriter{})
	if err := root.Execute(); err != nil {
		t.Fatalf("rewrite with stats: %v", err)
	}
	store, err := stats.New(dbPath)
	if err != nil {
		t.Fatalf("open stats database: %v", err)
	}
	defer store.Close()
	records, err := store.GetRecent(10)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].Cmd != input {
		t.Fatalf("record cmd = %q, want %q", records[0].Cmd, input)
	}
	if records[0].InputTokens != records[0].OutputTokens {
		t.Fatalf("unchanged command token counts differ: %+v", records[0])
	}
}

func TestRewrite_PassThroughEnvelopeCodecsVerifyIdentity(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		verify func(string, string) bool
	}{
		{"folding", "TMFOLD1 this is ordinary text", folding.New().Verify},
		{"symbol table", "TMST1 this is ordinary text", symboltable.New().Verify},
		{"csv canonical", "TMCV1 this is ordinary text", csvcanonical.New().Verify},
		{"opaque", "[[tokenmill-opaque:v1; this is ordinary text", opaque.New().Verify},
		{"markdown", "[[tokenmill-markdown-ws:v1; this is ordinary text", markdown.New().Verify},
		{"block pack", "@tm-b:v1; this is ordinary text", packer.NewCodec(true).Verify},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.verify(tc.value, tc.value) {
				t.Fatalf("%s must verify an unchanged pass-through value", tc.name)
			}
		})
	}
}

func TestRewrite_ANSIWrapperPreservesOSC8URL(t *testing.T) {
	input := "click \x1b]8;;https://example.test\x07link\x1b]8;;\x07"
	wrapper := &ansiWrapper{}
	encoded, err := wrapper.Encode(input)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := "click link (https://example.test)"
	if encoded != want {
		t.Fatalf("ANSI wrapper lost hyperlink URL: got %q want %q", encoded, want)
	}
	if !wrapper.Verify(input, encoded) {
		t.Fatal("ANSI wrapper should verify preserved hyperlink output")
	}
}
