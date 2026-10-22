package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/craigh33/adk-go-bedrock/bedrock/converse"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	systemoneagent "github.com/craigh33/adk-go-typesafe/agent/systemone"
	"github.com/craigh33/adk-go-typesafe/typesafe"
)

const (
	billing   = "billing"
	technical = "technical"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if os.Getenv("BEDROCK_MODEL_ID") == "" {
		return errors.New("set BEDROCK_MODEL_ID to an accessible model or inference profile")
	}

	client, err := typesafe.New(&typesafe.Options{
		APIKey:  os.Getenv("TYPESAFE_API_KEY"),
		BaseURL: os.Getenv("TYPESAFE_BASE_URL"),
		Model:   os.Getenv("TYPESAFE_MODEL"),
		Retry:   &typesafe.RetryPolicy{},
	})
	if err != nil {
		return err
	}

	llm, err := converse.New(ctx, os.Getenv("BEDROCK_MODEL_ID"), &converse.Options{Region: os.Getenv("AWS_REGION")})
	if err != nil {
		return err
	}

	triage, err := newRouter(client, llm)
	if err != nil {
		return err
	}

	r, err := runner.New(runner.Config{
		AppName:           "bedrock-routing-example",
		Agent:             triage,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return err
	}

	text := "I was charged twice. Please help me resolve the duplicate payment."
	if len(os.Args) > 1 {
		text = strings.Join(os.Args[1:], " ")
	}

	content := genai.NewContentFromText(text, genai.RoleUser)
	for event, err := range r.Run(ctx, "local-user", "demo-session", content, agent.RunConfig{}) {
		if err != nil {
			return err
		}

		if event.Content == nil || event.Partial {
			continue
		}

		for _, part := range event.Content.Parts {
			if part.Text != "" {
				fmt.Printf("[%s] %s\n", event.Author, part.Text)
			}
		}
	}

	return nil
}

func newRouter(client typesafe.Evaluator, llm model.LLM) (agent.Agent, error) {
	children := make(map[string]agent.Agent)
	for name, instruction := range map[string]string{
		billing: "Help the user understand payment and refund issues. " +
			"Explain the next steps; do not claim to issue refunds.",
		technical: "Help the user diagnose bugs and integration issues. Ask for missing details.",
		"review":  "The classification is uncertain. Ask a clarifying question before suggesting a specialist.",
	} {
		child, err := llmagent.New(llmagent.Config{
			Name:                     name,
			Model:                    llm,
			Instruction:              instruction,
			Mode:                     llmagent.ModeSingleTurn,
			DisallowTransferToParent: true,
			DisallowTransferToPeers:  true,
		})
		if err != nil {
			return nil, err
		}

		children[name] = child
	}

	return systemoneagent.New(systemoneagent.Config{
		Name:      "triage",
		API:       client,
		OutputKey: "ticket_assessment",
		Questions: map[string]typesafe.Question{
			"department": typesafe.Choice{
				Instructions: "Which team should handle this ticket?",
				Criteria: map[string]any{
					billing:   "Payments and refunds",
					technical: "Bugs and integrations",
				},
			},
		},
		Routing: &systemoneagent.Routing{
			Question:      "department",
			MinConfidence: 0.75,
			Routes: map[string]agent.Agent{
				billing:   children[billing],
				technical: children[technical],
			},
			Fallback: children["review"],
		},
	})
}
