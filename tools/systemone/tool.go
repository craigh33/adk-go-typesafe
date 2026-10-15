package systemone

import (
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"

	"github.com/craigh33/adk-go-typesafe/internal/mappers"
	"github.com/craigh33/adk-go-typesafe/typesafe"
)

// EvaluationAPI is the portion of the TypeSafe client used by the tool.
type EvaluationAPI = typesafe.Evaluator

// Config defines questions and rubrics for an ADK evaluation tool.
type Config struct {
	API         EvaluationAPI
	Name        string
	Description string
	Model       string

	// Questions must not be mutated after construction, including nested values.
	Questions map[string]typesafe.Question
}

// Input contains text and optional ADK image artifact names.
type Input struct {
	State     string   `json:"state"               jsonschema:"Text to evaluate against the configured questions"`
	Artifacts []string `json:"artifacts,omitempty" jsonschema:"Names of PNG or JPEG artifacts to evaluate with the text"`
}

// New creates an ADK function tool with application-defined questions.
// Name defaults to evaluate_systemone. Model defaults to the API client's model.
//
//nolint:gocognit
func New(cfg Config) (tool.Tool, error) {
	if cfg.API == nil {
		return nil, errors.New("systemone tool: API is required")
	}

	request := typesafe.Request{
		State:     "",
		Model:     cfg.Model,
		Questions: maps.Clone(cfg.Questions),
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("systemone tool: %w", err)
	}

	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = "evaluate_systemone"
	}

	if cfg.Description == "" {
		cfg.Description = "Evaluate text and optional image artifacts using predefined System One questions " +
			"and return answers with probabilities and confidence."
	}

	return functiontool.New(functiontool.Config{
		Name:        cfg.Name,
		Description: cfg.Description,
	}, func(ctx agent.Context, input Input) (map[string]any, error) {
		call := request
		call.State = input.State

		var store agent.Artifacts
		if len(input.Artifacts) != 0 {
			store = ctx.Artifacts()
		}

		images := make(map[string]string, len(input.Artifacts))
		call.Assets = make(map[string]typesafe.Image, len(input.Artifacts))

		for _, name := range input.Artifacts {
			switch {
			case store == nil:
				return nil, errors.New("systemone tool: an ADK artifact service is required for images")
			case images[name] != "":
				continue
			}

			artifact, err := store.Load(ctx, name)
			switch {
			case err != nil:
				return nil, fmt.Errorf("systemone tool: load artifact %q: %w", name, err)
			case artifact == nil || artifact.Part == nil || artifact.Part.InlineData == nil:
				return nil, fmt.Errorf("systemone tool: artifact %q must contain inline image data", name)
			}

			blob := artifact.Part.InlineData
			switch blob.MIMEType {
			case "image/png", "image/jpeg", "image/jpg":
			default:
				return nil, fmt.Errorf("systemone tool: artifact %q must contain a PNG or JPEG image", name)
			}

			if len(blob.Data) == 0 {
				return nil, fmt.Errorf("systemone tool: artifact %q has no image data", name)
			}

			reference := rand.Text()
			images[name] = reference
			call.Assets[reference] = typesafe.Image{Data: blob.Data}
		}

		if len(images) != 0 {
			call.State = map[string]any{"text": input.State, "images": images}
		}

		response, err := cfg.API.Evaluate(ctx, &call)
		if err != nil {
			return nil, fmt.Errorf("systemone tool: evaluate: %w", err)
		}

		record, err := mappers.ResponseMap(response)
		if err != nil {
			return nil, fmt.Errorf("systemone tool: map assessment: %w", err)
		}

		return record, nil
	})
}
