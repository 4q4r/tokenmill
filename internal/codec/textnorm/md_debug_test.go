package textnorm

import (
	"fmt"
	"testing"
)

func TestMDDebug(t *testing.T) {
	input := "see [docs](https://example.com/long/url) and [api](https://example.com/long/url) plus [other](https://other.example)."
	folded := FoldMarkdownLinks(input)
	fmt.Printf("folded=%q\n", folded)
	unfolded := UnfoldMarkdownLinks(folded)
	fmt.Printf("unfolded=%q\n", unfolded)
	fmt.Printf("equal=%v\n", unfolded == input)
}
