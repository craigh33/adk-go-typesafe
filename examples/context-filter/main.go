package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model/gemini"
	"google.golang.org/adk/v2/plugin"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/craigh33/adk-go-typesafe/plugins/contextfilter"
	"github.com/craigh33/adk-go-typesafe/typesafe"
)

func main() {
	observe := flag.Bool("observe", false, "Report proposed removals without filtering")
	flag.Parse()

	if err := run(*observe); err != nil {
		log.Fatal(err)
	}
}

func run(observe bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if os.Getenv("GEMINI_MODEL") == "" || os.Getenv("GOOGLE_API_KEY") == "" {
		return errors.New("set GEMINI_MODEL and GOOGLE_API_KEY")
	}

	client, err := typesafe.New(&typesafe.Options{
		APIKey:  os.Getenv("TYPESAFE_API_KEY"),
		BaseURL: os.Getenv("TYPESAFE_BASE_URL"),
	})
	if err != nil {
		return err
	}

	filter, err := contextfilter.New(contextfilter.Config{
		API:             client,
		Observe:         observe,
		KeepRecentTurns: 1,
		MinBytes:        1,
		Model:           os.Getenv("TYPESAFE_MODEL"),
		OnReport: func(_ agent.Context, report contextfilter.Report) {
			for _, decision := range report.Decisions {
				fmt.Printf("Messages [%d,%d): relevance=%.3f proposed removal=%t\n",
					decision.Start, decision.End, decision.Relevance, decision.Remove)
			}
			fmt.Printf("Removed %d turns; Jev input tokens: %d\n", report.RemovedTurns, report.Usage.InputTokens)
			if report.Err != nil {
				fmt.Printf("Review warning: %v\n", report.Err)
			}
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
		Name:        "assistant",
		Model:       llm,
		Instruction: "Answer briefly.",
	})
	if err != nil {
		return err
	}

	r, err := runner.New(runner.Config{
		AppName:           "context-filter-example",
		Agent:             a,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
		PluginConfig: runner.PluginConfig{
			Plugins: []*plugin.Plugin{filter},
		},
	})
	if err != nil {
		return err
	}

	for _, prompt := range []string{
		"For our task tracker, use Go and SQLite. Reply only: noted.",
		"Unrelated question: what is 12 times 7?",
		"Back to our task tracker: which language and database did we agree on?",
	} {
		fmt.Println("User:", prompt)

		content := genai.NewContentFromText(prompt, genai.RoleUser)
		for event, err := range r.Run(ctx, "local-user", "demo-session", content, agent.RunConfig{}) {
			if err != nil {
				return err
			}

			if event.Content != nil {
				for _, part := range event.Content.Parts {
					if part.Text != "" {
						fmt.Println(part.Text)
					}
				}
			}
		}
	}

	return nil
}
