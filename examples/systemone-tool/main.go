package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	systemonetool "github.com/craigh33/adk-go-typesafe/tools/systemone"
	"github.com/craigh33/adk-go-typesafe/typesafe"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if os.Getenv("GEMINI_MODEL") == "" || os.Getenv("GOOGLE_API_KEY") == "" {
		return errors.New("set GEMINI_MODEL and GOOGLE_API_KEY for the calling agent")
	}

	client, err := typesafe.New(&typesafe.Options{
		APIKey:  os.Getenv("TYPESAFE_API_KEY"),
		BaseURL: os.Getenv("TYPESAFE_BASE_URL"),
	})
	if err != nil {
		return err
	}

	classify, err := systemonetool.New(systemonetool.Config{
		API:         client,
		Name:        "classify_ticket",
		Description: "Classify a support ticket and assess its urgency.",
		Questions: map[string]typesafe.Question{
			"department": typesafe.Choice{
				Instructions: "Which team should handle this?",
				Criteria: map[string]any{
					"billing":   "Payments and refunds",
					"technical": "Bugs and integrations",
					"sales":     "Pricing and upgrades",
				},
			},
			"urgent": typesafe.Noul{
				Instructions: "Does this require immediate attention?",
			},
		},
	})
	if err != nil {
		return err
	}

	llm, err := gemini.NewModel(ctx, os.Getenv("GEMINI_MODEL"), &genai.ClientConfig{
		APIKey:  os.Getenv("GOOGLE_API_KEY"),
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return err
	}

	a, err := llmagent.New(llmagent.Config{
		Name:  "ticket_assistant",
		Model: llm,
		Tools: []tool.Tool{classify},
		Instruction: "Use classify_ticket to assess the user's support ticket. " +
			"Report its department, confidence, and urgency probability without making business decisions or taking actions.",
	})
	if err != nil {
		return err
	}

	r, err := runner.New(runner.Config{
		AppName:           "systemone-tool-example",
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return err
	}

	text := "I was charged twice. Please help when you can."
	if len(os.Args) > 1 {
		text = strings.Join(os.Args[1:], " ")
	}

	content := genai.NewContentFromText(text, genai.RoleUser)
	for event, runErr := range r.Run(ctx, "local-user", "demo-session", content, agent.RunConfig{}) {
		if runErr != nil {
			return runErr
		}

		if event.Author != a.Name() || event.LLMResponse.Partial || event.LLMResponse.Content == nil {
			continue
		}

		for _, part := range event.LLMResponse.Content.Parts {
			if part.Text != "" {
				fmt.Println(part.Text)
			}
		}
	}

	return nil
}
