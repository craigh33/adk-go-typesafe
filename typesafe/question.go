package typesafe

import (
	"encoding/json"

	apitypes "github.com/craigh33/adk-go-typesafe/internal/typesafe"
)

const (
	choiceType = "choice"
	scoreType  = "score"
	noulType   = "noul"
	boxType    = "box"
)

// Question is a Choice, Score, Noul, or Box question.
// JSON encoding sets the type discriminator automatically.
type Question interface {
	json.Marshaler
	kind() string
}

// Choice selects one of up to 255 named options. Instructions and descriptions
// accept strings, objects, arrays, or nil.
type Choice apitypes.ChoiceQuestion

func (q Choice) kind() string { return choiceType }

func (q Choice) MarshalJSON() ([]byte, error) {
	wire := apitypes.ChoiceQuestion(q)
	wire.Type = q.kind()
	return json.Marshal(wire)
}

// Score evaluates ordered levels, from zero to len(Criteria)-1. Instructions may
// be nil; level descriptions must be strings, objects, or arrays.
type Score apitypes.ScoreQuestion

func (q Score) kind() string { return scoreType }

func (q Score) MarshalJSON() ([]byte, error) {
	wire := apitypes.ScoreQuestion(q)
	wire.Type = q.kind()
	return json.Marshal(wire)
}

// Noul evaluates a yes/no question and returns the probability of yes.
type Noul apitypes.NoulQuestion

func (q Noul) kind() string { return noulType }

func (q Noul) MarshalJSON() ([]byte, error) {
	wire := apitypes.NoulQuestion(q)
	wire.Type = q.kind()
	return json.Marshal(wire)
}

// Box locates one target in an image. Requires image assets and Jev 1.14 or later.
type Box apitypes.BoxQuestion

func (q Box) kind() string { return boxType }

func (q Box) MarshalJSON() ([]byte, error) {
	wire := apitypes.BoxQuestion(q)
	wire.Type = q.kind()
	return json.Marshal(wire)
}

// NoulCriteria describes what yes and no mean for a Noul question.
type NoulCriteria = apitypes.NoulCriteria
