package main

func main() {
	// `tokenmill --version` works via the root command's Version:
	// cobra handles it; Execute just needs to run with the version template set.
	Execute()
}
