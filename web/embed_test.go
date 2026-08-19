package web

import (
	"io/fs"
	"testing"
)

func TestEmbeddedDistributionContainsIndex(t *testing.T) {
	index, err := fs.ReadFile(Dist, "index.html")
	if err != nil {
		t.Fatalf("read embedded index.html: %v", err)
	}
	if len(index) == 0 {
		t.Fatal("embedded index.html is empty")
	}
}
