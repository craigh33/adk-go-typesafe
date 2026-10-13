package contextfilter

import (
	"context"
	"encoding/json"
	"iter"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/craigh33/adk-go-typesafe/typesafe"
)

func call(id string) *genai.Content {
	return &genai.Content{
		Role: genai.RoleModel,
		Parts: []*genai.Part{
			{FunctionCall: &genai.FunctionCall{ID: id, Name: "lookup", Args: map[string]any{"query": "old"}}},
		},
	}
}

func result(id string) *genai.Content {
	return &genai.Content{
		Role: genai.RoleUser,
		Parts: []*genai.Part{
			{
				FunctionResponse: &genai.FunctionResponse{
					ID:       id,
					Name:     "lookup",
					Response: map[string]any{"value": "found"},
				},
			},
		},
	}
}

func TestProtectedGroups(t *testing.T) {
	t.Parallel()
	image := &genai.Content{
		Role:  genai.RoleUser,
		Parts: []*genai.Part{{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte("image payload")}}},
	}
	for _, tc := range []struct {
		name string
		old  []*genai.Content
		pin  bool
	}{
		{"preamble", []*genai.Content{reply("Earlier summary")}, false},
		{"system", []*genai.Content{user("Old"), {Role: "system", Parts: []*genai.Part{{Text: "Rules"}}}}, false},
		{"developer", []*genai.Content{user("Old"), {Role: "developer", Parts: []*genai.Part{{Text: "Rules"}}}}, false},
		{"image", []*genai.Content{image, reply("Description")}, false},
		{"thought", []*genai.Content{user("Old"), {Role: genai.RoleModel, Parts: []*genai.Part{{Thought: true, Text: "private reasoning"}}}}, false},
		{"missing call", []*genai.Content{user("Old"), result("missing")}, false},
		{"missing result", []*genai.Content{user("Old"), call("pending")}, false},
		{"cross turn pair", []*genai.Content{user("Old"), call("a"), user("Another"), result("a")}, false},
		{"duplicate calls", []*genai.Content{user("Old"), call("a"), user("Another"), call("a"), result("a")}, false},
		{"application pinned", []*genai.Content{user("Old"), reply("Keep this")}, true},
		{"nil message", []*genai.Content{user("Old"), nil}, false},
		{"nil part", []*genai.Content{user("Old"), {Role: genai.RoleModel, Parts: []*genai.Part{nil}}}, false},
		{"invalid call with a result", []*genai.Content{user("Old"), {Role: genai.RoleModel, Parts: []*genai.Part{{
			FunctionCall:     &genai.FunctionCall{ID: "a", Name: "lookup", Args: map[string]any{"value": math.NaN()}},
			FunctionResponse: &genai.FunctionResponse{ID: "a", Name: "lookup", Response: map[string]any{"found": true}},
		}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			current := user("Current")
			contents := append([]*genai.Content{}, tc.old...)
			contents = append(contents, user("Disposable"), reply("Unrelated"), current)
			api := evaluatorFunc(func(_ context.Context, req *typesafe.Request) (*typesafe.Response, error) {
				if len(req.Questions) != 1 {
					t.Fatalf("protected groups offered for removal: %+v", req.Questions)
				}
				encoded, _ := json.Marshal(req)
				if strings.Contains(string(encoded), "aW1hZ2UgcGF5bG9hZA==") ||
					strings.Contains(string(encoded), "private reasoning") {
					t.Fatal("opaque content sent to reviewer")
				}
				return scores(req), nil
			})
			cfg := testConfig(api)
			if tc.pin {
				cfg.Pin = func(_ agent.Context, content *genai.Content) bool { return content == tc.old[1] }
			}
			request := &model.LLMRequest{Contents: contents}
			report, err := apply(t, cfg, callbackContext{parent: t.Context(), input: current}, request)
			want := append(append([]*genai.Content{}, tc.old...), current)
			if err != nil || report.Err != nil || report.RemovedTurns != 1 ||
				!reflect.DeepEqual(request.Contents, want) {
				t.Fatalf("report=%+v err=%v", report, err)
			}
		})
	}
}

func TestRecentTurnsAndEmptyToolIDs(t *testing.T) {
	t.Parallel()
	current := user("Current")
	contents := []*genai.Content{user("Old"), call(""), result(""), user("Recent"), reply("Noted"), current}
	api := evaluatorFunc(func(_ context.Context, req *typesafe.Request) (*typesafe.Response, error) {
		if len(req.Questions) != 1 {
			t.Fatalf("recent turn offered for removal: %+v", req.Questions)
		}
		return scores(req), nil
	})
	cfg := testConfig(api)
	cfg.KeepRecentTurns = 2
	request := &model.LLMRequest{Contents: contents}
	report, err := apply(t, cfg, callbackContext{parent: t.Context(), input: current}, request)
	if err != nil || report.Err != nil || !reflect.DeepEqual(request.Contents, contents[3:]) {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestRolelessTurnBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, historyRole, currentRole string }{
		{"explicit user roles", genai.RoleUser, genai.RoleUser},
		{"roleless current input", genai.RoleUser, ""},
		{"roleless history", "", genai.RoleUser},
		{"all roleless", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			contents := []*genai.Content{
				user("Old"), call("a"), result("a"), reply("Done"),
				user("Recent"), reply("Noted"), user("Current"), call("b"), result("b"),
			}
			contents[0].Role, contents[4].Role, contents[6].Role = tc.historyRole, tc.historyRole, tc.currentRole
			for _, i := range []int{1, 2, 7, 8} {
				contents[i].Role = ""
			}
			before, _ := json.Marshal(contents)
			calls := 0
			api := evaluatorFunc(func(_ context.Context, req *typesafe.Request) (*typesafe.Response, error) {
				calls++
				state := req.State.(reviewState)
				if len(req.Questions) != 1 || req.Questions["0"] == nil || len(state.Groups) != 3 ||
					state.Groups[1].ID != "4" || state.Groups[2].ID != "6" ||
					!state.Groups[1].Pinned || !state.Groups[2].Pinned {
					t.Fatalf("incorrect user or tool boundaries: %+v", state.Groups)
				}
				return scores(req), nil
			})
			cfg := testConfig(api)
			cfg.KeepRecentTurns = 2
			request := &model.LLMRequest{Contents: contents}
			report, err := apply(t, cfg, callbackContext{parent: t.Context(), input: contents[6]}, request)
			if err != nil || report.Err != nil || calls != 1 || report.RemovedTurns != 1 ||
				!slices.Equal(request.Contents, contents[4:]) {
				t.Fatalf("calls=%d report=%+v err=%v", calls, report, err)
			}
			after, _ := json.Marshal(contents)
			if string(before) != string(after) {
				t.Fatal("original messages were changed")
			}
		})
	}
}

