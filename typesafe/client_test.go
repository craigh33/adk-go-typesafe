package typesafe

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const mixedResponse = `{"model":"jev-test","answers":{
"route":{"type":"choice","choice":"billing","confidence":0.8,"probabilities":{"billing":0.9,"support":0.1}},
"priority":{"type":"score","score":0.0,"confidence":1.0,"legend":{"0":"low","1":{"severity":"high"}},"probabilities":{"0":1.0,"1":0.0}},
"urgent":{"type":"noul","noul":0.0}},"usage":{"input_tokens":123,"output_tokens":0}}`

const mixedRequestJSON = `{"model":"jev-latest",
"state":{"message":"Please refund the duplicate charge","history":["hello",null]},
"questions":{
"route":{"type":"choice","instructions":{"task":"Choose a team"},"criteria":{"billing":null,"support":["technical"]}},
"priority":{"type":"score","instructions":"Rate urgency","criteria":["low",{"severity":"high"}]},
"urgent":{"type":"noul","instructions":"Is it urgent?","criteria":{"true":"Time sensitive","false":"Can wait"}}}}`

func mixedRequest() *Request {
	return &Request{
		State: map[string]any{"message": "Please refund the duplicate charge", "history": []any{"hello", nil}},
		Questions: map[string]Question{
			"route": Choice{
				Instructions: map[string]any{"task": "Choose a team"},
				Criteria:     map[string]any{"billing": nil, "support": []any{"technical"}},
			},
			"priority": &Score{
				Instructions: "Rate urgency",
				Criteria:     []any{"low", map[string]any{"severity": "high"}},
			},
			"urgent": Noul{
				Instructions: "Is it urgent?",
				Criteria:     &NoulCriteria{True: "Time sensitive", False: "Can wait"},
			},
		},
	}
}

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(&Options{APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestEvaluateMixedRequest(t *testing.T) {
	t.Parallel()
	request := mixedRequest()
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
			t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("missing authentication or content type")
		}
		var body, want map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if err := json.Unmarshal([]byte(mixedRequestJSON), &want); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("request = %#v, want %#v", body, want)
		}
		fmt.Fprint(w, mixedResponse)
	})
	response, err := client.Evaluate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	choice, ok := response.Answers["route"].(ChoiceAnswer)
	if !ok || choice.Choice != "billing" || choice.Confidence != 0.8 || choice.Probabilities["support"] != 0.1 {
		t.Fatalf("choice lost data: %#v", response.Answers["route"])
	}
	score, ok := response.Answers["priority"].(ScoreAnswer)
	if !ok || score.Score != 0 || !reflect.DeepEqual(score.Legend["1"], map[string]any{"severity": "high"}) {
		t.Fatalf("score lost data: %#v", response.Answers["priority"])
	}
	noul, ok := response.Answers["urgent"].(NoulAnswer)
	if !ok || noul.Noul != 0 || response.Usage.OutputTokens != 0 || response.Model != "jev-test" {
		t.Fatalf("response lost zero values or metadata: %#v", response)
	}
	if request.Model != "" {
		t.Fatal("Evaluate mutated the request")
	}
}

func TestModelOverrides(t *testing.T) {
	t.Parallel()
	for _, override := range []string{"", "jev-pinned"} {
		t.Run(override, func(t *testing.T) {
			t.Parallel()
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model string `json:"model"`
				}
				json.NewDecoder(r.Body).Decode(&body)
				want := "jev-client"
				if override != "" {
					want = override
				}
				if body.Model != want {
					t.Errorf("model = %q, want %q", body.Model, want)
				}
				fmt.Fprint(w, mixedResponse)
			})
			client.model = "jev-client"
			request := mixedRequest()
			request.Model = override
			if _, err := client.Evaluate(t.Context(), request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEvaluateConcurrent(t *testing.T) {
	t.Parallel()
	request := mixedRequest()
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, mixedResponse) })
	for i := range 8 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			response, err := client.Evaluate(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Answers) != len(request.Questions) || request.Model != "" {
				t.Fatal("concurrent evaluation lost answers or changed the shared request")
			}
		})
	}
}

