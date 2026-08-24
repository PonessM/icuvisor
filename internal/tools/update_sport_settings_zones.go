package tools

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/ricardocabral/icuvisor/internal/intervals"
	"github.com/ricardocabral/icuvisor/internal/safety"
)

type updateSportSettingsZoneRequest struct {
	Kind          string    `json:"kind"`
	Boundaries    []float64 `json:"boundaries"`
	Names         []string  `json:"names,omitempty"`
	namesProvided bool
}

type updateSportSettingsZoneEcho struct {
	Kind       string    `json:"kind"`
	Boundaries []float64 `json:"boundaries"`
	Names      []string  `json:"names,omitempty"`
}

func ensureSportSettingsZonesAllowed(zonesProvided bool, capability safety.Capability) error {
	if !zonesProvided || capability.CanDelete() {
		return nil
	}
	return NewUserError(zoneOverwriteGateMessage, errors.New("zone overwrite requires delete capability"))
}

func validateSportSettingsZones(zones []updateSportSettingsZoneRequest) error {
	if len(zones) == 0 {
		return errors.New("zones must contain at least one zone definition when supplied")
	}
	seen := map[string]bool{}
	for _, zone := range zones {
		kind := normalizeZoneKind(zone.Kind)
		if kind == "" {
			return errors.New("zone kind must be power, hr, or pace")
		}
		if seen[kind] {
			return fmt.Errorf("duplicate %s zone definition", kind)
		}
		seen[kind] = true
		if len(zone.Boundaries) == 0 {
			return fmt.Errorf("%s zone boundaries are required", kind)
		}
		if kind == "power" && zone.namesProvided && len(zone.Names) == 0 {
			return fmt.Errorf("%s zone names must be a nonempty array when supplied", kind)
		}
		if len(zone.Names) > 0 && len(zone.Names) != len(zone.Boundaries) {
			return fmt.Errorf("%s zone names must match boundaries length", kind)
		}
		if kind == "power" {
			for index, boundary := range zone.Boundaries {
				if math.IsNaN(boundary) || math.IsInf(boundary, 0) || boundary <= 0 || boundary != math.Trunc(boundary) {
					return errors.New("power zone boundaries must be finite positive integer percentages of FTP")
				}
				if index > 0 && boundary <= zone.Boundaries[index-1] {
					return errors.New("power zone boundaries must be strictly increasing percentages of FTP")
				}
			}
			continue
		}
		for index, boundary := range zone.Boundaries {
			if kind == "pace" {
				if boundary <= 0 || boundary > 200 || math.IsNaN(boundary) || math.IsInf(boundary, 0) {
					return errors.New("pace zone boundaries must be finite percentages in (0, 200]")
				}
				if index > 0 && boundary <= zone.Boundaries[index-1] {
					return errors.New("pace zone boundaries must be strictly increasing percentages")
				}
				continue
			}
			if boundary < 0 {
				return fmt.Errorf("%s zone boundaries must be >= 0", kind)
			}
		}
	}
	return nil
}

func sportSettingsZoneDefinitions(zones []updateSportSettingsZoneRequest) []intervals.SportSettingsZoneDefinition {
	definitions := make([]intervals.SportSettingsZoneDefinition, 0, len(zones))
	for _, zone := range zones {
		definition := intervals.SportSettingsZoneDefinition{Kind: normalizeZoneKind(zone.Kind), Names: append([]string(nil), zone.Names...)}
		switch definition.Kind {
		case "power":
			definition.PowerUpperBoundsPercentOfFTP = sportSettingsIntegerBoundaries(zone.Boundaries)
		case "hr":
			definition.HRBoundariesBPM = sportSettingsIntegerBoundaries(zone.Boundaries)
		case "pace":
			definition.PaceBoundariesPercentOfThreshold = append([]float64(nil), zone.Boundaries...)
		}
		definitions = append(definitions, definition)
	}
	return definitions
}

func sportSettingsZoneEchoes(zones []intervals.SportSettingsZoneDefinition) []updateSportSettingsZoneEcho {
	echoes := make([]updateSportSettingsZoneEcho, 0, len(zones))
	for _, zone := range zones {
		boundaries := append([]float64(nil), zone.PaceBoundariesPercentOfThreshold...)
		if zone.Kind == "power" {
			boundaries = sportSettingsFloatBoundaries(zone.PowerUpperBoundsPercentOfFTP)
		}
		if zone.Kind == "hr" || zone.Kind == "heart_rate" {
			boundaries = sportSettingsFloatBoundaries(zone.HRBoundariesBPM)
		}
		echoes = append(echoes, updateSportSettingsZoneEcho{Kind: zone.Kind, Boundaries: boundaries, Names: append([]string(nil), zone.Names...)})
	}
	return echoes
}

func sportSettingsIntegerBoundaries(values []float64) []int {
	out := make([]int, 0, len(values))
	for _, value := range values {
		out = append(out, int(value))
	}
	return out
}

func sportSettingsFloatBoundaries(values []int) []float64 {
	out := make([]float64, 0, len(values))
	for _, value := range values {
		out = append(out, float64(value))
	}
	return out
}

func normalizeZoneKind(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "power":
		return "power"
	case "hr", "heart_rate", "heart-rate":
		return "hr"
	case "pace":
		return "pace"
	default:
		return ""
	}
}
