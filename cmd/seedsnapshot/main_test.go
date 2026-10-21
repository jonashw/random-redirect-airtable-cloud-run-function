package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestReadSites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.csv")
	csv := "\ufeffName,URL,Active,Notes\n" +
		"a,https://a,checked,x\n" +
		"b,https://b,,\n" +
		"c,,checked,\n" +
		"\"d, inc\",https://d,checked,\n"
	if err := os.WriteFile(path, []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	sites, err := readSites(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(sites); got != "[{a https://a} {d, inc https://d}]" {
		t.Fatalf("got %s", got)
	}
}

func TestReadSitesMissingColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.csv")
	os.WriteFile(path, []byte("Name,URL\na,https://a\n"), 0o600)
	if _, err := readSites(path); err == nil {
		t.Fatal("want error for missing Active column")
	}
}
