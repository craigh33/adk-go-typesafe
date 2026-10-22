package systemone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"math"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/craigh33/adk-go-typesafe/typesafe"
)

type fakeAPI func(context.Context, *typesafe.Request) (*typesafe.Response, error)

func (f fakeAPI) Evaluate(ctx context.Context, request *typesafe.Request) (*typesafe.Response, error) {
	return f(ctx, request)
}

func answer(choice string, confidence float64) *typesafe.Response {
	return &typesafe.Response{
		Model: "jev-test",
		Answers: map[string]typesafe.Answer{
			"department": typesafe.ChoiceAnswer{Type: "choice", Choice: choice, Confidence: confidence},
		},
		Usage: typesafe.Usage{InputTokens: 12},
	}
}

func childAgent(t *testing.T, name string, calls *[]string) agent.Agent {
	t.Helper()
	a, err := agent.New(
		agent.Config{Name: name, Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(yield func(*session.Event, error) bool) {
				if _, err := ctx.Session().State().Get("assessment"); err != nil {
					t.Errorf("assessment not visible to child: %v", err)
				}
				*calls = append(*calls, ctx.Agent().Name())
				event := session.NewEvent(ctx, ctx.InvocationID())
				event.Author = name
				event.Content = genai.NewContentFromText("Handled by "+name, genai.RoleModel)
				yield(event, nil)
			}
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func config(api typesafe.Evaluator) Config {
	return Config{
		Name:      "triage",
		API:       api,
		OutputKey: "assessment",
		Model:     "jev-pinned",
		Questions: map[string]typesafe.Question{
			"department": typesafe.Choice{
				Criteria: map[string]any{"billing": "Payments", "technical": "Bugs", "other": "Other"},
			},
		},
	}
}

func runAgent(t *testing.T, a agent.Agent, content *genai.Content) ([]*session.Event, session.State, error) {
	t.Helper()
	service := session.InMemoryService()
	r, err := runner.New(runner.Config{AppName: "test", Agent: a, SessionService: service, AutoCreateSession: true})
	if err != nil {
		t.Fatal(err)
	}
	var events []*session.Event
	var runErr error
	for event, err := range r.Run(t.Context(), "user", "session", content, agent.RunConfig{}) {
		if err != nil {
			runErr = err
			break
		}
		events = append(events, event)
	}
	saved, err := service.Get(t.Context(), &session.GetRequest{AppName: "test", UserID: "user", SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	return events, saved.Session.State(), runErr
}

//nolint:gocognit
func TestRoutingAndState(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, choice string
		confidence   float64
		want         string
		fallback     bool
	}{
		{"confident", "billing", 0.9, "billing_agent", false},
		{"threshold inclusive", "technical", 0.75, "technical_agent", false},
		{"uncertain", "billing", 0.74, "review_agent", true},
		{"unmapped", "other", 0.99, "review_agent", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			api := fakeAPI(func(_ context.Context, req *typesafe.Request) (*typesafe.Response, error) {
				if req.State != "Charged twice" || req.Model != "jev-pinned" {
					t.Errorf("request = %#v", req)
				}
				return answer(tc.choice, tc.confidence), nil
			})
			var calls []string
			cfg := config(api)
			cfg.Routing = &Routing{Question: "department", MinConfidence: 0.75, Routes: map[string]agent.Agent{
				"billing": childAgent(
					t,
					"billing_agent",
					&calls,
				),
				"technical": childAgent(t, "technical_agent", &calls),
			}, Fallback: childAgent(t, "review_agent", &calls)}
			a, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(a.SubAgents()) != 3 {
				t.Fatal("children not registered with ADK")
			}
			events, state, err := runAgent(t, a, genai.NewContentFromText("Charged twice", genai.RoleUser))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, []string{tc.want}) || len(events) != 2 {
				t.Fatalf("calls = %v; events = %d", calls, len(events))
			}
			if events[0].ID == "" || events[0].InvocationID == "" || events[0].Author != "triage" {
				t.Fatalf("missing event metadata: %#v", events[0])
			}
			saved, err := state.Get("assessment")
			if err != nil {
				t.Fatal(err)
			}
			record, ok := saved.(map[string]any)
			if !ok || record["model"] != "jev-test" {
				t.Fatalf("saved = %#v", saved)
			}
			decision, ok := record["routing"].(map[string]any)
			if !ok || decision["agent"] != tc.want || decision["fallback"] != tc.fallback {
				t.Fatalf("decision = %#v", record["routing"])
			}
		})
	}
}

func TestClassificationStructuredState(t *testing.T) {
	t.Parallel()
	state := map[string]any{"ticket": "Charged twice"}
	cfg := config(fakeAPI(func(_ context.Context, req *typesafe.Request) (*typesafe.Response, error) {
		if !reflect.DeepEqual(req.State, state) {
			t.Errorf("state = %#v", req.State)
		}
		return answer("billing", 1), nil
	}))
	cfg.OutputKey = ""
	cfg.State = func(agent.InvocationContext) (any, error) { return state, nil }
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	events, saved, err := runAgent(t, a, genai.NewContentFromText("ignored", genai.RoleUser))
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %v, error = %v", events, err)
	}
	if _, err := saved.Get("triage"); err != nil {
		t.Fatal(err)
	}
}

//nolint:gocognit
func TestClassificationInlineImages(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		parts     []*genai.Part
		images    int
		wantError string
	}{
		{
			name: "text and images",
			parts: []*genai.Part{
				genai.NewPartFromText("Compare these"),
				genai.NewPartFromBytes([]byte("first"), "image/png"),
				nil,
				genai.NewPartFromBytes([]byte("second"), "image/jpeg"),
				{Text: "hidden", InlineData: &genai.Blob{Data: []byte("hidden"), MIMEType: "image/png"}, Thought: true},
			},
			images: 2,
		},
		{name: "image only", parts: []*genai.Part{genai.NewPartFromBytes([]byte("first"), "image/jpg")}, images: 1},
		{name: "empty image", parts: []*genai.Part{genai.NewPartFromBytes(nil, "image/png")}, wantError: "PNG or JPEG"},
		{name: "audio", parts: []*genai.Part{genai.NewPartFromBytes([]byte("audio"), "audio/wav")}, wantError: "PNG or JPEG"},
		{name: "file URI", parts: []*genai.Part{genai.NewPartFromURI("gs://bucket/photo.png", "image/png")}, wantError: "file URI"},
		{name: "no input", wantError: "text or an inline image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := 0
			response := &typesafe.Response{
				Model: "jev-1.14.0",
				Answers: map[string]typesafe.Answer{
					"target": typesafe.BoxAnswer{
						Type: "box",
						Box:  typesafe.BoxCoordinates{Top: 0, Bottom: 1, Left: 0, Right: 1},
					},
				},
			}
			cfg := config(fakeAPI(func(_ context.Context, request *typesafe.Request) (*typesafe.Response, error) {
				calls++
				state, ok := request.State.(string)
				if !ok || len(request.Assets) != tc.images || strings.Contains(state, "hidden") {
					t.Errorf("incorrect image projection: %#v", request)
				}

				for reference, asset := range request.Assets {
					if !strings.Contains(state, reference) ||
						(!bytes.Equal(asset.Data, []byte("first")) && !bytes.Equal(asset.Data, []byte("second"))) {
						t.Error("image bytes or reference lost")
					}
				}

				if tc.images == 2 && !strings.Contains(state, "Compare these") {
					t.Error("user text lost")
				}

				if _, ok := request.Questions["target"].(typesafe.Box); !ok || request.Model != "jev-1.14.0" {
					t.Error("box question or model lost")
				}

				return response, nil
			}))
			cfg.Model = "jev-1.14.0"
			cfg.Questions = map[string]typesafe.Question{
				"target": typesafe.Box{Instructions: "the target in the image"},
			}

			a, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}

			content := &genai.Content{Role: genai.RoleUser, Parts: tc.parts}
			before, _ := json.Marshal(content)
			events, saved, err := runAgent(t, a, content)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || calls != 0 {
					t.Fatalf("error = %v, API calls = %d", err, calls)
				}

				return
			}

			if err != nil || calls != 1 {
				t.Fatalf("error = %v, API calls = %d", err, calls)
			}

			data, _ := json.Marshal(response)
			var want map[string]any
			json.Unmarshal(data, &want)

			assessment, err := saved.Get("assessment")
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(assessment, want) || len(events) != 1 {
				t.Fatal("box coordinates or null confidence lost from ADK assessment")
			}

			var emitted map[string]any
			if err := json.Unmarshal([]byte(events[0].Content.Parts[0].Text), &emitted); err != nil ||
				!reflect.DeepEqual(emitted, want) {
				t.Error("box coordinates or null confidence lost from ADK assessment")
			}

			after, _ := json.Marshal(content)
			if !bytes.Equal(before, after) {
				t.Error("projection mutated the original ADK content")
			}
		})
	}
}

