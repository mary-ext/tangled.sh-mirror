package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
)

var (
	lightTheme = "catppuccin-latte"
	darkTheme  = "catppuccin-macchiato"
)

func main() {
	outFile := flag.String("out", "", "css output file path")
	flag.Parse()

	if *outFile == "" {
		fmt.Println("error: output file path is required")
		flag.Usage()
		os.Exit(1)
	}

	file, err := os.Create(*outFile)
	if err != nil {
		fmt.Printf("error creating file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	formatter := html.New(html.WithClasses(true))

	formatter.WriteCSS(file, styles.Get(lightTheme))

	file.WriteString("\n@media (prefers-color-scheme: dark) {\n")
	formatter.WriteCSS(file, styles.Get(darkTheme))
	file.WriteString("}\n")
}
