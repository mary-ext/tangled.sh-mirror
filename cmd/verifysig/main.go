package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"tangled.sh/tangled.sh/core/crypto"
)

func parseCommitObject(commitData string) (string, string, error) {
	lines := strings.Split(commitData, "\n")

	var payloadLines []string
	var signatureLines []string
	var inSignature bool
	var foundSignature bool

	for _, line := range lines {
		if strings.HasPrefix(line, "gpgsig ") {
			foundSignature = true
			inSignature = true
			// remove 'gpgsig' prefix
			sigLine := strings.TrimPrefix(line, "gpgsig ")
			signatureLines = append(signatureLines, sigLine)
			continue
		}

		if inSignature {
			// check if this line is part of the signature (starts with space)
			if strings.HasPrefix(line, " ") {
				// remove the leading space and add to signature
				sigLine := strings.TrimPrefix(line, " ")
				signatureLines = append(signatureLines, sigLine)
				continue
			} else {
				// end of signature block
				inSignature = false
				// this line is part of payload, so add it
				payloadLines = append(payloadLines, line)
			}
		} else {
			// regular payload line
			payloadLines = append(payloadLines, line)
		}
	}

	if !foundSignature {
		return "", commitData, nil // no signature found, return empty signature and full data as payload
	}

	signature := strings.Join(signatureLines, "\n")
	payload := strings.Join(payloadLines, "\n")

	return signature, payload, nil
}

func main() {
	var pubkeyPath string
	flag.StringVar(&pubkeyPath, "pubkey", "", "Path to the public key file")
	flag.Parse()

	var pubKey []byte
	var err error
	if pubkeyPath != "" {
		pubKey, err = os.ReadFile(pubkeyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading public key file: %v\n", err)
			os.Exit(1)
		}
	}

	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading from stdin: %v\n", err)
		os.Exit(1)
	}

	commitData := string(input)

	signature, payload, err := parseCommitObject(commitData)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing commit: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("signature")
	fmt.Println(signature)
	fmt.Println()
	fmt.Println("payload:")
	fmt.Println(payload)

	err, ok := crypto.VerifySignature(pubKey, []byte(signature), []byte(payload))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if ok {
		fmt.Println("ok")
	}
}