type stubSession struct {
	session.Session

	events session.Events
}

func (s stubSession) Events() session.Events { return s.events }

type stubEvents struct {
	session.Events

	events []*session.Event
}

func (e stubEvents) All() iter.Seq[*session.Event] { return slices.Values(e.events) }

func TestCompactionSummaryIsProtected(t *testing.T) {
	t.Parallel()
	summary := reply("Previously agreed constraints")
	event := &session.Event{
		Actions: session.EventActions{Compaction: &session.EventCompaction{CompactedContent: summary}},
	}
	current := user("Current")
	contents := []*genai.Content{
		user("Old"),
		reply("Previously agreed constraints"),
		user("Disposable"),
		reply("Done"),
		current,
	}
	api := evaluatorFunc(func(_ context.Context, req *typesafe.Request) (*typesafe.Response, error) {
		if len(req.Questions) != 1 {
			t.Fatal("summary offered for removal")
		}
		return scores(req), nil
	})
	request := &model.LLMRequest{Contents: contents}
	ctx := callbackContext{
		Context: &agent.ContextMock{},
		parent:  t.Context(), input: current,
		sess: stubSession{events: stubEvents{events: []*session.Event{event}}},
	}
	report, err := apply(t, testConfig(api), agent.NewCallbackContext(ctx, nil), request)
	if err != nil || report.Err != nil ||
		!reflect.DeepEqual(request.Contents, []*genai.Content{contents[0], contents[1], current}) {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}

func TestMissingSessionEvents(t *testing.T) {
	t.Parallel()
	api := evaluatorFunc(func(_ context.Context, req *typesafe.Request) (*typesafe.Response, error) {
		return scores(req), nil
	})
	for _, sess := range []session.Session{nil, stubSession{}, stubSession{events: stubEvents{}}} {
		current := user("Current")
		request := &model.LLMRequest{Contents: []*genai.Content{user("Old"), reply("Done"), current}}
		ctx := callbackContext{Context: &agent.ContextMock{}, parent: t.Context(), input: current, sess: sess}
		report, err := apply(t, testConfig(api), agent.NewCallbackContext(ctx, nil), request)
		if err != nil || report.Err != nil || report.RemovedTurns != 1 ||
			!slices.Equal(request.Contents, []*genai.Content{current}) {
			t.Fatalf("missing or empty event list: report=%+v err=%v", report, err)
		}
	}
}
