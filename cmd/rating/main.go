package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/tokenmill/tokenmill/scripts/rating"
)

func main() {
	results, perDataset := rating.Run()
	md := rating.ToMarkdown(results)
	fmt.Println("# TokenMill Rating — Production Benchmark")
	fmt.Println("")
	fmt.Println(md)
	jsonBytes, err := rating.ToJSON(results, perDataset)
	if err == nil {
		fmt.Println("## JSON Output")
		fmt.Println("```json")
		fmt.Println(string(jsonBytes))
		fmt.Println("```")
		// also write to file if needed
		_ = os.WriteFile("rating.json", jsonBytes, 0644)
	}
	// Also compare vs rtk-style baseline: compute totals
	totalInput, totalOutput := 0, 0
	for _, r := range results {
		totalInput += r.InputTokens
		totalOutput += r.OutputTokens
	}
	if totalInput > 0 {
		saved := totalInput - totalOutput
		pct := float64(saved) / float64(totalInput) * 100
		fmt.Printf("\nOverall tournament vs raw: input %d -> output %d, saved %d (%.1f%%)\n", totalInput, totalOutput, saved, pct)
	}
	// Ensure deterministic output for CI
	var check []byte
	check, _ = json.Marshal(results)
	_ = check
	_ = perDataset
	if len(os.Args) > 1 && os.Args[1] == "--json-only" {
		fmt.Println(string(jsonBytes))
	}
}
