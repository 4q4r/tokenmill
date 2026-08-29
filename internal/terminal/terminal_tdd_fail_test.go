package terminal

import "testing"

func TestStripANSI_OSC_ST_Terminator_Red(t *testing.T) {
	// OSC with ST \x1b\\ should be stripped same as BEL \x07
	in := "hello\x1b]0;mytitle\x1b\\world"
	want := "helloworld"
	got := StripANSI(in)
	if got != want {
		t.Fatalf("OSC ST: got %q want %q", got, want)
	}
	// mixed BEL and ST
	in2 := "a\x1b]0;title\x07b\x1b]0;title2\x1b\\c"
	want2 := "abc"
	got2 := StripANSI(in2)
	if got2 != want2 {
		t.Fatalf("OSC mixed: got %q want %q", got2, want2)
	}
}

func TestStripANSI_Hyperlink_ST_Red(t *testing.T) {
	// hyperlink with ST terminator
	in := "click \x1b]8;;https://example.com\x1b\\here\x1b]8;;\x1b\\ please"
	wantPreserve := "click here (https://example.com) please"
	got := StripANSI(in, true)
	if got != wantPreserve {
		t.Fatalf("hyperlink ST preserve: got %q want %q", got, wantPreserve)
	}
	wantStrip := "click here please"
	got2 := StripANSI(in, false)
	if got2 != wantStrip {
		t.Fatalf("hyperlink ST strip: got %q want %q", got2, wantStrip)
	}
	// HasANSI should detect ST
	if !HasANSI(in) {
		t.Fatal("HasANSI should detect ST terminator")
	}
	// hyperlink with BEL still works after change (regression)
	inBEL := "click \x1b]8;;https://example.com\x07here\x1b]8;;\x07 please"
	gotBEL := StripANSI(inBEL, true)
	if gotBEL != wantPreserve {
		t.Fatalf("hyperlink BEL preserve after fix: got %q want %q", gotBEL, wantPreserve)
	}
}
