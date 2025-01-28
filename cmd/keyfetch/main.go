package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
)

func main() {
	endpoint := flag.String("internal-api", "http://localhost:5444", "Internal API endpoint")
	repoguardPath := flag.String("repoguard-path", "/home/git/repoguard", "Path to the repoguard binary")
	flag.Parse()

	resp, err := http.Get(*endpoint + "/internal/allkeys")
	if err != nil {
		log.Fatalf("error fetching keys: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("error reading response body: %v", err)
	}

	var data map[string]string
	err = json.Unmarshal(body, &data)
	if err != nil {
		log.Fatalf("error unmarshalling response body: %v", err)
	}

	fmt.Print(formatKeyData(*repoguardPath, data))
}
