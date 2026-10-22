package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/genai"

	systemoneagent "github.com/craigh33/adk-go-typesafe/agent/systemone"
	"github.com/craigh33/adk-go-typesafe/typesafe"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: go run ./examples/systemone-images /path/to/photo.jpg")
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

	classify, err := systemoneagent.New(systemoneagent.Config{
		Name: "image_assessment",
		API:  client,
		Questions: map[string]typesafe.Question{
			"person_present": typesafe.Noul{
				Instructions: "Is there a person in the image?",
			},
		},
	})
	if err != nil {
		return err
	}

	r, err := runner.NewInMemory("systemone-images", classify)
	if err != nil {
		return err
	}

	content := &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			genai.NewPartFromText("Evaluate this photo."),
			genai.NewPartFromBytes(data, http.DetectContentType(data)),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for event, err := range r.Run(ctx, "local-user", "demo-session", content, agent.RunConfig{}) {
		if err != nil {
			return err
		}

		if event.Content != nil {
			for _, part := range event.Content.Parts {
				fmt.Println(part.Text)
			}
		}
	}

	return nil
}
