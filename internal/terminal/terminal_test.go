package terminal

import "testing"

func TestStripANSI_CSIColors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"red", "\x1b[31mred\x1b[0m", "red"},
		{"green bold", "\x1b[1;32mgreen\x1b[0m", "green"},
		{"reset", "\x1b[0m", ""},
		{"cursor", "\x1b[2J\x1b[Hhello", "hello"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StripANSI(tc.in)
			if got != tc.want {
				t.Fatalf("StripANSI(%q)=%q want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestStripANSI_256Color(t *testing.T) {
	in := "\x1b[38;5;196mred256\x1b[0m and \x1b[48;5;21mbluebg\x1b[0m"
	want := "red256 and bluebg"
	got := StripANSI(in)
	if got != want {
		t.Fatalf("256color: got %q want %q", got, want)
	}
}

func TestStripANSI_TrueColor(t *testing.T) {
	in := "\x1b[38;2;255;0;0mtrue red\x1b[0m \x1b[48;2;0;255;0mtrue bg\x1b[0m"
	want := "true red true bg"
	got := StripANSI(in)
	if got != want {
		t.Fatalf("truecolor: got %q want %q", got, want)
	}
}

func TestStripANSI_OSCHyperlinkPreserve(t *testing.T) {
	// hyperlink: \x1b]8;;https://example.com\x07link text\x1b]8;;\x07
	in := "click \x1b]8;;https://example.com\x07here\x1b]8;;\x07 please"
	// preserveHyperlink=true should keep URL as text (url)
	got := StripANSI(in, true)
	want := "click here (https://example.com) please"
	if got != want {
		t.Fatalf("preserve: got %q want %q", got, want)
	}
}

func TestStripANSI_OSCHyperlinkStrip(t *testing.T) {
	in := "click \x1b]8;;https://example.com\x07here\x1b]8;;\x07 please"
	got := StripANSI(in, false)
	want := "click here please"
	if got != want {
		t.Fatalf("strip: got %q want %q", got, want)
	}
	// also default StripANSI without flag should strip
	got2 := StripANSI(in)
	if got2 != want {
		t.Fatalf("default strip: got %q want %q", got2, want)
	}
}

func TestStripANSI_MultipleCSI(t *testing.T) {
	in := "\x1b[31mred\x1b[0m \x1b[32mgreen\x1b[0m \x1b[33myellow\x1b[0m"
	want := "red green yellow"
	got := StripANSI(in)
	if got != want {
		t.Fatalf("multiple: got %q want %q", got, want)
	}
}

func TestStripANSI_Empty(t *testing.T) {
	if got := StripANSI(""); got != "" {
		t.Fatalf("empty: got %q want empty", got)
	}
	if got := StripANSI("", true); got != "" {
		t.Fatalf("empty preserve: got %q", got)
	}
}

func TestStripANSI_NoANSIFastPath(t *testing.T) {
	in := "plain text without escape"
	got := StripANSI(in)
	if got != in {
		t.Fatalf("fastpath: got %q want %q", got, in)
	}
	if !HasANSI("\x1b[31mred\x1b[0m") {
		t.Fatal("HasANSI should be true for ANSI")
	}
	if HasANSI(in) {
		t.Fatal("HasANSI should be false for plain")
	}
	// Ensure pointer equality optimization? At least check that StripANSI returns same string for fast path
	// Not strictly required but check not corrupting
}

func TestHasANSI(t *testing.T) {
	if HasANSI("") {
		t.Fatal("empty should be false")
	}
	if !HasANSI("\x1b[0m") {
		t.Fatal("CSI should be detected")
	}
	if !HasANSI("\x1b]8;;https://a.com\x07text\x1b]8;;\x07") {
		t.Fatal("OSC hyperlink should be detected")
	}
	if HasANSI("no escape") {
		t.Fatal("false positive")
	}
}

func TestRenderCR_CRLFvsCR(t *testing.T) {
	// CRLF should become LF and not be treated as CR overwrite
	in := "a\r\nb\r\nc"
	want := "a\nb\nc"
	got := RenderCR(in)
	if got != want {
		t.Fatalf("CRLF: got %q want %q", got, want)
	}
	// isolated CR should keep last segment per line
	in2 := "old\rnew"
	want2 := "new"
	if got := RenderCR(in2); got != want2 {
		t.Fatalf("CR: got %q want %q", got, want2)
	}
	// CRLF + CR mix: "a\r\nb\roverride"
	in3 := "a\r\nb\roverride"
	// first Replace CRLF -> "a\nb\roverride" -> per line "b\roverride" -> "override"
	want3 := "a\noverride"
	if got := RenderCR(in3); got != want3 {
		t.Fatalf("mix: got %q want %q", got, want3)
	}
}

func TestRenderCR_Progress(t *testing.T) {
	in := "Downloading 1%\rDownloading 50%\rDownloading 100%\nDone"
	want := "Downloading 100%\nDone"
	got := RenderCR(in)
	if got != want {
		t.Fatalf("progress: got %q want %q", got, want)
	}
	// overlapping edge: spec says abcdefg\rxyz -> xyz (last segment) not xyzdefg
	in2 := "abcdefg\rxyz"
	want2 := "xyz"
	if got := RenderCR(in2); got != want2 {
		t.Fatalf("overlap: got %q want %q", got, want2)
	}
}

func TestRenderCR_MultipleLinesWithCR(t *testing.T) {
	in := "line1 old\rline1 new\nline2 progress 10%\rline2 progress 90%\nline3"
	want := "line1 new\nline2 progress 90%\nline3"
	got := RenderCR(in)
	if got != want {
		t.Fatalf("multiline CR: got %q want %q", got, want)
	}
}

func TestNormalizeTerminal(t *testing.T) {
	in := "\x1b[31mDownloading 1%\r\x1b[32mDownloading 100%\x1b[0m\r\nNext \x1b]8;;https://example.com\x07link\x1b]8;;\x07 line"
	// preserve=false
	got := NormalizeTerminal(in, false)
	// Step: StripANSI removes colors and hyperlink keeping "link", then RenderCR
	// Input after StripANSI(false): "Downloading 1%\rDownloading 100%\r\nNext link line"
	// After CRLF->LF: "Downloading 1%\rDownloading 100%\nNext link line"
	// Per line: "Downloading 1%\rDownloading 100%" -> "Downloading 100%"
	want := "Downloading 100%\nNext link line"
	if got != want {
		t.Fatalf("normalize false: got %q want %q", got, want)
	}
	// preserve=true
	got2 := NormalizeTerminal(in, true)
	want2 := "Downloading 100%\nNext link (https://example.com) line"
	if got2 != want2 {
		t.Fatalf("normalize true: got %q want %q", got2, want2)
	}
}

func TestStripANSI_OSCGeneric(t *testing.T) {
	// OSC other than hyperlink: e.g., title \x1b]0;title\x07 should be stripped
	in := "hello\x1b]0;mytitle\x07world"
	want := "helloworld"
	got := StripANSI(in)
	if got != want {
		t.Fatalf("OSC generic: got %q want %q", got, want)
	}
}

func TestRenderCR_EdgeEmptyAndNoCR(t *testing.T) {
	if got := RenderCR(""); got != "" {
		t.Fatalf("empty RenderCR got %q", got)
	}
	if got := RenderCR("no cr here\nsecond line"); got != "no cr here\nsecond line" {
		t.Fatalf("no CR: got %q", got)
	}
}
