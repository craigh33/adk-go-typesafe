package typesafe

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	apitypes "github.com/craigh33/adk-go-typesafe/internal/typesafe"
)

// Answer contains one of ChoiceAnswer, ScoreAnswer, NoulAnswer, or BoxAnswer.
type Answer interface {
	kind() string
}

// ChoiceAnswer contains the selected option and every option's probability.
type ChoiceAnswer apitypes.ChoiceAnswer

func (a ChoiceAnswer) kind() string { return choiceType }

// ScoreAnswer contains the probability-weighted level, its rubric, and uncertainty.
type ScoreAnswer apitypes.ScoreAnswer

func (a ScoreAnswer) kind() string { return scoreType }

// NoulAnswer contains the probability of yes, between zero and one.
type NoulAnswer apitypes.NoulAnswer

func (a NoulAnswer) kind() string { return noulType }

// BoxAnswer contains normalized image edges and optional confidence.
// A nil Confidence means the model did not provide confidence.
type BoxAnswer apitypes.BoxAnswer

func (a BoxAnswer) kind() string { return boxType }

// BoxCoordinates contains edges between zero and one, measured from the top-left.
type BoxCoordinates = apitypes.BoxCoordinates

// Usage reports the API's token counts.
type Usage apitypes.Usage

// Response holds answers under the original question names.
type Response struct {
	Model   string            `json:"model"`
	Answers map[string]Answer `json:"answers"`
	Usage   Usage             `json:"usage"`
}

// UnmarshalJSON decodes the API's discriminated answer types.
func (r *Response) UnmarshalJSON(data []byte) error {
	if err := requireFields(data, "model", "answers", "usage.input_tokens", "usage.output_tokens"); err != nil {
		return err
	}
	var wire apitypes.SystemOneResponse
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	if wire.Model == "" {
		return errors.New("response model is empty")
	}
	answers := make(map[string]Answer, len(wire.Answers))
	for name, raw := range wire.Answers {
		data, err := raw.MarshalJSON()
		if err != nil {
			return fmt.Errorf("answer %q: %w", name, err)
		}
		answer, err := decodeAnswer(data)
		if err != nil {
			return fmt.Errorf("answer %q: %w", name, err)
		}
		answers[name] = answer
	}
	*r = Response{Model: wire.Model, Answers: answers, Usage: Usage(wire.Usage)}
	return nil
}

//nolint:gocognit
func decodeAnswer(data []byte) (Answer, error) {
	var discriminator struct {
		Type       string          `json:"type"`
		Confidence json.RawMessage `json:"confidence"`
	}
	if err := json.Unmarshal(data, &discriminator); err != nil {
		return nil, err
	}
	switch discriminator.Type {
	case choiceType:
		if err := requireFields(data, "choice", "confidence", "probabilities"); err != nil {
			return nil, err
		}
		if err := requireProbabilities(data); err != nil {
			return nil, err
		}
		var answer ChoiceAnswer
		if err := json.Unmarshal(data, &answer); err != nil {
			return nil, err
		}
		return answer, nil
	case scoreType:
		if err := requireFields(data, "score", "confidence", "probabilities", "legend"); err != nil {
			return nil, err
		}
		if err := requireProbabilities(data); err != nil {
			return nil, err
		}
		var answer ScoreAnswer
		if err := json.Unmarshal(data, &answer); err != nil {
			return nil, err
		}
		return answer, nil
	case noulType:
		if err := requireFields(data, "noul"); err != nil {
			return nil, err
		}
		var answer NoulAnswer
		if err := json.Unmarshal(data, &answer); err != nil {
			return nil, err
		}
		return answer, nil
	case boxType:
		if err := requireFields(data, "box.top", "box.bottom", "box.left", "box.right"); err != nil {
			return nil, err
		}

		if len(discriminator.Confidence) == 0 {
			return nil, errors.New("missing required field \"confidence\"")
		}

		var answer BoxAnswer
		if err := json.Unmarshal(data, &answer); err != nil {
			return nil, err
		}

		return answer, nil
	default:
		return nil, fmt.Errorf("unknown answer type %q", discriminator.Type)
	}
}

func requireFields(data []byte, names ...string) error {
	for _, name := range names {
		value := json.RawMessage(data)
		for key := range strings.SplitSeq(name, ".") {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(value, &fields); err != nil {
				return err
			}
			value = fields[key]
			if len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return fmt.Errorf("missing required field %q", name)
			}
		}
	}
	return nil
}

func requireProbabilities(data []byte) error {
	var fields struct {
		Probabilities map[string]*float64 `json:"probabilities"`
	}
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for key, value := range fields.Probabilities {
		if value == nil {
			return fmt.Errorf("probability for %q must not be null", key)
		}
	}
	return nil
}
