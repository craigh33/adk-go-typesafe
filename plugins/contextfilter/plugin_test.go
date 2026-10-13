package contextfilter

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/craigh33/adk-go-typesafe/typesafe"
)

type evaluatorFunc func(context.Context, *typesafe.Request) (*typesafe.Response, error)

func (f evaluatorFunc) Evaluate(ctx context.Context, request *typesafe.Request) (*typesafe.Response, error) {
	return f(ctx, request)
}

type callbackContext struct {
	agent.Context

	parent context.Context
	input  *genai.Content
	sess   session.Session
}

func (c callbackContext) Deadline() (time.Time, bool) { return c.parent.Deadline() }
func (c callbackContext) Done() <-chan struct{}       { return c.parent.Done() }
func (c callbackContext) Err() error                  { return c.parent.Err() }
func (c callbackContext) Value(key any) any           { return c.parent.Value(key) }
func (c callbackContext) UserContent() *genai.Content { return c.input }
func (c callbackContext) Session() session.Session    { return c.sess }

func (c callbackContext) WithICDelta(*agent.InvocationContextDelta) agent.InvocationContext {
	return c
}

func user(text string) *genai.Content  { return genai.NewContentFromText(text, genai.RoleUser) }
func reply(text string) *genai.Content { return genai.NewContentFromText(text, genai.RoleModel) }

func scores(request *typesafe.Request) *typesafe.Response {
	answers := make(map[string]typesafe.Answer, len(request.Questions))
	for id := range request.Questions {
		answers[id] = typesafe.NoulAnswer{Type: "noul", Noul: 0}
	}
	return &typesafe.Response{Model: "jev-test", Answers: answers, Usage: typesafe.Usage{InputTokens: 10}}
}

func testConfig(api typesafe.Evaluator) Config {
	return Config{API: api, KeepRecentTurns: 1, MinBytes: 1}
}

func apply(t *testing.T, cfg Config, ctx agent.Context, request *model.LLMRequest) (Report, error) {
	t.Helper()
	var report Report
	reports := 0
	cfg.OnReport = func(_ agent.Context, result Report) {
		report = result
		reports++
	}
	filter, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	response, err := filter.BeforeModelCallback()(ctx, request)
	if response != nil {
		t.Fatal("filter must not substitute a model response")
	}
	if reports != 1 {
		t.Fatalf("expected one report, got %d", reports)
	}
	return report, err
}

//nolint:gocognit // Exercise distinct evaluator failures through the plugin callback.
func TestReviewFailuresAndCancellation(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"error", "nil", "timeout", "cancel", "already-canceled"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			current := user("Current")
			contents := []*genai.Content{user("Old"), reply("Done"), current}
			calls := 0
			api := evaluatorFunc(func(ctx context.Context, req *typesafe.Request) (*typesafe.Response, error) {
				calls++
				switch mode {
				case "cancel":
					cancel()
					return scores(req), nil
				case "timeout":
					<-ctx.Done()
					return scores(req), nil // Even a late successful result must be ignored.
				case "nil":
					return nil, nil //nolint:nilnil // Deliberately invalid evaluator response.
				default:
					return nil, errors.New("unavailable")
				}
			})
			cfg := testConfig(api)
			cfg.Timeout = time.Millisecond
			if mode == "already-canceled" {
				cancel()
			}
			request := &model.LLMRequest{Contents: contents}
			report, err := apply(t, cfg, callbackContext{parent: parent, input: current}, request)
			if !reflect.DeepEqual(request.Contents, contents) {
				t.Fatal("failure discarded context")
			}
			if mode == "cancel" || mode == "already-canceled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation swallowed: %v", err)
				}
			} else if err != nil || report.Err == nil {
				t.Fatalf("review failure not reported: report=%+v err=%v", report, err)
			}
			if mode == "already-canceled" && calls != 0 {
				t.Fatal("called evaluator after cancellation")
			}
		})
	}
}

