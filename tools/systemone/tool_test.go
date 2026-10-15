//nolint:cyclop // Runner tests raise package complexity.
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
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/craigh33/adk-go-typesafe/typesafe"
)

type fakeAPI struct {
	request  *typesafe.Request
	ctx      context.Context
	response *typesafe.Response
	err      error
}

func (f *fakeAPI) Evaluate(ctx context.Context, request *typesafe.Request) (*typesafe.Response, error) {
	f.request, f.ctx = request, ctx
	return f.response, f.err
}

type testContext struct{ agent.StrictContextMock }

func (c *testContext) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }

func (c *testContext) Artifacts() agent.Artifacts { return nil }

type runnableTool interface {
	Run(agent.Context, any) (map[string]any, error)
	ProcessRequest(agent.Context, *model.LLMRequest) error
}

func TestToolRoundTrip(t *testing.T) {
	t.Parallel()
	var response typesafe.Response
	if err := json.Unmarshal(
		[]byte(
			`{"model":"jev-test","answers":{"urgent":{"type":"noul","noul":0}},"usage":{"input_tokens":1,"output_tokens":0}}`,
		),
		&response,
	); err != nil {
		t.Fatal(err)
	}
	api := &fakeAPI{response: &response}
	value, err := New(
		Config{
			API:         api,
			Name:        "assess_urgency",
			Description: "Check urgency",
			Model:       "jev-pinned",
			Questions:   map[string]typesafe.Question{"urgent": typesafe.Noul{Instructions: "Urgent?"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := value.(runnableTool)
	if !ok {
		t.Fatal("tool is not executable by ADK")
	}
	ctx := &testContext{StrictContextMock: agent.StrictContextMock{Ctx: t.Context()}}
	req := &model.LLMRequest{}
	if err := tool.ProcessRequest(ctx, req); err != nil {
		t.Fatal(err)
	}
	if req.Tools["assess_urgency"] != value || req.Config == nil || len(req.Config.Tools) != 1 {
		t.Fatal("tool not registered in the LLM request")
	}
	decl := req.Config.Tools[0].FunctionDeclarations[0]
	data, err := json.Marshal(decl.ParametersJsonSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	json.Unmarshal(data, &schema)
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(props) != 2 || props["state"] == nil || props["artifacts"] == nil {
		t.Fatalf("unexpected generated input schema: %s", data)
	}
	result, err := tool.Run(ctx, map[string]any{"state": "No rush"})
	if err != nil {
		t.Fatal(err)
	}
	if api.ctx != ctx || api.request.State != "No rush" || api.request.Model != "jev-pinned" ||
		len(api.request.Questions) != 1 {
		t.Fatalf("wrong evaluation request: %#v", api.request)
	}
	data, err = json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var got typesafe.Response
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	answer, ok := got.Answers["urgent"].(typesafe.NoulAnswer)
	if !ok || answer.Noul != 0 || got.Model != "jev-test" || got.Usage.OutputTokens != 0 {
		t.Fatalf("lost response fields: %s", data)
	}
	for _, args := range []map[string]any{{}, {"state": 3}, {"state": "text", "questions": map[string]any{}}} {
		if _, err := tool.Run(ctx, args); err == nil {
			t.Errorf("accepted invalid arguments: %v", args)
		}
	}
}

//nolint:gocognit
func TestToolImageArtifacts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, owner string
		part        *genai.Part
		wantError   bool
	}{
		{name: "PNG", part: genai.NewPartFromBytes([]byte("picture"), "image/png")},
		{name: "JPEG", part: genai.NewPartFromBytes([]byte("picture"), "image/jpeg")},
		{name: "missing", wantError: true},
		{name: "other user", owner: "someone-else", part: genai.NewPartFromBytes([]byte("picture"), "image/png"), wantError: true},
		{name: "text", part: genai.NewPartFromText("not an image"), wantError: true},
		{name: "audio", part: genai.NewPartFromBytes([]byte("audio"), "audio/wav"), wantError: true},
		{name: "empty image", part: genai.NewPartFromBytes(nil, "image/png"), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			service := artifact.InMemoryService()
			if tc.part != nil {
				owner := tc.owner
				if owner == "" {
					owner = "user"
				}

				if _, err := service.Save(t.Context(), &artifact.SaveRequest{
					AppName: "test", UserID: owner, SessionID: "session", FileName: "photo.png", Part: tc.part,
				}); err != nil {
					t.Fatal(err)
				}
			}

			api := &fakeAPI{
				response: &typesafe.Response{
					Model: "jev-1.14.0",
					Answers: map[string]typesafe.Answer{
						"visible": typesafe.NoulAnswer{Type: "noul", Noul: 1},
						"target": typesafe.BoxAnswer{
							Type: "box",
							Box:  typesafe.BoxCoordinates{Top: 0, Bottom: 1, Left: 0, Right: 1},
						},
					},
				},
			}

			value, err := New(
				Config{
					API:   api,
					Model: "jev-1.14.0",
					Questions: map[string]typesafe.Question{
						"visible": typesafe.Noul{Instructions: "A person in images?"},
						"target":  typesafe.Box{Instructions: "the person in images"},
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}

			callable, ok := value.(runnableTool)
			if !ok {
				t.Fatal("tool cannot run")
			}

			a, err := agent.New(agent.Config{
				Name: "artifact_test",
				Run: func(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
					return func(yield func(*session.Event, error) bool) {
						toolCtx := agent.NewToolContext(ctx, "test-call", nil, nil)
						record, err := callable.Run(
							toolCtx,
							map[string]any{
								"state":     "Look at the photo",
								"artifacts": []string{"photo.png", "photo.png"},
							},
						)
						if err != nil {
							yield(nil, err)
							return
						}

						data, _ := json.Marshal(api.response)
						var want map[string]any
						json.Unmarshal(data, &want)
						if !reflect.DeepEqual(record, want) {
							t.Error("box coordinates or null confidence lost from tool response")
						}

						event := session.NewEvent(ctx, ctx.InvocationID())
						event.Author = "artifact_test"
						event.Content = genai.NewContentFromText("done", genai.RoleModel)
						yield(event, nil)
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}

			r, err := runner.New(runner.Config{
				AppName:           "test",
				Agent:             a,
				SessionService:    session.InMemoryService(),
				ArtifactService:   service,
				AutoCreateSession: true,
			})
			if err != nil {
				t.Fatal(err)
			}

			var runErr error
			for _, err := range r.Run(t.Context(), "user", "session", genai.NewContentFromText("Evaluate photo.png", genai.RoleUser), agent.RunConfig{}) {
				if err != nil {
					runErr = err
					break
				}
			}

			if tc.wantError {
				if runErr == nil || api.request != nil {
					t.Fatalf("error = %v, evaluated = %v", runErr, api.request != nil)
				}

				return
			}

			if runErr != nil || api.request == nil || len(api.request.Assets) != 1 {
				t.Fatalf("error = %v, request = %#v", runErr, api.request)
			}

			if _, ok := api.request.Questions["target"].(typesafe.Box); !ok || api.request.Model != "jev-1.14.0" {
				t.Error("box question or model lost")
			}

			state, ok := api.request.State.(map[string]any)
			if !ok || state["text"] != "Look at the photo" {
				t.Fatalf("state = %#v", api.request.State)
			}

			images, ok := state["images"].(map[string]string)
			if !ok || images["photo.png"] == "" ||
				!bytes.Equal(api.request.Assets[images["photo.png"]].Data, []byte("picture")) {
				t.Error("artifact name, reference or bytes lost")
			}
		})
	}
}

func TestToolImagesRequireArtifactService(t *testing.T) {
	t.Parallel()

	api := &fakeAPI{}
	value, err := New(Config{API: api, Questions: map[string]typesafe.Question{"a": typesafe.Noul{}}})
	if err != nil {
		t.Fatal(err)
	}

	callable, ok := value.(runnableTool)
	if !ok {
		t.Fatal("tool cannot run")
	}

	ctx := &testContext{StrictContextMock: agent.StrictContextMock{Ctx: t.Context()}}
	_, err = callable.Run(ctx, map[string]any{"state": "Photo", "artifacts": []string{"photo.png"}})
	if err == nil || !strings.Contains(err.Error(), "artifact service") || api.request != nil {
		t.Fatalf("error = %v, evaluated = %v", err, api.request != nil)
	}
}

func TestToolErrors(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatal("nil API accepted")
	}
	if _, err := New(Config{API: &fakeAPI{}}); err == nil {
		t.Fatal("empty questions accepted")
	}
	failure := errors.New("evaluation failed")
	for _, api := range []*fakeAPI{
		{err: failure}, {},
		{response: &typesafe.Response{Answers: map[string]typesafe.Answer{"a": typesafe.NoulAnswer{Noul: math.NaN()}}}},
	} {
		value, err := New(
			Config{API: api, Questions: map[string]typesafe.Question{"a": typesafe.Noul{Instructions: "Test?"}}},
		)
		if err != nil {
			t.Fatal(err)
		}
		tool, ok := value.(runnableTool)
		if !ok {
			t.Fatal("tool cannot run")
		}
		ctx := &testContext{StrictContextMock: agent.StrictContextMock{Ctx: t.Context()}}
		_, err = tool.Run(ctx, map[string]any{"state": "text"})
		if err == nil || (api.err != nil && !errors.Is(err, failure)) {
			t.Fatalf("upstream error/nil response lost: %v", err)
		}
		if api.err == nil && !strings.Contains(err.Error(), "systemone tool: map assessment:") {
			t.Fatalf("missing caller context: %v", err)
		}
		var cause *json.UnsupportedValueError
		if api.response != nil && !errors.As(err, &cause) {
			t.Fatalf("lost encoding error: %v", err)
		}
	}
}