func TestEvaluateInvalidRequests(t *testing.T) {
	t.Parallel()
	var nilChoice *Choice
	var nilBox *Box
	many := make(map[string]any)
	for i := range 256 {
		many[strconv.Itoa(i)] = nil
	}
	tests := map[string]*Request{
		"nil":           nil,
		"nil state":     {Questions: map[string]Question{"a": Noul{}}},
		"numeric state": {State: 5, Questions: map[string]Question{"a": Noul{}}},
		"no questions":  {State: "text"},
		"nil question":  {State: "text", Questions: map[string]Question{"a": nil}},
		"typed nil":     {State: "text", Questions: map[string]Question{"a": nilChoice}},
		"typed nil box": {State: "text", Questions: map[string]Question{"a": nilBox}},
		"empty choice":  {State: "text", Questions: map[string]Question{"a": Choice{}}},
		"large choice":  {State: "text", Questions: map[string]Question{"a": Choice{Criteria: many}}},
		"empty score":   {State: "text", Questions: map[string]Question{"a": Score{}}},
		"null level": {
			State:     "text",
			Questions: map[string]Question{"a": Score{Criteria: []any{"low", nil}}},
		},
		"numeric instructions": {State: "text", Questions: map[string]Question{"a": Noul{Instructions: 5}}},
		"numeric box instructions": {State: "text", Questions: map[string]Question{
			"a": Box{Instructions: 5},
		}},
		"unencodable state": {State: make(chan int), Questions: map[string]Question{"a": Noul{}}},
	}
	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	t.Cleanup(func() {
		if calls.Load() != 0 {
			t.Error("invalid requests reached the server")
		}
	})
	for name, request := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := client.Evaluate(t.Context(), request); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestEvaluateBoxAnswer(t *testing.T) {
	t.Parallel()

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 280, 280))); err != nil {
		t.Fatal(err)
	}

	request := mixedRequest()
	request.Model = "jev-1.14.0"
	request.State = map[string]any{"photo": "photo-reference"}
	request.Assets = map[string]Image{"photo-reference": {Data: encoded.Bytes()}}
	request.Questions["bench"] = Box{
		Type:         "ignored",
		Instructions: map[string]any{"target": "the park bench in `photo`"},
	}

	const answerJSON = `{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":0.8},"confidence":0.92}`
	body := strings.Replace(mixedResponse, `"answers":{`, `"answers":{"bench":`+answerJSON+`,`, 1)
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Typesafe-Danger-Zone-Images") != "" {
			t.Error("box request automatically enabled an image flag")
		}

		var input struct {
			Model     string                     `json:"model"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}

		var question struct {
			Type         string            `json:"type"`
			Instructions map[string]string `json:"instructions"`
		}
		if err := json.Unmarshal(input.Questions["bench"], &question); err != nil {
			t.Error(err)
		}

		if input.Model != request.Model || question.Type != "box" ||
			question.Instructions["target"] != "the park bench in `photo`" {
			t.Errorf("box request lost model, type or instructions: %+v", input)
		}

		fmt.Fprint(w, body)
	})

	response, err := client.Evaluate(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}

	answer, ok := response.Answers["bench"].(BoxAnswer)
	if !ok || answer.Type != "box" || answer.Box != (BoxCoordinates{Top: 0, Bottom: 1, Left: 0, Right: 0.8}) {
		t.Fatalf("box lost coordinates or type: %#v", response.Answers["bench"])
	}

	if answer.Confidence == nil || *answer.Confidence != 0.92 {
		t.Fatalf("confidence lost: %v", answer.Confidence)
	}
}

func TestBoxAnswerConfidence(t *testing.T) {
	t.Parallel()

	for _, confidence := range []string{"null", "0", "0.92", "1"} {
		t.Run(confidence, func(t *testing.T) {
			t.Parallel()

			body := `{"model":"jev-1.14.0","answers":{"target":{` +
				`"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1},"confidence":` + confidence +
				`}},"usage":{"input_tokens":1,"output_tokens":1}}`
			var response Response
			if err := json.Unmarshal([]byte(body), &response); err != nil {
				t.Fatal(err)
			}

			answer, ok := response.Answers["target"].(BoxAnswer)
			if !ok {
				t.Fatalf("unexpected answer: %#v", response.Answers["target"])
			}

			var want *float64
			if err := json.Unmarshal([]byte(confidence), &want); err != nil ||
				!reflect.DeepEqual(answer.Confidence, want) {
				t.Fatalf("confidence changed: %v", answer.Confidence)
			}

			data, err := json.Marshal(&response)
			if err != nil {
				t.Fatal(err)
			}

			var roundTrip Response
			if err := json.Unmarshal(data, &roundTrip); err != nil || !reflect.DeepEqual(response, roundTrip) {
				t.Fatalf("box response changed after JSON round trip: %v", err)
			}
		})
	}
}

