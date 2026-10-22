package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/craigh33/adk-go-typesafe/typesafe"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := typesafe.New(&typesafe.Options{
		APIKey:  os.Getenv("TYPESAFE_API_KEY"),
		BaseURL: os.Getenv("TYPESAFE_BASE_URL"),
	})
	if err != nil {
		return err
	}

	text := "I was charged twice. Please help when you can."
	if len(os.Args) > 1 {
		text = strings.Join(os.Args[1:], " ")
	}

	response, err := client.Evaluate(ctx, &typesafe.Request{
		State: map[string]any{"message": text},
		Questions: map[string]typesafe.Question{
			"department": typesafe.Choice{
				Instructions: "Which team should handle this?",
				Criteria: map[string]any{
					"billing":   "Payments and refunds",
					"technical": "Bugs and integrations",
					"sales":     "Pricing and upgrades",
				},
			},
			"urgency": typesafe.Score{
				Instructions: "How urgent is this?",
				Criteria:     []any{"Can wait", "Needs attention soon", "Needs attention now"},
			},
			"refund_requested": typesafe.Noul{
				Instructions: "Does the customer request a refund?",
			},
		},
	})
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return fmt.Errorf("format response: %w", err)
	}

	fmt.Println(string(data))

	return nil
}
