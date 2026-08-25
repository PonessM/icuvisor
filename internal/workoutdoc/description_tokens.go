package workoutdoc

import (
	"fmt"
	"strings"
)

// StructuralTokenInDescriptionError reports a structured step whose free-text
// label contains a token the Intervals.icu DSL treats as duration or distance.
type StructuralTokenInDescriptionError struct {
	Step    Step
	Token   string
	Kind    string
	Context string
}

func (e *StructuralTokenInDescriptionError) Error() string {
	if e == nil {
		return "step description contains a structural token"
	}
	context := e.Context
	if context == "" {
		context = "step"
	}
	kind := e.Kind
	if kind == "" {
		kind = "duration or distance"
	}
	if kind == "press-lap control" {
		return fmt.Sprintf("%s description contains %s token %q; set press_lap:true instead of including it in description", context, kind, e.Token)
	}
	return fmt.Sprintf("%s description contains %s token %q; put duration/distance in structured fields, not in description", context, kind, e.Token)
}

func descriptionStructuralTokenError(step Step, context string) error {
	if token, kind, ok := structuralTokenInDescription(step.Description); ok {
		return &StructuralTokenInDescriptionError{Step: step, Token: token, Kind: kind, Context: context}
	}
	return nil
}

func structuralTokenInDescription(description string) (token string, kind string, ok bool) {
	tokens := strings.Fields(description)
	for index, raw := range tokens {
		candidate := normalizeDescriptionToken(raw)
		if candidate == "" {
			continue
		}
		if index+1 < len(tokens) && strings.EqualFold(candidate, "press") && strings.EqualFold(normalizeDescriptionToken(tokens[index+1]), "lap") {
			return "Press lap", "press-lap control", true
		}
		lower := strings.ToLower(candidate)
		if _, parsed := parseDurationToken(lower); parsed {
			return candidate, "duration", true
		}
		if _, parsed := parseDistanceToken(lower); parsed {
			return candidate, "distance", true
		}
	}
	return "", "", false
}

func normalizeDescriptionToken(token string) string {
	return strings.Trim(token, " \t\r\n.,;:!?()[]{}<>\"'`")
}
