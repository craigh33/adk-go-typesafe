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
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	systemonecallbacks "github.com/craigh33/adk-go-typesafe/callbacks/systemone"
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
		return errors.New("set GEMINI_MODEL and GOOGLE_API_KEY")
	}

	client, err := typesafe.New(&typesafe.Options{
		APIKey:  os.Getenv("TYPESAFE_API_KEY"),
		BaseURL: os.Getenv("TYPESAFE_BASE_URL"),
		Model:   os.Getenv("TYPESAFE_MODEL"),
	})
	if err != nil {
		return err
	}

	cfg, err := agentConfig(client)
	if err != nil {
		return err
	}

	cfg.Model, err = gemini.NewModel(ctx, os.Getenv("GEMINI_MODEL"), &genai.ClientConfig{
		APIKey:  os.Getenv("GOOGLE_API_KEY"),
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return err
	}

	a, err := llmagent.New(cfg)
	if err != nil {
		return err
	}

	r, err := runner.New(runner.Config{
		AppName:           "assessment-example",
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return err
	}

	text := "Draft a support ticket about a duplicate charge."
	if len(os.Args) > 1 {
		text = strings.Join(os.Args[1:], " ")
	}

	content := genai.NewContentFromText(text, genai.RoleUser)
	for event, err := range r.Run(ctx, "local-user", "demo-session", content, agent.RunConfig{}) {
		if err != nil {
			return err
		}

		if event.Content == nil {
			continue
		}

		for _, part := range event.Content.Parts {
			if part.Text != "" {
				fmt.Println(part.Text)
			}
		}
	}

	return nil
}

func agentConfig(client typesafe.Evaluator) (llmagent.Config, error) {
	assessment := systemonecallbacks.Config{
		API: client,
		Questions: map[string]typesafe.Question{
			"contains_secret": typesafe.Noul{
				Instructions: "Does the supplied content contain a password, API key, or private credential? " +
					"Mentioning credentials without their values is not a secret.",
			},
		},
		Policy:    secretPolicy,
		OutputKey: "input_assessment",
	}

	before, err := systemonecallbacks.BeforeModel(assessment)
	if err != nil {
		return llmagent.Config{}, err
	}

	assessment.OutputKey = "output_assessment"
	after, err := systemonecallbacks.AfterModel(assessment)
	if err != nil {
		return llmagent.Config{}, err
	}

	assessment.OutputKey = "tool_assessment"
	beforeTool, err := systemonecallbacks.BeforeTool(assessment)
	if err != nil {
		return llmagent.Config{}, err
	}

	draft, err := functiontool.New(
		functiontool.Config{
			Name:        "draft_ticket",
			Description: "Prepare a support ticket draft without sending it.",
		},
		func(
			_ agent.Context,
			input struct {
				Text string `json:"text"`
			},
		) (map[string]any, error) {
			return map[string]any{"draft": input.Text}, nil
		},
	)
	if err != nil {
		return llmagent.Config{}, err
	}

	return llmagent.Config{
		Name: "support",
		Instruction: "Use draft_ticket when asked to draft a support ticket. " +
			"Never claim that a draft has been submitted. " +
			"If a tool result is blocked, relay its reason without retrying.",
		Tools:                []tool.Tool{draft},
		BeforeModelCallbacks: []llmagent.BeforeModelCallback{before},
		AfterModelCallbacks:  []llmagent.AfterModelCallback{after},
		BeforeToolCallbacks:  []llmagent.BeforeToolCallback{beforeTool},
	}, nil
}

func secretPolicy(_ agent.Context, response *typesafe.Response) (systemonecallbacks.Decision, error) {
	answer, ok := response.Answers["contains_secret"].(typesafe.NoulAnswer)
	if !ok {
		return systemonecallbacks.Decision{}, errors.New("missing contains_secret assessment")
	}

	return systemonecallbacks.Decision{
		Block:  answer.Noul >= 0.5,
		Reason: "Remove credentials before continuing.",
	}, nil
}
