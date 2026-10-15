package typesafe

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // Register JPEG decoding.
	_ "image/png"  // Register PNG decoding.
	"net/http"
	"strings"

	apitypes "github.com/craigh33/adk-go-typesafe/internal/typesafe"
)

const (
	maxChoices = 255
	minLevels  = 1
	maxLevels  = 10

	maxImageBytes      = 16 << 20
	minImageDimension  = 256
	maxImageDimension  = 1568
	minImagePixels     = 78400
	maxAssetReference  = 1024
	maxAssetReferences = 64 << 10
)

// Image holds upright PNG or JPEG bytes, encoded as base64 without resizing or rotation.
type Image struct {
	Data []byte
}

// Request evaluates named questions against text or structured JSON state.
// An empty Model uses the client's default. Do not mutate a request while it is
// being evaluated.
type Request struct {
	State     any                 `json:"state"`
	Model     string              `json:"model"`
	Questions map[string]Question `json:"questions"`

	// Assets maps unique references to images. Each reference must appear in state,
	// instructions, or criteria. Use random references to avoid matching other text.
	Assets map[string]Image `json:"assets,omitempty"`
}

func (r *Request) MarshalJSON() ([]byte, error) {
	questions := make(map[string]apitypes.Question, len(r.Questions))
	for name, question := range r.Questions {
		data, err := json.Marshal(question)
		if err != nil {
			return nil, fmt.Errorf("question %q: %w", name, err)
		}

		var wire apitypes.Question
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, err
		}

		questions[name] = wire
	}

	wire := apitypes.SystemOneRequest{
		State:     r.State,
		Model:     r.Model,
		Questions: questions,
	}

	if len(r.Assets) != 0 {
		assets := make(map[string]apitypes.Asset, len(r.Assets))
		imageType := "image"

		for reference, asset := range r.Assets {
			data := base64.StdEncoding.EncodeToString(asset.Data)
			format := apitypes.AssetFormat(strings.TrimPrefix(http.DetectContentType(asset.Data), "image/"))

			assets[reference] = apitypes.Asset{
				Type:       &imageType,
				Format:     &format,
				DataBase64: &data,
			}
		}

		wire.Assets = &assets
	}

	return json.Marshal(wire)
}

// Validate checks JSON shapes, rubric limits, image headers and dimensions.
// The API validates image contents and reference usage in the request.
func (r *Request) Validate() error {
	if r == nil {
		return errors.New("typesafe: request is required")
	}

	if err := validateContent(r.State, false); err != nil {
		return fmt.Errorf("typesafe: state: %w", err)
	}

	if len(r.Questions) == 0 {
		return errors.New("typesafe: questions must not be empty")
	}

	for name, question := range r.Questions {
		if strings.TrimSpace(name) == "" {
			return errors.New("typesafe: question name must not be blank")
		}

		if err := validateQuestion(question); err != nil {
			return fmt.Errorf("typesafe: question %q: %w", name, err)
		}
	}

	referenceBytes := 0
	for reference, asset := range r.Assets {
		referenceBytes += len(reference)

		switch {
		case strings.TrimSpace(reference) == "" || len(reference) > maxAssetReference ||
			referenceBytes > maxAssetReferences:
			return errors.New(
				"typesafe: asset references must be nonblank, at most 1024 bytes each and 64 KiB combined",
			)
		case len(asset.Data) == 0 || len(asset.Data) > maxImageBytes:
			return fmt.Errorf("typesafe: asset %q: image must contain 1 byte to 16 MiB", reference)
		}

		config, format, err := image.DecodeConfig(bytes.NewReader(asset.Data))
		switch {
		case err != nil || (format != "png" && format != "jpeg"):
			return fmt.Errorf("typesafe: asset %q: expected a PNG or JPEG image", reference)
		case config.Width < minImageDimension || config.Height < minImageDimension ||
			config.Width > maxImageDimension || config.Height > maxImageDimension ||
			config.Width*config.Height < minImagePixels:
			return fmt.Errorf(
				"typesafe: asset %q: image sides must be 256 to 1568 pixels with at least 78400 total pixels",
				reference,
			)
		}
	}

	return nil
}

func validateContent(value any, nullable bool) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	data = bytes.TrimSpace(data)
	if nullable && bytes.Equal(data, []byte("null")) {
		return nil
	}
	if len(data) != 0 && (data[0] == '"' || data[0] == '{' || data[0] == '[') {
		return nil
	}
	return errors.New("expected a string, object, or array")
}

func validateQuestion(question Question) error {
	data, err := json.Marshal(question)
	if err != nil {
		return fmt.Errorf("encode question: %w", err)
	}
	var wire struct {
		Type         string          `json:"type"`
		Instructions json.RawMessage `json:"instructions"`
		Criteria     json.RawMessage `json:"criteria"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("decode question: %w", err)
	}
	if err := validateContent(wire.Instructions, true); err != nil {
		return fmt.Errorf("instructions: %w", err)
	}
	switch wire.Type {
	case choiceType:
		return validateChoiceCriteria(wire.Criteria)
	case scoreType:
		return validateScoreCriteria(wire.Criteria)
	case noulType:
		return validateNoulCriteria(wire.Criteria)
	case boxType:
		return nil
	default:
		return errors.New("question must be a non-nil Choice, Score, Noul, or Box")
	}
}

func validateChoiceCriteria(data json.RawMessage) error {
	var criteria map[string]json.RawMessage
	if err := json.Unmarshal(data, &criteria); err != nil {
		return fmt.Errorf("choice criteria: %w", err)
	}
	if len(criteria) == 0 || len(criteria) > maxChoices {
		return fmt.Errorf("choice requires 1 to %d options", maxChoices)
	}
	for _, value := range criteria {
		if err := validateContent(value, true); err != nil {
			return fmt.Errorf("choice criteria: %w", err)
		}
	}
	return nil
}

func validateScoreCriteria(data json.RawMessage) error {
	var criteria []json.RawMessage
	if err := json.Unmarshal(data, &criteria); err != nil {
		return fmt.Errorf("score criteria: %w", err)
	}
	if len(criteria) < minLevels || len(criteria) > maxLevels {
		return fmt.Errorf("score requires %d to %d levels", minLevels, maxLevels)
	}
	for _, value := range criteria {
		if err := validateContent(value, false); err != nil {
			return fmt.Errorf("score criteria: %w", err)
		}
	}
	return nil
}

func validateNoulCriteria(data json.RawMessage) error {
	if len(data) == 0 {
		return nil
	}
	var criteria NoulCriteria
	if err := json.Unmarshal(data, &criteria); err != nil {
		return fmt.Errorf("noul criteria: %w", err)
	}
	for _, value := range []any{criteria.True, criteria.False} {
		if err := validateContent(value, true); err != nil {
			return fmt.Errorf("noul criteria: %w", err)
		}
	}
	return nil
}
