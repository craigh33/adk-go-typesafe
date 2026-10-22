package typesafe

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

func validateAnswers(req *Request, response *Response) error {
	if len(response.Answers) != len(req.Questions) {
		return errors.New("answer count does not match question count")
	}
	if response.Usage.InputTokens < 0 || response.Usage.OutputTokens < 0 {
		return errors.New("token usage must not be negative")
	}
	for name, question := range req.Questions {
		answer, ok := response.Answers[name]
		if !ok || answer.kind() != question.kind() {
			return fmt.Errorf("answer %q is missing or has the wrong type", name)
		}
		if err := validateAnswer(question, answer); err != nil {
			return fmt.Errorf("answer %q: %w", name, err)
		}
	}
	return nil
}

func validateAnswer(question Question, answer Answer) error {
	data, err := json.Marshal(question)
	if err != nil {
		return err
	}
	var wire struct {
		Criteria json.RawMessage `json:"criteria"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	switch value := answer.(type) {
	case ChoiceAnswer:
		var options map[string]json.RawMessage
		if err := json.Unmarshal(wire.Criteria, &options); err != nil {
			return err
		}
		if _, ok := options[value.Choice]; !ok {
			return errors.New("choice is not a configured option")
		}
		return validateProbabilities(value.Confidence, value.Probabilities, options)
	case ScoreAnswer:
		return validateScoreAnswer(wire.Criteria, value)
	case NoulAnswer:
		if value.Noul < 0 || value.Noul > 1 {
			return errors.New("noul must be between zero and one")
		}
	case BoxAnswer:
		box := value.Box
		switch {
		case min(box.Top, box.Bottom, box.Left, box.Right) < 0 || max(box.Top, box.Bottom, box.Left, box.Right) > 1:
			return errors.New("box edges must be between zero and one")
		case box.Top >= box.Bottom || box.Left >= box.Right:
			return errors.New("box must have top < bottom and left < right")
		case value.Confidence != nil && (*value.Confidence < 0 || *value.Confidence > 1):
			return errors.New("confidence must be between zero and one")
		}
	}
	return nil
}

func validateScoreAnswer(criteria json.RawMessage, value ScoreAnswer) error {
	var levels []json.RawMessage
	if err := json.Unmarshal(criteria, &levels); err != nil {
		return err
	}
	if value.Score < 0 || value.Score > float64(len(levels)-1) {
		return errors.New("score is outside the configured rubric")
	}
	if len(value.Legend) != len(levels) {
		return errors.New("score legend does not match the configured rubric")
	}
	options := make(map[string]json.RawMessage, len(levels))
	for index, level := range levels {
		key := strconv.Itoa(index)
		options[key] = level
		description, ok := value.Legend[key]
		if !ok {
			return errors.New("score legend is missing a level")
		}
		if err := validateContent(description, false); err != nil {
			return fmt.Errorf("score legend: %w", err)
		}
	}
	return validateProbabilities(value.Confidence, value.Probabilities, options)
}

func validateProbabilities(
	confidence float64,
	probabilities map[string]float64,
	options map[string]json.RawMessage,
) error {
	if confidence < 0 || confidence > 1 {
		return errors.New("confidence must be between zero and one")
	}
	if len(probabilities) != len(options) {
		return errors.New("probabilities do not match the configured criteria")
	}
	for key := range options {
		probability, ok := probabilities[key]
		if !ok || probability < 0 || probability > 1 {
			return fmt.Errorf("invalid or missing probability for %q", key)
		}
	}
	return nil
}