func TestMappingErrorsIdentifyAgent(t *testing.T) {
	t.Parallel()
	for _, response := range []*typesafe.Response{nil, answer("billing", math.NaN())} {
		cfg := config(fakeAPI(func(context.Context, *typesafe.Request) (*typesafe.Response, error) {
			return response, nil
		}))
		cfg.State = func(agent.InvocationContext) (any, error) { return "Input", nil }
		calls := 0
		for event, err := range cfg.run(&agent.StrictContextMock{Ctx: t.Context()}) {
			calls++
			if event != nil || err == nil || !strings.Contains(err.Error(), "systemone agent: map assessment:") {
				t.Fatalf("missing caller context: event=%v err=%v", event, err)
			}
			var cause *json.UnsupportedValueError
			if response != nil && !errors.As(err, &cause) {
				t.Fatalf("lost encoding error: %v", err)
			}
		}
		if calls != 1 {
			t.Fatalf("expected one mapping failure, got %d", calls)
		}
	}
}

func TestRoutingFailsWithoutRunningChild(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		response *typesafe.Response
		err      error
	}{
		{"API failure", nil, errors.New("unavailable")},
		{"nil response", nil, nil},
		{"missing answer", &typesafe.Response{Model: "jev-test"}, nil},
		{"invalid confidence", answer("billing", math.NaN()), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			child := childAgent(t, "review_agent", &calls)
			cfg := config(
				fakeAPI(
					func(context.Context, *typesafe.Request) (*typesafe.Response, error) { return tc.response, tc.err },
				),
			)
			cfg.Routing = &Routing{
				Question: "department",
				Routes:   map[string]agent.Agent{"billing": child},
				Fallback: child,
			}
			a, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if len(a.SubAgents()) != 1 {
				t.Fatal("shared child not deduplicated")
			}
			_, _, err = runAgent(t, a, genai.NewContentFromText("text", genai.RoleUser))
			if err == nil || len(calls) != 0 {
				t.Fatalf("calls = %v; error = %v", calls, err)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("lost API error: %v", err)
			}
		})
	}
}

func TestConfigurationValidation(t *testing.T) {
	t.Parallel()
	for name, edit := range map[string]func(*Config){
		"missing API":       func(c *Config) { c.API = nil },
		"reserved name":     func(c *Config) { c.Name = "user" },
		"empty questions":   func(c *Config) { c.Questions = nil },
		"missing fallback":  func(c *Config) { c.Routing = &Routing{Question: "department"} },
		"wrong question":    func(c *Config) { c.Routing = &Routing{Question: "missing"} },
		"invalid threshold": func(c *Config) { c.Routing = &Routing{Question: "department", MinConfidence: math.NaN()} },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg := config(
				fakeAPI(
					func(context.Context, *typesafe.Request) (*typesafe.Response, error) { return answer("billing", 1), nil },
				),
			)
			edit(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}
