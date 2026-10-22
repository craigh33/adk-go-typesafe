package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/craigh33/adk-go-typesafe/typesafe"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: go run ./examples/typesafe-images /path/to/photo.jpg")
	}

	//nolint:gosec // User-selected image path.
	data, err := os.ReadFile(os.Args[1])
	if err != nil {
		return fmt.Errorf("read image: %w", err)
	}

	client, err := typesafe.New(&typesafe.Options{
		APIKey:  os.Getenv("TYPESAFE_API_KEY"),
		BaseURL: os.Getenv("TYPESAFE_BASE_URL"),
		Headers: http.Header{
			"Typesafe-Danger-Zone-Images": {"true"},
		},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	reference := rand.Text()
	response, err := client.Evaluate(ctx, &typesafe.Request{
		State: map[string]any{"photo": reference},
		Assets: map[string]typesafe.Image{
			reference: {Data: data},
		},
		Questions: map[string]typesafe.Question{
			"person_present": typesafe.Noul{
				Instructions: "Is there a person in `photo`?",
			},
			"scene": typesafe.Choice{
				Instructions: "Where was `photo` taken?",
				Criteria: map[string]any{
					"indoors":  "Inside a building",
					"outdoors": "Outside",
					"unclear":  "Cannot tell",
				},
			},
		},
	})
	if err != nil {
		return err
	}

	output, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return fmt.Errorf("format response: %w", err)
	}

	fmt.Println(string(output))

	return nil
}