func TestSkipsShortOrUnknownContext(t *testing.T) {
	t.Parallel()
	current := user("Current")
	api := evaluatorFunc(func(context.Context, *typesafe.Request) (*typesafe.Response, error) {
		t.Fatal("unexpected API call")
		return nil, errors.New("unexpected call")
	})
	for _, contents := range [][]*genai.Content{
		{}, {current}, {user("Old"), reply("Done"), current},
	} {
		request := &model.LLMRequest{Contents: contents}
		report, err := apply(t, Config{API: api}, callbackContext{parent: t.Context(), input: current}, request)
		if err != nil || report.Err != nil || !reflect.DeepEqual(request.Contents, contents) {
			t.Fatal("short context should be unchanged")
		}
	}
	request := &model.LLMRequest{Contents: []*genai.Content{user("Old"), user("Delegated input")}}
	if _, err := apply(t, testConfig(api), callbackContext{parent: t.Context(), input: current}, request); err != nil {
		t.Fatal(err)
	}
	current = &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{InlineData: &genai.Blob{MIMEType: "image/png"}}},
	}
	request.Contents = []*genai.Content{user("Old"), reply("Done"), current}
	if _, err := apply(t, testConfig(api), callbackContext{parent: t.Context(), input: current}, request); err != nil {
		t.Fatal(err)
	}
	current = user("Only current turn")
	request.Contents = []*genai.Content{current}
	if _, err := apply(t, testConfig(api), callbackContext{parent: t.Context(), input: current}, request); err != nil {
		t.Fatal(err)
	}
	current = &genai.Content{Role: "custom", Parts: []*genai.Part{{Text: "Current"}}}
	request.Contents = []*genai.Content{user("Old"), reply("Done"), current}
	if _, err := apply(t, testConfig(api), callbackContext{parent: t.Context(), input: current}, request); err != nil {
		t.Fatal(err)
	}
	current = user("Current")
	request.Contents = []*genai.Content{user("Old"), reply("Done"), current}
	request.Config = &genai.GenerateContentConfig{SystemInstruction: &genai.Content{
		Parts: []*genai.Part{{InlineData: &genai.Blob{MIMEType: "image/png"}}},
	}}
	if _, err := apply(t, testConfig(api), callbackContext{parent: t.Context(), input: current}, request); err != nil {
		t.Fatal(err)
	}
}

func TestConfig(t *testing.T) {
	t.Parallel()
	api := evaluatorFunc(
		func(_ context.Context, req *typesafe.Request) (*typesafe.Response, error) { return scores(req), nil },
	)
	for _, cfg := range []Config{
		{}, {API: api, RemovalThreshold: -1}, {API: api, RemovalThreshold: 2},
		{API: api, RemovalThreshold: math.NaN()}, {API: api, RemovalThreshold: math.Inf(1)},
		{API: api, KeepRecentTurns: -1}, {API: api, MinBytes: -1}, {API: api, MaxRequestBytes: -1}, {API: api, Timeout: -1},
	} {
		if _, err := New(cfg); err == nil {
			t.Fatalf("accepted invalid config: %+v", cfg)
		}
	}
	filter, err := New(Config{API: api})
	if err != nil {
		t.Fatal(err)
	}
	if filter.Name() != "context_filter" {
		t.Fatalf("unexpected plugin name: %q", filter.Name())
	}
	if _, err := filter.BeforeModelCallback()(callbackContext{parent: t.Context()}, nil); err == nil {
		t.Fatal("accepted nil request")
	}
	custom, err := New(Config{API: api, Name: "history"})
	if err != nil || custom.Name() != "history" {
		t.Fatalf("custom plugin name was not used: %v", err)
	}
}

func TestSkipsTextlessCurrentInput(t *testing.T) {
	t.Parallel()
	api := evaluatorFunc(func(context.Context, *typesafe.Request) (*typesafe.Response, error) {
		t.Fatal("textless current input must not trigger evaluation")
		return nil, errors.New("unexpected call")
	})
	for _, input := range []*genai.Content{
		nil, {Role: genai.RoleUser}, user(""), user(" \t\n"),
		{Role: genai.RoleUser, Parts: []*genai.Part{nil}},
		{Role: genai.RoleUser, Parts: []*genai.Part{{Thought: true, Text: "Hidden reasoning"}}},
		result("a"),
	} {
		contents := []*genai.Content{user("Old request"), reply("Done"), input}
		request := &model.LLMRequest{Contents: contents}
		report, err := apply(t, testConfig(api), callbackContext{parent: t.Context(), input: input}, request)
		if err != nil || report.Err != nil || len(report.Decisions) != 0 ||
			!reflect.DeepEqual(request.Contents, contents) {
			t.Fatalf("textless input changed history: input=%+v report=%+v err=%v", input, report, err)
		}
	}
}
