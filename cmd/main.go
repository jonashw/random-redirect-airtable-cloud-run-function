package main

import (
	"fmt"
	"os"

	"github.com/GoogleCloudPlatform/functions-framework-go/funcframework"
	_ "github.com/jwilson4/go-tshirt"
)

func main() {
	port := "8080"
	if envPort := os.Getenv("PORT"); envPort != "" {
		port = envPort
	}
	fmt.Printf("Listening on http://localhost:%s\n", port)
	if err := funcframework.Start(port); err != nil {
		fmt.Fprintf(os.Stderr, "funcframework.Start: %v\n", err)
		os.Exit(1)
	}
}