func TestBoxDoesNotEnableImageFlags(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Typesafe-Danger-Zone-Images") != "" {
			t.Error("box request automatically enabled an image flag")
		}

		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}

		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"detail":"box questions need an image"}`)
	})
	client.retry = RetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}

	request := &Request{State: "", Questions: map[string]Question{"target": &Box{}}}
	_, err := client.Evaluate(t.Context(), request)
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusUnprocessableEntity || attempts.Load() != 2 {
		t.Fatalf("box request or retry failed: %v (attempts: %d)", err, attempts.Load())
	}
}

func TestEvaluateRejectsMalformedBoxes(t *testing.T) {
	t.Parallel()

	const valid = `{"type":"box","box":{"top":0,"bottom":1,"left":0,"right":1},"confidence":null}`
	tests := map[string]string{
		"missing box":         `{"type":"box","confidence":null}`,
		"null box":            `{"type":"box","box":null,"confidence":null}`,
		"missing confidence":  strings.Replace(valid, `,"confidence":null`, "", 1),
		"negative confidence": strings.Replace(valid, `"confidence":null`, `"confidence":-0.1`, 1),
		"high confidence":     strings.Replace(valid, `"confidence":null`, `"confidence":1.1`, 1),
		"string confidence":   strings.Replace(valid, `"confidence":null`, `"confidence":"0.9"`, 1),
		"reversed vertical": strings.NewReplacer(
			`"top":0`, `"top":0.8`, `"bottom":1`, `"bottom":0.2`,
		).Replace(valid),
		"reversed horizontal": strings.NewReplacer(
			`"left":0`, `"left":0.8`, `"right":1`, `"right":0.2`,
		).Replace(valid),
		"zero height": strings.Replace(valid, `"bottom":1`, `"bottom":0`, 1),
		"zero width":  strings.Replace(valid, `"right":1`, `"right":0`, 1),
		"wrong type":  `{"type":"noul","noul":0.5}`,
	}
	for _, edge := range []string{"top", "bottom", "left", "right"} {
		for _, value := range []string{"null", "-0.1", "1.1", `"0.5"`} {
			original := `"` + edge + `":0`
			if edge == "bottom" || edge == "right" {
				original = `"` + edge + `":1`
			}

			tests[edge+" "+value] = strings.Replace(valid, original, `"`+edge+`":`+value, 1)
		}

		tests["missing "+edge] = strings.Replace(valid, `"`+edge+`":`, `"ignored":`, 1)
	}

	for name, answer := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprint(w, `{"model":"jev-1.14.0","answers":{"target":`+answer+
					`},"usage":{"input_tokens":1,"output_tokens":1}}`)
			})
			request := &Request{
				State:     "photo-reference",
				Questions: map[string]Question{"target": Box{Instructions: "the target"}},
			}
			if _, err := client.Evaluate(t.Context(), request); err == nil {
				t.Error("accepted malformed box response")
			}
		})
	}
}

func TestEvaluateRejectsMalformedResponses(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"invalid JSON": `{`,
		"missing answer": strings.Replace(
			mixedResponse,
			`"urgent":{"type":"noul","noul":0.0}`,
			`"other":{"type":"noul","noul":0.0}`,
			1,
		),
		"missing probability": strings.Replace(mixedResponse, `"noul":0.0`, `"ignored":0.0`, 1),
		"null probability":    strings.Replace(mixedResponse, `"noul":0.0`, `"noul":null`, 1),
		"out of range":        strings.Replace(mixedResponse, `"noul":0.0`, `"noul":1.1`, 1),
		"wrong type": strings.Replace(
			mixedResponse,
			`"urgent":{"type":"noul","noul":0.0}`,
			`"urgent":{"type":"choice","choice":"billing","confidence":1,"probabilities":{"billing":1}}`,
			1,
		),
		"unknown type":         strings.Replace(mixedResponse, `"type":"noul"`, `"type":"future"`, 1),
		"unexpected choice":    strings.Replace(mixedResponse, `"choice":"billing"`, `"choice":"admin"`, 1),
		"null distribution":    strings.Replace(mixedResponse, `"1":0.0`, `"1":null`, 1),
		"negative probability": strings.Replace(mixedResponse, `"billing":0.9`, `"billing":-0.5`, 1),
		"invalid legend":       strings.Replace(mixedResponse, `"0":"low"`, `"0":null`, 1),
		"missing usage":        strings.Replace(mixedResponse, `"output_tokens":0`, `"ignored":0`, 1),
		"negative usage":       strings.Replace(mixedResponse, `"output_tokens":0`, `"output_tokens":-1`, 1),
		"score outside rubric": strings.Replace(mixedResponse, `"score":0.0`, `"score":2.0`, 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, body) })
			if _, err := client.Evaluate(t.Context(), mixedRequest()); err == nil {
				t.Error("accepted malformed response")
			}
		})
	}
}

func TestAPIErrors(t *testing.T) {
	t.Parallel()
	for _, status := range []int{401, 422, 429, 529} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.Header().Set("Retry-After", "3")
				w.WriteHeader(status)
				fmt.Fprint(w, `{"detail":"private request content"}`)
			})
			_, err := client.Evaluate(t.Context(), mixedRequest())
			var apiError *APIError
			if !errors.As(err, &apiError) || apiError.StatusCode != status || apiError.RetryAfter != "3" {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(apiError.Body, "private request content") ||
				strings.Contains(err.Error(), "private request content") {
				t.Fatal("error body handling is wrong")
			}
			if calls.Load() != 1 {
				t.Fatal("unexpected automatic retry")
			}
		})
	}
}

func TestCancellationAndTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	client := testClient(t, func(_ http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Evaluate(ctx, mixedRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err := client.Evaluate(ctx, mixedRequest()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline lost: %v", err)
	}
}

func TestEvaluatePreservesRoundedProbabilities(t *testing.T) {
	t.Parallel()
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, strings.Replace(mixedResponse, `"billing":0.9`, `"billing":0.8995`, 1))
	})
	response, err := client.Evaluate(t.Context(), mixedRequest())
	if err != nil {
		t.Fatal(err)
	}
	choice, ok := response.Answers["route"].(ChoiceAnswer)
	if !ok || choice.Probabilities["billing"] != 0.8995 {
		t.Fatal("changed the API's rounded probabilities")
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	t.Parallel()
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer target.Close()
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	_, err := client.Evaluate(t.Context(), mixedRequest())
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.StatusCode != http.StatusTemporaryRedirect || redirected.Load() {
		t.Fatalf("redirect followed: %v", err)
	}
}

func TestResponseSizeLimit(t *testing.T) {
	t.Parallel()
	client := testClient(
		t,
		func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, strings.Repeat(" ", maxResponseBytes+1)) },
	)
	if _, err := client.Evaluate(t.Context(), mixedRequest()); err == nil || !strings.Contains(err.Error(), "8 MiB") {
		t.Fatalf("size limit not enforced: %v", err)
	}
}

func TestRequestHeaders(t *testing.T) {
	t.Parallel()

	headers := http.Header{
		"Typesafe-Danger-Zone-Images":       {"true"},
		"Typesafe-Danger-Zone-Long-Context": {"true"},
		"X-Custom":                          {"one", "two"},
		"accept":                            {"application/json; profile=custom"},
		"Authorization":                     {"Bearer custom-token"},
		"Content-Type":                      {"application/json; charset=utf-8"},
	}
	want := headers.Clone()
	var calls atomic.Int32
	client, err := New(&Options{
		APIKey:  "test-key",
		Headers: headers,
		Retry:   &RetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			for name, values := range want {
				if !reflect.DeepEqual(request.Header.Values(name), values) {
					t.Errorf("header %q = %v, want %v", name, request.Header.Values(name), values)
				}
			}

			if request.Header.Get("X-Added") != "" {
				t.Error("client retained the caller's header map")
			}

			request.Header.Set("Typesafe-Danger-Zone-Images", "transport changed")
			request.Header["X-Custom"][0] = "transport changed"

			status := http.StatusOK
			if calls.Add(1) == 1 {
				status = http.StatusTooManyRequests
			}

			return &http.Response{
				StatusCode: status,
				Body:       io.NopCloser(strings.NewReader(mixedResponse)),
				Header:     make(http.Header),
			}, nil
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	headers.Set("Typesafe-Danger-Zone-Images", "caller changed")
	headers["X-Custom"][0] = "caller changed"
	headers.Set("X-Added", "later")

	for range 2 {
		if _, err := client.Evaluate(t.Context(), mixedRequest()); err != nil {
			t.Fatal(err)
		}
	}

	if calls.Load() != 3 {
		t.Fatalf("requests including retries = %d, want 3", calls.Load())
	}
}

func TestNewRejectsDuplicateHeaders(t *testing.T) {
	t.Parallel()

	for _, headers := range []http.Header{
		{"X-Tag": {"first"}, "x-tag": {"second"}},
		{"Accept": {"application/json"}, "accept": {"text/plain"}},
		{"X-Tag": nil, "x-tag": {"second"}},
	} {
		if _, err := New(&Options{APIKey: "test-key", Headers: headers}); err == nil ||
			!strings.Contains(err.Error(), "duplicate header") {
			t.Errorf("headers = %v, error = %v", headers, err)
		}
	}
}

func TestNewOptions(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", " env-key ")
	client, err := New(nil)
	if err != nil || client.headers.Get("Authorization") != "Bearer env-key" || client.http.Timeout != defaultTimeout {
		t.Fatalf("defaults: %v", err)
	}
	provided := &http.Client{Timeout: time.Second}
	client, err = New(&Options{APIKey: "explicit", HTTPClient: provided, BaseURL: "https://example.com/prefix/"})
	if err != nil || client.headers.Get("Authorization") != "Bearer explicit" ||
		client.endpoint != "https://example.com/prefix/v1/systemone" {
		t.Fatalf("options: %v", err)
	}
	if provided.CheckRedirect != nil {
		t.Fatal("mutated the caller's HTTP client")
	}
	t.Setenv("TYPESAFE_API_KEY", "")
	if _, err := New(nil); err == nil {
		t.Fatal("missing key accepted")
	}
	for _, key := range []string{"key\nsecret", "key space", "héllo"} {
		if _, err := New(&Options{APIKey: key}); err == nil || strings.Contains(err.Error(), key) {
			t.Fatal("invalid key accepted or disclosed")
		}
	}
	for _, base := range []string{"relative", "ftp://example.com", "https://u:p@example.com", "https://example.com?q=1", "https://example.com/#x"} {
		if _, err := New(&Options{APIKey: "key", BaseURL: base}); err == nil {
			t.Errorf("invalid base URL accepted: %s", base)
		}
	}
}

//nolint:gocognit
func TestEvaluateImageAssets(t *testing.T) {
	t.Parallel()

	for _, format := range []string{"png", "jpeg"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()

			var encoded bytes.Buffer
			picture := image.NewGray(image.Rect(0, 0, 280, 280))
			var err error
			if format == "png" {
				err = png.Encode(&encoded, picture)
			} else {
				err = jpeg.Encode(&encoded, picture, nil)
			}

			if err != nil {
				t.Fatal(err)
			}

			original := bytes.Clone(encoded.Bytes())
			request := mixedRequest()
			request.State = map[string]any{"photos": []any{"image-state"}}
			request.Questions["route"] = Choice{
				Instructions: map[string]any{"compare": "Compare with image-instructions."},
				Criteria:     map[string]any{"billing": "image-criteria", "support": "Other"},
			}
			request.Questions["priority"] = Score{Criteria: []any{"image-criteria", "High"}}
			request.Questions["urgent"] = Noul{Criteria: &NoulCriteria{True: []any{"image-criteria"}}}
			request.Assets = map[string]Image{
				"image-state":        {Data: encoded.Bytes()},
				"image-instructions": {Data: encoded.Bytes()},
				"image-criteria":     {Data: encoded.Bytes()},
			}

			pointerJSON, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}

			valueJSON, err := json.Marshal(*request)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(valueJSON, pointerJSON) {
				t.Fatal("request value and pointer encoded different image payloads")
			}

			var attempts atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Typesafe-Danger-Zone-Images") != "" {
					t.Error("image request automatically enabled an image flag")
				}

				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("image request is not JSON")
				}

				var body struct {
					State     any                        `json:"state"`
					Questions map[string]json.RawMessage `json:"questions"`
					Assets    map[string]struct {
						Type       string `json:"type"`
						Format     string `json:"format"`
						DataBase64 string `json:"data_base64"`
						Part       string `json:"part"`
					} `json:"assets"`
				}

				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}

				if !reflect.DeepEqual(body.State, request.State) || len(body.Assets) != len(request.Assets) {
					t.Error("image state or assets lost")
				}

				for reference := range request.Assets {
					asset := body.Assets[reference]
					data, err := base64.StdEncoding.Strict().DecodeString(asset.DataBase64)
					if err != nil || !bytes.Equal(data, original) || asset.Type != "image" ||
						asset.Format != format || asset.Part != "" {
						t.Errorf("asset %q lost its format or image data", reference)
					}
				}

				for name, want := range request.Questions {
					var got, expected any
					data, err := json.Marshal(want)
					if err != nil {
						t.Error(err)
					}
					json.Unmarshal(data, &expected)
					json.Unmarshal(body.Questions[name], &got)
					if !reflect.DeepEqual(got, expected) {
						t.Errorf("question %q lost its image references", name)
					}
				}

				if attempts.Add(1) == 1 {
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				fmt.Fprint(w, mixedResponse)
			})
			client.retry = RetryPolicy{MaxAttempts: 2, InitialBackoff: time.Millisecond, MaxBackoff: time.Millisecond}
			if _, err := client.Evaluate(t.Context(), request); err != nil {
				t.Fatal(err)
			}

			if attempts.Load() != 2 || request.Model != "" ||
				!bytes.Equal(request.Assets["image-state"].Data, original) {
				t.Error("retry failed or evaluation mutated the request")
			}
		})
	}
}

func TestEvaluateImagesAndTextConcurrent(t *testing.T) {
	t.Parallel()

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 280, 280))); err != nil {
		t.Fatal(err)
	}

	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}

		if r.Header.Get("Typesafe-Danger-Zone-Images") != "" {
			t.Error("request automatically enabled an image flag")
		}
		fmt.Fprint(w, mixedResponse)
	})
	for i := range 8 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()

			request := mixedRequest()
			request.Assets = map[string]Image{}
			if i%2 == 0 {
				request.State = "image-state"
				request.Assets["image-state"] = Image{Data: encoded.Bytes()}
			}

			if _, err := client.Evaluate(t.Context(), request); err != nil {
				t.Fatal(err)
			}
		})
	}
}

//nolint:gocognit
func TestValidateImageAssets(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, mixedResponse)
	})
	t.Cleanup(func() {
		if calls.Load() != 0 {
			t.Error("invalid image requests reached the server")
		}
	})
	for _, tc := range []struct {
		name          string
		width, height int
		format        string
		reference     string
		references    int
		size          int
		wantError     string
	}{
		{name: "minimum area", width: 280, height: 280},
		{name: "minimum side", width: 256, height: 307},
		{name: "maximum sides", width: 1568, height: 1568},
		{name: "minimum JPEG", width: 280, height: 280, format: "jpeg"},
		{name: "empty", wantError: "1 byte to 16 MiB"},
		{name: "invalid header", size: 100, wantError: "PNG or JPEG"},
		{name: "unsupported format", width: 280, height: 280, format: "gif", wantError: "PNG or JPEG"},
		{name: "file too large", size: (16 << 20) + 1, wantError: "1 byte to 16 MiB"},
		{name: "side too small", width: 255, height: 400, wantError: "256 to 1568"},
		{name: "width too large", width: 1569, height: 280, wantError: "256 to 1568"},
		{name: "height too large", width: 280, height: 1569, wantError: "256 to 1568"},
		{name: "area too small", width: 256, height: 256, wantError: "78400"},
		{name: "blank reference", width: 280, height: 280, reference: " \t", wantError: "references"},
		{name: "reference limit", width: 280, height: 280, reference: strings.Repeat("a", 1024)},
		{name: "long reference", width: 280, height: 280, reference: strings.Repeat("a", 1025), wantError: "references"},
		{name: "UTF-8 reference bytes", width: 280, height: 280, reference: strings.Repeat("é", 513), wantError: "references"},
		{name: "combined reference limit", width: 280, height: 280, references: 64},
		{name: "combined references too large", width: 280, height: 280, references: 65, wantError: "references"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var encoded bytes.Buffer
			if tc.width != 0 {
				picture := image.NewGray(image.Rect(0, 0, tc.width, tc.height))
				var err error
				switch tc.format {
				case "jpeg":
					err = jpeg.Encode(&encoded, picture, nil)
				case "gif":
					err = gif.Encode(&encoded, picture, nil)
				default:
					err = png.Encode(&encoded, picture)
				}

				if err != nil {
					t.Fatal(err)
				}
			} else {
				encoded.Write(make([]byte, tc.size))
			}

			reference := tc.reference
			if reference == "" {
				reference = "image-state"
			}

			request := mixedRequest()
			request.State = reference
			request.Assets = map[string]Image{reference: {Data: encoded.Bytes()}}
			if tc.references != 0 {
				request.Assets = make(map[string]Image, tc.references)
				for i := range tc.references {
					request.Assets[fmt.Sprintf("%01024d", i)] = Image{Data: encoded.Bytes()}
				}
			}

			err := request.Validate()
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("validation error = %v, want %q", err, tc.wantError)
			}

			if _, err := client.Evaluate(t.Context(), request); err == nil {
				t.Error("invalid image request was accepted")
			}
		})
	}
}

func TestValidateImageAssetSizeLimit(t *testing.T) {
	t.Parallel()

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 280, 280))); err != nil {
		t.Fatal(err)
	}

	data := make([]byte, maxImageBytes)
	copy(data, encoded.Bytes())

	for _, tc := range []struct {
		name       string
		size       int
		references int
		wantError  bool
	}{
		{name: "single maximum image", size: maxImageBytes, references: 1},
		{name: "encoded limit", size: 12 << 20, references: 2},
		{name: "base64 padding exceeds limit", size: (12 << 20) + 1, references: 2, wantError: true},
		{name: "shared image bytes", size: maxImageBytes, references: 32, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			request := mixedRequest()
			request.Assets = make(map[string]Image, tc.references)
			references := make([]string, tc.references)
			for i := range references {
				references[i] = "photo-" + strconv.Itoa(i)
				request.Assets[references[i]] = Image{Data: data[:tc.size]}
			}
			request.State = references

			err := request.Validate()
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "32 MiB") {
					t.Fatalf("oversized assets passed validation before encoding: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestImageRequestSizeLimit(t *testing.T) {
	t.Parallel()

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewGray(image.Rect(0, 0, 280, 280))); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		fmt.Fprint(w, mixedResponse)
	})
	request := mixedRequest()
	request.State = strings.Repeat("x", 32<<20) + "image-state"
	request.Assets = map[string]Image{"image-state": {Data: encoded.Bytes()}}
	if _, err := client.Evaluate(t.Context(), request); err == nil || !strings.Contains(err.Error(), "32 MiB") {
		t.Fatalf("request size limit not enforced: %v", err)
	}

	if calls.Load() != 0 {
		t.Error("oversized image request reached the server")
	}
}
