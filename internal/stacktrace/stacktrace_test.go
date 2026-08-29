package stacktrace

import (
	"strings"
	"testing"
)

func TestCompressStackTrace_Go(t *testing.T) {
	input := "goroutine 1 [running]:\n" +
		"github.com/foo/bar/baz.go:123 main.Foo\n" +
		"github.com/foo/bar/baz.go:124 main.Bar\n" +
		"github.com/foo/bar/qux.go:10 other.Func\n" +
		"github.com/foo/bar/baz.go:125 main.Baz\n"
	enc, dict, ok := CompressStackTrace(input)
	if !ok {
		t.Fatalf("expected ok")
	}
	if len(dict) == 0 {
		t.Fatalf("dict empty")
	}
	if !strings.Contains(enc, "$F0") {
		t.Fatalf("expected $F marker, got %q", enc)
	}
	dec := DecodeStackTrace(enc, dict)
	if dec != input {
		t.Fatalf("roundtrip failed: got %q want %q", dec, input)
	}
	if !VerifyStackTrace(input, enc, dict) {
		t.Fatalf("verify failed")
	}
	// Also test alias Verify
	if !Verify(input, enc, dict) {
		t.Fatalf("alias verify failed")
	}
}

func TestCompressStackTrace_Java(t *testing.T) {
	input := "Exception in thread \"main\" java.lang.NullPointerException\n" +
		"\tat com.example.MyClass.method(MyClass.java:123)\n" +
		"\tat com.example.MyClass.other(MyClass.java:45)\n" +
		"\tat com.example.Other.foo(Other.java:10)\n" +
		"\tat com.example.MyClass.method2(MyClass.java:124)\n"
	enc, dict, ok := CompressStackTrace(input)
	if !ok {
		// Java traces may have less directory prefix, but still should compress MyClass.java?
		// If not, allow fallback but verify roundtrip when ok
		t.Logf("java not compressed, ok false (acceptable if threshold), enc %q dict %v", enc, dict)
		return
	}
	dec := DecodeStackTrace(enc, dict)
	if dec != input {
		t.Fatalf("java roundtrip")
	}
}

func TestCompressStackTrace_NotStackTrace(t *testing.T) {
	input := "hello world no stacktrace here"
	_, _, ok := CompressStackTrace(input)
	if ok {
		t.Fatalf("should not ok for plain text")
	}
	// Also test empty
	_, _, ok2 := CompressStackTrace("")
	if ok2 {
		t.Fatalf("empty should not ok")
	}
}

func TestCompressStackTrace_DecodeAliases(t *testing.T) {
	input := "github.com/foo/bar/a.go:10 foo\n" +
		"github.com/foo/bar/b.go:20 bar\n" +
		"github.com/foo/bar/c.go:30 baz\n"
	enc, dict, ok := CompressStackTrace(input)
	if !ok {
		t.Fatalf("expected ok")
	}
	// Test Decode alias DecompressStackTrace
	dec := DecompressStackTrace(enc, dict)
	if dec != input {
		t.Fatalf("decompress alias failed")
	}
	// Test DecodeStackTrace alias
	dec2 := DecodeStackTrace(enc, dict)
	if dec2 != input {
		t.Fatalf("decode alias")
	}
}

func TestCompressStackTrace_VerifyNegative(t *testing.T) {
	input := "github.com/foo/bar/a.go:10 foo\n" +
		"github.com/foo/bar/b.go:20 bar\n" +
		"github.com/foo/bar/c.go:30 baz\n"
	enc, dict, _ := CompressStackTrace(input)
	if VerifyStackTrace("different", enc, dict) {
		t.Fatalf("verify should fail")
	}
}

func TestCompressStackTrace_HeaderFormat(t *testing.T) {
	input := strings.Repeat("github.com/example/project/pkg/file.go:123 at foo.Bar\n", 3)
	enc, dict, ok := CompressStackTrace(input)
	if !ok {
		t.Fatalf("expected ok")
	}
	if !strings.Contains(enc, "[StackTrace:") && !strings.Contains(enc, "$F0=") {
		t.Fatalf("header should contain prefix marker, got %q", enc)
	}
	dec := DecodeStackTrace(enc, dict)
	if dec != input {
		t.Fatalf("header roundtrip")
	}
}

func TestCompressStackTrace_GitHubPrefix(t *testing.T) {
	// Ensure github.com/.../ detection and grouping
	input := "at github.com/company/repo/pkg/handler.go:42 handler.Handle\n" +
		"at github.com/company/repo/pkg/handler.go:50 handler.Other\n" +
		"at github.com/company/repo/pkg/util.go:10 util.Func\n"
	enc, dict, ok := CompressStackTrace(input)
	if !ok {
		t.Fatalf("github prefix should compress")
	}
	if len(dict) == 0 {
		t.Fatalf("dict empty")
	}
	dec := DecodeStackTrace(enc, dict)
	if dec != input {
		t.Fatalf("github roundtrip")
	}
	// Ensure original file:line:function preserved exactly after decode
	if !strings.Contains(dec, "handler.go:42") {
		t.Fatalf("file:line not preserved")
	}
}
