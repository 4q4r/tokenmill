package main

import "os"

func main() {
	// Allow `tokenmill --version` to work via root Version.
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-V") {
		// cobra handles version; we just ensure Execute sets version template.
	}
	Execute()
}
