package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"vocat/internal/vowifi"
)

const defaultTarURL = "https://github.com/dwilliamsuk/ios-carrier-bundles/archive/refs/heads/latest.tar.gz"

func main() {
	tarURL := flag.String("url", defaultTarURL, "URL to ios-carrier-bundles tar.gz archive")
	localTar := flag.String("file", "", "path to local .tar.gz archive")
	outputFile := flag.String("output", filepath.Join("internal", "vowifi", "carrier_profiles.json"), "output carrier_profiles.json path")
	flag.Parse()

	var reader io.Reader
	if *localTar != "" {
		f, err := os.Open(*localTar)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", *localTar, err)
			os.Exit(1)
		}
		defer f.Close()
		reader = f
	} else {
		fmt.Printf("Downloading %s ...\n", *tarURL)
		client := &http.Client{Timeout: 3 * time.Minute}
		resp, err := client.Get(*tarURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Download error: %v\n", err)
			os.Exit(1)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "HTTP %s\n", resp.Status)
			os.Exit(1)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Read error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Downloaded %d bytes. Parsing archive...\n", len(data))
		reader = bytes.NewReader(data)
	}

	gz, err := gzip.NewReader(reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Gzip error: %v\n", err)
		os.Exit(1)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	bundlePlists := make(map[string]map[string][]byte)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Tar error: %v\n", err)
			break
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.ReplaceAll(hdr.Name, "\\", "/")
		if strings.Contains(strings.ToLower(name), "/signatures/") {
			continue
		}
		base := path.Base(name)
		if !strings.EqualFold(base, "carrier.plist") && (!strings.HasPrefix(strings.ToLower(base), "overrides") || !strings.EqualFold(path.Ext(base), ".plist")) {
			continue
		}
		// e.g. ios-carrier-bundles-latest/Carrier Bundles/EE_uk.bundle/carrier.plist
		bundleDir := path.Dir(name)
		bundleName := path.Base(bundleDir)
		if !strings.HasSuffix(strings.ToLower(bundleName), ".bundle") {
			continue
		}

		content, err := io.ReadAll(tr)
		if err != nil {
			continue
		}
		if bundlePlists[bundleName] == nil {
			bundlePlists[bundleName] = make(map[string][]byte)
		}
		bundlePlists[bundleName][base] = content
	}

	fmt.Printf("Found %d distinct carrier bundles. Extracting VoWiFi profiles...\n", len(bundlePlists))

	var sortedBundleNames []string
	for k := range bundlePlists {
		sortedBundleNames = append(sortedBundleNames, k)
	}
	sort.Strings(sortedBundleNames)

	var extractedRules []any
	seenIDs := make(map[string]bool)
	successCount := 0
	skipCount := 0

	for _, bundleName := range sortedBundleNames {
		plists := bundlePlists[bundleName]
		rule, _, err := vowifi.ImportCarrierBundlePlists(bundleName, plists)
		if err != nil {
			skipCount++
			continue
		}
		if seenIDs[rule.ID] {
			continue
		}
		seenIDs[rule.ID] = true
		extractedRules = append(extractedRules, rule)
		successCount++
	}

	fmt.Printf("Extracted %d valid carrier profile rules (skipped %d without valid VoWiFi selectors).\n", successCount, skipCount)

	doc := map[string]any{
		"version": vowifi.CarrierProfileSchemaVersion,
		"metadata": map[string]any{
			"source":       "dwilliamsuk/ios-carrier-bundles",
			"generated_at": time.Now().UTC().Format(time.RFC3339),
			"count":        len(extractedRules),
		},
		"profiles": extractedRules,
	}

	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "JSON encode error: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*outputFile, append(encoded, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Write error to %s: %v\n", *outputFile, err)
		os.Exit(1)
	}

	fmt.Printf("Successfully wrote %d rules (%d bytes) to %s\n", len(extractedRules), len(encoded), *outputFile)
}
